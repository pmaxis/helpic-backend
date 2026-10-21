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

// Board handles GET /api/v1/board?with={clientID|personal|freelancerID|all}&project={id}&projects={id,…|none}.
// with=all is every task the user can see, the boards' columns merged by name.
//
//	@Summary		A board with its tasks
//	@Description	One pair's board, a project's, or with=all: every task the user can see, the boards' columns merged by name.
//	@Tags			board
//	@Produce		json
//	@Param			with		query		string					false	"A counterpart's ID, "personal", or "all""
//	@Param			project		query		string					false	"A project ID"
//	@Param			projects	query		string					false	"Comma-separated project IDs; "none" keeps tasks outside any"
//	@Success		200			{object}	BoardDTO				"OK"
//	@Failure		401			{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		404			{object}	response.ErrorEnvelope	"Not found"
//	@Router			/board [get]
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
	p, lanes, merged, err := h.svc.Board(r.Context(), userID, role, sc)
	if err != nil {
		writeErr(w, err)
		return
	}
	if merged {
		response.JSON(w, http.StatusOK, MergedDTO(lanes, role, userID))
		return
	}
	response.JSON(w, http.StatusOK, LanesDTO(p, lanes, role, userID))
}

// Columns handles GET /api/v1/board/columns with the same query as Board.
//
//	@Summary		A board's columns
//	@Description	The same query as /board, without tasks.
//	@Tags			board
//	@Produce		json
//	@Param			with	query		string					false	"A counterpart's ID, or "personal""
//	@Param			project	query		string					false	"A project ID"
//	@Success		200		{object}	BoardDTO				"OK"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		404		{object}	response.ErrorEnvelope	"Not found"
//	@Router			/board/columns [get]
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

// CreateColumn handles POST /api/v1/board/columns.
//
//	@Summary		Add a column
//	@Description	The board's freelancer, or the author of the project (project_id).
//	@Tags			board
//	@Accept			json
//	@Produce		json
//	@Param			body	body	ColumnRequest	true	"Request body"
//	@Success		201		"Created"
//	@Failure		400		{object}	response.ErrorEnvelope	"Invalid input"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		403		{object}	response.ErrorEnvelope	"Not allowed"
//	@Failure		404		{object}	response.ErrorEnvelope	"Not found"
//	@Router			/board/columns [post]
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

// UpdateColumn handles PUT /api/v1/board/columns/{id}.
//
//	@Summary	Rename a column or change its category
//	@Tags		board
//	@Accept		json
//	@Produce	json
//	@Param		id		path	string			true	"Column ID"
//	@Param		body	body	ColumnRequest	true	"Request body"
//	@Success	204		"No content"
//	@Failure	400		{object}	response.ErrorEnvelope	"Invalid input"
//	@Failure	401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure	403		{object}	response.ErrorEnvelope	"Not allowed"
//	@Failure	404		{object}	response.ErrorEnvelope	"Not found"
//	@Router		/board/columns/{id} [put]
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

// DeleteColumn handles DELETE /api/v1/board/columns/{id}.
//
//	@Summary		Delete a column
//	@Description	Its tasks move to another column of the same bucket.
//	@Tags			board
//	@Param			id	path	string	true	"Column ID"
//	@Success		204	"No content"
//	@Failure		401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		403	{object}	response.ErrorEnvelope	"Not allowed"
//	@Failure		404	{object}	response.ErrorEnvelope	"Not found"
//	@Failure		409	{object}	response.ErrorEnvelope	"Conflicts with the current state"
//	@Router			/board/columns/{id} [delete]
func (h *Handler) DeleteColumn(w http.ResponseWriter, r *http.Request) {
	userID, _, ok := request.UserRole(w, r)
	if !ok {
		return
	}
	writeDone(w, http.StatusNoContent, h.svc.DeleteColumn(r.Context(), userID, chi.URLParam(r, "id")))
}

// Reorder handles PUT /api/v1/board/columns.
//
//	@Summary	Reorder a board's columns
//	@Tags		board
//	@Accept		json
//	@Param		body	body	OrderRequest	true	"Every column ID, in the new order"
//	@Success	204		"No content"
//	@Failure	400		{object}	response.ErrorEnvelope	"Invalid input"
//	@Failure	401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure	403		{object}	response.ErrorEnvelope	"Not allowed"
//	@Router		/board/columns [put]
func (h *Handler) Reorder(w http.ResponseWriter, r *http.Request) {
	userID, _, ok := request.UserRole(w, r)
	if !ok {
		return
	}
	var req OrderRequest
	if !request.Decode(w, r, &req) {
		return
	}
	writeDone(w, http.StatusNoContent, h.svc.Reorder(r.Context(), userID, req.IDs))
}

// Move handles POST /api/v1/board/moves.
//
//	@Summary		Move a card
//	@Description	Into a column after a card (column_id, after_task_id), or on the all board into a lane (lane).
//	@Tags			board
//	@Accept			json
//	@Produce		json
//	@Param			body	body		MoveRequest				true	"Request body"
//	@Success		200		{object}	map[string]tasks.DTO	"task"
//	@Failure		400		{object}	response.ErrorEnvelope	"Invalid input"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		404		{object}	response.ErrorEnvelope	"Not found"
//	@Failure		409		{object}	response.ErrorEnvelope	"The move isn't allowed"
//	@Router			/board/moves [post]
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
		response.Error(w, http.StatusForbidden, response.CodeForbidden, "Only the freelancer or the project's author can change the board's columns")
	case errors.Is(err, ErrHasTasks):
		response.Error(w, http.StatusConflict, response.CodeConflict, "This column has tasks. Move them to another column first, or add another column that counts the same way")
	case errors.Is(err, ErrLastColumn):
		response.Error(w, http.StatusConflict, response.CodeConflict, "A board needs at least one column")
	case errors.Is(err, tasks.ErrArchived):
		response.Error(w, http.StatusConflict, response.CodeConflict, "This task is archived: it's closed and stays as it is")
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
