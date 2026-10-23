package admin

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/pmaxis/helpic/internal/request"
	"github.com/pmaxis/helpic/internal/response"
)

// SessionDTO is a device an account is signed in on.
type SessionDTO struct {
	ID         string    `json:"id"`
	UserAgent  string    `json:"user_agent"`
	IP         string    `json:"ip"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at"`
}

// Sessions handles GET /api/v1/admin/users/{id}/sessions.
//
//	@Summary		An account's devices
//	@Description	The devices the account is signed in on, the most recently used first: the browser, the address and when.
//	@Tags			admin
//	@Produce		json
//	@Param			id	path		string					true	"User ID"
//	@Success		200	{object}	map[string][]SessionDTO	"sessions"
//	@Failure		401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		403	{object}	response.ErrorEnvelope	"Not an admin, or two-factor is off"
//	@Failure		404	{object}	response.ErrorEnvelope	"No such account"
//	@Router			/admin/users/{id}/sessions [get]
func (h *Handler) Sessions(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.Sessions(r.Context(), chi.URLParam(r, "id"))
	if errors.Is(err, ErrNotFound) {
		response.Error(w, http.StatusNotFound, response.CodeNotFound, "Resource not found")
		return
	}
	if err != nil {
		response.Internal(w, err)
		return
	}
	out := make([]SessionDTO, 0, len(list))
	for _, s := range list {
		out = append(out, SessionDTO{ID: s.ID, UserAgent: s.UserAgent, IP: s.IP, CreatedAt: s.CreatedAt, LastUsedAt: s.LastUsedAt})
	}
	response.JSON(w, http.StatusOK, map[string][]SessionDTO{"sessions": out})
}

// EndSession handles POST /api/v1/admin/users/{id}/sessions/{session}/end.
//
//	@Summary		Sign an account out of one device
//	@Description	Ends one of the account's sessions at once and records it with the reason; the other devices stay signed in.
//	@Tags			admin
//	@Accept			json
//	@Produce		json
//	@Param			id		path		string						true	"User ID"
//	@Param			session	path		string						true	"Session ID"
//	@Param			body	body		ActionRequest				true	"Request body"
//	@Success		200		{object}	map[string]AccountDetailDTO	"user, as it is now"
//	@Failure		400		{object}	response.ErrorEnvelope		"Reason too long"
//	@Failure		401		{object}	response.ErrorEnvelope		"Not signed in"
//	@Failure		403		{object}	response.ErrorEnvelope		"Not an admin, or two-factor is off"
//	@Failure		404		{object}	response.ErrorEnvelope		"No such account, or no such session of it"
//	@Router			/admin/users/{id}/sessions/{session}/end [post]
func (h *Handler) EndSession(w http.ResponseWriter, r *http.Request) {
	adminID, ok := request.User(w, r)
	if !ok {
		return
	}
	reason, ok := readReason(w, r)
	if !ok {
		return
	}
	d, err := h.svc.EndSession(r.Context(), adminID, chi.URLParam(r, "id"), chi.URLParam(r, "session"), reason)
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrSessionNotFound) {
		response.Error(w, http.StatusNotFound, response.CodeNotFound, "Resource not found")
		return
	}
	if err != nil {
		response.Internal(w, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]AccountDetailDTO{"user": toAccountDetailDTO(d)})
}

// LimitDTO is a rate limit refusing someone right now.
type LimitDTO struct {
	// Scope is the limit: sign-in, sign-up, refresh, login-failures,
	// login-failures-email, password-checks, two-factor-codes, reset,
	// reset-email or invites.
	Scope string `json:"scope"`
	Key   string `json:"key"`
	// Kind is what the key is: ip, email, email_ip or user.
	Kind  string `json:"kind"`
	Email string `json:"email"`
	IP    string `json:"ip"`
	// AccountID and AccountEmail are the account it holds back; null and
	// empty when there is none.
	AccountID    *string `json:"account_id"`
	AccountEmail string  `json:"account_email"`
	// Burst attempts are let through at once, then one every EverySeconds.
	Burst        int       `json:"burst"`
	EverySeconds float64   `json:"every_seconds"`
	OpensAt      time.Time `json:"opens_at"`
}

// Limits handles GET /api/v1/admin/limits?q={text}&account={id}.
//
//	@Summary		Rate limits holding someone back
//	@Description	The rate limits refusing attempts right now, the longest first, with the account each holds back and when it lets the next attempt through.
//	@Tags			admin
//	@Produce		json
//	@Param			q		query		string					false	"Part of the email, the address or the account's email"
//	@Param			account	query		string					false	"Only the limits on this account"
//	@Success		200		{object}	map[string][]LimitDTO	"limits"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		403		{object}	response.ErrorEnvelope	"Not an admin, or two-factor is off"
//	@Router			/admin/limits [get]
func (h *Handler) Limits(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	list, err := h.svc.Limits(r.Context(), LimitFilter{Query: q.Get("q"), AccountID: q.Get("account")})
	if err != nil {
		response.Internal(w, err)
		return
	}
	out := make([]LimitDTO, 0, len(list))
	for _, l := range list {
		out = append(out, LimitDTO{
			Scope: l.Scope, Key: l.Key, Kind: l.Kind, Email: l.Email, IP: l.IP,
			AccountID: l.AccountID, AccountEmail: l.AccountEmail,
			Burst: l.Burst, EverySeconds: l.Every.Seconds(), OpensAt: l.OpensAt,
		})
	}
	response.JSON(w, http.StatusOK, map[string][]LimitDTO{"limits": out})
}

// ClearLimitRequest names the limit to lift.
type ClearLimitRequest struct {
	Scope string `json:"scope"`
	Key   string `json:"key"`
}

// ClearLimit handles POST /api/v1/admin/limits/clear.
//
//	@Summary		Lift a rate limit
//	@Description	Lets the attempts through again at once, as if the limit had filled up, and records it.
//	@Tags			admin
//	@Accept			json
//	@Param			body	body	ClearLimitRequest	true	"Request body"
//	@Success		204
//	@Failure		401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		403	{object}	response.ErrorEnvelope	"Not an admin, or two-factor is off"
//	@Failure		404	{object}	response.ErrorEnvelope	"The limit has lifted already"
//	@Router			/admin/limits/clear [post]
func (h *Handler) ClearLimit(w http.ResponseWriter, r *http.Request) {
	adminID, ok := request.User(w, r)
	if !ok {
		return
	}
	var req ClearLimitRequest
	if !request.Decode(w, r, &req) {
		return
	}
	err := h.svc.ClearLimit(r.Context(), adminID, req.Scope, req.Key)
	if errors.Is(err, ErrLimitNotFound) {
		response.Error(w, http.StatusNotFound, response.CodeNotFound, "Resource not found")
		return
	}
	if err != nil {
		response.Internal(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
