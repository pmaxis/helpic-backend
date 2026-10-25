package policies

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/pmaxis/helpic/internal/i18n"
	"github.com/pmaxis/helpic/internal/request"
	"github.com/pmaxis/helpic/internal/response"
)

// Handler exposes the policies over HTTP.
type Handler struct {
	svc *Service
}

// NewHandler creates a policies Handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// PublicRoutes mounts the endpoint anyone may read, signed in or not.
func (h *Handler) PublicRoutes(r chi.Router) {
	r.Get("/policies/{kind}", h.Get)
}

// AdminRoutes mounts the panel's endpoints, inside the admin panel's group.
func (h *Handler) AdminRoutes(r chi.Router) {
	r.Get("/policies", h.All)
	r.Put("/policies/{kind}/{locale}", h.Save)
	r.Delete("/policies/{kind}/{locale}", h.Reset)
}

// PolicyDTO is one policy in one language.
type PolicyDTO struct {
	// Kind is privacy, terms or cookies.
	Kind   string `json:"kind"`
	Locale string `json:"locale"`
	// Body is plain lines: "## " a heading, "- " a list item, an empty line
	// between paragraphs.
	Body string `json:"body"`
	// Custom is whether an admin wrote it; otherwise it is Helpic's own.
	Custom    bool       `json:"custom"`
	UpdatedAt *time.Time `json:"updated_at"`
}

// AdminPolicyDTO is a policy with who last wrote it, for the panel.
type AdminPolicyDTO struct {
	PolicyDTO
	// UpdatedBy is the admin's email; empty for the default or once the
	// account is gone.
	UpdatedBy string `json:"updated_by"`
}

func toDTO(p Policy) PolicyDTO {
	return PolicyDTO{Kind: p.Kind, Locale: p.Locale, Body: p.Body, Custom: p.Custom, UpdatedAt: p.UpdatedAt}
}

func toAdminDTO(p Policy) AdminPolicyDTO {
	return AdminPolicyDTO{PolicyDTO: toDTO(p), UpdatedBy: p.UpdatedBy}
}

// Get handles GET /api/v1/policies/{kind}?locale={en|uk}.
//
//	@Summary		A policy
//	@Description	The privacy policy, the terms of use or the cookie policy, for anyone: as an admin wrote it, or else the text Helpic comes with. In the language asked for, or else the request's.
//	@Tags			policies
//	@Produce		json
//	@Param			kind	path		string					true	"privacy, terms or cookies"
//	@Param			locale	query		string					false	"en or uk; the request's language by default"
//	@Success		200		{object}	map[string]PolicyDTO	"policy"
//	@Failure		404		{object}	response.ErrorEnvelope	"No such policy or language"
//	@Router			/policies/{kind} [get]
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	locale := r.URL.Query().Get("locale")
	if locale == "" {
		locale = i18n.FromContext(r.Context())
	}
	p, err := h.svc.Get(r.Context(), chi.URLParam(r, "kind"), locale)
	if errors.Is(err, ErrUnknown) {
		response.Error(w, http.StatusNotFound, response.CodeNotFound, "Resource not found")
		return
	}
	if err != nil {
		response.Internal(w, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]PolicyDTO{"policy": toDTO(p)})
}

// All handles GET /api/v1/admin/policies.
//
//	@Summary		The policies
//	@Description	Every policy in every language, with whether an admin wrote it, when and who.
//	@Tags			admin
//	@Produce		json
//	@Success		200	{object}	map[string][]AdminPolicyDTO	"policies"
//	@Failure		401	{object}	response.ErrorEnvelope		"Not signed in"
//	@Failure		403	{object}	response.ErrorEnvelope		"Not an admin, or two-factor is off"
//	@Router			/admin/policies [get]
func (h *Handler) All(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.All(r.Context())
	if err != nil {
		response.Internal(w, err)
		return
	}
	out := make([]AdminPolicyDTO, 0, len(list))
	for _, p := range list {
		out = append(out, toAdminDTO(p))
	}
	response.JSON(w, http.StatusOK, map[string][]AdminPolicyDTO{"policies": out})
}

// SaveRequest is a policy's new text.
type SaveRequest struct {
	Body string `json:"body"`
}

// Save handles PUT /api/v1/admin/policies/{kind}/{locale}.
//
//	@Summary		Write a policy
//	@Description	Replaces a policy's text in one language; readers see it at once.
//	@Tags			admin
//	@Accept			json
//	@Produce		json
//	@Param			kind	path		string						true	"privacy, terms or cookies"
//	@Param			locale	path		string						true	"en or uk"
//	@Param			body	body		SaveRequest					true	"The text"
//	@Success		200		{object}	map[string]AdminPolicyDTO	"policy"
//	@Failure		400		{object}	response.ErrorEnvelope		"Empty or too long"
//	@Failure		401		{object}	response.ErrorEnvelope		"Not signed in"
//	@Failure		403		{object}	response.ErrorEnvelope		"Not an admin, or two-factor is off"
//	@Failure		404		{object}	response.ErrorEnvelope		"No such policy or language"
//	@Router			/admin/policies/{kind}/{locale} [put]
func (h *Handler) Save(w http.ResponseWriter, r *http.Request) {
	adminID, ok := request.User(w, r)
	if !ok {
		return
	}
	var req SaveRequest
	if !request.Decode(w, r, &req) {
		return
	}
	body := strings.TrimSpace(strings.ReplaceAll(req.Body, "\r\n", "\n"))
	switch {
	case body == "":
		response.ValidationError(w, map[string]string{"body": "Write the policy, or put the default back"})
		return
	case len(body) > MaxBody:
		response.ValidationError(w, map[string]string{"body": "The policy is too long"})
		return
	}
	p, err := h.svc.Save(r.Context(), adminID, chi.URLParam(r, "kind"), chi.URLParam(r, "locale"), body)
	h.answer(w, p, err)
}

// Reset handles DELETE /api/v1/admin/policies/{kind}/{locale}.
//
//	@Summary		Put a policy's default back
//	@Description	Forgets the admin's text in one language: readers see the one Helpic comes with.
//	@Tags			admin
//	@Produce		json
//	@Param			kind	path		string						true	"privacy, terms or cookies"
//	@Param			locale	path		string						true	"en or uk"
//	@Success		200		{object}	map[string]AdminPolicyDTO	"policy"
//	@Failure		401		{object}	response.ErrorEnvelope		"Not signed in"
//	@Failure		403		{object}	response.ErrorEnvelope		"Not an admin, or two-factor is off"
//	@Failure		404		{object}	response.ErrorEnvelope		"No such policy or language"
//	@Router			/admin/policies/{kind}/{locale} [delete]
func (h *Handler) Reset(w http.ResponseWriter, r *http.Request) {
	p, err := h.svc.Reset(r.Context(), chi.URLParam(r, "kind"), chi.URLParam(r, "locale"))
	h.answer(w, p, err)
}

func (h *Handler) answer(w http.ResponseWriter, p Policy, err error) {
	if errors.Is(err, ErrUnknown) {
		response.Error(w, http.StatusNotFound, response.CodeNotFound, "Resource not found")
		return
	}
	if err != nil {
		response.Internal(w, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]AdminPolicyDTO{"policy": toAdminDTO(p)})
}
