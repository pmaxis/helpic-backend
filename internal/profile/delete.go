package profile

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/pmaxis/helpic/internal/account"
	"github.com/pmaxis/helpic/internal/auth"
	"github.com/pmaxis/helpic/internal/request"
	"github.com/pmaxis/helpic/internal/response"
	"github.com/pmaxis/helpic/internal/user"
)

// Deleting your own account takes the password and, with two-factor on, a
// code; package account says what goes and what stays.

// ErrAdminAccount is returned when an admin deletes their own account: admin
// accounts are the cli's to manage.
var ErrAdminAccount = errors.New("an admin account can't be deleted here")

// accounts deletes accounts. Implemented by *account.Service.
type accounts interface {
	Deletable(ctx context.Context, userID string) error
	Delete(ctx context.Context, userID string, bySupport bool, before account.Before) error
}

// UseAccounts lets people delete their account.
func (s *Service) UseAccounts(a accounts) {
	s.accounts = a
}

// DeleteAccount deletes the user's account once the password and, when
// two-factor is on, a code show it's them.
func (s *Service) DeleteAccount(ctx context.Context, userID, password, code string) error {
	u, err := s.verify(ctx, userID, password)
	if err != nil {
		return err
	}
	if u.Role == user.RoleAdmin {
		return ErrAdminAccount
	}
	// Before the code, so a refusal doesn't spend one.
	if err := s.accounts.Deletable(ctx, userID); err != nil {
		return err
	}
	if u.TwoFactor {
		if err := s.sessions.ConfirmCode(ctx, userID, code); err != nil {
			return err
		}
	}
	return s.accounts.Delete(ctx, userID, false, nil)
}

// DeleteAccountRequest is the JSON body for DELETE /api/v1/me.
type DeleteAccountRequest struct {
	CurrentPassword string `json:"current_password"`
	// Code is asked for when the account has two-factor on: six digits from
	// the app, or a recovery code.
	Code string `json:"code,omitempty"`
}

// Validate returns per-field errors.
func (r DeleteAccountRequest) Validate() map[string]string {
	fields := map[string]string{}
	if strings.TrimSpace(r.CurrentPassword) == "" {
		fields["current_password"] = "Enter your current password"
	}
	return fields
}

// DeleteAccount handles DELETE /api/v1/me.
//
//	@Summary		Delete your account
//	@Description	Needs the current password and, with two-factor on, a code from the app or a recovery code (without one, 403 TWO_FACTOR_REQUIRED). The work shared with others stays with them under "Deleted user"; personal work, settings, sessions and links are deleted, and the email is freed. Signs out and emails the old address. Refused while the client's company still has people, and for admins.
//	@Tags			account
//	@Accept			json
//	@Param			body	body	DeleteAccountRequest	true	"Request body"
//	@Success		204		"Deleted and signed out"
//	@Failure		400		{object}	response.ErrorEnvelope	"Wrong password (field current_password) or code (field code)"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		403		{object}	response.ErrorEnvelope	"A code is needed, or an admin's account"
//	@Failure		409		{object}	response.ErrorEnvelope	"The company still has people"
//	@Failure		429		{object}	response.ErrorEnvelope	"Too many attempts"
//	@Router			/me [delete]
func (h *Handler) DeleteAccount(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	var req DeleteAccountRequest
	if !request.Decode(w, r, &req) {
		return
	}
	if fields := req.Validate(); len(fields) > 0 {
		response.ValidationError(w, fields)
		return
	}
	err := h.svc.DeleteAccount(r.Context(), userID, req.CurrentPassword, req.Code)
	if auth.WriteTwoFactorError(w, err) {
		return
	}
	switch {
	case errors.Is(err, ErrWrongPassword):
		response.ValidationError(w, map[string]string{"current_password": "Wrong password"})
	case errors.Is(err, ErrAdminAccount):
		response.Error(w, http.StatusForbidden, response.CodeForbidden, "An admin account can't be deleted from the settings")
	case errors.Is(err, account.ErrHasTeam):
		response.Error(w, http.StatusConflict, response.CodeConflict, "Your company still has people in it. Remove them under Team first, so they don't lose its work without warning.")
	case errors.Is(err, user.ErrNotFound), errors.Is(err, account.ErrNotFound):
		response.Error(w, http.StatusUnauthorized, response.CodeUnauthorized, "Authentication required")
	case err != nil:
		response.Internal(w, err)
	default:
		h.cookie.Clear(w)
		w.WriteHeader(http.StatusNoContent)
	}
}
