package admin

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/pmaxis/helpic/internal/account"
	"github.com/pmaxis/helpic/internal/request"
	"github.com/pmaxis/helpic/internal/response"
)

// ActionDelete deletes the account for its owner, as they could themselves
// (see package account), and emails them.
const ActionDelete = "delete"

// Accounts deletes accounts. Implemented by *account.Service.
type Accounts interface {
	Deletable(ctx context.Context, userID string) error
	Delete(ctx context.Context, userID string, bySupport bool, before account.Before) error
}

// UseAccounts lets admins delete accounts.
func (s *Service) UseAccounts(a Accounts) {
	s.accounts = a
}

// Deletable checks that the account can be deleted: ErrNotFound for no such
// account or an admin's, account.ErrHasTeam while its company has people.
func (s *Service) Deletable(ctx context.Context, id string) error {
	return notFound(s.accounts.Deletable(ctx, id))
}

// Delete deletes the account as the admin adminID and records it with the
// reason, under the email it had.
func (s *Service) Delete(ctx context.Context, adminID, id, reason string) error {
	return notFound(s.accounts.Delete(ctx, id, true, func(ctx context.Context, tx pgx.Tx, d account.Deleted) error {
		return record(ctx, tx, adminID, ActionDelete, d.Email, reason)
	}))
}

func notFound(err error) error {
	if errors.Is(err, account.ErrNotFound) {
		return ErrNotFound
	}
	return err
}

// DeleteUser handles DELETE /api/v1/admin/users/{id}.
//
//	@Summary		Delete an account
//	@Description	As the person could themselves: the work they shared stays with the others under "Deleted user", the rest of theirs is deleted, the email is freed, and the old address is told. Asks for the admin's two-factor code again and is recorded with the reason. Refused while the client's company still has people. Admins' accounts can't be deleted.
//	@Tags			admin
//	@Accept			json
//	@Param			id		path	string			true	"User ID"
//	@Param			body	body	ActionRequest	true	"Request body"
//	@Success		204		"Deleted"
//	@Failure		400		{object}	response.ErrorEnvelope	"Reason too long, or the code missing or wrong (field code)"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		403		{object}	response.ErrorEnvelope	"Not an admin, or two-factor is off"
//	@Failure		404		{object}	response.ErrorEnvelope	"No such account"
//	@Failure		409		{object}	response.ErrorEnvelope	"The company still has people"
//	@Failure		429		{object}	response.ErrorEnvelope	"Too many wrong codes"
//	@Router			/admin/users/{id} [delete]
func (h *Handler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	adminID, ok := request.User(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")
	// Before the code, so a refusal doesn't spend one.
	if writeDeleteError(w, h.svc.Deletable(r.Context(), id)) {
		return
	}
	reason, ok := h.readAction(w, r, adminID)
	if !ok {
		return
	}
	if writeDeleteError(w, h.svc.Delete(r.Context(), adminID, id, reason)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeDeleteError answers for a deletion that failed, and reports whether
// err was one.
func writeDeleteError(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, ErrNotFound):
		response.Error(w, http.StatusNotFound, response.CodeNotFound, "Resource not found")
	case errors.Is(err, account.ErrHasTeam):
		response.Error(w, http.StatusConflict, response.CodeConflict, "Their company still has people in it. They have to be removed from the team first.")
	default:
		response.Internal(w, err)
	}
	return true
}
