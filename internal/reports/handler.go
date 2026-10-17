package reports

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/pmaxis/helpic/internal/middleware"
	"github.com/pmaxis/helpic/internal/request"
	"github.com/pmaxis/helpic/internal/response"
	"github.com/pmaxis/helpic/internal/user"
)

// Handler exposes the report endpoints over HTTP.
type Handler struct {
	svc *Service
	pdf *PDFRenderer
}

// NewHandler creates a reports Handler.
func NewHandler(svc *Service, pdf *PDFRenderer) *Handler {
	return &Handler{svc: svc, pdf: pdf}
}

// Routes mounts the endpoints. Must be called inside an authenticated group;
// making and changing reports is for freelancers.
func (h *Handler) Routes(r chi.Router) {
	r.Route("/reports", func(r chi.Router) {
		r.Get("/", h.List)
		r.Get("/{id}", h.Get)
		r.Get("/{id}/html", h.HTML)
		r.Get("/{id}/pdf", h.PDF)
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

// List handles GET /api/reports.
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
	response.JSON(w, http.StatusOK, map[string]any{"reports": out, "pdf_available": h.pdf.Available()})
}

// Get handles GET /api/reports/{id}.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	rep, err := h.svc.Get(r.Context(), userID, chi.URLParam(r, "id"))
	writeReport(w, http.StatusOK, rep, userID, err)
}

// Create handles POST /api/reports.
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

// Update handles PUT /api/reports/{id}.
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

// UpdateItem handles PUT /api/reports/{id}/items/{itemID}: the task's notes.
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

// RemoveItem handles DELETE /api/reports/{id}/items/{itemID}.
func (h *Handler) RemoveItem(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	rep, err := h.svc.RemoveItem(r.Context(), userID, chi.URLParam(r, "id"), chi.URLParam(r, "itemID"))
	writeReport(w, http.StatusOK, rep, userID, err)
}

// Send handles POST /api/reports/{id}/send.
func (h *Handler) Send(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	rep, err := h.svc.Send(r.Context(), userID, chi.URLParam(r, "id"))
	writeReport(w, http.StatusOK, rep, userID, err)
}

// MarkPaid handles POST /api/reports/{id}/paid: the client paid it.
func (h *Handler) MarkPaid(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	rep, err := h.svc.MarkPaid(r.Context(), userID, chi.URLParam(r, "id"))
	writeReport(w, http.StatusOK, rep, userID, err)
}

// History handles GET /api/reports/{id}/history.
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

// Delete handles DELETE /api/reports/{id}.
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

// HTML handles GET /api/reports/{id}/html: the on-screen sheet, for servers
// that can't print PDFs.
func (h *Handler) HTML(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	rep, err := h.svc.Get(r.Context(), userID, chi.URLParam(r, "id"))
	if err != nil {
		writeReport(w, 0, Report{}, "", err)
		return
	}
	html, err := Render(rep, ModePreview)
	if err != nil {
		response.Internal(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	// Shown in a sandboxed iframe; forbid scripts outright.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline' https://fonts.googleapis.com; font-src https://fonts.gstatic.com")
	_, _ = w.Write(html)
}

// PDF handles GET /api/reports/{id}/pdf: the file, printed by headless Chrome.
// ?download=1 asks the browser to save it rather than show it.
func (h *Handler) PDF(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	rep, err := h.svc.Get(r.Context(), userID, chi.URLParam(r, "id"))
	if err != nil {
		writeReport(w, 0, Report{}, "", err)
		return
	}
	html, err := Render(rep, ModePDF)
	if err != nil {
		response.Internal(w, err)
		return
	}
	pdf, err := h.pdf.Render(r.Context(), html)
	if errors.Is(err, ErrPDFUnavailable) {
		response.Error(w, http.StatusServiceUnavailable, response.CodeInternal, "PDF export is not available on this server")
		return
	}
	if err != nil {
		slog.Error("report pdf failed", "report_id", rep.ID, "error", err)
		response.Error(w, http.StatusInternalServerError, response.CodeInternal, "Could not generate the PDF")
		return
	}
	disposition := "inline"
	if r.URL.Query().Get("download") != "" {
		disposition = "attachment"
	}
	name := fileName(rep.Title)
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`%s; filename="%s"; filename*=UTF-8''%s`, disposition, asciiFallback(name), url.PathEscape(name)))
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(pdf)
}

var unsafeFileChars = regexp.MustCompile(`[\\/:*?"<>|\x00-\x1f]+`)

// fileName builds e.g. "Work report — Oct 2026.pdf".
func fileName(title string) string {
	base := strings.TrimSpace(unsafeFileChars.ReplaceAllString(title, " "))
	if base == "" {
		base = "Work report"
	}
	return base + ".pdf"
}

// asciiFallback is the filename for clients that ignore filename*.
func asciiFallback(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 0x20 && r < 0x7f && r != '"' && r != '\\' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
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
