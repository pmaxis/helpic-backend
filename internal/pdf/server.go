package pdf

import (
	"io"
	"log/slog"
	"net/http"
)

const maxHTMLBytes = 10 << 20

// NewHandler serves POST /render: an HTML document in, its PDF out.
func NewHandler(chrome *Chrome) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /render", func(w http.ResponseWriter, r *http.Request) {
		html, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxHTMLBytes))
		if err != nil {
			http.Error(w, "unreadable or too large html", http.StatusRequestEntityTooLarge)
			return
		}
		pdf, err := chrome.Render(r.Context(), html)
		if err != nil {
			slog.Error("pdf render failed", "error", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write(pdf)
	})
	return mux
}
