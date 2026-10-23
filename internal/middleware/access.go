package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

// actor is filled in by Auth so the request log, which runs outside Auth,
// can name who made the call. A pointer because the value lives in the
// context the logger created, and Auth only receives a child of it.
type actor struct {
	id   string
	role string
}

type actorKey struct{}

// noteActor records the signed-in user on the request the logger is watching.
func noteActor(ctx context.Context, id, role string) {
	if a, ok := ctx.Value(actorKey{}).(*actor); ok {
		a.id = id
		a.role = role
	}
}

// Logger writes one line per request: what was called, how it ended, how long
// it took, and who called it when the request was signed in. /health is
// skipped so a probe does not drown out the rest.
func Logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}

		start := time.Now()
		who := &actor{}
		rec := &statusRecorder{ResponseWriter: w}
		ctx := context.WithValue(r.Context(), actorKey{}, who)
		next.ServeHTTP(rec, r.WithContext(ctx))

		status := rec.status
		if !rec.wrote {
			status = http.StatusOK
		}
		pattern := routePattern(r)
		level := slog.LevelInfo
		if status >= http.StatusInternalServerError {
			level = slog.LevelError
		} else if status >= http.StatusBadRequest && !(status == http.StatusUnauthorized && probes[pattern]) {
			level = slog.LevelWarn
		}

		attrs := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"status", status,
			"duration_ms", time.Since(start).Milliseconds(),
			"ip", ClientIP(r),
		}
		if pattern != "" {
			attrs = append(attrs, "route", pattern)
		}
		if who.id != "" {
			attrs = append(attrs, "user_id", who.id, "role", who.role)
		}
		slog.Log(r.Context(), level, "request", attrs...)
	})
}

// probes are where the frontend asks whether anyone is signed in: a 401
// there is the usual answer for a visitor, not a warning.
var probes = map[string]bool{"/api/v1/me": true, "/api/v1/auth/refresh": true}

// routePattern is the matched route, with placeholders instead of ids, so
// logs group by endpoint. Empty when the request never reached the router.
func routePattern(r *http.Request) string {
	rc := chi.RouteContext(r.Context())
	if rc == nil {
		return ""
	}
	return rc.RoutePattern()
}

// statusRecorder remembers the status the handler sent.
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *statusRecorder) WriteHeader(code int) {
	if !w.wrote {
		w.status = code
		w.wrote = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusRecorder) Write(b []byte) (int, error) {
	if !w.wrote {
		w.status = http.StatusOK
		w.wrote = true
	}
	return w.ResponseWriter.Write(b)
}

// Unwrap gives http.ResponseController the underlying writer.
func (w *statusRecorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }
