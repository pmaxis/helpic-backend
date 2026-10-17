package projects

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/pmaxis/helpic/internal/request"
	"github.com/pmaxis/helpic/internal/response"
	"github.com/pmaxis/helpic/internal/user"
)

// Handler exposes the project endpoints over HTTP.
type Handler struct {
	svc *Service
}

// NewHandler creates a projects Handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// Routes mounts the endpoints. Must be called inside an authenticated group.
func (h *Handler) Routes(r chi.Router) {
	r.Route("/projects", func(r chi.Router) {
		r.Get("/", h.List)
		r.Post("/", h.Create)
		r.Get("/{id}", h.Get)
		r.Put("/{id}", h.Update)
		r.Delete("/{id}", h.Delete)
	})
}

// List handles GET /api/v1/projects?with={userID|personal}.
//
//	@Summary	Your projects
//	@Tags		projects
//	@Produce	json
//	@Param		with	query		string					false	"A counterpart's ID, or "personal""
//	@Success	200		{object}	map[string][]DTO		"projects"
//	@Failure	401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Router		/projects [get]
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	userID, role, ok := request.UserRole(w, r)
	if !ok {
		return
	}
	list, err := h.svc.List(r.Context(), userID, role, r.URL.Query().Get("with"))
	if err != nil {
		response.Internal(w, err)
		return
	}
	out := make([]DTO, 0, len(list))
	for _, p := range list {
		out = append(out, ToDTO(p, userID))
	}
	response.JSON(w, http.StatusOK, map[string][]DTO{"projects": out})
}

// Get handles GET /api/v1/projects/{id}.
//
//	@Summary	A project
//	@Tags		projects
//	@Produce	json
//	@Param		id	path		string					true	"Project ID"
//	@Success	200	{object}	map[string]DTO			"project"
//	@Failure	401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure	404	{object}	response.ErrorEnvelope	"Not found"
//	@Router		/projects/{id} [get]
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	userID, _, ok := request.UserRole(w, r)
	if !ok {
		return
	}
	p, err := h.svc.Get(r.Context(), userID, chi.URLParam(r, "id"))
	writeProject(w, http.StatusOK, p, userID, err)
}

// Create handles POST /api/v1/projects.
//
//	@Summary		Start a project
//	@Description	A client names the freelancer; a freelancer the client, or none for a personal project.
//	@Tags			projects
//	@Accept			json
//	@Produce		json
//	@Param			body	body		Request					true	"Request body"
//	@Success		201		{object}	map[string]DTO			"project"
//	@Failure		400		{object}	response.ErrorEnvelope	"Invalid input"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Router			/projects [post]
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	userID, role, ok := request.UserRole(w, r)
	if !ok {
		return
	}
	var req Request
	if !request.Decode(w, r, &req) {
		return
	}
	params, fields := req.Validate(role)
	if len(fields) > 0 {
		response.ValidationError(w, fields)
		return
	}
	p, err := h.svc.Create(r.Context(), userID, role, params)
	if errors.Is(err, ErrNotLinked) {
		if role == user.RoleFreelancer {
			response.ValidationError(w, map[string]string{"client_id": "This isn't one of your clients"})
		} else {
			response.ValidationError(w, map[string]string{"freelancer_id": "You don't work with this freelancer"})
		}
		return
	}
	writeProject(w, http.StatusCreated, p, userID, err)
}

// Update handles PUT /api/v1/projects/{id}.
//
//	@Summary		Edit a project
//	@Description	Its creator only.
//	@Tags			projects
//	@Accept			json
//	@Produce		json
//	@Param			id		path		string					true	"Project ID"
//	@Param			body	body		Request					true	"Request body"
//	@Success		200		{object}	map[string]DTO			"project"
//	@Failure		400		{object}	response.ErrorEnvelope	"Invalid input"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		403		{object}	response.ErrorEnvelope	"Not allowed"
//	@Failure		404		{object}	response.ErrorEnvelope	"Not found"
//	@Router			/projects/{id} [put]
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	userID, _, ok := request.UserRole(w, r)
	if !ok {
		return
	}
	var req Request
	if !request.Decode(w, r, &req) {
		return
	}
	params, fields := req.Validate("")
	if len(fields) > 0 {
		response.ValidationError(w, fields)
		return
	}
	p, err := h.svc.Update(r.Context(), userID, chi.URLParam(r, "id"), params)
	writeProject(w, http.StatusOK, p, userID, err)
}

// Delete handles DELETE /api/v1/projects/{id}.
//
//	@Summary		Delete a project
//	@Description	Its creator only; its tasks stay, out of the project.
//	@Tags			projects
//	@Param			id	path	string	true	"Project ID"
//	@Success		204	"No content"
//	@Failure		401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		403	{object}	response.ErrorEnvelope	"Not allowed"
//	@Failure		404	{object}	response.ErrorEnvelope	"Not found"
//	@Router			/projects/{id} [delete]
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	userID, _, ok := request.UserRole(w, r)
	if !ok {
		return
	}
	if err := h.svc.Delete(r.Context(), userID, chi.URLParam(r, "id")); err != nil {
		writeProject(w, 0, Project{}, "", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeProject(w http.ResponseWriter, status int, p Project, viewerID string, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		response.Error(w, http.StatusNotFound, response.CodeNotFound, "Project not found")
	case errors.Is(err, ErrNotOwner):
		response.Error(w, http.StatusForbidden, response.CodeForbidden, "Only the project's creator can change it")
	case err != nil:
		response.Internal(w, err)
	default:
		response.JSON(w, status, map[string]DTO{"project": ToDTO(p, viewerID)})
	}
}
