package team

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/pmaxis/helpic/internal/auth"
	"github.com/pmaxis/helpic/internal/request"
	"github.com/pmaxis/helpic/internal/response"
	"github.com/pmaxis/helpic/internal/user"
)

// Handler exposes the team endpoints over HTTP.
type Handler struct {
	svc    *Service
	cookie auth.CookieConfig
}

// NewHandler creates a team Handler; the cookie config signs a new member in.
func NewHandler(svc *Service, cookie auth.CookieConfig) *Handler {
	return &Handler{svc: svc, cookie: cookie}
}

// ClientRoutes mounts the endpoints for clients. Must be called inside an
// authenticated, client-only group.
func (h *Handler) ClientRoutes(r chi.Router) {
	r.Route("/team", func(r chi.Router) {
		r.Get("/", h.Overview)
		r.Put("/", h.Rename)
		r.Post("/invites", h.Invite)
		r.Delete("/invites/{id}", h.RevokeInvite)
		r.Delete("/members/{id}", h.RemoveMember)
	})
}

// PublicRoutes mounts the join page endpoints, which need no session.
func (h *Handler) PublicRoutes(r chi.Router) {
	r.Get("/team-invites/{token}", h.FindInvite)
	r.Post("/team-invites/{token}/accept", h.Join)
}

// Overview handles GET /api/v1/team.
//
//	@Summary		Your company and its team
//	@Description	Clients only. The owner also gets the members and pending invitations.
//	@Tags			team
//	@Produce		json
//	@Success		200	{object}	map[string]OverviewDTO	"team"
//	@Failure		401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		403	{object}	response.ErrorEnvelope	"Not allowed"
//	@Router			/team [get]
func (h *Handler) Overview(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	o, err := h.svc.Overview(r.Context(), userID)
	writeOverview(w, o, err)
}

// Rename handles PUT /api/v1/team.
//
//	@Summary	Name your company
//	@Tags		team
//	@Accept		json
//	@Produce	json
//	@Param		body	body		NameRequest				true	"Request body"
//	@Success	200		{object}	map[string]OverviewDTO	"team"
//	@Failure	400		{object}	response.ErrorEnvelope	"Invalid input"
//	@Failure	401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure	403		{object}	response.ErrorEnvelope	"Members don't run the team"
//	@Router		/team [put]
func (h *Handler) Rename(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	var req NameRequest
	if !request.Decode(w, r, &req) {
		return
	}
	name, fields := req.Validate()
	if len(fields) > 0 {
		response.ValidationError(w, fields)
		return
	}
	o, err := h.svc.Rename(r.Context(), userID, name)
	writeOverview(w, o, err)
}

// Invite handles POST /api/v1/team/invites.
//
//	@Summary		Invite someone to your team
//	@Description	They join with a new account through /join/{token}.
//	@Tags			team
//	@Accept			json
//	@Produce		json
//	@Param			body	body		InviteRequest			true	"Request body"
//	@Success		201		{object}	map[string]InviteDTO	"invite"
//	@Failure		400		{object}	response.ErrorEnvelope	"Invalid input"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		403		{object}	response.ErrorEnvelope	"Members don't run the team"
//	@Router			/team/invites [post]
func (h *Handler) Invite(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	var req InviteRequest
	if !request.Decode(w, r, &req) {
		return
	}
	p, fields := req.Validate()
	if len(fields) > 0 {
		response.ValidationError(w, fields)
		return
	}
	inv, err := h.svc.Invite(r.Context(), userID, p)
	switch {
	case errors.Is(err, ErrAccountExists):
		response.ValidationError(w, map[string]string{"email": "This email already has a Helpic account — invite a new one"})
	case errors.Is(err, ErrAlreadyInvited):
		response.ValidationError(w, map[string]string{"email": "An invitation for this email is already pending"})
	case err != nil:
		writeError(w, err)
	default:
		response.JSON(w, http.StatusCreated, map[string]InviteDTO{"invite": toInviteDTO(inv)})
	}
}

