package comments

import (
	"errors"
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
		r.Post("/", h.Create)
		r.Put("/{id}", h.Update)
		r.Delete("/{id}", h.Delete)
	})
}

// List handles GET /api/comments?task={id}.
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

// Create handles POST /api/comments?task={id}.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	c, err := h.svc.Create(r.Context(), userID, r.URL.Query().Get("task"), body)
	writeComment(w, http.StatusCreated, c, userID, err)
}

// Update handles PUT /api/comments/{id}.
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

// Delete handles DELETE /api/comments/{id}.
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
	case errors.Is(err, ErrNotAuthor):
		response.Error(w, http.StatusForbidden, response.CodeForbidden, "Only the author can change a comment")
	default:
		response.Internal(w, err)
	}
}
