package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/pmaxis/helpic/internal/middleware"
	"github.com/pmaxis/helpic/internal/request"
	"github.com/pmaxis/helpic/internal/response"
	"github.com/pmaxis/helpic/internal/user"
)

// CookieConfig describes how the auth cookies are written. Values come from
// environment configuration so development and production can differ.
type CookieConfig struct {
	// Name and Path are the access token's cookie, sent with every request.
	Name string
	Path string
	// RefreshName and RefreshPath are the refresh token's cookie, sent only
	// to the auth endpoints.
	RefreshName string
	RefreshPath string
	Secure      bool
	SameSite    http.SameSite
}

// Handler exposes the authentication endpoints over HTTP. It owns request
// parsing, validation and response shaping only — no business logic.
type Handler struct {
	svc    *Service
	cookie CookieConfig
}

// NewHandler creates an auth Handler.
func NewHandler(svc *Service, cookie CookieConfig) *Handler {
	return &Handler{svc: svc, cookie: cookie}
}

// Register handles POST /api/v1/auth/register.
//
//	@Summary	Sign up as a freelancer
//	@Tags		auth
//	@Accept		json
//	@Produce	json
//	@Param		body	body		RegisterRequest			true	"Request body"
//	@Success	201		{object}	user.Response			"The new account"
//	@Failure	400		{object}	response.ErrorEnvelope	"Invalid input"
//	@Failure	409		{object}	response.ErrorEnvelope	"The email is taken"
//	@Router		/auth/register [post]
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var req RegisterRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	params, fieldErrs := req.Validate()
	if len(fieldErrs) > 0 {
		response.ValidationError(w, fieldErrs)
		return
	}

	u, err := h.svc.Register(r.Context(), params, user.RoleFreelancer)
	if errors.Is(err, ErrEmailExists) {
		response.Error(w, http.StatusConflict, response.CodeEmailExists, "An account with this email already exists")
		return
	}
	if err != nil {
		response.Internal(w, err)
		return
	}

	response.JSON(w, http.StatusCreated, user.NewResponse(u))
}

// Login handles POST /api/v1/auth/login.
//
//	@Summary		Sign in
//	@Description	Sets the httpOnly auth cookie that every other endpoint reads.
//	@Tags			auth
//	@Accept			json
//	@Produce		json
//	@Param			body	body		LoginRequest			true	"Request body"
//	@Success		200		{object}	user.Response			"OK"
//	@Failure		400		{object}	response.ErrorEnvelope	"Invalid input"
//	@Failure		401		{object}	response.ErrorEnvelope	"Wrong email or password"
//	@Router			/auth/login [post]
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	params, fieldErrs := req.Validate()
	if len(fieldErrs) > 0 {
		response.ValidationError(w, fieldErrs)
		return
	}

	u, tokens, err := h.svc.Login(r.Context(), params, ClientOf(r))
	if errors.Is(err, ErrInvalidCredentials) {
		response.Error(w, http.StatusUnauthorized, response.CodeInvalidCredential, "Invalid email or password")
		return
	}
	if err != nil {
		response.Internal(w, err)
		return
	}

	h.cookie.Set(w, tokens)
	response.JSON(w, http.StatusOK, user.NewResponse(u))
}

// Refresh handles POST /api/v1/auth/refresh.
//
//	@Summary		Refresh the session
//	@Description	Swaps the refresh cookie for a new one and sets a new access cookie. A refresh token used again after it was replaced ends the session.
//	@Tags			auth
//	@Success		204	"Refreshed"
//	@Failure		401	{object}	response.ErrorEnvelope	"No session, or it ended"
//	@Router			/auth/refresh [post]
func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	tokens, err := h.svc.Refresh(r.Context(), h.cookie.refreshToken(r))
	if errors.Is(err, ErrSessionInvalid) {
		h.cookie.Clear(w)
		response.Error(w, http.StatusUnauthorized, response.CodeUnauthorized, "Your session has ended")
		return
	}
	if err != nil {
		response.Internal(w, err)
		return
	}
	h.cookie.Set(w, tokens)
	w.WriteHeader(http.StatusNoContent)
}

