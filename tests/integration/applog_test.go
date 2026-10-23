package integration

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"testing"

	"github.com/pmaxis/helpic/internal/applog"
)

// logSome logs through the applog handler the way the server does and waits
// for the sink to write.
func logSome(t *testing.T, log func(*slog.Logger)) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), `TRUNCATE app_logs`); err != nil {
		t.Fatal(err)
	}
	sink := applog.NewSink(applog.NewRepository(testPool), slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { sink.Run(ctx); close(done) }()
	log(slog.New(applog.NewHandler(slog.DiscardHandler, sink)))
	cancel()
	<-done
}

func TestAdmin_Logs(t *testing.T) {
	srv, _ := newAPI(t)
	boss := signInAdmin(t, srv, "boss@example.com")
	turnOnTwoFactor(t, srv, boss)
	someone := signUpFreelancer(t, srv, "someone@example.com")

	logSome(t, func(l *slog.Logger) {
		l.Info("request", "status", 200)
		l.Warn("request", "path", "/api/v1/invites/secret-token", "route", "/api/v1/invites/{token}", "status", 404)
		l.With("job", "email").Error("background job failed", "error", errors.New("smtp down"))
	})

	logs := func(query string) map[string]any {
		t.Helper()
		resp := get(t, boss, srv.URL+"/api/v1/admin/logs"+query)
		wantStatus(t, resp, http.StatusOK)
		return decode(t, resp)
	}
	entries := func(page map[string]any) []map[string]any {
		var out []map[string]any
		for _, e := range page["logs"].([]any) {
			out = append(out, e.(map[string]any))
		}
		return out
	}

	all := logs("")
	got := entries(all)
	if len(got) != 2 || all["warnings"] != 1.0 || all["errors"] != 1.0 || all["total"] != 2.0 {
		t.Fatalf("all = %v", all)
	}
	// The newest first; INFO isn't kept.
	failed, request := got[0], got[1]
	if failed["level"] != "ERROR" || failed["message"] != "background job failed" {
		t.Fatalf("first = %v", failed)
	}
	if a := failed["attrs"].(map[string]any); a["job"] != "email" || a["error"] != "smtp down" {
		t.Fatalf("error attrs = %v", a)
	}
	// The route stays, the path with its token doesn't.
	if a := request["attrs"].(map[string]any); a["route"] != "/api/v1/invites/{token}" || a["status"] != 404.0 || a["path"] != nil {
		t.Fatalf("request attrs = %v", a)
	}

	if got := entries(logs("?level=warn")); len(got) != 1 || got[0]["level"] != "WARN" {
		t.Fatalf("warnings = %v", got)
	}
	if got := entries(logs("?q=SMTP")); len(got) != 1 || got[0]["level"] != "ERROR" {
		t.Fatalf("search = %v", got)
	}

	if got := logs("?level=warn"); got["total"] != 1.0 {
		t.Fatalf("total with a filter = %v", got["total"])
	}
	second := logs("?limit=1&page=2")
	if got := entries(second); len(got) != 1 || got[0]["level"] != "WARN" || second["total"] != 2.0 || second["page"] != 2.0 {
		t.Fatalf("second page = %v", second)
	}
	if got := entries(logs("?limit=1&page=3")); len(got) != 0 {
		t.Fatalf("past the last page = %v", got)
	}

	for _, bad := range []string{"?level=info", "?page=0", "?limit=201"} {
		resp := get(t, boss, srv.URL+"/api/v1/admin/logs"+bad)
		wantStatus(t, resp, http.StatusBadRequest)
		resp.Body.Close()
	}
	wantStatus(t, get(t, someone, srv.URL+"/api/v1/admin/logs"), http.StatusForbidden)
}
