package comments

import (
	"errors"
	"maps"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/pmaxis/helpic/internal/request"
	"github.com/pmaxis/helpic/internal/response"
	"github.com/pmaxis/helpic/internal/tasks"
)

// Handler exposes the comment endpoints over HTTP.
type Handler struct {
	svc *Service
}

// NewHandler creates a comments Handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// Routes mounts the endpoints. Must be called inside an authenticated group.
func (h *Handler) Routes(r chi.Router) {
	r.Route("/comments", func(r chi.Router) {
		r.Get("/", h.List)
		r.Get("/people", h.People)
		r.Post("/", h.Create)
		r.Put("/{id}", h.Update)
		r.Delete("/{id}", h.Delete)
	})
}

// List handles GET /api/v1/comments?task={id}.
//
//	@Summary		A task's comments
//	@Description	Oldest first.
//	@Tags			comments
//	@Produce		json
//	@Param			task	query		string					true	"Task ID"
//	@Success		200		{object}	map[string][]DTO		"comments"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		404		{object}	response.ErrorEnvelope	"Not found"
//	@Router			/comments [get]
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	list, err := h.svc.List(r.Context(), userID, r.URL.Query().Get("task"))
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]DTO, 0, len(list))
	for _, c := range list {
		out = append(out, ToDTO(c, userID))
	}
	response.JSON(w, http.StatusOK, map[string][]DTO{"comments": out})
}

// People handles GET /api/v1/comments/people?task={id}.
//
//	@Summary		Who a comment can mention
//	@Description	Everyone who sees the task, by name. A comment mentions someone as @[Name](user ID).
//	@Tags			comments
//	@Produce		json
//	@Param			task	query		string					true	"Task ID"
//	@Success		200		{object}	map[string][]PersonDTO	"people"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		404		{object}	response.ErrorEnvelope	"Not found"
//	@Router			/comments/people [get]
func (h *Handler) People(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	people, err := h.svc.People(r.Context(), userID, r.URL.Query().Get("task"))
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]PersonDTO, 0, len(people))
	for _, p := range people {
		out = append(out, PersonDTO{ID: p.ID, Name: p.Name, Email: p.Email})
	}
	response.JSON(w, http.StatusOK, map[string][]PersonDTO{"people": out})
}

// Create handles POST /api/v1/comments?task={id}.
//
//	@Summary		Comment on a task
//	@Description	Markdown. Mentioning someone who sees the task, as @[Name](user ID), tells them; anyone else's mention stays plain text. A pin puts it on a spot of one of the task's images.
//	@Tags			comments
//	@Accept			json
//	@Produce		json
//	@Param			task	query		string					true	"Task ID"
//	@Param			body	body		Request					true	"Request body"
//	@Success		201		{object}	map[string]DTO			"comment"
//	@Failure		400		{object}	response.ErrorEnvelope	"Invalid input"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		404		{object}	response.ErrorEnvelope	"Not found"
//	@Router			/comments [post]
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	var req Request
	if !request.Decode(w, r, &req) {
		return
	}
	body, fields := req.Validate()
	pin, pinFields := req.ValidatePin()
	if len(fields)+len(pinFields) > 0 {
		all := map[string]string{}
		maps.Copy(all, fields)
		maps.Copy(all, pinFields)
		response.ValidationError(w, all)
		return
	}
	c, err := h.svc.CreatePinned(r.Context(), userID, r.URL.Query().Get("task"), body, pin)
	writeComment(w, http.StatusCreated, c, userID, err)
}

// Update handles PUT /api/v1/comments/{id}.
//
//	@Summary	Edit your comment
//	@Tags		comments
//	@Accept		json
//	@Produce	json
//	@Param		id		path		string					true	"Comment ID"
//	@Param		body	body		Request					true	"Request body"
//	@Success	200		{object}	map[string]DTO			"comment"
//	@Failure	400		{object}	response.ErrorEnvelope	"Invalid input"
//	@Failure	401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure	403		{object}	response.ErrorEnvelope	"Not allowed"
//	@Failure	404		{object}	response.ErrorEnvelope	"Not found"
//	@Router		/comments/{id} [put]
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	c, err := h.svc.Update(r.Context(), userID, chi.URLParam(r, "id"), body)
	writeComment(w, http.StatusOK, c, userID, err)
}

// Delete handles DELETE /api/v1/comments/{id}.
//
//	@Summary	Delete your comment
//	@Tags		comments
//	@Param		id	path	string	true	"Comment ID"
//	@Success	204	"No content"
//	@Failure	401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure	403	{object}	response.ErrorEnvelope	"Not allowed"
//	@Failure	404	{object}	response.ErrorEnvelope	"Not found"
//	@Router		/comments/{id} [delete]
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

func readBody(w http.ResponseWriter, r *http.Request) (string, bool) {
	var req Request
	if !request.Decode(w, r, &req) {
		return "", false
	}
	body, fields := req.Validate()
	if len(fields) > 0 {
		response.ValidationError(w, fields)
		return "", false
	}
	return body, true
}

func writeComment(w http.ResponseWriter, status int, c Comment, viewerID string, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	response.JSON(w, status, map[string]DTO{"comment": ToDTO(c, viewerID)})
}

func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, tasks.ErrNotFound):
		response.Error(w, http.StatusNotFound, response.CodeNotFound, "Comment not found")
	case errors.Is(err, ErrNotPinnable):
		response.ValidationError(w, map[string]string{"pin": "Comments can only point at an image on this task"})
	case errors.Is(err, ErrNotAuthor):
		response.Error(w, http.StatusForbidden, response.CodeForbidden, "Only the author can change a comment")
	case errors.Is(err, tasks.ErrArchived):
		response.Error(w, http.StatusConflict, response.CodeConflict, "This task is archived: its discussion is closed")
	default:
		response.Internal(w, err)
	}
}