// Logout handles POST /api/v1/auth/logout. It ends the session and clears the
// auth cookies.
//
//	@Summary		Sign out
//	@Description	Ends this device's session and clears the auth cookies.
//	@Tags			auth
//	@Success		204	"Signed out"
//	@Router			/auth/logout [post]
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Logout(r.Context(), h.cookie.refreshToken(r)); err != nil {
		response.Internal(w, err)
		return
	}
	h.cookie.Clear(w)
	w.WriteHeader(http.StatusNoContent)
}

// SessionRoutes mounts the endpoints for the user's signed-in devices. Must be
// called inside an authenticated group.
func (h *Handler) SessionRoutes(r chi.Router) {
	r.Route("/me/sessions", func(r chi.Router) {
		r.Get("/", h.Sessions)
		r.Delete("/", h.EndOtherSessions)
		r.Delete("/{id}", h.EndSession)
	})
}

// Sessions handles GET /api/v1/me/sessions.
//
//	@Summary	Your signed-in devices
//	@Tags		account
//	@Produce	json
//	@Success	200	{object}	map[string][]SessionDTO	"sessions, the most recently used first"
//	@Failure	401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Router		/me/sessions [get]
func (h *Handler) Sessions(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	list, err := h.svc.Sessions(r.Context(), userID)
	if err != nil {
		response.Internal(w, err)
		return
	}
	current := middleware.SessionID(r.Context())
	out := make([]SessionDTO, 0, len(list))
	for _, sess := range list {
		out = append(out, ToSessionDTO(sess, current))
	}
	response.JSON(w, http.StatusOK, map[string][]SessionDTO{"sessions": out})
}

// EndSession handles DELETE /api/v1/me/sessions/{id}.
//
//	@Summary		Sign a device out
//	@Description	Its access token works until it expires, minutes at most; it can't be refreshed.
//	@Tags			account
//	@Param			id	path	string	true	"Session ID"
//	@Success		204	"Signed out"
//	@Failure		401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		404	{object}	response.ErrorEnvelope	"No such session"
//	@Router			/me/sessions/{id} [delete]
func (h *Handler) EndSession(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	err := h.svc.EndSession(r.Context(), userID, chi.URLParam(r, "id"))
	if errors.Is(err, ErrSessionNotFound) {
		response.Error(w, http.StatusNotFound, response.CodeNotFound, "Session not found")
		return
	}
	if err != nil {
		response.Internal(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// EndOtherSessions handles DELETE /api/v1/me/sessions.
//
//	@Summary	Sign out every other device
//	@Tags		account
//	@Success	204	"Signed out"
//	@Failure	401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Router		/me/sessions [delete]
func (h *Handler) EndOtherSessions(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	if err := h.svc.EndOtherSessions(r.Context(), userID, middleware.SessionID(r.Context())); err != nil {
		response.Internal(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Set writes the access cookie and, when the tokens carry one, the refresh
// cookie.
func (c CookieConfig) Set(w http.ResponseWriter, t Tokens) {
	c.write(w, c.Name, c.Path, t.Access, t.AccessExpires)
	if t.Refresh != "" {
		c.write(w, c.RefreshName, c.RefreshPath, t.Refresh, t.RefreshExpires)
	}
}

// Clear expires both auth cookies.
func (c CookieConfig) Clear(w http.ResponseWriter) {
	c.write(w, c.Name, c.Path, "", time.Time{})
	c.write(w, c.RefreshName, c.RefreshPath, "", time.Time{})
}

// write sets an httpOnly cookie; a zero expiry expires it.
func (c CookieConfig) write(w http.ResponseWriter, name, path, value string, expiresAt time.Time) {
	maxAge := -1
	if expiresAt.IsZero() {
		expiresAt = time.Unix(0, 0)
	} else {
		maxAge = int(time.Until(expiresAt).Seconds())
	}
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     path,
		HttpOnly: true,
		Secure:   c.Secure,
		SameSite: c.SameSite,
		Expires:  expiresAt,
		MaxAge:   maxAge,
	})
}

func (c CookieConfig) refreshToken(r *http.Request) string {
	ck, err := r.Cookie(c.RefreshName)
	if err != nil {
		return ""
	}
	return ck.Value
}

// decodeJSON strictly decodes the request body, writing a 400 on failure.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		response.Error(w, http.StatusBadRequest, response.CodeValidation, "Request body is not valid JSON")
		return false
	}
	return true
}
