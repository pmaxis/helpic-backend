package admin

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/pmaxis/helpic/internal/mail"
	"github.com/pmaxis/helpic/internal/request"
	"github.com/pmaxis/helpic/internal/response"
)

// mailPage is a page's size for emails and sign-ups; maxMailPage the most
// one may ask for.
const (
	mailPage    = 25
	maxMailPage = 100
)

// paging reads ?page= and ?limit=, answering 400 itself when they're wrong.
func paging(w http.ResponseWriter, q url.Values, size, most int, tooMany string) (page, limit int, ok bool) {
	page, limit = 1, size
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > most {
			response.ValidationError(w, map[string]string{"limit": tooMany})
			return 0, 0, false
		}
		limit = n
	}
	if raw := q.Get("page"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			response.ValidationError(w, map[string]string{"page": "This page link is broken. Start from the first page."})
			return 0, 0, false
		}
		page = n
	}
	return page, limit, true
}

// EmailDTO is an outgoing email: whom to, about what and how sending it
// went. Never the body.
type EmailDTO struct {
	ID      string `json:"id"`
	To      string `json:"to"`
	Subject string `json:"subject"`
	// Status is queued, failed or sent.
	Status    string `json:"status"`
	Attempts  int    `json:"attempts"`
	LastError string `json:"last_error"`
	// NextAttemptAt is when a queued email is tried next.
	NextAttemptAt time.Time  `json:"next_attempt_at"`
	SentAt        *time.Time `json:"sent_at"`
	CreatedAt     time.Time  `json:"created_at"`
}

func toEmailDTO(q mail.Queued) EmailDTO {
	return EmailDTO{
		ID: q.ID, To: q.To, Subject: q.Subject, Status: q.Status, Attempts: q.Attempts, LastError: q.LastError,
		NextAttemptAt: q.NextAttemptAt, SentAt: q.SentAt, CreatedAt: q.CreatedAt,
	}
}

// EmailCountsDTO is how many emails stand where over the week kept.
type EmailCountsDTO struct {
	Queued int `json:"queued"`
	Failed int `json:"failed"`
	Sent   int `json:"sent"`
	// LastSent is when an email last went out; null when none has lately.
	LastSent *time.Time `json:"last_sent"`
}

func toEmailCountsDTO(c mail.QueueCounts) EmailCountsDTO {
	return EmailCountsDTO{Queued: c.Queued, Failed: c.Failed, Sent: c.Sent, LastSent: c.LastSent}
}

// EmailsResponse is a page of emails, how many match and the counts.
type EmailsResponse struct {
	Emails []EmailDTO     `json:"emails"`
	Page   int            `json:"page"`
	Limit  int            `json:"limit"`
	Total  int            `json:"total"`
	Counts EmailCountsDTO `json:"counts"`
}

// Emails handles GET /api/v1/admin/emails?status={status}&q={text}&page={n}&limit={n}.
//
//	@Summary		Outgoing email
//	@Description	The emails Helpic sent or is sending, the newest first, kept for a week: whom to, the subject and how sending went. Never the body, which can hold one-time links.
//	@Tags			admin
//	@Produce		json
//	@Param			status	query		string					false	"queued, failed or sent"
//	@Param			q		query		string					false	"Part of the address or the subject"
//	@Param			page	query		int						false	"The page, from 1"
//	@Param			limit	query		int						false	"Emails per page, 25 by default, at most 100"
//	@Success		200		{object}	EmailsResponse			"OK"
//	@Failure		400		{object}	response.ErrorEnvelope	"Invalid status, page or limit"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		403		{object}	response.ErrorEnvelope	"Not an admin, or two-factor is off"
//	@Router			/admin/emails [get]
func (h *Handler) Emails(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := mail.QueueFilter{Status: q.Get("status"), Query: q.Get("q")}
	switch f.Status {
	case "", mail.StatusQueued, mail.StatusFailed, mail.StatusSent:
	default:
		response.ValidationError(w, map[string]string{"status": "Choose queued, failed or sent"})
		return
	}
	var ok bool
	if f.Page, f.Limit, ok = paging(w, q, mailPage, maxMailPage, "Ask for 1 to 100 entries"); !ok {
		return
	}
	list, total, err := h.svc.Emails(r.Context(), f)
	if err != nil {
		response.Internal(w, err)
		return
	}
	counts, err := h.svc.EmailCounts(r.Context())
	if err != nil {
		response.Internal(w, err)
		return
	}
	out := EmailsResponse{Emails: make([]EmailDTO, 0, len(list)), Page: f.Page, Limit: f.Limit, Total: total, Counts: toEmailCountsDTO(counts)}
	for _, e := range list {
		out.Emails = append(out.Emails, toEmailDTO(e))
	}
	response.JSON(w, http.StatusOK, out)
}

