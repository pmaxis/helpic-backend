package middleware

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestLogger_RecordsTheCall(t *testing.T) {
	line := captureLog(t, func() {
		r := chi.NewRouter()
		r.Use(Logger)
		r.Post("/api/v1/tasks/{id}", func(w http.ResponseWriter, r *http.Request) {
			noteActor(r.Context(), "user-1", "client")
			w.WriteHeader(http.StatusNoContent)
		})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/abc", nil)
		req.RemoteAddr = "203.0.113.9:4000"
		r.ServeHTTP(httptest.NewRecorder(), req)
	})

	if line["msg"] != "request" || line["level"] != "INFO" {
		t.Fatalf("log line: %v", line)
	}
	if line["method"] != http.MethodPost || line["path"] != "/api/v1/tasks/abc" {
		t.Fatalf("request: %v", line)
	}
	if line["route"] != "/api/v1/tasks/{id}" || line["status"] != float64(http.StatusNoContent) {
		t.Fatalf("outcome: %v", line)
	}
	if line["ip"] != "203.0.113.9" || line["user_id"] != "user-1" || line["role"] != "client" {
		t.Fatalf("caller: %v", line)
	}
	if _, ok := line["duration_ms"]; !ok {
		t.Fatalf("missing duration: %v", line)
	}
}

func TestLogger_WarnsOnClientErrors(t *testing.T) {
	line := captureLog(t, func() {
		Logger(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/missing", nil))
	})
	if line["level"] != "WARN" || line["status"] != float64(http.StatusNotFound) {
		t.Fatalf("log line: %v", line)
	}
}

func TestLogger_SkipsHealth(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	Logger(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/health", nil))

	if buf.Len() != 0 {
		t.Fatalf("health was logged: %s", buf.String())
	}
}

func captureLog(t *testing.T, call func()) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	call()

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("log %q: %v", buf.String(), err)
	}
	return line
}
