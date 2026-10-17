package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/pmaxis/helpic/internal/response"
	"github.com/pmaxis/helpic/internal/user"
)

// CookieConfig describes how the auth cookie is written. Values come from
// environment configuration so development and production can differ.
type CookieConfig struct {
	Name     string
	Path     string
	Secure   bool
	SameSite http.SameSite
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

// Register handles POST /api/auth/register.
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

// Login handles POST /api/auth/login.
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

	u, jwtString, expiresAt, err := h.svc.Login(r.Context(), params)
	if errors.Is(err, ErrInvalidCredentials) {
		response.Error(w, http.StatusUnauthorized, response.CodeInvalidCredential, "Invalid email or password")
		return
	}
	if err != nil {
		response.Internal(w, err)
		return
	}

	h.cookie.Set(w, jwtString, expiresAt)
	response.JSON(w, http.StatusOK, user.NewResponse(u))
}

// Logout handles POST /api/auth/logout. It clears the auth cookie.
func (h *Handler) Logout(w http.ResponseWriter, _ *http.Request) {
	h.cookie.Clear(w)
	w.WriteHeader(http.StatusNoContent)
}

// Set writes the session cookie carrying the given token.
func (c CookieConfig) Set(w http.ResponseWriter, value string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     c.Name,
		Value:    value,
		Path:     c.Path,
		HttpOnly: true,
		Secure:   c.Secure,
		SameSite: c.SameSite,
		Expires:  expiresAt,
		MaxAge:   int(time.Until(expiresAt).Seconds()),
	})
}

// Clear expires the session cookie.
func (c CookieConfig) Clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     c.Name,
		Value:    "",
		Path:     c.Path,
		HttpOnly: true,
		Secure:   c.Secure,
		SameSite: c.SameSite,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
	})
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
