package timelog

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/pmaxis/helpic/internal/request"
	"github.com/pmaxis/helpic/internal/response"
	"github.com/pmaxis/helpic/internal/tasks"
)

// Handler exposes the time entry endpoints over HTTP.
type Handler struct {
	svc *Service
}

// NewHandler creates a timelog Handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// Routes mounts the endpoints. Must be called inside an authenticated group.
func (h *Handler) Routes(r chi.Router) {
	r.Route("/time-entries", func(r chi.Router) {
		r.Get("/", h.List)
		r.Post("/", h.Create)
		r.Put("/{id}", h.Update)
		r.Delete("/{id}", h.Delete)
	})
	r.Route("/timer", func(r chi.Router) {
		r.Get("/", h.Running)
		r.Post("/", h.Start)
		r.Post("/stop", h.Stop)
	})
}

// List handles GET /api/v1/time-entries?task={id}.
//
//	@Summary		The time on a task
//	@Description	Newest day first. For whoever sees the task's price.
//	@Tags			time
//	@Produce		json
//	@Param			task	query		string					true	"Task ID"
//	@Success		200		{object}	map[string][]DTO		"entries"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		403		{object}	response.ErrorEnvelope	"Not allowed"
//	@Failure		404		{object}	response.ErrorEnvelope	"Not found"
//	@Router			/time-entries [get]
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
	for _, e := range list {
		out = append(out, ToDTO(e, userID))
	}
	response.JSON(w, http.StatusOK, map[string][]DTO{"entries": out})
}

// Create handles POST /api/v1/time-entries?task={id}.
//
//	@Summary		Write time on a task
//	@Description	The task's freelancer only, until it's archived.
//	@Tags			time
//	@Accept			json
//	@Produce		json
//	@Param			task	query		string					true	"Task ID"
//	@Param			body	body		Request					true	"Request body"
//	@Success		201		{object}	map[string]DTO			"entry"
//	@Failure		400		{object}	response.ErrorEnvelope	"Invalid input"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		403		{object}	response.ErrorEnvelope	"Not allowed"
//	@Failure		404		{object}	response.ErrorEnvelope	"Not found"
//	@Failure		409		{object}	response.ErrorEnvelope	"Archived"
//	@Router			/time-entries [post]
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	p, ok := readParams(w, r)
	if !ok {
		return
	}
	e, err := h.svc.Create(r.Context(), userID, r.URL.Query().Get("task"), p)
	writeEntry(w, http.StatusCreated, e, userID, err)
}

// Update handles PUT /api/v1/time-entries/{id}.
//
//	@Summary	Change your time entry
//	@Tags		time
//	@Accept		json
//	@Produce	json
//	@Param		id		path		string					true	"Entry ID"
//	@Param		body	body		Request					true	"Request body"
//	@Success	200		{object}	map[string]DTO			"entry"
//	@Failure	400		{object}	response.ErrorEnvelope	"Invalid input"
//	@Failure	401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure	403		{object}	response.ErrorEnvelope	"Not allowed"
//	@Failure	404		{object}	response.ErrorEnvelope	"Not found"
//	@Failure	409		{object}	response.ErrorEnvelope	"Running, or archived"
//	@Router		/time-entries/{id} [put]
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	p, ok := readParams(w, r)
	if !ok {
		return
	}
	e, err := h.svc.Update(r.Context(), userID, chi.URLParam(r, "id"), p)
	writeEntry(w, http.StatusOK, e, userID, err)
}

// Delete handles DELETE /api/v1/time-entries/{id}.
//
//	@Summary	Delete your time entry
//	@Tags		time
//	@Param		id	path	string	true	"Entry ID"
//	@Success	204	"No content"
//	@Failure	401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure	403	{object}	response.ErrorEnvelope	"Not allowed"
//	@Failure	404	{object}	response.ErrorEnvelope	"Not found"
//	@Failure	409	{object}	response.ErrorEnvelope	"Archived"
//	@Router		/time-entries/{id} [delete]
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

// Running handles GET /api/v1/timer.
//
//	@Summary		Your running timer
//	@Description	Null when none runs.
//	@Tags			time
//	@Produce		json
//	@Success		200	{object}	map[string]DTO			"timer"
//	@Failure		401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Router			/timer [get]
func (h *Handler) Running(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	e, err := h.svc.Running(r.Context(), userID)
	if errors.Is(err, ErrNoTimer) {
		response.JSON(w, http.StatusOK, map[string]*DTO{"timer": nil})
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}
	d := ToDTO(e, userID)
	response.JSON(w, http.StatusOK, map[string]*DTO{"timer": &d})
}

// Start handles POST /api/v1/timer?task={id}.
//
//	@Summary		Start a timer on a task
//	@Description	The task's freelancer only. A timer already running stops and keeps its time.
//	@Tags			time
//	@Produce		json
//	@Param			task	query		string					true	"Task ID"
//	@Success		201		{object}	map[string]DTO			"timer"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		403		{object}	response.ErrorEnvelope	"Not allowed"
//	@Failure		404		{object}	response.ErrorEnvelope	"Not found"
//	@Failure		409		{object}	response.ErrorEnvelope	"Archived"
//	@Router			/timer [post]
func (h *Handler) Start(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	e, err := h.svc.Start(r.Context(), userID, r.URL.Query().Get("task"))
	if err != nil {
		writeError(w, err)
		return
	}
	response.JSON(w, http.StatusCreated, map[string]DTO{"timer": ToDTO(e, userID)})
}

// Stop handles POST /api/v1/timer/stop.
//
//	@Summary		Stop your timer
//	@Description	Its time is added to its entry.
//	@Tags			time
//	@Produce		json
//	@Success		200	{object}	map[string]DTO			"entry"
//	@Failure		401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		409	{object}	response.ErrorEnvelope	"No timer running"
//	@Router			/timer/stop [post]
func (h *Handler) Stop(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	e, err := h.svc.Stop(r.Context(), userID)
	writeEntry(w, http.StatusOK, e, userID, err)
}

func readParams(w http.ResponseWriter, r *http.Request) (Params, bool) {
	var req Request
	if !request.Decode(w, r, &req) {
		return Params{}, false
	}
	p, fields := req.Validate()
	if len(fields) > 0 {
		response.ValidationError(w, fields)
		return Params{}, false
	}
	return p, true
}

func writeEntry(w http.ResponseWriter, status int, e Entry, viewerID string, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	response.JSON(w, status, map[string]DTO{"entry": ToDTO(e, viewerID)})
}

func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, tasks.ErrNotFound):
		response.Error(w, http.StatusNotFound, response.CodeNotFound, "Task not found")
	case errors.Is(err, ErrNotFound):
		response.Error(w, http.StatusNotFound, response.CodeNotFound, "Time entry not found")
	case errors.Is(err, ErrNotAllowed):
		response.Error(w, http.StatusForbidden, response.CodeForbidden, "Only the task's freelancer writes time on it")
	case errors.Is(err, ErrNotAuthor):
		response.Error(w, http.StatusForbidden, response.CodeForbidden, "Only the author can change a time entry")
	case errors.Is(err, ErrRunning):
		response.Error(w, http.StatusConflict, response.CodeConflict, "The timer is still running. Stop it first.")
	case errors.Is(err, ErrNoTimer):
		response.Error(w, http.StatusConflict, response.CodeConflict, "No timer is running")
	case errors.Is(err, ErrBilled):
		response.Error(w, http.StatusConflict, response.CodeConflict, "This task is on a sent report: its time is billed")
	case errors.Is(err, tasks.ErrArchived):
		response.Error(w, http.StatusConflict, response.CodeConflict, "This task is archived: its time is closed")
	default:
		response.Internal(w, err)
	}
}
