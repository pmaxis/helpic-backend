package reports

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/pmaxis/helpic/internal/middleware"
	"github.com/pmaxis/helpic/internal/request"
	"github.com/pmaxis/helpic/internal/response"
	"github.com/pmaxis/helpic/internal/user"
)

// Handler exposes the report endpoints over HTTP.
type Handler struct {
	svc *Service
}

// NewHandler creates a reports Handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// Routes mounts the endpoints. Must be called inside an authenticated group;
// making and changing reports is for freelancers.
func (h *Handler) Routes(r chi.Router) {
	r.Route("/reports", func(r chi.Router) {
		r.Get("/", h.List)
		r.Get("/{id}", h.Get)
		r.Get("/{id}/history", h.History)
		r.Post("/{id}/paid", h.MarkPaid)

		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireRole(user.RoleFreelancer))
			r.Post("/", h.Create)
			r.Put("/{id}", h.Update)
			r.Delete("/{id}", h.Delete)
			r.Put("/{id}/items/{itemID}", h.UpdateItem)
			r.Delete("/{id}/items/{itemID}", h.RemoveItem)
			r.Post("/{id}/send", h.Send)
		})
	})
}

// List handles GET /api/v1/reports.
//
//	@Summary		Your reports
//	@Description	A freelancer's own; a client's once sent. Team members get none.
//	@Tags			reports
//	@Produce		json
//	@Success		200	{object}	ListResponse			"OK"
//	@Failure		401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Router			/reports [get]
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	list, err := h.svc.List(r.Context(), userID)
	if err != nil {
		response.Internal(w, err)
		return
	}
	out := make([]DTO, 0, len(list))
	for _, rep := range list {
		out = append(out, ToDTO(rep, userID))
	}
	response.JSON(w, http.StatusOK, ListResponse{Reports: out})
}

// Get handles GET /api/v1/reports/{id}.
//
//	@Summary	A report with its tasks
//	@Tags		reports
//	@Produce	json
//	@Param		id	path		string					true	"Report ID"
//	@Success	200	{object}	map[string]DTO			"report"
//	@Failure	401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure	404	{object}	response.ErrorEnvelope	"Not found"
//	@Router		/reports/{id} [get]
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	rep, err := h.svc.Get(r.Context(), userID, chi.URLParam(r, "id"))
	writeReport(w, http.StatusOK, rep, userID, err)
}

// Create handles POST /api/v1/reports.
//
//	@Summary		Make a report
//	@Description	Freelancers only. Snapshots the tasks completed in the period (in project_ids, if any), leaving out ones a paid report covers.
//	@Tags			reports
//	@Accept			json
//	@Produce		json
//	@Param			body	body		CreateRequest			true	"Request body"
//	@Success		201		{object}	map[string]DTO			"report"
//	@Failure		400		{object}	response.ErrorEnvelope	"Invalid input"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		403		{object}	response.ErrorEnvelope	"Not allowed"
//	@Router			/reports [post]
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	var req CreateRequest
	if !request.Decode(w, r, &req) {
		return
	}
	p, fields := req.Validate()
	if len(fields) > 0 {
		response.ValidationError(w, fields)
		return
	}
	rep, err := h.svc.Create(r.Context(), userID, p)
	switch {
	case errors.Is(err, ErrNotLinked):
		response.ValidationError(w, map[string]string{"client_id": "This isn't one of your clients"})
	case errors.Is(err, ErrProjectMismatch):
		response.ValidationError(w, map[string]string{"project_ids": "One of these projects isn't with that client"})
	default:
		writeReport(w, http.StatusCreated, rep, userID, err)
	}
}

// Update handles PUT /api/v1/reports/{id}.
//
//	@Summary		Edit a draft
//	@Description	Title, summary and kind.
//	@Tags			reports
//	@Accept			json
//	@Produce		json
//	@Param			id		path		string					true	"Report ID"
//	@Param			body	body		UpdateRequest			true	"Request body"
//	@Success		200		{object}	map[string]DTO			"report"
//	@Failure		400		{object}	response.ErrorEnvelope	"Invalid input"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		403		{object}	response.ErrorEnvelope	"Not allowed"
//	@Failure		404		{object}	response.ErrorEnvelope	"Not found"
//	@Failure		409		{object}	response.ErrorEnvelope	"Already sent"
//	@Router			/reports/{id} [put]
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	var req UpdateRequest
	if !request.Decode(w, r, &req) {
		return
	}
	p, fields := req.Validate()
	if len(fields) > 0 {
		response.ValidationError(w, fields)
		return
	}
	rep, err := h.svc.Update(r.Context(), userID, chi.URLParam(r, "id"), p)
	writeReport(w, http.StatusOK, rep, userID, err)
}

// UpdateItem handles PUT /api/v1/reports/{id}/items/{itemID}: the task's notes.
//
//	@Summary	Rewrite a task's notes in a draft
//	@Tags		reports
//	@Accept		json
//	@Produce	json
//	@Param		id		path		string					true	"Report ID"
//	@Param		itemID	path		string					true	"Report item ID"
//	@Param		body	body		ItemRequest				true	"Request body"
//	@Success	200		{object}	map[string]DTO			"report"
//	@Failure	400		{object}	response.ErrorEnvelope	"Invalid input"
//	@Failure	401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure	403		{object}	response.ErrorEnvelope	"Not allowed"
//	@Failure	404		{object}	response.ErrorEnvelope	"Not found"
//	@Failure	409		{object}	response.ErrorEnvelope	"Already sent"
//	@Router		/reports/{id}/items/{itemID} [put]
func (h *Handler) UpdateItem(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	var req ItemRequest
	if !request.Decode(w, r, &req) {
		return
	}
	notes, fields := req.Validate()
	if len(fields) > 0 {
		response.ValidationError(w, fields)
		return
	}
	rep, err := h.svc.SetItemNotes(r.Context(), userID, chi.URLParam(r, "id"), chi.URLParam(r, "itemID"), notes)
	writeReport(w, http.StatusOK, rep, userID, err)
}

