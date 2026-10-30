package branding

import (
	"bytes"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/pmaxis/helpic/internal/request"
	"github.com/pmaxis/helpic/internal/response"
)

// multipartSlack covers the multipart framing around the logo itself.
const multipartSlack = 64 << 10

// Handler exposes the endpoints for the look of reports over HTTP.
type Handler struct {
	svc *Service
}

// NewHandler creates a branding Handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// Routes mounts the endpoints. Must be called inside a group for freelancers:
// reports are theirs.
func (h *Handler) Routes(r chi.Router) {
	r.Route("/me/branding", func(r chi.Router) {
		r.Get("/", h.Get)
		r.Put("/", h.Set)
		r.Get("/logo", h.Logo)
		r.Put("/logo", h.SetLogo)
		r.Delete("/logo", h.RemoveLogo)
	})
}

// Get handles GET /api/v1/me/branding.
//
//	@Summary	How your reports look
//	@Tags		branding
//	@Produce	json
//	@Success	200	{object}	Response				"OK"
//	@Failure	401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure	403	{object}	response.ErrorEnvelope	"Not a freelancer"
//	@Router		/me/branding [get]
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	b, err := h.svc.Get(r.Context(), userID)
	writeBranding(w, b, err)
}

// Set handles PUT /api/v1/me/branding: the accent color and business details.
//
//	@Summary		Change how your reports look
//	@Description	The accent color ("#rrggbb", null for none) and the business details printed at the end of a report. Drafts show them at once; sent reports keep the ones they were sent with.
//	@Tags			branding
//	@Accept			json
//	@Produce		json
//	@Param			body	body		Request					true	"Request body"
//	@Success		200		{object}	Response				"OK"
//	@Failure		400		{object}	response.ErrorEnvelope	"Invalid input"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		403		{object}	response.ErrorEnvelope	"Not a freelancer"
//	@Router			/me/branding [put]
func (h *Handler) Set(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	var req Request
	if !request.Decode(w, r, &req) {
		return
	}
	p, fields := req.Validate()
	if len(fields) > 0 {
		response.ValidationError(w, fields)
		return
	}
	b, err := h.svc.Set(r.Context(), userID, p)
	writeBranding(w, b, err)
}

// SetLogo handles PUT /api/v1/me/branding/logo: a multipart form with one
// "file" part.
//
//	@Summary		Put a logo on your reports
//	@Description	A PNG or JPEG of up to 512 KB and 4000 pixels a side, in place of the one you had.
//	@Tags			branding
//	@Accept			multipart/form-data
//	@Produce		json
//	@Param			file	formData	file					true	"The logo"
//	@Success		200		{object}	Response				"OK"
//	@Failure		400		{object}	response.ErrorEnvelope	"Not an image"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		403		{object}	response.ErrorEnvelope	"Not a freelancer"
//	@Failure		413		{object}	response.ErrorEnvelope	"Too large"
//	@Router			/me/branding/logo [put]
func (h *Handler) SetLogo(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxLogoBytes+multipartSlack)
	mr, err := r.MultipartReader()
	if err != nil {
		response.ValidationError(w, map[string]string{"file": "Choose an image for the logo"})
		return
	}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			response.ValidationError(w, map[string]string{"file": "Choose an image for the logo"})
			return
		}
		if err != nil {
			writeBranding(w, Branding{}, uploadError(err))
			return
		}
		if part.FormName() != "file" {
			continue
		}
		b, err := h.svc.SetLogo(r.Context(), userID, part)
		writeBranding(w, b, uploadError(err))
		return
	}
}

// uploadError turns a body over the request limit into ErrTooLarge.
func uploadError(err error) error {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		return ErrTooLarge
	}
	return err
}

// RemoveLogo handles DELETE /api/v1/me/branding/logo.
//
//	@Summary		Take the logo off your reports
//	@Description	Reports you sent keep theirs.
//	@Tags			branding
//	@Produce		json
//	@Success		200	{object}	Response				"OK"
//	@Failure		401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		403	{object}	response.ErrorEnvelope	"Not a freelancer"
//	@Router			/me/branding/logo [delete]
func (h *Handler) RemoveLogo(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	b, err := h.svc.RemoveLogo(r.Context(), userID)
	writeBranding(w, b, err)
}

// Logo handles GET /api/v1/me/branding/logo.
//
//	@Summary	Your logo
//	@Tags		branding
//	@Produce	png,jpeg
//	@Success	200	{file}		file					"The image"
//	@Failure	401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure	404	{object}	response.ErrorEnvelope	"No logo"
//	@Router		/me/branding/logo [get]
func (h *Handler) Logo(w http.ResponseWriter, r *http.Request) {
	userID, ok := request.User(w, r)
	if !ok {
		return
	}
	l, err := h.svc.OwnLogo(r.Context(), userID)
	if err != nil {
		WriteLogoError(w, err)
		return
	}
	ServeLogo(w, r, l)
}

// ServeLogo writes a logo as an image, which never runs anything. A logo
// never changes, so its address may be cached for long.
func ServeLogo(w http.ResponseWriter, r *http.Request, l Logo) {
	w.Header().Set("Content-Type", l.ContentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeContent(w, r, "", l.CreatedAt, bytes.NewReader(l.Data))
}

// WriteLogoError answers a request for a logo that failed.
func WriteLogoError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrNotFound) {
		response.Error(w, http.StatusNotFound, response.CodeNotFound, "No logo")
		return
	}
	response.Internal(w, err)
}

func writeBranding(w http.ResponseWriter, b Branding, err error) {
	switch {
	case errors.Is(err, ErrNotImage):
		response.ValidationError(w, map[string]string{"file": "The logo must be a PNG or JPEG image"})
	case errors.Is(err, ErrTooBig):
		response.ValidationError(w, map[string]string{"file": "The logo must be at most 4000 pixels on a side"})
	case errors.Is(err, ErrTooLarge):
		response.Error(w, http.StatusRequestEntityTooLarge, response.CodeValidation, "The logo must be at most 512 KB")
	case errors.Is(err, ErrNotFound):
		response.Error(w, http.StatusUnauthorized, response.CodeUnauthorized, "Authentication required")
	case err != nil:
		response.Internal(w, err)
	default:
		response.JSON(w, http.StatusOK, Response{Branding: ToDTO(b)})
	}
}
