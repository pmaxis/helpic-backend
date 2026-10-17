package profile

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/pmaxis/helpic/internal/auth"
	"github.com/pmaxis/helpic/internal/middleware"
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

// UpdateName handles PUT /api/v1/me: the first and last name.
//
//	@Summary	Change your name
//	@Tags		account
//	@Accept		json
//	@Produce	json
//	@Param		body	body		NameRequest				true	"Request body"
//	@Success	200		{object}	user.Response			"OK"
//	@Failure	400		{object}	response.ErrorEnvelope	"Invalid input"
//	@Failure	401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Router		/me [put]
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

// ChangeEmail handles PUT /api/v1/me/email.
//
//	@Summary		Change your sign-in email
//	@Description	Needs the current password.
//	@Tags			account
//	@Accept			json
//	@Produce		json
//	@Param			body	body		EmailRequest			true	"Request body"
//	@Success		200		{object}	user.Response			"OK"
//	@Failure		400		{object}	response.ErrorEnvelope	"Invalid input"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Router			/me/email [put]
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

// ChangePassword handles PUT /api/v1/me/password.
//
//	@Summary		Change your password
//	@Description	Needs the current password. Signs out every other device.
//	@Tags			account
//	@Accept			json
//	@Produce		json
//	@Param			body	body		PasswordRequest			true	"Request body"
//	@Success		200		{object}	user.Response			"OK"
//	@Failure		400		{object}	response.ErrorEnvelope	"Invalid input"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Router			/me/password [put]
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
	u, err := h.svc.ChangePassword(r.Context(), userID, middleware.SessionID(r.Context()), req.CurrentPassword, req.Password)
	writeUser(w, u, err)
}

// SetCurrency handles PUT /api/v1/me/currency: what a freelancer bills in.
//
//	@Summary		Set the billing currency
//	@Description	What a freelancer bills in: USD, EUR, GBP, UAH, PLN or CAD.
//	@Tags			account
//	@Accept			json
//	@Produce		json
//	@Param			body	body		CurrencyRequest			true	"Request body"
//	@Success		200		{object}	user.Response			"OK"
//	@Failure		400		{object}	response.ErrorEnvelope	"Invalid input"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Router			/me/currency [put]
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