// RevokeInvite handles DELETE /api/v1/team/invites/{id}.
//
//	@Summary	Revoke a team invitation
//	@Tags		team
//	@Param		id	path	string	true	"Invitation ID"
//	@Success	204	"No content"
//	@Failure	401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure	403	{object}	response.ErrorEnvelope	"Not allowed"
//	@Failure	404	{object}	response.ErrorEnvelope	"Not found"
//	@Router		/team/invites/{id} [delete]
func (h *Handler) RevokeInvite(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	writeDone(w, h.svc.RevokeInvite(r.Context(), userID, chi.URLParam(r, "id")))
}

// RemoveMember handles DELETE /api/v1/team/members/{id}.
//
//	@Summary	Remove a team member
//	@Tags		team
//	@Param		id	path	string	true	"Member's user ID"
//	@Success	204	"No content"
//	@Failure	401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure	403	{object}	response.ErrorEnvelope	"Not allowed"
//	@Failure	404	{object}	response.ErrorEnvelope	"Not found"
//	@Router		/team/members/{id} [delete]
func (h *Handler) RemoveMember(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	writeDone(w, h.svc.RemoveMember(r.Context(), userID, chi.URLParam(r, "id")))
}

// FindInvite handles GET /api/v1/team-invites/{token}.
//
//	@Summary		Look up a team invitation
//	@Description	Public: the join page.
//	@Tags			invitations
//	@Produce		json
//	@Param			token	path		string					true	"Invitation token"
//	@Success		200		{object}	map[string]LookupDTO	"invite"
//	@Failure		404		{object}	response.ErrorEnvelope	"Invalid, used or expired"
//	@Router			/team-invites/{token} [get]
func (h *Handler) FindInvite(w http.ResponseWriter, r *http.Request) {
	l, err := h.svc.Find(r.Context(), chi.URLParam(r, "token"))
	if err != nil {
		writeError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]LookupDTO{"invite": {
		Email:     l.Invite.Email,
		Name:      l.Invite.Name,
		Company:   l.Company,
		Owner:     PersonDTO(l.Owner),
		ExpiresAt: l.Invite.ExpiresAt,
	}})
}

// Join handles POST /api/v1/team-invites/{token}/accept: it creates the member's
// account, adds it to the company and starts a session.
//
//	@Summary		Join a team
//	@Description	Public. Creates the member's account, adds it to the company and sets the auth cookie.
//	@Tags			invitations
//	@Accept			json
//	@Produce		json
//	@Param			token	path		string					true	"Invitation token"
//	@Param			body	body		JoinRequest				true	"Request body"
//	@Success		200		{object}	user.Response			"OK"
//	@Failure		400		{object}	response.ErrorEnvelope	"Invalid input"
//	@Failure		404		{object}	response.ErrorEnvelope	"Invalid, used or expired"
//	@Router			/team-invites/{token}/accept [post]
func (h *Handler) Join(w http.ResponseWriter, r *http.Request) {
	var req JoinRequest
	if !request.Decode(w, r, &req) {
		return
	}
	p, fields := req.Validate()
	if len(fields) > 0 {
		response.ValidationError(w, fields)
		return
	}
	session, err := h.svc.Join(r.Context(), chi.URLParam(r, "token"), p, auth.ClientOf(r))
	switch {
	case errors.Is(err, auth.ErrEmailExists):
		writeError(w, ErrInviteInvalid)
	case err != nil:
		writeError(w, err)
	default:
		h.cookie.Set(w, session.Tokens)
		response.JSON(w, http.StatusOK, user.NewResponse(session.User))
	}
}

func writeOverview(w http.ResponseWriter, o Overview, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]OverviewDTO{"team": toOverviewDTO(o)})
}

func writeDone(w http.ResponseWriter, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotOwner):
		response.Error(w, http.StatusForbidden, response.CodeForbidden, "Only the company's owner manages the team")
	case errors.Is(err, ErrInviteInvalid):
		response.Error(w, http.StatusNotFound, response.CodeNotFound, "This invitation is invalid, already used or expired")
	case errors.Is(err, ErrNotFound):
		response.Error(w, http.StatusNotFound, response.CodeNotFound, "Not found")
	default:
		response.Internal(w, err)
	}
}
