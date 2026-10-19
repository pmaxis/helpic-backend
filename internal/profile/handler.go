package profile

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/pmaxis/helpic/internal/auth"
	"github.com/pmaxis/helpic/internal/i18n"
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

// PublicRoutes mounts the endpoints that need no signed-in user.
func (h *Handler) PublicRoutes(r chi.Router) {
	r.Post("/email-changes/confirm", h.ConfirmEmail)
}

// Routes mounts the endpoints. Must be called inside an authenticated group.
// guard wraps the ones that check the current password, to limit guessing.
func (h *Handler) Routes(r chi.Router, guard func(http.Handler) http.Handler) {
	r.Put("/me", h.UpdateName)
	r.With(guard).Put("/me/email", h.ChangeEmail)
	r.With(guard).Put("/me/password", h.ChangePassword)
	r.Put("/me/currency", h.SetCurrency)
	r.Put("/me/locale", h.SetLocale)
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
//	@Description	Needs the current password. Emails a confirmation link to the new address; the email changes once it is opened (POST /email-changes/confirm).
//	@Tags			account
//	@Accept			json
//	@Produce		json
//	@Param			body	body		EmailRequest			true	"Request body"
//	@Success		202		{object}	PendingEmailDTO			"Confirmation sent"
//	@Failure		400		{object}	response.ErrorEnvelope	"Invalid input, the same email, or taken"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		429		{object}	response.ErrorEnvelope	"Too many attempts"
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
	err := h.svc.RequestEmailChange(r.Context(), userID, email, req.CurrentPassword)
	switch {
	case errors.Is(err, ErrWrongPassword):
		response.ValidationError(w, map[string]string{"current_password": "Wrong password"})
	case errors.Is(err, ErrSameEmail):
		response.ValidationError(w, map[string]string{"email": "That's your email already"})
	case errors.Is(err, auth.ErrEmailExists):
		response.ValidationError(w, map[string]string{"email": "This email is already used by another account"})
	case err != nil:
		response.Internal(w, err)
	default:
		response.JSON(w, http.StatusAccepted, PendingEmailDTO{PendingEmail: email})
	}
}

// ConfirmEmail handles POST /api/v1/email-changes/confirm.
//
//	@Summary		Confirm a new sign-in email
//	@Description	Opened from the link emailed to the new address; needs no signed-in user.
//	@Tags			account
//	@Accept			json
//	@Produce		json
//	@Param			body	body		ConfirmEmailRequest		true	"Request body"
//	@Success		200		{object}	user.Response			"The account, with its new email"
//	@Failure		400		{object}	response.ErrorEnvelope	"The link is invalid, used or expired, or the email was taken since (field token)"
//	@Failure		429		{object}	response.ErrorEnvelope	"Too many attempts from this address"
//	@Router			/email-changes/confirm [post]
func (h *Handler) ConfirmEmail(w http.ResponseWriter, r *http.Request) {
	var req ConfirmEmailRequest
	if !request.Decode(w, r, &req) {
		return
	}
	u, err := h.svc.ConfirmEmailChange(r.Context(), req.Token)
	switch {
	case errors.Is(err, ErrEmailChangeInvalid):
		response.ValidationError(w, map[string]string{"token": "This link has expired or was already used. Change your email again in Settings."})
	case errors.Is(err, auth.ErrEmailExists):
		response.ValidationError(w, map[string]string{"token": "Another account has used this email since. Pick a different one in Settings."})
	case err != nil:
		response.Internal(w, err)
	default:
		response.JSON(w, http.StatusOK, user.NewResponse(u))
	}
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
//	@Failure		429		{object}	response.ErrorEnvelope	"Too many attempts"
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

// SetLocale handles PUT /api/v1/me/locale: the language they read Helpic in.
//
//	@Summary		Set your language
//	@Description	"en" or "uk": the interface, and the email and Telegram messages you get.
//	@Tags			account
//	@Accept			json
//	@Produce		json
//	@Param			body	body		LocaleRequest			true	"Request body"
//	@Success		200		{object}	user.Response			"OK"
//	@Failure		400		{object}	response.ErrorEnvelope	"Unknown language"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Router			/me/locale [put]
func (h *Handler) SetLocale(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	var req LocaleRequest
	if !request.Decode(w, r, &req) {
		return
	}
	if !i18n.Valid(req.Locale) {
		response.ValidationError(w, map[string]string{"locale": "Choose English or Ukrainian"})
		return
	}
	u, err := h.svc.SetLocale(r.Context(), userID, req.Locale)
	writeUser(w, u, err)
}

// SetCurrency handles PUT /api/v1/me/currency: what a freelancer bills in.
//
//	@Summary		Set the billing currency
//	@Description	What a freelancer bills in: USD, EUR, GBP, UAH, PLN or CAD. Refused while a task still has a price, so amounts are not relabeled.
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
	case errors.Is(err, ErrCurrencyLocked):
		response.ValidationError(w, map[string]string{"currency": "You already have prices. Clear them before changing the currency, so the amounts aren't relabeled."})
	case errors.Is(err, user.ErrNotFound):
		response.Error(w, http.StatusUnauthorized, response.CodeUnauthorized, "Authentication required")
	case err != nil:
		response.Internal(w, err)
	default:
		response.JSON(w, http.StatusOK, user.NewResponse(u))
	}
}
