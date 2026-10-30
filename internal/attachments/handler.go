package attachments

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/pmaxis/helpic/internal/request"
	"github.com/pmaxis/helpic/internal/response"
	"github.com/pmaxis/helpic/internal/tasks"
)

// multipartSlack covers the multipart framing around the file itself.
const multipartSlack = 1 << 20

// Handler exposes the attachment endpoints over HTTP.
type Handler struct {
	svc *Service
}

// NewHandler creates an attachments Handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// Routes mounts the endpoints. Must be called inside an authenticated group.
func (h *Handler) Routes(r chi.Router) {
	r.Route("/attachments", func(r chi.Router) {
		r.Get("/", h.List)
		r.Post("/", h.Upload)
		r.Get("/{id}", h.Download)
		r.Delete("/{id}", h.Delete)
		r.Put("/{id}/review", h.Review)
	})
}

// List handles GET /api/v1/attachments?task={id}.
//
//	@Summary	A task's files
//	@Tags		attachments
//	@Produce	json
//	@Param		task	query		string					true	"Task ID"
//	@Success	200		{object}	ListResponse			"OK"
//	@Failure	401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure	404		{object}	response.ErrorEnvelope	"Not found"
//	@Router		/attachments [get]
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	list, canReview, err := h.svc.List(r.Context(), userID, r.URL.Query().Get("task"))
	if err != nil {
		WriteError(w, err)
		return
	}
	out := make([]DTO, 0, len(list))
	for _, a := range list {
		d := ToDTO(a, userID)
		d.CanReview = canReview && a.Latest
		out = append(out, d)
	}
	response.JSON(w, http.StatusOK, ListResponse{Attachments: out, MaxSizeBytes: h.svc.MaxSize()})
}

// Upload handles POST /api/v1/attachments?task={id}: a multipart form with one
// "file" part.
//
//	@Summary		Attach a file to a task
//	@Description	Multipart, one "file" part, up to MAX_UPLOAD_MB. The type is read from the bytes. With a virus scanner set up the file comes back with scan "pending" and downloads once it is checked.
//	@Tags			attachments
//	@Accept			multipart/form-data
//	@Produce		json
//	@Param			task		query		string					true	"Task ID"
//	@Param			replaces	query		string					false	"A file of the task this is a new version of"
//	@Param			file		formData	file					true	"The file"
//	@Success		201			{object}	map[string]DTO			"attachment"
//	@Failure		400			{object}	response.ErrorEnvelope	"Invalid input"
//	@Failure		401			{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		404			{object}	response.ErrorEnvelope	"Not found"
//	@Failure		413			{object}	response.ErrorEnvelope	"Too large"
//	@Router			/attachments [post]
func (h *Handler) Upload(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, h.svc.MaxSize()+multipartSlack)
	mr, err := r.MultipartReader()
	if err != nil {
		response.ValidationError(w, map[string]string{"file": "Choose a file to upload"})
		return
	}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			response.ValidationError(w, map[string]string{"file": "Choose a file to upload"})
			return
		}
		if err != nil {
			WriteError(w, uploadError(err))
			return
		}
		if part.FormName() != "file" {
			continue
		}
		q := r.URL.Query()
		a, err := h.svc.Upload(r.Context(), userID, q.Get("task"), part.FileName(), q.Get("replaces"), part)
		if err != nil {
			WriteError(w, uploadError(err))
			return
		}
		response.JSON(w, http.StatusCreated, map[string]DTO{"attachment": ToDTO(a, userID)})
		return
	}
}

// Download handles GET /api/v1/attachments/{id}. Files are always downloaded;
// ?inline=1 shows a safe image in place.
//
//	@Summary		Download a file
//	@Description	Always as a download, except PNG/JPEG/GIF/WebP images with inline=1. A file still being checked for viruses (scan "pending") or found infected answers 409.
//	@Tags			attachments
//	@Produce		octet-stream
//	@Param			id		path		string					true	"Attachment ID"
//	@Param			inline	query		string					false	"1 to show a safe image in place"
//	@Success		200		{file}		file					"The file"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		404		{object}	response.ErrorEnvelope	"Not found"
//	@Failure		409		{object}	response.ErrorEnvelope	"Being checked for viruses, or infected"
//	@Router			/attachments/{id} [get]
func (h *Handler) Download(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	a, f, err := h.svc.Open(r.Context(), userID, chi.URLParam(r, "id"))
	if err != nil {
		WriteError(w, err)
		return
	}
	defer f.Close()
	Serve(w, r, a, f)
}

