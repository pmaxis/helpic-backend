package profile

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/pmaxis/helpic/internal/auth"
	"github.com/pmaxis/helpic/internal/request"
	"github.com/pmaxis/helpic/internal/response"
	"github.com/pmaxis/helpic/internal/user"
)

// Handler exposes the account settings endpoints over HTTP.
type Handler struct {
	svc *Service
}

// NewHandler creates a profile Handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// Routes mounts the endpoints. Must be called inside an authenticated group.
func (h *Handler) Routes(r chi.Router) {
	r.Put("/me", h.UpdateName)
	r.Put("/me/email", h.ChangeEmail)
	r.Put("/me/password", h.ChangePassword)
	r.Put("/me/currency", h.SetCurrency)
}

// UpdateName handles PUT /api/me: the first and last name.
func (h *Handler) UpdateName(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	var req NameRequest
	if !request.Decode(w, r, &req) {
		return
	}
	n, fields := req.Validate()
	if len(fields) > 0 {
		response.ValidationError(w, fields)
		return
	}
	u, err := h.svc.UpdateName(r.Context(), userID, n)
	writeUser(w, u, err)
}

// ChangeEmail handles PUT /api/me/email.
func (h *Handler) ChangeEmail(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	var req EmailRequest
	if !request.Decode(w, r, &req) {
		return
	}
	email, fields := req.Validate()
	if len(fields) > 0 {
		response.ValidationError(w, fields)
		return
	}
	u, err := h.svc.ChangeEmail(r.Context(), userID, email, req.CurrentPassword)
	if errors.Is(err, auth.ErrEmailExists) {
		response.ValidationError(w, map[string]string{"email": "This email is already used by another account"})
		return
	}
	writeUser(w, u, err)
}

// ChangePassword handles PUT /api/me/password.
func (h *Handler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	var req PasswordRequest
	if !request.Decode(w, r, &req) {
		return
	}
	if fields := req.Validate(); len(fields) > 0 {
		response.ValidationError(w, fields)
		return
	}
	u, err := h.svc.ChangePassword(r.Context(), userID, req.CurrentPassword, req.Password)
	writeUser(w, u, err)
}

// SetCurrency handles PUT /api/me/currency: what a freelancer bills in.
func (h *Handler) SetCurrency(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	var req CurrencyRequest
	if !request.Decode(w, r, &req) {
		return
	}
	currency, fields := req.Validate()
	if len(fields) > 0 {
		response.ValidationError(w, fields)
		return
	}
	u, err := h.svc.SetCurrency(r.Context(), userID, currency)
	writeUser(w, u, err)
}

func writeUser(w http.ResponseWriter, u user.User, err error) {
	switch {
	case errors.Is(err, ErrWrongPassword):
		response.ValidationError(w, map[string]string{"current_password": "Wrong password"})
	case errors.Is(err, user.ErrNotFound):
		response.Error(w, http.StatusUnauthorized, response.CodeUnauthorized, "Authentication required")
	case err != nil:
		response.Internal(w, err)
	default:
		response.JSON(w, http.StatusOK, user.NewResponse(u))
	}
}
