package integration

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pmaxis/helpic/internal/admin"
	"github.com/pmaxis/helpic/internal/config"
)

func TestAdmin_EndAnotherAdminsSession(t *testing.T) {
	bot, tg := newFakeBot(t)
	srv, _, _ := newAPIWith(t, func(cfg *config.Config) {
		cfg.TelegramAPIURL = tg.URL
		cfg.AlertBotToken = "test-token"
		cfg.AlertChatID = alertChat
		cfg.AlertLocale = "en"
	})
	boss, codes := stepUpAdmin(t, srv, "boss@example.com")
	laptop := signInAdmin(t, srv, "second@example.com")

	resp := get(t, boss, srv.URL+"/api/v1/admin/admins")
	wantStatus(t, resp, http.StatusOK)
	var secondID, sessionID string
	for _, a := range decode(t, resp)["admins"].([]any) {
		if a := a.(map[string]any); a["email"] == "second@example.com" {
			secondID = a["id"].(string)
			sessionID = a["sessions"].([]any)[0].(map[string]any)["id"].(string)
		}
	}

	end := srv.URL + "/api/v1/admin/admins/" + secondID + "/sessions/" + sessionID + "/end"
	wantStatus(t, post(t, boss, end, map[string]string{"reason": "Stolen laptop"}), http.StatusBadRequest)
	wantStatus(t, post(t, boss, end, map[string]string{"reason": "Stolen laptop", "code": codes.next(t)}), http.StatusNoContent)
	wantStatus(t, get(t, laptop, srv.URL+"/api/v1/me"), http.StatusUnauthorized)

	bot.waitSent(t, alertChat, "boss@example.com signed the admin second@example.com out of a device")
	if e := lastEmailTo(t, "second@example.com"); !strings.Contains(e.Subject, "Signed out of a device") || !strings.Contains(e.Text, "boss@example.com") {
		t.Fatalf("email = %q", e.Subject)
	}
	a := adminActions(t, srv, boss, "")["actions"].([]any)[0].(map[string]any)
	if a["action"] != "end_session" || a["target_email"] != "second@example.com" || a["reason"] != "Stolen laptop" {
		t.Fatalf("action = %v", a)
	}
	// Gone now; and a freelancer isn't an admin.
	wantStatus(t, post(t, boss, end, map[string]string{"code": codes.next(t)}), http.StatusNotFound)
}

func TestCLI_LockAndResetAdmin(t *testing.T) {
	srv, _ := newAPI(t)
	ctx := context.Background()
	svc := admin.NewService(admin.NewRepository(testPool), time.UTC)
	boss, _ := stepUpAdmin(t, srv, "boss@example.com")

	if err := svc.LockAdmin(ctx, "Boss@Example.com"); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, get(t, boss, srv.URL+"/api/v1/me"), http.StatusUnauthorized)
	wantStatus(t, login(t, srv, newClient(), "boss@example.com", "supersecret1"), http.StatusForbidden)

	password, err := svc.ResetAdmin(ctx, "boss@example.com")
	if err != nil {
		t.Fatal(err)
	}
	// The old password and two-factor are gone; the new password alone signs in.
	wantStatus(t, login(t, srv, newClient(), "boss@example.com", "supersecret1"), http.StatusUnauthorized)
	c := newClient()
	wantStatus(t, login(t, srv, c, "boss@example.com", password), http.StatusOK)
	// The panel stays shut until two-factor is on again.
	wantStatus(t, get(t, c, srv.URL+"/api/v1/admin/stats"), http.StatusForbidden)

	if err := svc.LockAdmin(ctx, "dev@example.com"); !errors.Is(err, admin.ErrAdminNotFound) {
		t.Fatalf("locking a non-admin = %v", err)
	}
	var log []string
	rows, err := testPool.Query(ctx, `SELECT admin_email || ' ' || action FROM admin_actions ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		log = append(log, s)
	}
	if strings.Join(log, ", ") != "server (cli) lock_admin, server (cli) reset_admin" {
		t.Fatalf("log = %v", log)
	}
}
