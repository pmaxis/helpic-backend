package user

import (
	"errors"
	"net/http"

	"github.com/pmaxis/helpic/internal/middleware"
	"github.com/pmaxis/helpic/internal/response"
)

// Handler exposes user endpoints over HTTP.
type Handler struct {
	svc *Service
}

// NewHandler creates a user Handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// Me handles GET /api/me. It requires the auth middleware to have populated the
// request context with the authenticated user ID.
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, response.CodeUnauthorized, "Authentication required")
		return
	}

	u, err := h.svc.GetByID(r.Context(), userID)
	if errors.Is(err, ErrNotFound) {
		response.Error(w, http.StatusUnauthorized, response.CodeUnauthorized, "Authentication required")
		return
	}
	if err != nil {
		response.Internal(w, err)
		return
	}

	response.JSON(w, http.StatusOK, NewResponse(u))
}
