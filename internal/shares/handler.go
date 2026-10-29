package shares

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/pmaxis/helpic/internal/attachments"
	"github.com/pmaxis/helpic/internal/request"
	"github.com/pmaxis/helpic/internal/response"
	"github.com/pmaxis/helpic/internal/tasks"
)

// Handler exposes task links over HTTP.
type Handler struct {
	svc *Service
}

// NewHandler creates a shares Handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// TaskRoutes mounts managing a task's link, inside the authenticated
// /tasks route.
func (h *Handler) TaskRoutes(r chi.Router) {
	r.Get("/{id}/share", h.Get)
	r.Post("/{id}/share", h.Create)
	r.Delete("/{id}/share", h.Delete)
}

// PublicRoutes mounts what a link opens, for anyone holding it.
func (h *Handler) PublicRoutes(r chi.Router) {
	r.Get("/shared/{token}", h.Shared)
	r.Get("/shared/{token}/files/{id}", h.File)
}

// Get handles GET /api/v1/tasks/{id}/share.
//
//	@Summary		A task's public link
//	@Description	Its creator or its freelancer only. share is null when the task has none.
//	@Tags			shares
//	@Produce		json
//	@Param			id	path		string					true	"Task ID"
//	@Success		200	{object}	Response				"OK"
//	@Failure		401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		403	{object}	response.ErrorEnvelope	"Not the creator or the freelancer"
//	@Failure		404	{object}	response.ErrorEnvelope	"Not found"
//	@Router			/tasks/{id}/share [get]
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	sh, err := h.svc.Get(r.Context(), userID, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, err)
		return
	}
	out := Response{}
	if sh != nil {
		out.Share = &DTO{URL: h.svc.URL(*sh), CreatedAt: sh.CreatedAt}
	}
	response.JSON(w, http.StatusOK, out)
}

// Create handles POST /api/v1/tasks/{id}/share.
//
//	@Summary		Share a task by a link
//	@Description	Turns on a link anyone may open, without an account, to read the task, its subtasks and its files; not its people, price, time or comments. Its creator or its freelancer only, archived or not. A task that has a link keeps it (200).
//	@Tags			shares
//	@Produce		json
//	@Param			id	path		string					true	"Task ID"
//	@Success		201	{object}	Response				"Turned on"
//	@Success		200	{object}	Response				"Already on"
//	@Failure		401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		403	{object}	response.ErrorEnvelope	"Not the creator or the freelancer"
//	@Failure		404	{object}	response.ErrorEnvelope	"Not found"
//	@Router			/tasks/{id}/share [post]
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	sh, created, err := h.svc.Create(r.Context(), userID, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	response.JSON(w, status, Response{Share: &DTO{URL: h.svc.URL(sh), CreatedAt: sh.CreatedAt}})
}

// Delete handles DELETE /api/v1/tasks/{id}/share.
//
//	@Summary		Turn a task's link off
//	@Description	The link opens nothing from now on; sharing again makes a new one. Its creator or its freelancer only.
//	@Tags			shares
//	@Param			id	path	string	true	"Task ID"
//	@Success		204	"No content"
//	@Failure		401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		403	{object}	response.ErrorEnvelope	"Not the creator or the freelancer"
//	@Failure		404	{object}	response.ErrorEnvelope	"Not found"
//	@Router			/tasks/{id}/share [delete]
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	if err := h.svc.Delete(r.Context(), userID, chi.URLParam(r, "id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Shared handles GET /api/v1/shared/{token}.
//
//	@Summary		A shared task
//	@Description	No sign-in: the link is the proof. The task, its subtasks and its files.
//	@Tags			shares
//	@Produce		json
//	@Param			token	path		string					true	"The link's token"
//	@Success		200		{object}	SharedResponse			"OK"
//	@Failure		404		{object}	response.ErrorEnvelope	"No such link, or it was turned off"
//	@Failure		429		{object}	response.ErrorEnvelope	"Too many requests"
//	@Router			/shared/{token} [get]
func (h *Handler) Shared(w http.ResponseWriter, r *http.Request) {
	t, subtasks, files, err := h.svc.Shared(r.Context(), chi.URLParam(r, "token"))
	if err != nil {
		writeError(w, err)
		return
	}
	// Not kept anywhere on the way: the link may be turned off any moment.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex")
	response.JSON(w, http.StatusOK, SharedResponse{Task: toTaskDTO(t, subtasks, files)})
}

// File handles GET /api/v1/shared/{token}/files/{id}.
//
//	@Summary		A file of a shared task
//	@Description	No sign-in. Always as a download, except PNG/JPEG/GIF/WebP images with inline=1; a file still being checked for viruses, or found infected, answers 409.
//	@Tags			shares
//	@Produce		octet-stream
//	@Param			token	path		string					true	"The link's token"
//	@Param			id		path		string					true	"File ID"
//	@Param			inline	query		string					false	"1 to show a safe image in place"
//	@Success		200		{file}		file					"The file"
//	@Failure		404		{object}	response.ErrorEnvelope	"Not found"
//	@Failure		409		{object}	response.ErrorEnvelope	"Being checked for viruses, or infected"
//	@Failure		429		{object}	response.ErrorEnvelope	"Too many requests"
//	@Router			/shared/{token}/files/{id} [get]
func (h *Handler) File(w http.ResponseWriter, r *http.Request) {
	a, f, err := h.svc.OpenFile(r.Context(), chi.URLParam(r, "token"), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, err)
		return
	}
	defer f.Close()
	w.Header().Set("X-Robots-Tag", "noindex")
	attachments.Serve(w, r, a, f)
}

func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		response.Error(w, http.StatusNotFound, response.CodeNotFound, "This link doesn't open anything: it was turned off, or it's mistyped")
	case errors.Is(err, tasks.ErrNotFound):
		response.Error(w, http.StatusNotFound, response.CodeNotFound, "Task not found")
	case errors.Is(err, ErrNotAllowed):
		response.Error(w, http.StatusForbidden, response.CodeForbidden, "Only whoever created the task, or its freelancer, can share it")
	default:
		attachments.WriteError(w, err)
	}
}
