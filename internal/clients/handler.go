package clients

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/pmaxis/helpic/internal/auth"
	"github.com/pmaxis/helpic/internal/request"
	"github.com/pmaxis/helpic/internal/response"
	"github.com/pmaxis/helpic/internal/user"
)

// Handler exposes invitations and freelancer–client links over HTTP.
type Handler struct {
	svc    *Service
	repo   *Repository
	cookie auth.CookieConfig
	// breaches turns away new passwords known from a breach; nil allows them.
	breaches auth.BreachChecker
}

// RefuseBreached turns away the password of a new client account when the
// checker knows it from a breach.
func (h *Handler) RefuseBreached(breaches auth.BreachChecker) {
	h.breaches = breaches
}

// NewHandler creates a clients Handler. The cookie config is used to sign the
// client in after accepting an invitation.
func NewHandler(svc *Service, repo *Repository, cookie auth.CookieConfig) *Handler {
	return &Handler{svc: svc, repo: repo, cookie: cookie}
}

// PublicRoutes mounts the invitation page endpoints, which need no session:
// a client's page for a freelancer's invitation, and a freelancer's for a
// client's.
func (h *Handler) PublicRoutes(r chi.Router) {
	r.Post("/invites/lookup", h.FindInvite)
	r.Post("/invites/accept", h.AcceptInvite)
	r.Post("/freelancer-invites/lookup", h.FindFreelancerInvite)
	r.Post("/freelancer-invites/accept", h.AcceptFreelancerInvite)
}

// FreelancerRoutes mounts the freelancer-only endpoints. guard limits the
// invitations sent, each an email to an address the freelancer types.
func (h *Handler) FreelancerRoutes(r chi.Router, guard func(http.Handler) http.Handler) {
	r.Get("/clients", h.ListClients)
	r.Route("/invitations", func(r chi.Router) {
		r.Get("/", h.ListInvitations)
		r.With(guard).Post("/", h.Invite)
		r.Delete("/{id}", h.DeleteInvitation)
	})
}

// ClientRoutes mounts the client-only endpoints. guard limits the
// invitations sent, each an email to an address the client types.
func (h *Handler) ClientRoutes(r chi.Router, guard func(http.Handler) http.Handler) {
	r.Get("/freelancers", h.ListFreelancers)
	r.Route("/freelancer-invitations", func(r chi.Router) {
		r.Get("/", h.ListFreelancerInvitations)
		r.With(guard).Post("/", h.InviteFreelancer)
		r.Delete("/{id}", h.DeleteFreelancerInvitation)
	})
}

// ListClients handles GET /api/v1/clients.
//
//	@Summary		Your clients
//	@Description	Freelancers only. Each with their open task count.
//	@Tags			people
//	@Produce		json
//	@Success		200	{object}	map[string][]LinkDTO	"clients"
//	@Failure		401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		403	{object}	response.ErrorEnvelope	"Not allowed"
//	@Router			/clients [get]
func (h *Handler) ListClients(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	links, err := h.repo.ListClients(r.Context(), userID)
	if err != nil {
		response.Internal(w, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string][]LinkDTO{"clients": toLinkDTOs(links)})
}

// ListFreelancers handles GET /api/v1/freelancers.
//
//	@Summary		Your freelancers
//	@Description	Clients only; a team member sees the company owner's.
//	@Tags			people
//	@Produce		json
//	@Success		200	{object}	map[string][]LinkDTO	"freelancers"
//	@Failure		401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		403	{object}	response.ErrorEnvelope	"Not allowed"
//	@Router			/freelancers [get]
func (h *Handler) ListFreelancers(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	links, err := h.repo.ListFreelancers(r.Context(), userID)
	if err != nil {
		response.Internal(w, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string][]LinkDTO{"freelancers": toLinkDTOs(links)})
}

