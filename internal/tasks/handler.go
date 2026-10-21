package tasks

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/pmaxis/helpic/internal/request"
	"github.com/pmaxis/helpic/internal/response"
	"github.com/pmaxis/helpic/internal/user"
)

// Handler exposes the task endpoints over HTTP.
type Handler struct {
	svc *Service
}

// NewHandler creates a tasks Handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// Routes mounts the endpoints. Must be called inside an authenticated group.
func (h *Handler) Routes(r chi.Router) {
	r.Route("/tasks", func(r chi.Router) {
		r.Get("/", h.List)
		r.Post("/", h.Create)
		r.Get("/{id}", h.Get)
		r.Put("/{id}", h.Update)
		r.Delete("/{id}", h.Delete)
		r.Post("/{id}/status", h.SetStatus)
		r.Put("/{id}/price", h.SetPrice)
		r.Put("/{id}/priority", h.SetPriority)
		r.Put("/{id}/labels", h.SetLabels)
		r.Get("/{id}/history", h.History)
		r.Get("/{id}/people", h.People)
		r.Put("/{id}/freelancer", h.AssignFreelancer)
		r.Post("/{id}/participants", h.AddParticipant)
		r.Delete("/{id}/participants/{userID}", h.RemoveParticipant)
		r.Post("/{id}/archive", h.Archive)
	})
	r.Get("/archive", h.ListArchive)
}

// List handles GET /api/v1/tasks?status=new,in_progress&with={userID|personal}&project={id}.
//
//	@Summary		Your tasks
//	@Description	Open ones first, then by due date.
//	@Tags			tasks
//	@Produce		json
//	@Param			status	query		string					false	"Comma-separated: new,in_progress,done"
//	@Param			with	query		string					false	"A counterpart's ID, or "personal""
//	@Param			project	query		string					false	"A project ID"
//	@Success		200		{object}	map[string][]DTO		"tasks"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Router			/tasks [get]
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	userID, role, ok := request.UserRole(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	list, err := h.svc.List(r.Context(), userID, role, Filter{
		Statuses:      ParseStatuses(q.Get("status")),
		CounterpartID: q.Get("with"),
		ProjectID:     q.Get("project"),
	})
	if err != nil {
		response.Internal(w, err)
		return
	}
	out := make([]DTO, 0, len(list))
	for _, t := range list {
		out = append(out, ToDTO(t, role, userID))
	}
	response.JSON(w, http.StatusOK, map[string][]DTO{"tasks": out})
}

// archivePage caps an archive page; maxArchivePage is the most one may ask for.
const (
	archivePage    = 50
	maxArchivePage = 200
)

// ArchiveResponse is a page of the archive. Next, when set, is the cursor
// (?before=) of the page after it.
type ArchiveResponse struct {
	Tasks []DTO  `json:"tasks"`
	Next  string `json:"next,omitempty"`
}

// ListArchive handles GET /api/v1/archive?with={userID|personal}&project={id}&before={cursor}&limit={n}.
//
//	@Summary		Archived tasks
//	@Description	Tasks closed for good, the most recently archived first: shared work whose report was paid, and personal tasks put away by hand. Pages follow the next cursor.
//	@Tags			tasks
//	@Produce		json
//	@Param			with	query		string					false	"A client's or freelancer's ID, or personal"
//	@Param			project	query		string					false	"Project ID"
//	@Param			before	query		string					false	"The next cursor of the previous page"
//	@Param			limit	query		int						false	"Tasks per page, 50 by default, at most 200"
//	@Success		200		{object}	ArchiveResponse			"tasks and the next page's cursor"
//	@Failure		400		{object}	response.ErrorEnvelope	"Invalid cursor or limit"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Router			/archive [get]
func (h *Handler) ListArchive(w http.ResponseWriter, r *http.Request) {
	userID, role, ok := request.UserRole(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	f := ArchiveFilter{With: q.Get("with"), ProjectID: q.Get("project"), Limit: archivePage}
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxArchivePage {
			response.ValidationError(w, map[string]string{"limit": "Ask for 1 to 200 tasks"})
			return
		}
		f.Limit = n
	}
	if raw := q.Get("before"); raw != "" {
		c, ok := parseCursor(raw)
		if !ok {
			response.ValidationError(w, map[string]string{"before": "This page link is broken. Start from the first page."})
			return
		}
		f.Before = &c
	}
	list, more, err := h.svc.ListArchive(r.Context(), userID, role, f)
	if err != nil {
		response.Internal(w, err)
		return
	}
	out := ArchiveResponse{Tasks: make([]DTO, 0, len(list))}
	for _, t := range list {
		out.Tasks = append(out.Tasks, ToDTO(t, role, userID))
	}
	if more {
		last := list[len(list)-1]
		out.Next = last.ArchivedAt.UTC().Format(time.RFC3339Nano) + "_" + last.ID
	}
	response.JSON(w, http.StatusOK, out)
}

