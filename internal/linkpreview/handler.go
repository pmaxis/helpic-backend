package linkpreview

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/pmaxis/helpic/internal/i18n"
	"github.com/pmaxis/helpic/internal/request"
	"github.com/pmaxis/helpic/internal/response"
)

// Titler reads a page's title; a Fetcher does.
type Titler interface {
	Title(ctx context.Context, url, language string) (string, error)
}

// Handler exposes the title of a page over HTTP.
type Handler struct {
	titles Titler
}

// NewHandler creates a link preview Handler.
func NewHandler(titles Titler) *Handler {
	return &Handler{titles: titles}
}

// Routes mounts the endpoint for signed-in users; limit holds back how many
// pages one user has fetched.
func (h *Handler) Routes(r chi.Router, limit func(http.Handler) http.Handler) {
	r.With(limit).Post("/link-preview", h.Preview)
}

// Request asks for the title of the page at URL.
type Request struct {
	URL string `json:"url" example:"https://example.com/docs"`
}

// Response is the page's title.
type Response struct {
	Title string `json:"title" example:"Example Domain"`
}

// Preview handles POST /api/v1/link-preview.
//
//	@Summary		A web page's title
//	@Description	For naming a link after the page it leads to: the page's og:title, twitter:title or <title>. Only public http(s) addresses on ports 80 and 443; the server reads the page for up to 5 seconds, its first 512 KB. Titles are remembered for an hour.
//	@Tags			links
//	@Accept			json
//	@Produce		json
//	@Param			body	body		Request					true	"Request body"
//	@Success		200		{object}	Response				"OK"
//	@Failure		400		{object}	response.ErrorEnvelope	"Not a web address"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		422		{object}	response.ErrorEnvelope	"The page couldn't be read or has no title"
//	@Failure		429		{object}	response.ErrorEnvelope	"Too many pages asked for"
//	@Router			/link-preview [post]
func (h *Handler) Preview(w http.ResponseWriter, r *http.Request) {
	if _, ok := request.User(w, r); !ok {
		return
	}
	var req Request
	if !request.Decode(w, r, &req) {
		return
	}
	title, err := h.titles.Title(r.Context(), req.URL, i18n.FromContext(r.Context()))
	switch {
	case err == nil:
		response.JSON(w, http.StatusOK, Response{Title: title})
	case errors.Is(err, ErrInvalidURL):
		response.ValidationError(w, map[string]string{"url": "Enter a public web address"})
	default:
		response.Error(w, http.StatusUnprocessableEntity, response.CodeUnprocessable, "Couldn't read the page's title")
	}
}