// ListInvitations handles GET /api/v1/invitations.
//
//	@Summary	Pending client invitations
//	@Tags		people
//	@Produce	json
//	@Success	200	{object}	map[string][]InvitationDTO	"invitations"
//	@Failure	401	{object}	response.ErrorEnvelope		"Not signed in"
//	@Failure	403	{object}	response.ErrorEnvelope		"Not allowed"
//	@Router		/invitations [get]
func (h *Handler) ListInvitations(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	invs, err := h.repo.ListInvitations(r.Context(), userID)
	if err != nil {
		response.Internal(w, err)
		return
	}
	out := make([]InvitationDTO, 0, len(invs))
	for _, i := range invs {
		out = append(out, toInvitationDTO(i))
	}
	response.JSON(w, http.StatusOK, map[string][]InvitationDTO{"invitations": out})
}

// Invite handles POST /api/v1/invitations.
//
//	@Summary		Invite a client
//	@Description	Emails the invitation link to the client; only they get it.
//	@Tags			people
//	@Accept			json
//	@Produce		json
//	@Param			body	body		InviteRequest				true	"Request body"
//	@Success		201		{object}	map[string]InvitationDTO	"invitation"
//	@Failure		400		{object}	response.ErrorEnvelope		"Invalid input"
//	@Failure		401		{object}	response.ErrorEnvelope		"Not signed in"
//	@Failure		403		{object}	response.ErrorEnvelope		"Not allowed"
//	@Router			/invitations [post]
func (h *Handler) Invite(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	var req InviteRequest
	if !request.Decode(w, r, &req) {
		return
	}
	params, fields := req.Validate()
	if len(fields) > 0 {
		response.ValidationError(w, fields)
		return
	}

	inv, err := h.svc.Invite(r.Context(), userID, params)
	switch {
	case errors.Is(err, ErrSelfInvite):
		response.ValidationError(w, map[string]string{"email": "You can't invite yourself"})
	case errors.Is(err, ErrNotClientEmail):
		response.ValidationError(w, map[string]string{"email": "This email belongs to a freelancer account"})
	case errors.Is(err, ErrTeamMember):
		response.ValidationError(w, map[string]string{"email": "This person is on a client's team; they work through that client"})
	case errors.Is(err, ErrAlreadyClient):
		response.ValidationError(w, map[string]string{"email": "This person is already your client"})
	case errors.Is(err, ErrAlreadyInvited):
		response.ValidationError(w, map[string]string{"email": "An invitation for this email is already pending"})
	case err != nil:
		response.Internal(w, err)
	default:
		response.JSON(w, http.StatusCreated, map[string]InvitationDTO{"invitation": toInvitationDTO(inv)})
	}
}

