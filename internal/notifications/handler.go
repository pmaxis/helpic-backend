package notifications

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/pmaxis/helpic/internal/request"
	"github.com/pmaxis/helpic/internal/response"
)

// Handler exposes the user's notifications over HTTP.
type Handler struct {
	svc *Service
}

// NewHandler creates a notifications Handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// Routes mounts the endpoints. Must be called inside an authenticated group.
func (h *Handler) Routes(r chi.Router) {
	r.Route("/notifications", func(r chi.Router) {
		r.Get("/", h.List)
		r.Get("/unread-count", h.UnreadCount)
		r.Post("/read-all", h.MarkAllRead)
		r.Post("/{id}/read", h.MarkRead)
	})
}

// ListResponse is the latest notifications and how many are unread.
type ListResponse struct {
	Notifications []DTO `json:"notifications"`
	Unread        int   `json:"unread"`
}

// ActorDTO is who did the thing.
type ActorDTO struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// DTO is a notification as its recipient sees it.
type DTO struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Actor     ActorDTO  `json:"actor"`
	TaskID    *string   `json:"task_id"`
	ProjectID *string   `json:"project_id"`
	ReportID  *string   `json:"report_id"`
	Subject   string    `json:"subject"`
	Detail    string    `json:"detail"`
	Read      bool      `json:"read"`
	CreatedAt time.Time `json:"created_at"`
}

func toDTO(n Notification) DTO {
	d := DTO{
		ID:        n.ID,
		Kind:      n.Kind,
		Actor:     ActorDTO{ID: n.ActorID, Name: n.ActorName, Email: n.ActorEmail},
		Subject:   n.Subject,
		Detail:    n.Detail,
		Read:      n.ReadAt != nil,
		CreatedAt: n.CreatedAt,
	}
	if n.TaskID != "" {
		d.TaskID = &n.TaskID
	}
	if n.ProjectID != "" {
		d.ProjectID = &n.ProjectID
	}
	if n.ReportID != "" {
		d.ReportID = &n.ReportID
	}
	return d
}

// List handles GET /api/v1/notifications.
//
//	@Summary		Your notifications
//	@Description	The latest 50, and how many are unread.
//	@Tags			notifications
//	@Produce		json
//	@Success		200	{object}	ListResponse			"OK"
//	@Failure		401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Router			/notifications [get]
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	list, unread, err := h.svc.List(r.Context(), userID)
	if err != nil {
		response.Internal(w, err)
		return
	}
	out := make([]DTO, 0, len(list))
	for _, n := range list {
		out = append(out, toDTO(n))
	}
	response.JSON(w, http.StatusOK, ListResponse{Notifications: out, Unread: unread})
}

// UnreadCount handles GET /api/v1/notifications/unread-count — cheap enough to poll.
//
//	@Summary		How many are unread
//	@Description	Cheap enough to poll.
//	@Tags			notifications
//	@Produce		json
//	@Success		200	{object}	map[string]int			"unread"
//	@Failure		401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Router			/notifications/unread-count [get]
func (h *Handler) UnreadCount(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	n, err := h.svc.UnreadCount(r.Context(), userID)
	if err != nil {
		response.Internal(w, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]int{"unread": n})
}

// MarkRead handles POST /api/v1/notifications/{id}/read.
//
//	@Summary	Mark one read
//	@Tags		notifications
//	@Param		id	path	string	true	"Notification ID"
//	@Success	204	"No content"
//	@Failure	401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure	404	{object}	response.ErrorEnvelope	"Not found"
//	@Router		/notifications/{id}/read [post]
func (h *Handler) MarkRead(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	err := h.svc.MarkRead(r.Context(), userID, chi.URLParam(r, "id"))
	if errors.Is(err, ErrNotFound) {
		response.Error(w, http.StatusNotFound, response.CodeNotFound, "Notification not found")
		return
	}
	if err != nil {
		response.Internal(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// MarkAllRead handles POST /api/v1/notifications/read-all.
//
//	@Summary	Mark all read
//	@Tags		notifications
//	@Success	204	"No content"
//	@Failure	401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Router		/notifications/read-all [post]
func (h *Handler) MarkAllRead(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	if err := h.svc.MarkAllRead(r.Context(), userID); err != nil {
		response.Internal(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
