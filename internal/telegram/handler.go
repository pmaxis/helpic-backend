package telegram

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/pmaxis/helpic/internal/request"
	"github.com/pmaxis/helpic/internal/response"
)

// Handler exposes the Telegram connection endpoints over HTTP.
type Handler struct {
	svc *Service
}

// NewHandler creates a telegram Handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// Routes mounts the endpoints. Must be called inside an authenticated group.
func (h *Handler) Routes(r chi.Router) {
	r.Route("/me/telegram", func(r chi.Router) {
		r.Get("/", h.Status)
		r.Post("/link", h.Connect)
		r.Put("/kinds", h.SetKinds)
		r.Delete("/", h.Disconnect)
	})
}

// Status handles GET /api/v1/me/telegram.
//
//	@Summary	Your Telegram connection
//	@Tags		telegram
//	@Produce	json
//	@Success	200	{object}	map[string]StatusDTO	"telegram"
//	@Failure	401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Router		/me/telegram [get]
func (h *Handler) Status(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	s, err := h.svc.Status(r.Context(), userID)
	if err != nil {
		response.Internal(w, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]StatusDTO{"telegram": ToStatusDTO(s)})
}

// Connect handles POST /api/v1/me/telegram/link.
//
//	@Summary		Get a link that connects Telegram
//	@Description	A one-time t.me link to the bot, good for 15 minutes: opening it in Telegram connects that chat.
//	@Tags			telegram
//	@Produce		json
//	@Success		200	{object}	map[string]ConnectDTO	"link"
//	@Failure		401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		503	{object}	response.ErrorEnvelope	"No bot on this server, or it isn't ready"
//	@Router			/me/telegram/link [post]
func (h *Handler) Connect(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	url, expires, err := h.svc.ConnectURL(r.Context(), userID)
	switch {
	case errors.Is(err, ErrDisabled):
		response.Error(w, http.StatusServiceUnavailable, response.CodeInternal, "Telegram is not set up on this server")
	case errors.Is(err, ErrNotReady):
		response.Error(w, http.StatusServiceUnavailable, response.CodeInternal, "The Telegram bot is starting. Try again in a moment.")
	case err != nil:
		response.Internal(w, err)
	default:
		response.JSON(w, http.StatusOK, map[string]ConnectDTO{"link": {URL: url, ExpiresAt: expires}})
	}
}

// SetKinds handles PUT /api/v1/me/telegram/kinds.
//
//	@Summary		Choose what goes to Telegram
//	@Description	The notification kinds sent to the connected chat; see "available" in GET /me/telegram.
//	@Tags			telegram
//	@Accept			json
//	@Produce		json
//	@Param			body	body		KindsRequest			true	"Request body"
//	@Success		200		{object}	map[string]StatusDTO	"telegram"
//	@Failure		400		{object}	response.ErrorEnvelope	"Unknown kind"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		404		{object}	response.ErrorEnvelope	"Telegram is not connected"
//	@Router			/me/telegram/kinds [put]
func (h *Handler) SetKinds(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	var req KindsRequest
	if !request.Decode(w, r, &req) {
		return
	}
	l, err := h.svc.SetKinds(r.Context(), userID, req.Kinds)
	switch {
	case errors.Is(err, ErrUnknownKind):
		response.ValidationError(w, map[string]string{"kinds": "Unknown notification kind"})
	case errors.Is(err, ErrNotConnected):
		response.Error(w, http.StatusNotFound, response.CodeNotFound, "Telegram is not connected")
	case err != nil:
		response.Internal(w, err)
	default:
		response.JSON(w, http.StatusOK, map[string]StatusDTO{"telegram": ToStatusDTO(Status{Enabled: true, Link: &l})})
	}
}

// Disconnect handles DELETE /api/v1/me/telegram.
//
//	@Summary	Disconnect Telegram
//	@Tags		telegram
//	@Success	204	"Disconnected"
//	@Failure	401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure	404	{object}	response.ErrorEnvelope	"Telegram is not connected"
//	@Router		/me/telegram [delete]
func (h *Handler) Disconnect(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	err := h.svc.Disconnect(r.Context(), userID)
	if errors.Is(err, ErrNotConnected) {
		response.Error(w, http.StatusNotFound, response.CodeNotFound, "Telegram is not connected")
		return
	}
	if err != nil {
		response.Internal(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