// parseCursor reads a next cursor: the archive time and the task's ID.
func parseCursor(raw string) (ArchiveCursor, bool) {
	at, id, ok := strings.Cut(raw, "_")
	if !ok || id == "" {
		return ArchiveCursor{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return ArchiveCursor{}, false
	}
	return ArchiveCursor{At: t, ID: id}, true
}

// Archive handles POST /api/v1/tasks/{id}/archive.
//
//	@Summary		Archive a personal task
//	@Description	Puts your own personal task away for good: it leaves the boards and can't be changed. Shared work is archived when its report is paid.
//	@Tags			tasks
//	@Produce		json
//	@Param			id	path		string					true	"Task ID"
//	@Success		200	{object}	map[string]DTO			"task"
//	@Failure		401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		403	{object}	response.ErrorEnvelope	"Not your personal task"
//	@Failure		404	{object}	response.ErrorEnvelope	"Not found"
//	@Failure		409	{object}	response.ErrorEnvelope	"Already archived"
//	@Router			/tasks/{id}/archive [post]
func (h *Handler) Archive(w http.ResponseWriter, r *http.Request) {
	userID, role, ok := request.UserRole(w, r)
	if !ok {
		return
	}
	t, err := h.svc.Archive(r.Context(), userID, chi.URLParam(r, "id"))
	writeTask(w, http.StatusOK, t, role, userID, err)
}

// Get handles GET /api/v1/tasks/{id}.
//
//	@Summary	A task
//	@Tags		tasks
//	@Produce	json
//	@Param		id	path		string					true	"Task ID"
//	@Success	200	{object}	map[string]DTO			"task"
//	@Failure	401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure	404	{object}	response.ErrorEnvelope	"Not found"
//	@Router		/tasks/{id} [get]
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	userID, role, ok := request.UserRole(w, r)
	if !ok {
		return
	}
	t, err := h.svc.Get(r.Context(), userID, chi.URLParam(r, "id"))
	writeTask(w, http.StatusOK, t, role, userID, err)
}

// Create handles POST /api/v1/tasks.
//
//	@Summary		Set a task
//	@Description	A client names the freelancer; a freelancer the client, or none for a personal task. A project, or a board column (status_id), fixes who it's with.
//	@Tags			tasks
//	@Accept			json
//	@Produce		json
//	@Param			body	body		Request					true	"Request body"
//	@Success		201		{object}	map[string]DTO			"task"
//	@Failure		400		{object}	response.ErrorEnvelope	"Invalid input"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		409		{object}	response.ErrorEnvelope	"Conflicts with the current state"
//	@Router			/tasks [post]
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
	t, err := h.svc.Create(r.Context(), userID, role, params)
	if errors.Is(err, ErrNotLinked) {
		if role == user.RoleFreelancer {
			response.ValidationError(w, map[string]string{"client_id": "This isn't one of your clients"})
		} else {
			response.ValidationError(w, map[string]string{"freelancer_id": "You don't work with this freelancer"})
		}
		return
	}
	writeTask(w, http.StatusCreated, t, role, userID, err)
}