// RemoveItem handles DELETE /api/v1/reports/{id}/items/{itemID}.
//
//	@Summary	Drop a task from a draft
//	@Tags		reports
//	@Produce	json
//	@Param		id		path		string					true	"Report ID"
//	@Param		itemID	path		string					true	"Report item ID"
//	@Success	200		{object}	map[string]DTO			"report"
//	@Failure	401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure	403		{object}	response.ErrorEnvelope	"Not allowed"
//	@Failure	404		{object}	response.ErrorEnvelope	"Not found"
//	@Failure	409		{object}	response.ErrorEnvelope	"Already sent"
//	@Router		/reports/{id}/items/{itemID} [delete]
func (h *Handler) RemoveItem(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	rep, err := h.svc.RemoveItem(r.Context(), userID, chi.URLParam(r, "id"), chi.URLParam(r, "itemID"))
	writeReport(w, http.StatusOK, rep, userID, err)
}

// Send handles POST /api/v1/reports/{id}/send.
//
//	@Summary		Send a draft to its client
//	@Description	Freezes it, its prices and currency; the client is notified.
//	@Tags			reports
//	@Produce		json
//	@Param			id	path		string					true	"Report ID"
//	@Success		200	{object}	map[string]DTO			"report"
//	@Failure		401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		403	{object}	response.ErrorEnvelope	"Not allowed"
//	@Failure		404	{object}	response.ErrorEnvelope	"Not found"
//	@Failure		409	{object}	response.ErrorEnvelope	"Already sent, or personal"
//	@Router			/reports/{id}/send [post]
func (h *Handler) Send(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	rep, err := h.svc.Send(r.Context(), userID, chi.URLParam(r, "id"))
	writeReport(w, http.StatusOK, rep, userID, err)
}

// MarkPaid handles POST /api/v1/reports/{id}/paid: the client paid it.
//
//	@Summary		Mark a sent report paid
//	@Description	The report's client only. Its tasks move to the board's Paid column; the freelancer is notified.
//	@Tags			reports
//	@Produce		json
//	@Param			id	path		string					true	"Report ID"
//	@Success		200	{object}	map[string]DTO			"report"
//	@Failure		401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		403	{object}	response.ErrorEnvelope	"Not the report's client"
//	@Failure		404	{object}	response.ErrorEnvelope	"Not found"
//	@Failure		409	{object}	response.ErrorEnvelope	"Not sent, or already paid"
//	@Router			/reports/{id}/paid [post]
func (h *Handler) MarkPaid(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	rep, err := h.svc.MarkPaid(r.Context(), userID, chi.URLParam(r, "id"))
	writeReport(w, http.StatusOK, rep, userID, err)
}

// History handles GET /api/v1/reports/{id}/history.
//
//	@Summary		A report's history
//	@Description	Its client sees only the sending and the payment.
//	@Tags			reports
//	@Produce		json
//	@Param			id	path		string					true	"Report ID"
//	@Success		200	{object}	map[string][]EventDTO	"events"
//	@Failure		401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		404	{object}	response.ErrorEnvelope	"Not found"
//	@Router			/reports/{id}/history [get]
func (h *Handler) History(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	events, err := h.svc.History(r.Context(), userID, chi.URLParam(r, "id"))
	if err != nil {
		writeReport(w, 0, Report{}, "", err)
		return
	}
	out := make([]EventDTO, 0, len(events))
	for _, e := range events {
		out = append(out, ToEventDTO(e))
	}
	response.JSON(w, http.StatusOK, map[string][]EventDTO{"events": out})
}

// Delete handles DELETE /api/v1/reports/{id}.
//
//	@Summary		Delete a report
//	@Description	Its freelancer, sent or not; a sent one is gone for the client too.
//	@Tags			reports
//	@Param			id	path	string	true	"Report ID"
//	@Success		204	"No content"
//	@Failure		401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		403	{object}	response.ErrorEnvelope	"Not allowed"
//	@Failure		404	{object}	response.ErrorEnvelope	"Not found"
//	@Router			/reports/{id} [delete]
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	if err := h.svc.Delete(r.Context(), userID, chi.URLParam(r, "id")); err != nil {
		writeReport(w, 0, Report{}, "", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeReport(w http.ResponseWriter, status int, rep Report, viewerID string, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		response.Error(w, http.StatusNotFound, response.CodeNotFound, "Report not found")
	case errors.Is(err, ErrNoClient):
		response.Error(w, http.StatusConflict, response.CodeConflict, "A personal report has no client to send it to — download it instead")
	case errors.Is(err, ErrNotClient):
		response.Error(w, http.StatusForbidden, response.CodeForbidden, "Only the client marks a report as paid")
	case errors.Is(err, ErrNotSent):
		response.Error(w, http.StatusConflict, response.CodeConflict, "Only a sent report that isn't paid yet can be marked as paid")
	case errors.Is(err, ErrSent):
		response.Error(w, http.StatusConflict, response.CodeConflict, "This report was already sent and can't be changed")
	case err != nil:
		response.Internal(w, err)
	default:
		response.JSON(w, status, map[string]DTO{"report": ToDTO(rep, viewerID)})
	}
}
