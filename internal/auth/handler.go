package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/pmaxis/helpic/internal/middleware"
	"github.com/pmaxis/helpic/internal/ratelimit"
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
	// failures counts wrong passwords per email; nil doesn't limit them.
	failures ratelimit.Gate
	// resets counts reset links asked for per email; nil doesn't limit them.
	resets ratelimit.Gate
}

// NewHandler creates an auth Handler.
func NewHandler(svc *Service, cookie CookieConfig) *Handler {
	return &Handler{svc: svc, cookie: cookie}
}

// LimitResetRequests stops emailing reset links to an address once l runs
// out. The answer stays the same, so it tells nothing and floods no inbox.
func (h *Handler) LimitResetRequests(l ratelimit.Gate) {
	h.resets = l
}

// LimitFailedLogins refuses sign-ins to an email once l runs out: each wrong
// password takes a token, a right one takes none.
func (h *Handler) LimitFailedLogins(l ratelimit.Gate) {
	h.failures = l
}

// Register handles POST /api/v1/auth/register.
//
//	@Summary		Sign up as a freelancer
//	@Description	Emails a confirmation link. The account is created when that link is opened, not before.
//	@Tags			auth
//	@Accept		json
//	@Produce	json
//	@Param		body	body		RegisterRequest			true	"Request body"
//	@Success	202		{object}	map[string]string		"Confirmation emailed; no account yet"
//	@Failure	400		{object}	response.ErrorEnvelope	"Invalid input"
//	@Failure	409		{object}	response.ErrorEnvelope	"The email is taken"
//	@Failure	429		{object}	response.ErrorEnvelope	"Too many sign-ups from this address"
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

	err := h.svc.RequestSignup(r.Context(), params)
	if errors.Is(err, ErrEmailExists) {
		response.Error(w, http.StatusConflict, response.CodeEmailExists, "An account with this email already exists")
		return
	}
	if err != nil {
		response.Internal(w, err)
		return
	}

	response.JSON(w, http.StatusAccepted, map[string]string{"email": params.Email})
}

// ConfirmSignup handles POST /api/v1/auth/signups/confirm.
//
//	@Summary		Confirm a sign-up email
//	@Description	Creates the freelancer account and signs this browser in. The link is the proof the inbox is theirs.
//	@Tags			auth
//	@Accept			json
//	@Produce		json
//	@Param			body	body		SignupConfirmRequest	true	"Request body"
//	@Success		201		{object}	user.Response			"The new account, signed in"
//	@Failure		400		{object}	response.ErrorEnvelope	"The link is invalid, replaced, used or expired (field token)"
//	@Failure		409		{object}	response.ErrorEnvelope	"The email was taken before the link was opened"
//	@Failure		429		{object}	response.ErrorEnvelope	"Too many attempts from this address"
//	@Router			/auth/signups/confirm [post]
func (h *Handler) ConfirmSignup(w http.ResponseWriter, r *http.Request) {
	var req SignupConfirmRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Token == "" {
		response.ValidationError(w, map[string]string{"token": "This link has expired or was already used. Ask for a new one."})
		return
	}

	u, tokens, err := h.svc.ConfirmSignup(r.Context(), req.Token, ClientOf(r))
	if errors.Is(err, ErrSignupInvalid) {
		response.ValidationError(w, map[string]string{"token": "This link has expired or was already used. Ask for a new one."})
		return
	}
	if errors.Is(err, ErrEmailExists) {
		response.Error(w, http.StatusConflict, response.CodeEmailExists, "An account with this email already exists")
		return
	}
	if err != nil {
		response.Internal(w, err)
		return
	}

	h.cookie.Set(w, tokens)
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
//	@Failure		429		{object}	response.ErrorEnvelope	"Too many attempts from this address, or wrong passwords for this email"
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

	if h.failures != nil {
		ok, retry, err := h.failures.Check(r.Context(), params.Email)
		if err != nil {
			response.Internal(w, err)
			return
		}
		if !ok {
			ratelimit.TooMany(w, retry)
			return
		}
	}

	u, tokens, err := h.svc.Login(r.Context(), params, ClientOf(r))
	if errors.Is(err, ErrInvalidCredentials) {
		if h.failures != nil {
			if err := h.failures.Take(r.Context(), params.Email); err != nil {
				response.Internal(w, err)
				return
			}
		}
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
//	@Failure		429	{object}	response.ErrorEnvelope	"Too many refreshes from this address"
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

// Forgot handles POST /api/v1/auth/password/forgot.
//
//	@Summary		Ask for a password reset link
//	@Description	Emails a one-time link to the account with this email. The answer is the same whether there is one or not.
//	@Tags			auth
//	@Accept			json
//	@Param			body	body	ForgotRequest	true	"Request body"
//	@Success		204		"Sent, if there is such an account"
//	@Failure		400		{object}	response.ErrorEnvelope	"Invalid email"
//	@Failure		429		{object}	response.ErrorEnvelope	"Too many attempts from this address"
//	@Router			/auth/password/forgot [post]
func (h *Handler) Forgot(w http.ResponseWriter, r *http.Request) {
	var req ForgotRequest
	if !request.Decode(w, r, &req) {
		return
	}
	email, fields := req.Validate()
	if len(fields) > 0 {
		response.ValidationError(w, fields)
		return
	}
	if h.resets != nil {
		ok, _, err := h.resets.Allow(r.Context(), email)
		if err != nil {
			response.Internal(w, err)
			return
		}
		if !ok {
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	if err := h.svc.RequestReset(r.Context(), email); err != nil {
		response.Internal(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Reset handles POST /api/v1/auth/password/reset.
//
//	@Summary		Set a new password with a reset link
//	@Description	Signs out every device; sign in with the new password.
//	@Tags			auth
//	@Accept			json
//	@Param			body	body	ResetRequest	true	"Request body"
//	@Success		204		"Password set"
//	@Failure		400		{object}	response.ErrorEnvelope	"Invalid password, or the link is invalid, used or expired (field token)"
//	@Failure		429		{object}	response.ErrorEnvelope	"Too many attempts from this address"
//	@Router			/auth/password/reset [post]
func (h *Handler) Reset(w http.ResponseWriter, r *http.Request) {
	var req ResetRequest
	if !request.Decode(w, r, &req) {
		return
	}
	if fields := req.Validate(); len(fields) > 0 {
		response.ValidationError(w, fields)
		return
	}
	err := h.svc.ResetPassword(r.Context(), req.Token, req.Password)
	if errors.Is(err, ErrResetInvalid) {
		response.ValidationError(w, map[string]string{"token": "This link has expired or was already used. Ask for a new one."})
		return
	}
	if err != nil {
		response.Internal(w, err)
		return
	}
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
