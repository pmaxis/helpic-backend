package attachments

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	list, err := h.svc.List(r.Context(), userID, r.URL.Query().Get("task"))
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]DTO, 0, len(list))
	for _, a := range list {
		out = append(out, ToDTO(a, userID))
	}
	response.JSON(w, http.StatusOK, ListResponse{Attachments: out, MaxSizeBytes: h.svc.MaxSize()})
}

// Upload handles POST /api/v1/attachments?task={id}: a multipart form with one
// "file" part.
//
//	@Summary		Attach a file to a task
//	@Description	Multipart, one "file" part, up to MAX_UPLOAD_MB. The type is read from the bytes.
//	@Tags			attachments
//	@Accept			multipart/form-data
//	@Produce		json
//	@Param			task	query		string					true	"Task ID"
//	@Param			file	formData	file					true	"The file"
//	@Success		201		{object}	map[string]DTO			"attachment"
//	@Failure		400		{object}	response.ErrorEnvelope	"Invalid input"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		404		{object}	response.ErrorEnvelope	"Not found"
//	@Failure		413		{object}	response.ErrorEnvelope	"Too large"
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
			writeError(w, uploadError(err))
			return
		}
		if part.FormName() != "file" {
			continue
		}
		a, err := h.svc.Upload(r.Context(), userID, r.URL.Query().Get("task"), part.FileName(), part)
		if err != nil {
			writeError(w, uploadError(err))
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
//	@Description	Always as a download, except PNG/JPEG/GIF/WebP images with inline=1.
//	@Tags			attachments
//	@Produce		octet-stream
//	@Param			id		path		string					true	"Attachment ID"
//	@Param			inline	query		string					false	"1 to show a safe image in place"
//	@Success		200		{file}		file					"The file"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		404		{object}	response.ErrorEnvelope	"Not found"
//	@Router			/attachments/{id} [get]
func (h *Handler) Download(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	a, f, err := h.svc.Open(r.Context(), userID, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, err)
		return
	}
	defer f.Close()

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
		writeError(w, err)
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

func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, tasks.ErrNotFound):
		response.Error(w, http.StatusNotFound, response.CodeNotFound, "File not found")
	case errors.Is(err, ErrNotAllowed):
		response.Error(w, http.StatusForbidden, response.CodeForbidden, "Only whoever uploaded a file can delete it")
	case errors.Is(err, ErrTooLarge):
		response.Error(w, http.StatusRequestEntityTooLarge, response.CodeValidation, "The file is too large")
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
