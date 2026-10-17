package board

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/pmaxis/helpic/internal/request"
	"github.com/pmaxis/helpic/internal/response"
	"github.com/pmaxis/helpic/internal/tasks"
)

// Handler exposes the board endpoints over HTTP.
type Handler struct {
	svc *Service
}

// NewHandler creates a board Handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// Routes mounts the endpoints. Must be called inside an authenticated group.
func (h *Handler) Routes(r chi.Router) {
	r.Route("/board", func(r chi.Router) {
		r.Get("/", h.Board)
		r.Get("/columns", h.Columns)
		r.Post("/columns", h.CreateColumn)
		r.Put("/columns", h.Reorder)
		r.Put("/columns/{id}", h.UpdateColumn)
		r.Delete("/columns/{id}", h.DeleteColumn)
		r.Post("/moves", h.Move)
		h.labelRoutes(r)
	})
}

func scopeOf(r *http.Request) Scope {
	q := r.URL.Query()
	return Scope{With: q.Get("with"), ProjectID: q.Get("project"), Projects: parseProjects(q.Get("projects"))}
}

// Board handles GET /api/board?with={clientID|personal|freelancerID|all}&project={id}&projects={id,…|none}.
// with=all is a freelancer's every task on one board, its columns merged by name.
func (h *Handler) Board(w http.ResponseWriter, r *http.Request) {
	userID, role, ok := request.UserRole(w, r)
	if !ok {
		return
	}
	sc := scopeOf(r)
	if sc.With == AllWith && sc.ProjectID == "" {
		lanes, err := h.svc.AllBoard(r.Context(), userID, role, sc)
		if err != nil {
			writeErr(w, err)
			return
		}
		response.JSON(w, http.StatusOK, MergedDTO(lanes, role, userID))
		return
	}
	p, lanes, err := h.svc.Board(r.Context(), userID, role, sc)
	if err != nil {
		writeErr(w, err)
		return
	}
	response.JSON(w, http.StatusOK, LanesDTO(p, lanes, role, userID))
}

// Columns handles GET /api/board/columns with the same query as Board.
func (h *Handler) Columns(w http.ResponseWriter, r *http.Request) {
	userID, role, ok := request.UserRole(w, r)
	if !ok {
		return
	}
	p, cols, err := h.svc.Columns(r.Context(), userID, role, scopeOf(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	response.JSON(w, http.StatusOK, ColumnsDTO(p, cols, userID))
}

// CreateColumn handles POST /api/board/columns.
func (h *Handler) CreateColumn(w http.ResponseWriter, r *http.Request) {
	userID, role, ok := request.UserRole(w, r)
	if !ok {
		return
	}
	var req ColumnRequest
	if !request.Decode(w, r, &req) {
		return
	}
	cp, fields := req.Validate()
	if len(fields) > 0 {
		response.ValidationError(w, fields)
		return
	}
	writeDone(w, http.StatusCreated, h.svc.CreateColumn(r.Context(), userID, role, Scope{With: req.With, ProjectID: req.ProjectID}, cp))
}

// UpdateColumn handles PUT /api/board/columns/{id}.
func (h *Handler) UpdateColumn(w http.ResponseWriter, r *http.Request) {
	userID, _, ok := request.UserRole(w, r)
	if !ok {
		return
	}
	var req ColumnRequest
	if !request.Decode(w, r, &req) {
		return
	}
	cp, fields := req.Validate()
	if len(fields) > 0 {
		response.ValidationError(w, fields)
		return
	}
	writeDone(w, http.StatusNoContent, h.svc.UpdateColumn(r.Context(), userID, chi.URLParam(r, "id"), cp))
}

// DeleteColumn handles DELETE /api/board/columns/{id}.
func (h *Handler) DeleteColumn(w http.ResponseWriter, r *http.Request) {
	userID, _, ok := request.UserRole(w, r)
	if !ok {
		return
	}
	writeDone(w, http.StatusNoContent, h.svc.DeleteColumn(r.Context(), userID, chi.URLParam(r, "id")))
}

// Reorder handles PUT /api/board/columns.
func (h *Handler) Reorder(w http.ResponseWriter, r *http.Request) {
	userID, role, ok := request.UserRole(w, r)
	if !ok {
		return
	}
	var req OrderRequest
	if !request.Decode(w, r, &req) {
		return
	}
	writeDone(w, http.StatusNoContent, h.svc.Reorder(r.Context(), userID, role, req.IDs))
}

// Move handles POST /api/board/moves.
func (h *Handler) Move(w http.ResponseWriter, r *http.Request) {
	userID, role, ok := request.UserRole(w, r)
	if !ok {
		return
	}
	var req MoveRequest
	if !request.Decode(w, r, &req) {
		return
	}
	var (
		t   tasks.Task
		err error
	)
	if req.Lane != "" {
		t, err = h.svc.MoveToLane(r.Context(), userID, role, req.TaskID, req.Lane)
	} else {
		t, err = h.svc.Move(r.Context(), userID, role, req.TaskID, req.ColumnID, req.AfterTaskID)
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]tasks.DTO{"task": tasks.ToDTO(t, role, userID)})
}

func writeDone(w http.ResponseWriter, status int, err error) {
	switch {
	case err != nil:
		writeErr(w, err)
	case status == http.StatusNoContent:
		w.WriteHeader(status)
	default:
		response.JSON(w, status, map[string]bool{"ok": true})
	}
}

func writeErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNoBoard):
		response.Error(w, http.StatusNotFound, response.CodeNotFound, "Board not found")
	case errors.Is(err, ErrNotFound):
		response.Error(w, http.StatusNotFound, response.CodeNotFound, "Column not found")
	case errors.Is(err, ErrNotManager):
		response.Error(w, http.StatusForbidden, response.CodeForbidden, "Only the freelancer can change the board's columns")
	case errors.Is(err, ErrHasTasks):
		response.Error(w, http.StatusConflict, response.CodeConflict, "This column has tasks. Move them to another column first, or add another column of the same category")
	case errors.Is(err, ErrLastColumn):
		response.Error(w, http.StatusConflict, response.CodeConflict, "A board needs at least one column")
	case errors.Is(err, tasks.ErrTransition):
		response.Error(w, http.StatusConflict, response.CodeConflict, "You can't move this task to that column")
	case errors.Is(err, tasks.ErrNotFound):
		response.Error(w, http.StatusNotFound, response.CodeNotFound, "Task not found")
	case errors.Is(err, tasks.ErrStatusNotFound), errors.Is(err, tasks.ErrStatusMismatch):
		response.Error(w, http.StatusNotFound, response.CodeNotFound, "Column not found")
	default:
		response.Internal(w, err)
	}
}