// DeleteInvitation handles DELETE /api/v1/invitations/{id}.
//
//	@Summary	Revoke a client invitation
//	@Tags		people
//	@Param		id	path	string	true	"Invitation ID"
//	@Success	204	"No content"
//	@Failure	401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure	403	{object}	response.ErrorEnvelope	"Not allowed"
//	@Failure	404	{object}	response.ErrorEnvelope	"Not found"
//	@Router		/invitations/{id} [delete]
func (h *Handler) DeleteInvitation(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	err := h.repo.DeleteInvitation(r.Context(), userID, chi.URLParam(r, "id"))
	if errors.Is(err, ErrNotFound) {
		response.Error(w, http.StatusNotFound, response.CodeNotFound, "Invitation not found")
		return
	}
	if err != nil {
		response.Internal(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// FindInvite handles POST /api/v1/invites/lookup.
//
//	@Summary		Look up a client invitation
//	@Description	Public: the invitation page.
//	@Tags			invitations
//	@Accept			json
//	@Produce		json
//	@Param			body	body		LookupRequest			true	"Request body"
//	@Success		200		{object}	map[string]LookupDTO	"invitation"
//	@Failure		404		{object}	response.ErrorEnvelope	"Invalid, used or expired"
//	@Router			/invites/lookup [post]
func (h *Handler) FindInvite(w http.ResponseWriter, r *http.Request) {
	var req LookupRequest
	if !request.Decode(w, r, &req) {
		return
	}
	l, ok := h.lookup(w, r, req.Token)
	if !ok {
		return
	}
	response.JSON(w, http.StatusOK, map[string]LookupDTO{"invitation": {
		Email:         l.Invitation.Email,
		Name:          l.Invitation.Name,
		Freelancer:    toPartyDTO(l.Freelancer),
		AccountExists: l.AccountExists,
		ExpiresAt:     l.Invitation.ExpiresAt,
	}})
}

// AcceptInvite handles POST /api/v1/invites/accept. It creates the client
// account (or signs in to the existing one), links it with the freelancer and
// starts a session.
//
//	@Summary		Accept a client invitation
//	@Description	Public. Creates the client account (name, password, confirmation) or signs in to the existing one (password, and a code when it has two-factor on), links it with the freelancer and sets the auth cookie.
//	@Tags			invitations
//	@Accept			json
//	@Produce		json
//	@Param			body	body		AcceptRequest			true	"Request body"
//	@Success		200		{object}	user.Response			"OK"
//	@Failure		400		{object}	response.ErrorEnvelope	"Invalid input, or a wrong code (field code)"
//	@Failure		403		{object}	response.ErrorEnvelope	"The password is right; the account also needs a code (TWO_FACTOR_REQUIRED)"
//	@Failure		404		{object}	response.ErrorEnvelope	"Invalid, used or expired"
//	@Failure		429		{object}	response.ErrorEnvelope	"Too many wrong codes for this account"
//	@Router			/invites/accept [post]
func (h *Handler) AcceptInvite(w http.ResponseWriter, r *http.Request) {
	var req AcceptRequest
	if !request.Decode(w, r, &req) {
		return
	}
	l, ok := h.lookup(w, r, req.Token)
	if !ok {
		return
	}

	var (
		session Session
		err     error
	)
	if l.AccountExists {
		if req.Password == "" {
			response.ValidationError(w, map[string]string{"password": "Password is required"})
			return
		}
		session, err = h.svc.AcceptExisting(r.Context(), l, req.Password, req.Code, auth.ClientOf(r))
	} else {
		params, fields := req.ValidateNew()
		if len(fields) > 0 {
			response.ValidationError(w, fields)
			return
		}
		if msg := auth.BreachedPassword(r.Context(), h.breaches, params.Password); msg != "" {
			response.ValidationError(w, map[string]string{"password": msg})
			return
		}
		session, err = h.svc.AcceptNew(r.Context(), l, params, auth.ClientOf(r))
	}

	if auth.WriteSignInError(w, err) {
		return
	}
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials):
		response.ValidationError(w, map[string]string{"password": "Wrong password"})
	case errors.Is(err, auth.ErrEmailExists):
		response.Error(w, http.StatusConflict, response.CodeEmailExists, "An account with this email already exists. Reload the page and sign in.")
	case errors.Is(err, ErrInvitationInvalid), errors.Is(err, ErrNotClientEmail):
		writeInvalidInvite(w)
	case err != nil:
		response.Internal(w, err)
	default:
		h.cookie.Set(w, session.Tokens)
		response.JSON(w, http.StatusOK, user.NewResponse(session.User))
	}
}

func (h *Handler) lookup(w http.ResponseWriter, r *http.Request, token string) (Lookup, bool) {
	l, err := h.svc.Find(r.Context(), token)
	if errors.Is(err, ErrInvitationInvalid) || errors.Is(err, ErrNotClientEmail) {
		writeInvalidInvite(w)
		return Lookup{}, false
	}
	if err != nil {
		response.Internal(w, err)
		return Lookup{}, false
	}
	return l, true
}

func writeInvalidInvite(w http.ResponseWriter) {
	response.Error(w, http.StatusNotFound, response.CodeNotFound, "This invitation is invalid, already used or expired")
}

// ListFreelancerInvitations handles GET /api/v1/freelancer-invitations.
//
//	@Summary		Pending freelancer invitations
//	@Description	Clients only, and not a company's members: the owner invites.
//	@Tags			people
//	@Produce		json
//	@Success		200	{object}	map[string][]InvitationDTO	"invitations"
//	@Failure		401	{object}	response.ErrorEnvelope		"Not signed in"
//	@Failure		403	{object}	response.ErrorEnvelope		"Not allowed"
//	@Router			/freelancer-invitations [get]
func (h *Handler) ListFreelancerInvitations(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	invs, err := h.svc.FreelancerInvitations(r.Context(), userID)
	if errors.Is(err, ErrNotOwner) {
		writeNotOwner(w)
		return
	}
	if err != nil {
		response.Internal(w, err)
		return
	}
	out := make([]InvitationDTO, 0, len(invs))
	for _, i := range invs {
		out = append(out, toFreelancerInvitationDTO(i))
	}
	response.JSON(w, http.StatusOK, map[string][]InvitationDTO{"invitations": out})
}

// InviteFreelancer handles POST /api/v1/freelancer-invitations.
//
//	@Summary		Invite a freelancer
//	@Description	Clients only, and not a company's members. Emails the invitation link to the freelancer; only they get it.
//	@Tags			people
//	@Accept			json
//	@Produce		json
//	@Param			body	body		InviteRequest				true	"Request body"
//	@Success		201		{object}	map[string]InvitationDTO	"invitation"
//	@Failure		400		{object}	response.ErrorEnvelope		"Invalid input"
//	@Failure		401		{object}	response.ErrorEnvelope		"Not signed in"
//	@Failure		403		{object}	response.ErrorEnvelope		"Not allowed"
//	@Router			/freelancer-invitations [post]
func (h *Handler) InviteFreelancer(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	var req InviteRequest
	if !request.Decode(w, r, &req) {
		return
	}
	params, fields := req.Validate()
	if len(fields) > 0 {
		response.ValidationError(w, fields)
		return
	}

	inv, err := h.svc.InviteFreelancer(r.Context(), userID, params)
	switch {
	case errors.Is(err, ErrNotOwner):
		writeNotOwner(w)
	case errors.Is(err, ErrSelfInvite):
		response.ValidationError(w, map[string]string{"email": "You can't invite yourself"})
	case errors.Is(err, ErrNotFreelancerEmail):
		response.ValidationError(w, map[string]string{"email": "This email belongs to a client's account"})
	case errors.Is(err, ErrAlreadyFreelancer):
		response.ValidationError(w, map[string]string{"email": "You already work with this freelancer"})
	case errors.Is(err, ErrAlreadyInvited):
		response.ValidationError(w, map[string]string{"email": "An invitation for this email is already pending"})
	case err != nil:
		response.Internal(w, err)
	default:
		response.JSON(w, http.StatusCreated, map[string]InvitationDTO{"invitation": toFreelancerInvitationDTO(inv)})
	}
}

// DeleteFreelancerInvitation handles DELETE /api/v1/freelancer-invitations/{id}.
//
//	@Summary	Revoke a freelancer invitation
//	@Tags		people
//	@Param		id	path	string	true	"Invitation ID"
//	@Success	204	"No content"
//	@Failure	401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure	403	{object}	response.ErrorEnvelope	"Not allowed"
//	@Failure	404	{object}	response.ErrorEnvelope	"Not found"
//	@Router		/freelancer-invitations/{id} [delete]
func (h *Handler) DeleteFreelancerInvitation(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	err := h.repo.DeleteFreelancerInvitation(r.Context(), userID, chi.URLParam(r, "id"))
	if errors.Is(err, ErrNotFound) {
		response.Error(w, http.StatusNotFound, response.CodeNotFound, "Invitation not found")
		return
	}
	if err != nil {
		response.Internal(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// FindFreelancerInvite handles POST /api/v1/freelancer-invites/lookup.
//
//	@Summary		Look up a freelancer invitation
//	@Description	Public: the page a client's invitation opens.
//	@Tags			invitations
//	@Accept			json
//	@Produce		json
//	@Param			body	body		LookupRequest					true	"Request body"
//	@Success		200		{object}	map[string]FreelancerLookupDTO	"invitation"
//	@Failure		404		{object}	response.ErrorEnvelope			"Invalid, used or expired"
//	@Router			/freelancer-invites/lookup [post]
func (h *Handler) FindFreelancerInvite(w http.ResponseWriter, r *http.Request) {
	var req LookupRequest
	if !request.Decode(w, r, &req) {
		return
	}
	l, ok := h.lookupFreelancer(w, r, req.Token)
	if !ok {
		return
	}
	response.JSON(w, http.StatusOK, map[string]FreelancerLookupDTO{"invitation": {
		Email:         l.Invitation.Email,
		Name:          l.Invitation.Name,
		Client:        toPartyDTO(l.Client),
		Company:       l.Company,
		AccountExists: l.AccountExists,
		ExpiresAt:     l.Invitation.ExpiresAt,
	}})
}

// AcceptFreelancerInvite handles POST /api/v1/freelancer-invites/accept. It
// creates the freelancer account (or signs in to the existing one), links it
// with the client and starts a session.
//
//	@Summary		Accept a freelancer invitation
//	@Description	Public. Creates the freelancer account (name, password, confirmation) or signs in to the existing one (password, and a code when it has two-factor on), links it with the client and sets the auth cookie.
//	@Tags			invitations
//	@Accept			json
//	@Produce		json
//	@Param			body	body		AcceptRequest			true	"Request body"
//	@Success		200		{object}	user.Response			"OK"
//	@Failure		400		{object}	response.ErrorEnvelope	"Invalid input, or a wrong code (field code)"
//	@Failure		403		{object}	response.ErrorEnvelope	"The password is right; the account also needs a code (TWO_FACTOR_REQUIRED)"
//	@Failure		404		{object}	response.ErrorEnvelope	"Invalid, used or expired"
//	@Failure		429		{object}	response.ErrorEnvelope	"Too many wrong codes for this account"
//	@Router			/freelancer-invites/accept [post]
func (h *Handler) AcceptFreelancerInvite(w http.ResponseWriter, r *http.Request) {
	var req AcceptRequest
	if !request.Decode(w, r, &req) {
		return
	}
	l, ok := h.lookupFreelancer(w, r, req.Token)
	if !ok {
		return
	}

	var (
		session Session
		err     error
	)
	if l.AccountExists {
		if req.Password == "" {
			response.ValidationError(w, map[string]string{"password": "Password is required"})
			return
		}
		session, err = h.svc.AcceptExistingFreelancer(r.Context(), l, req.Password, req.Code, auth.ClientOf(r))
	} else {
		params, fields := req.ValidateNew()
		if len(fields) > 0 {
			response.ValidationError(w, fields)
			return
		}
		if msg := auth.BreachedPassword(r.Context(), h.breaches, params.Password); msg != "" {
			response.ValidationError(w, map[string]string{"password": msg})
			return
		}
		session, err = h.svc.AcceptNewFreelancer(r.Context(), l, params, auth.ClientOf(r))
	}

	if auth.WriteSignInError(w, err) {
		return
	}
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials):
		response.ValidationError(w, map[string]string{"password": "Wrong password"})
	case errors.Is(err, auth.ErrEmailExists):
		response.Error(w, http.StatusConflict, response.CodeEmailExists, "An account with this email already exists. Reload the page and sign in.")
	case errors.Is(err, ErrInvitationInvalid), errors.Is(err, ErrNotFreelancerEmail):
		writeInvalidInvite(w)
	case err != nil:
		response.Internal(w, err)
	default:
		h.cookie.Set(w, session.Tokens)
		response.JSON(w, http.StatusOK, user.NewResponse(session.User))
	}
}

func (h *Handler) lookupFreelancer(w http.ResponseWriter, r *http.Request, token string) (FreelancerLookup, bool) {
	l, err := h.svc.FindFreelancerInvite(r.Context(), token)
	if errors.Is(err, ErrInvitationInvalid) || errors.Is(err, ErrNotFreelancerEmail) {
		writeInvalidInvite(w)
		return FreelancerLookup{}, false
	}
	if err != nil {
		response.Internal(w, err)
		return FreelancerLookup{}, false
	}
	return l, true
}

func writeNotOwner(w http.ResponseWriter) {
	response.Error(w, http.StatusForbidden, response.CodeForbidden, "Only the company's owner invites freelancers")
}