// Serve writes a file Open or OpenOnTask opened: always as a download,
// except a safe image asked for with ?inline=1.
func Serve(w http.ResponseWriter, r *http.Request, a Attachment, f *os.File) {
	disposition := "attachment"
	if r.URL.Query().Get("inline") != "" && a.IsImage() {
		disposition = "inline"
	}
	w.Header().Set("Content-Type", a.ContentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`%s; filename="%s"; filename*=UTF-8''%s`, disposition, asciiFallback(a.FileName), url.PathEscape(a.FileName)))
	// Never let a browser guess a type or run anything from an upload.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src 'self'; sandbox")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeContent(w, r, "", a.CreatedAt, f)
}

// Review handles PUT /api/v1/attachments/{id}/review.
//
//	@Summary		Approve a file, or ask for changes
//	@Description	Someone on the task's client side, on a file's latest version: "approved", "changes_requested", or "" to take it back. The freelancer is told.
//	@Tags			attachments
//	@Accept			json
//	@Produce		json
//	@Param			id		path		string					true	"Attachment ID"
//	@Param			body	body		ReviewRequest			true	"Request body"
//	@Success		200		{object}	map[string]DTO			"attachment"
//	@Failure		400		{object}	response.ErrorEnvelope	"Invalid input"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		403		{object}	response.ErrorEnvelope	"Not the client's side"
//	@Failure		404		{object}	response.ErrorEnvelope	"Not found"
//	@Failure		409		{object}	response.ErrorEnvelope	"Archived, or not the latest version"
//	@Router			/attachments/{id}/review [put]
func (h *Handler) Review(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	var req ReviewRequest
	if !request.Decode(w, r, &req) {
		return
	}
	review, fields := req.Validate()
	if len(fields) > 0 {
		response.ValidationError(w, fields)
		return
	}
	a, err := h.svc.Review(r.Context(), userID, chi.URLParam(r, "id"), review)
	if err != nil {
		WriteError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]DTO{"attachment": ToDTO(a, userID)})
}

// Delete handles DELETE /api/v1/attachments/{id}.
//
//	@Summary		Delete a file
//	@Description	Its uploader only.
//	@Tags			attachments
//	@Param			id	path	string	true	"Attachment ID"
//	@Success		204	"No content"
//	@Failure		401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		403	{object}	response.ErrorEnvelope	"Not allowed"
//	@Failure		404	{object}	response.ErrorEnvelope	"Not found"
//	@Router			/attachments/{id} [delete]
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	if err := h.svc.Delete(r.Context(), userID, chi.URLParam(r, "id")); err != nil {
		WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// uploadError turns a body over the request limit into ErrTooLarge.
func uploadError(err error) error {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		return ErrTooLarge
	}
	return err
}

// WriteError answers with what went wrong with a file.
func WriteError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, tasks.ErrNotFound):
		response.Error(w, http.StatusNotFound, response.CodeNotFound, "File not found")
	case errors.Is(err, tasks.ErrArchived):
		response.Error(w, http.StatusConflict, response.CodeConflict, "This task is archived: its files stay as they are")
	case errors.Is(err, ErrNotReviewer):
		response.Error(w, http.StatusForbidden, response.CodeForbidden, "Only the client's side approves a file or asks for changes")
	case errors.Is(err, ErrNotLatest):
		response.Error(w, http.StatusConflict, response.CodeConflict, "A newer version replaced this file: review that one")
	case errors.Is(err, ErrNotAllowed):
		response.Error(w, http.StatusForbidden, response.CodeForbidden, "Only whoever uploaded a file can delete it")
	case errors.Is(err, ErrTooLarge):
		response.Error(w, http.StatusRequestEntityTooLarge, response.CodeValidation, "The file is too large")
	case errors.Is(err, ErrQuota):
		response.Error(w, http.StatusRequestEntityTooLarge, response.CodeValidation, "You've used up your space for files. Delete some of yours to add more.")
	case errors.Is(err, ErrScanPending):
		response.Error(w, http.StatusConflict, response.CodeConflict, "This file is still being checked for viruses. Try again in a moment.")
	case errors.Is(err, ErrInfected):
		response.Error(w, http.StatusConflict, response.CodeConflict, "This file was removed: the virus check found malware in it")
	case errors.Is(err, ErrEmpty):
		response.ValidationError(w, map[string]string{"file": "The file is empty"})
	default:
		response.Internal(w, err)
	}
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
