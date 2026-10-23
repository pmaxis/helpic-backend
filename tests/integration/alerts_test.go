package integration

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/pmaxis/helpic/internal/config"
	"github.com/pmaxis/helpic/internal/mail"
)

// alertChat is the admins' chat in the tests.
const alertChat = -1001234567890

func checkState(t *testing.T, body map[string]any, name string) string {
	t.Helper()
	for _, c := range body["checks"].([]any) {
		if c := c.(map[string]any); c["name"] == name {
			return c["state"].(string)
		}
	}
	t.Fatalf("no %s check in %v", name, body["checks"])
	return ""
}

func TestAlerts_Off(t *testing.T) {
	srv, _ := newAPI(t)
	boss := signInAdmin(t, srv, "boss@example.com")
	turnOnTwoFactor(t, srv, boss)

	wantStatus(t, post(t, boss, srv.URL+"/api/v1/admin/system/test-alert", nil), http.StatusConflict)
	resp := get(t, boss, srv.URL+"/api/v1/admin/system")
	wantStatus(t, resp, http.StatusOK)
	if s := checkState(t, decode(t, resp), "alerts"); s != "off" {
		t.Fatalf("alerts = %s", s)
	}
}

func TestAlerts_TestMessage(t *testing.T) {
	bot, tg := newFakeBot(t)
	srv, _, _ := newAPIWith(t, func(cfg *config.Config) {
		cfg.TelegramAPIURL = tg.URL
		cfg.AlertBotToken = "test-token"
		cfg.AlertChatID = alertChat
		cfg.AlertLocale = "en"
	})
	boss := signInAdmin(t, srv, "boss@example.com")
	turnOnTwoFactor(t, srv, boss)

	wantStatus(t, post(t, boss, srv.URL+"/api/v1/admin/system/test-alert", nil), http.StatusNoContent)
	bot.waitSent(t, alertChat, "Alerts work")
	bot.waitSent(t, alertChat, "boss@example.com")

	resp := get(t, boss, srv.URL+"/api/v1/admin/system")
	wantStatus(t, resp, http.StatusOK)
	if s := checkState(t, decode(t, resp), "alerts"); s != "ok" {
		t.Fatalf("alerts = %s", s)
	}
}

func TestOutbox_Trouble(t *testing.T) {
	newAPI(t) // empties the tables
	ctx := context.Background()
	start := time.Now()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO emails (to_address, subject, html, text, attempts, last_error, failed_at) VALUES
			('a@example.com', 'Retrying', 'x', 'x', 3, 'smtp: timeout', NULL),
			('b@example.com', 'Blip', 'x', 'x', 1, 'smtp: blip', NULL),
			('c@example.com', 'Given up', 'x', 'x', 8, '550 no such user', now()),
			('d@example.com', 'Given up long ago', 'x', 'x', 8, '550', now() - interval '1 hour')`); err != nil {
		t.Fatal(err)
	}
	tr, err := mail.NewOutbox(testPool, mail.Log{}).Trouble(ctx, start.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if tr.Retrying != 1 || tr.LastError != "smtp: timeout" {
		t.Fatalf("retrying = %d, %q", tr.Retrying, tr.LastError)
	}
	if len(tr.GivenUp) != 1 || tr.GivenUp[0].To != "c@example.com" {
		t.Fatalf("given up = %+v", tr.GivenUp)
	}
}