// RetryEmail handles POST /api/v1/admin/emails/{id}/retry.
//
//	@Summary		Send an email again
//	@Description	Sends an email that wasn't sent on the next run, with a fresh set of tries, and records it. A sent email can't be: its body is gone.
//	@Tags			admin
//	@Produce		json
//	@Param			id	path		string					true	"Email ID"
//	@Success		200	{object}	map[string]EmailDTO		"email, as it is now"
//	@Failure		401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		403	{object}	response.ErrorEnvelope	"Not an admin, or two-factor is off"
//	@Failure		404	{object}	response.ErrorEnvelope	"No such email, or it was sent"
//	@Router			/admin/emails/{id}/retry [post]
func (h *Handler) RetryEmail(w http.ResponseWriter, r *http.Request) {
	adminID, ok := request.User(w, r)
	if !ok {
		return
	}
	q, err := h.svc.RetryEmail(r.Context(), adminID, chi.URLParam(r, "id"))
	if errors.Is(err, mail.ErrNotQueued) {
		response.Error(w, http.StatusNotFound, response.CodeNotFound, "Resource not found")
		return
	}
	if err != nil {
		response.Internal(w, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]EmailDTO{"email": toEmailDTO(q)})
}

// SignupDTO is a public sign-up that hasn't opened its confirmation link.
type SignupDTO struct {
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	Locale    string    `json:"locale"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	// EmailStatus is how the newest email to the address went: queued,
	// failed or sent; empty once it is no longer kept.
	EmailStatus string `json:"email_status"`
	EmailError  string `json:"email_error"`
}

// SignupsResponse is a page of sign-ups and how many match in all.
type SignupsResponse struct {
	Signups []SignupDTO `json:"signups"`
	Page    int         `json:"page"`
	Limit   int         `json:"limit"`
	Total   int         `json:"total"`
}

// Signups handles GET /api/v1/admin/signups?q={text}&page={n}&limit={n}.
//
//	@Summary		Sign-ups waiting
//	@Description	Public sign-ups that haven't opened their confirmation link and haven't expired, the newest first, with how the newest email to the address went.
//	@Tags			admin
//	@Produce		json
//	@Param			q		query		string					false	"Part of the email or the name"
//	@Param			page	query		int						false	"The page, from 1"
//	@Param			limit	query		int						false	"Sign-ups per page, 25 by default, at most 100"
//	@Success		200		{object}	SignupsResponse			"OK"
//	@Failure		400		{object}	response.ErrorEnvelope	"Invalid page or limit"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		403		{object}	response.ErrorEnvelope	"Not an admin, or two-factor is off"
//	@Router			/admin/signups [get]
func (h *Handler) Signups(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := SignupFilter{Query: q.Get("q")}
	var ok bool
	if f.Page, f.Limit, ok = paging(w, q, mailPage, maxMailPage, "Ask for 1 to 100 entries"); !ok {
		return
	}
	list, total, err := h.svc.Signups(r.Context(), f)
	if err != nil {
		response.Internal(w, err)
		return
	}
	out := SignupsResponse{Signups: make([]SignupDTO, 0, len(list)), Page: f.Page, Limit: f.Limit, Total: total}
	for _, s := range list {
		out.Signups = append(out.Signups, SignupDTO{
			Email: s.Email, Name: s.Name, Locale: s.Locale, CreatedAt: s.CreatedAt, ExpiresAt: s.ExpiresAt,
			EmailStatus: s.EmailStatus, EmailError: s.EmailError,
		})
	}
	response.JSON(w, http.StatusOK, out)
}

// signupEmail is the {email} in the path, decoded.
func signupEmail(r *http.Request) string {
	raw := chi.URLParam(r, "email")
	if email, err := url.PathUnescape(raw); err == nil {
		return email
	}
	return raw
}

// ResendSignup handles POST /api/v1/admin/signups/{email}/resend.
//
//	@Summary		Send a sign-up its link again
//	@Description	Emails a new confirmation link, good for 24 hours, in the language the sign-up was made in, and records it. The old link stops working.
//	@Tags			admin
//	@Param			email	path	string	true	"The sign-up's email"
//	@Success		204
//	@Failure		401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		403	{object}	response.ErrorEnvelope	"Not an admin, or two-factor is off"
//	@Failure		404	{object}	response.ErrorEnvelope	"No sign-up waiting for this email"
//	@Router			/admin/signups/{email}/resend [post]
func (h *Handler) ResendSignup(w http.ResponseWriter, r *http.Request) {
	h.signupAction(w, r, h.svc.ResendSignup)
}

// DeleteSignup handles DELETE /api/v1/admin/signups/{email}.
//
//	@Summary		Remove a sign-up
//	@Description	Removes a sign-up nobody confirmed, so its link stops working, and records it. The address can sign up again.
//	@Tags			admin
//	@Param			email	path	string	true	"The sign-up's email"
//	@Success		204
//	@Failure		401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		403	{object}	response.ErrorEnvelope	"Not an admin, or two-factor is off"
//	@Failure		404	{object}	response.ErrorEnvelope	"No sign-up waiting for this email"
//	@Router			/admin/signups/{email} [delete]
func (h *Handler) DeleteSignup(w http.ResponseWriter, r *http.Request) {
	h.signupAction(w, r, h.svc.DeleteSignup)
}

func (h *Handler) signupAction(w http.ResponseWriter, r *http.Request, do func(ctx context.Context, adminID, email string) error) {
	adminID, ok := request.User(w, r)
	if !ok {
		return
	}
	err := do(r.Context(), adminID, signupEmail(r))
	if errors.Is(err, ErrSignupNotFound) {
		response.Error(w, http.StatusNotFound, response.CodeNotFound, "Resource not found")
		return
	}
	if err != nil {
		response.Internal(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
