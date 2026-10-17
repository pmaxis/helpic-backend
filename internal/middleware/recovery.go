package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/pmaxis/helpic/internal/response"
)

// Recovery returns middleware that converts an unhandled panic into a logged
// 500 response. Stack traces are logged server-side only, never sent to clients.
func Recovery(devMode bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					if devMode {
						slog.Error("panic recovered", "panic", rec, "stack", string(debug.Stack()), "path", r.URL.Path)
					} else {
						slog.Error("panic recovered", "panic", rec, "path", r.URL.Path)
					}
					response.Error(w, http.StatusInternalServerError, response.CodeInternal, "Something went wrong")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