// Update handles PUT /api/v1/tasks/{id}.
//
//	@Summary		Edit a task
//	@Description	Title, description, due date, project. Its creator or its freelancer, in any status.
//	@Tags			tasks
//	@Accept			json
//	@Produce		json
//	@Param			id		path		string					true	"Task ID"
//	@Param			body	body		Request					true	"Request body"
//	@Success		200		{object}	map[string]DTO			"task"
//	@Failure		400		{object}	response.ErrorEnvelope	"Invalid input"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		403		{object}	response.ErrorEnvelope	"Not allowed"
//	@Failure		404		{object}	response.ErrorEnvelope	"Not found"
//	@Router			/tasks/{id} [put]
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	userID, role, ok := request.UserRole(w, r)
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
	t, err := h.svc.Update(r.Context(), userID, chi.URLParam(r, "id"), params)
	writeTask(w, http.StatusOK, t, role, userID, err)
}

// Delete handles DELETE /api/v1/tasks/{id}.
//
//	@Summary		Delete a task
//	@Description	Its creator, while it's new.
//	@Tags			tasks
//	@Param			id	path	string	true	"Task ID"
//	@Success		204	"No content"
//	@Failure		401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		404	{object}	response.ErrorEnvelope	"Not found"
//	@Failure		409	{object}	response.ErrorEnvelope	"Conflicts with the current state"
//	@Router			/tasks/{id} [delete]
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	userID, _, ok := request.UserRole(w, r)
	if !ok {
		return
	}
	err := h.svc.Delete(r.Context(), userID, chi.URLParam(r, "id"))
	if err != nil {
		writeTask(w, 0, Task{}, "", "", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// SetPrice handles PUT /api/v1/tasks/{id}/price.
//
//	@Summary		Price a task
//	@Description	The task's freelancer only, in any status. A null price clears it.
//	@Tags			tasks
//	@Accept			json
//	@Produce		json
//	@Param			id		path		string					true	"Task ID"
//	@Param			body	body		PriceRequest			true	"Request body"
//	@Success		200		{object}	map[string]DTO			"task"
//	@Failure		400		{object}	response.ErrorEnvelope	"Invalid input"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		403		{object}	response.ErrorEnvelope	"Not allowed"
//	@Failure		404		{object}	response.ErrorEnvelope	"Not found"
//	@Router			/tasks/{id}/price [put]
func (h *Handler) SetPrice(w http.ResponseWriter, r *http.Request) {
	userID, role, ok := request.UserRole(w, r)
	if !ok {
		return
	}
	var req PriceRequest
	if !request.Decode(w, r, &req) {
		return
	}
	cents, fields := req.Validate()
	if len(fields) > 0 {
		response.ValidationError(w, fields)
		return
	}
	t, err := h.svc.SetPrice(r.Context(), userID, chi.URLParam(r, "id"), cents)
	writeTask(w, http.StatusOK, t, role, userID, err)
}

// SetPriority handles PUT /api/v1/tasks/{id}/priority.
//
//	@Summary		Set a task's priority
//	@Description	Either side, in any status.
//	@Tags			tasks
//	@Accept			json
//	@Produce		json
//	@Param			id		path		string					true	"Task ID"
//	@Param			body	body		PriorityRequest			true	"Request body"
//	@Success		200		{object}	map[string]DTO			"task"
//	@Failure		400		{object}	response.ErrorEnvelope	"Invalid input"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		404		{object}	response.ErrorEnvelope	"Not found"
//	@Router			/tasks/{id}/priority [put]
func (h *Handler) SetPriority(w http.ResponseWriter, r *http.Request) {
	userID, role, ok := request.UserRole(w, r)
	if !ok {
		return
	}
	var req PriorityRequest
	if !request.Decode(w, r, &req) {
		return
	}
	priority, fields := req.Validate()
	if len(fields) > 0 {
		response.ValidationError(w, fields)
		return
	}
	t, err := h.svc.SetPriority(r.Context(), userID, chi.URLParam(r, "id"), priority)
	writeTask(w, http.StatusOK, t, role, userID, err)
}

// SetLabels handles PUT /api/v1/tasks/{id}/labels.
//
//	@Summary		Set a task's labels
//	@Description	Replaces them with labels of the task's board.
//	@Tags			tasks
//	@Accept			json
//	@Produce		json
//	@Param			id		path		string					true	"Task ID"
//	@Param			body	body		LabelsRequest			true	"Request body"
//	@Success		200		{object}	map[string]DTO			"task"
//	@Failure		400		{object}	response.ErrorEnvelope	"Invalid input"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		404		{object}	response.ErrorEnvelope	"Not found"
//	@Router			/tasks/{id}/labels [put]
func (h *Handler) SetLabels(w http.ResponseWriter, r *http.Request) {
	userID, role, ok := request.UserRole(w, r)
	if !ok {
		return
	}
	var req LabelsRequest
	if !request.Decode(w, r, &req) {
		return
	}
	ids, fields := req.Validate()
	if len(fields) > 0 {
		response.ValidationError(w, fields)
		return
	}
	t, err := h.svc.SetLabels(r.Context(), userID, chi.URLParam(r, "id"), ids)
	writeTask(w, http.StatusOK, t, role, userID, err)
}

// AssignFreelancer handles PUT /api/v1/tasks/{id}/freelancer.
//
//	@Summary		Change a task's freelancer
//	@Description	An empty freelancer_id takes the assignee off. The task's author stays a participant.
//	@Tags			tasks
//	@Accept			json
//	@Produce		json
//	@Param			id		path		string					true	"Task ID"
//	@Param			body	body		object					true	"freelancer_id"
//	@Success		200		{object}	map[string]DTO			"task"
//	@Failure		400		{object}	response.ErrorEnvelope	"Not a freelancer on the project"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		403		{object}	response.ErrorEnvelope	"Not allowed"
//	@Failure		404		{object}	response.ErrorEnvelope	"Not found"
//	@Router			/tasks/{id}/freelancer [put]
func (h *Handler) AssignFreelancer(w http.ResponseWriter, r *http.Request) {
	userID, role, ok := request.UserRole(w, r)
	if !ok {
		return
	}
	var req struct {
		FreelancerID string `json:"freelancer_id"`
	}
	if !request.Decode(w, r, &req) {
		return
	}
	t, err := h.svc.AssignFreelancer(r.Context(), userID, chi.URLParam(r, "id"), strings.TrimSpace(req.FreelancerID))
	writeTask(w, http.StatusOK, t, role, userID, err)
}

// People handles GET /api/v1/tasks/{id}/people: who on the client's side can
// be on the task.
//
//	@Summary		Who can be on a task
//	@Description	The task's client and their company's members and contractors, saying who is on it already. Empty for a personal task.
//	@Tags			tasks
//	@Produce		json
//	@Param			id	path		string					true	"Task ID"
//	@Success		200	{object}	map[string][]PersonDTO	"people"
//	@Failure		401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		404	{object}	response.ErrorEnvelope	"Not found"
//	@Router			/tasks/{id}/people [get]
func (h *Handler) People(w http.ResponseWriter, r *http.Request) {
	userID, _, ok := request.UserRole(w, r)
	if !ok {
		return
	}
	people, err := h.svc.People(r.Context(), userID, chi.URLParam(r, "id"))
	if err != nil {
		writeTask(w, 0, Task{}, "", "", err)
		return
	}
	out := make([]PersonDTO, 0, len(people))
	for _, p := range people {
		out = append(out, PersonDTO(p))
	}
	response.JSON(w, http.StatusOK, map[string][]PersonDTO{"people": out})
}

// AddParticipant handles POST /api/v1/tasks/{id}/participants.
//
//	@Summary		Add someone to a task
//	@Description	Someone from the client's side; they're notified, and a contractor sees the task from now on. A contractor can add people only to tasks they set.
//	@Tags			tasks
//	@Accept			json
//	@Produce		json
//	@Param			id		path		string					true	"Task ID"
//	@Param			body	body		ParticipantRequest		true	"Request body"
//	@Success		200		{object}	map[string]DTO			"task"
//	@Failure		400		{object}	response.ErrorEnvelope	"Not on the client's side"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		403		{object}	response.ErrorEnvelope	"Not allowed"
//	@Failure		404		{object}	response.ErrorEnvelope	"Not found"
//	@Router			/tasks/{id}/participants [post]
func (h *Handler) AddParticipant(w http.ResponseWriter, r *http.Request) {
	userID, role, ok := request.UserRole(w, r)
	if !ok {
		return
	}
	var req ParticipantRequest
	if !request.Decode(w, r, &req) {
		return
	}
	t, err := h.svc.AddParticipant(r.Context(), userID, chi.URLParam(r, "id"), req.UserID)
	writeTask(w, http.StatusOK, t, role, userID, err)
}

// RemoveParticipant handles DELETE /api/v1/tasks/{id}/participants/{userID}.
//
//	@Summary		Take someone off a task
//	@Description	Anyone can take themselves off; the freelancer and the creator stay. A contractor taken off no longer sees the task.
//	@Tags			tasks
//	@Param			id		path	string	true	"Task ID"
//	@Param			userID	path	string	true	"Participant's user ID"
//	@Success		204		"No content"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		403		{object}	response.ErrorEnvelope	"Not allowed"
//	@Failure		404		{object}	response.ErrorEnvelope	"Not found"
//	@Failure		409		{object}	response.ErrorEnvelope	"The freelancer or the creator"
//	@Router			/tasks/{id}/participants/{userID} [delete]
func (h *Handler) RemoveParticipant(w http.ResponseWriter, r *http.Request) {
	userID, _, ok := request.UserRole(w, r)
	if !ok {
		return
	}
	err := h.svc.RemoveParticipant(r.Context(), userID, chi.URLParam(r, "id"), chi.URLParam(r, "userID"))
	if err != nil {
		writeTask(w, 0, Task{}, "", "", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// History handles GET /api/v1/tasks/{id}/history: who changed what and when.
//
//	@Summary		A task's history
//	@Description	Who changed what and when, oldest first. Team members don't see price changes.
//	@Tags			tasks
//	@Produce		json
//	@Param			id	path		string					true	"Task ID"
//	@Success		200	{object}	map[string][]EventDTO	"events"
//	@Failure		401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		404	{object}	response.ErrorEnvelope	"Not found"
//	@Router			/tasks/{id}/history [get]
func (h *Handler) History(w http.ResponseWriter, r *http.Request) {
	userID, _, ok := request.UserRole(w, r)
	if !ok {
		return
	}
	events, err := h.svc.History(r.Context(), userID, chi.URLParam(r, "id"))
	if err != nil {
		writeTask(w, 0, Task{}, "", "", err)
		return
	}
	out := make([]EventDTO, 0, len(events))
	for _, e := range events {
		out = append(out, ToEventDTO(e))
	}
	response.JSON(w, http.StatusOK, map[string][]EventDTO{"events": out})
}

// SetStatus handles POST /api/v1/tasks/{id}/status.
//
//	@Summary		Move a task
//	@Description	To an exact board column (status_id), or to the first column of a category (status), as the role rules allow.
//	@Tags			tasks
//	@Accept			json
//	@Produce		json
//	@Param			id		path		string					true	"Task ID"
//	@Param			body	body		StatusRequest			true	"Request body"
//	@Success		200		{object}	map[string]DTO			"task"
//	@Failure		400		{object}	response.ErrorEnvelope	"Invalid input"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		404		{object}	response.ErrorEnvelope	"Not found"
//	@Failure		409		{object}	response.ErrorEnvelope	"The move isn't allowed"
//	@Router			/tasks/{id}/status [post]
func (h *Handler) SetStatus(w http.ResponseWriter, r *http.Request) {
	userID, role, ok := request.UserRole(w, r)
	if !ok {
		return
	}
	var req StatusRequest
	if !request.Decode(w, r, &req) {
		return
	}
	var (
		t   Task
		err error
	)
	if req.StatusID != "" {
		t, err = h.svc.MoveTo(r.Context(), userID, role, chi.URLParam(r, "id"), req.StatusID)
	} else {
		t, err = h.svc.SetStatus(r.Context(), userID, role, chi.URLParam(r, "id"), req.Status)
	}
	writeTask(w, http.StatusOK, t, role, userID, err)
}

func writeTask(w http.ResponseWriter, status int, t Task, role, viewerID string, err error) {
	switch {
	case errors.Is(err, ErrStatusNotFound):
		response.ValidationError(w, map[string]string{"status_id": "Status not found"})
	case errors.Is(err, ErrStatusMismatch):
		response.ValidationError(w, map[string]string{"status_id": "This status is on another board"})
	case errors.Is(err, ErrProjectNotFound):
		response.ValidationError(w, map[string]string{"project_id": "Project not found"})
	case errors.Is(err, ErrProjectMismatch):
		response.ValidationError(w, map[string]string{"project_id": "This project is with someone else"})
	case errors.Is(err, ErrNotLinked):
		response.ValidationError(w, map[string]string{"freelancer_id": "You don't work with this freelancer"})
	case errors.Is(err, ErrFreelancerNotOnProject):
		response.ValidationError(w, map[string]string{"freelancer_id": "This freelancer isn't on the project"})
	case errors.Is(err, ErrNoProjectFreelancer):
		response.ValidationError(w, map[string]string{"project_id": "Add a freelancer to the project first"})
	case errors.Is(err, ErrChooseFreelancer):
		response.ValidationError(w, map[string]string{"freelancer_id": "Choose a freelancer"})
	case errors.Is(err, ErrNotFound):
		response.Error(w, http.StatusNotFound, response.CodeNotFound, "Task not found")
	case errors.Is(err, ErrEditNotAllowed):
		response.Error(w, http.StatusForbidden, response.CodeForbidden, "Only whoever created the task, or its freelancer, can edit it")
	case errors.Is(err, ErrNotEditable):
		response.Error(w, http.StatusConflict, response.CodeConflict, "The task can't be changed in its current status")
	case errors.Is(err, ErrLabelMismatch):
		response.ValidationError(w, map[string]string{"label_ids": "These labels aren't on this task's board"})
	case errors.Is(err, ErrPriceNotAllowed):
		response.Error(w, http.StatusForbidden, response.CodeForbidden, "Only the freelancer sets the price")
	case errors.Is(err, ErrPeopleNotAllowed):
		response.Error(w, http.StatusForbidden, response.CodeForbidden, "A contractor changes who is on a task only when they set it")
	case errors.Is(err, ErrNotClientSide):
		response.ValidationError(w, map[string]string{"user_id": "Only the client and their team can be added"})
	case errors.Is(err, ErrKeepParticipant):
		response.Error(w, http.StatusConflict, response.CodeConflict, "The freelancer and whoever set the task stay on it")
	case errors.Is(err, ErrArchived):
		response.Error(w, http.StatusConflict, response.CodeConflict, "This task is archived: it's closed and stays as it is")
	case errors.Is(err, ErrArchiveNotAllowed):
		response.Error(w, http.StatusForbidden, response.CodeForbidden, "Only your own personal task goes to the archive by hand; shared work goes there when its report is paid")
	case errors.Is(err, ErrAssignNotAllowed):
		response.Error(w, http.StatusForbidden, response.CodeForbidden, "Only the client and their team choose who does the task")
	case errors.Is(err, ErrNeedsFreelancer):
		response.Error(w, http.StatusConflict, response.CodeConflict, "A task outside a project needs a freelancer")
	case errors.Is(err, ErrTransition):
		response.Error(w, http.StatusConflict, response.CodeConflict, "This status change isn't allowed")
	case err != nil:
		response.Internal(w, err)
	default:
		response.JSON(w, status, map[string]DTO{"task": ToDTO(t, role, viewerID)})
	}
}
