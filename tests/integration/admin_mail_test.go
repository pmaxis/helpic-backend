package integration

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestAdmin_EmailQueue(t *testing.T) {
	srv, _ := newAPI(t)
	boss := signInAdmin(t, srv, "boss@example.com")
	turnOnTwoFactor(t, srv, boss)
	wantStatus(t, register(t, newClient(), srv.URL, "new@example.com", "supersecret1"), http.StatusAccepted)
	wantStatus(t, register(t, newClient(), srv.URL, "other@example.com", "supersecret1"), http.StatusAccepted)

	// One went out, one was given up on.
	ctx := context.Background()
	if _, err := testPool.Exec(ctx, `UPDATE emails SET sent_at = now(), html = '', text = '' WHERE to_address = 'other@example.com'`); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE emails SET attempts = 8, last_error = 'smtp: connection refused' WHERE to_address = 'new@example.com'`); err != nil {
		t.Fatal(err)
	}

	resp := get(t, boss, srv.URL+"/api/v1/admin/emails?status=failed")
	wantStatus(t, resp, http.StatusOK)
	page := decode(t, resp)
	emails := page["emails"].([]any)
	if page["total"] != 1.0 || len(emails) != 1 {
		t.Fatalf("failed emails = %v", page)
	}
	failed := emails[0].(map[string]any)
	if failed["to"] != "new@example.com" || failed["status"] != "failed" || failed["last_error"] != "smtp: connection refused" {
		t.Fatalf("failed email = %v", failed)
	}
	if _, ok := failed["html"]; ok {
		t.Fatal("the body is shown")
	}
	if c := field(page, "counts"); c["failed"] != 1.0 || c["sent"] != 1.0 || c["last_sent"] == nil {
		t.Fatalf("counts = %v", c)
	}
	wantStatus(t, get(t, boss, srv.URL+"/api/v1/admin/emails?status=lost"), http.StatusBadRequest)

	// Sending it again starts the tries over.
	resp = post(t, boss, srv.URL+"/api/v1/admin/emails/"+failed["id"].(string)+"/retry", nil)
	wantStatus(t, resp, http.StatusOK)
	if e := field(decode(t, resp), "email"); e["status"] != "queued" || e["attempts"] != 0.0 {
		t.Fatalf("after retry = %v", e)
	}

	// A sent email can't be: its body is gone.
	resp = get(t, boss, srv.URL+"/api/v1/admin/emails?status=sent")
	wantStatus(t, resp, http.StatusOK)
	sent := decode(t, resp)["emails"].([]any)[0].(map[string]any)
	wantStatus(t, post(t, boss, srv.URL+"/api/v1/admin/emails/"+sent["id"].(string)+"/retry", nil), http.StatusNotFound)
	wantStatus(t, post(t, boss, srv.URL+"/api/v1/admin/emails/not-an-id/retry", nil), http.StatusNotFound)

	list := adminActions(t, srv, boss, "")
	a := list["actions"].([]any)[0].(map[string]any)
	if list["total"] != 1.0 || a["action"] != "retry_email" || a["target_email"] != "new@example.com" || a["target_id"] != nil {
		t.Fatalf("actions = %v", list)
	}
}

func TestAdmin_Signups(t *testing.T) {
	srv, _ := newAPI(t)
	boss := signInAdmin(t, srv, "boss@example.com")
	turnOnTwoFactor(t, srv, boss)
	wantStatus(t, register(t, newClient(), srv.URL, "slow@example.com", "supersecret1"), http.StatusAccepted)
	wantStatus(t, register(t, newClient(), srv.URL, "gone@example.com", "supersecret1"), http.StatusAccepted)
	oldLink := linkToken(t, lastEmailTo(t, "slow@example.com"), "/confirm-signup/")

	resp := get(t, boss, srv.URL+"/api/v1/admin/signups?q=slow")
	wantStatus(t, resp, http.StatusOK)
	page := decode(t, resp)
	signups := page["signups"].([]any)
	if page["total"] != 1.0 || len(signups) != 1 {
		t.Fatalf("sign-ups = %v", page)
	}
	if s := signups[0].(map[string]any); s["email"] != "slow@example.com" || s["name"] != "Test User" || s["email_status"] != "queued" {
		t.Fatalf("sign-up = %v", s)
	}

	// A new link: the old one stops working, the new one makes the account.
	wantStatus(t, post(t, boss, srv.URL+"/api/v1/admin/signups/slow%40example.com/resend", nil), http.StatusNoContent)
	if n := len(emailsTo(t, "slow@example.com")); n != 2 {
		t.Fatalf("emails to slow@ = %d", n)
	}
	resp = post(t, newClient(), srv.URL+"/api/v1/auth/signups/confirm", map[string]string{"token": oldLink})
	if resp.StatusCode == http.StatusCreated {
		t.Fatal("the old link still works")
	}
	resp.Body.Close()
	wantStatus(t, confirmSignup(t, newClient(), srv.URL, "slow@example.com"), http.StatusCreated)

	wantStatus(t, do(t, boss, http.MethodDelete, srv.URL+"/api/v1/admin/signups/gone@example.com", nil), http.StatusNoContent)
	wantStatus(t, do(t, boss, http.MethodDelete, srv.URL+"/api/v1/admin/signups/gone@example.com", nil), http.StatusNotFound)
	wantStatus(t, post(t, boss, srv.URL+"/api/v1/admin/signups/nobody@example.com/resend", nil), http.StatusNotFound)

	resp = get(t, boss, srv.URL+"/api/v1/admin/signups")
	wantStatus(t, resp, http.StatusOK)
	if n := decode(t, resp)["total"]; n != 0.0 {
		t.Fatalf("sign-ups left = %v", n)
	}
	if got := fmt.Sprint(actionNames(adminActions(t, srv, boss, ""))); got != "[delete_signup resend_signup]" {
		t.Fatalf("actions = %v", got)
	}
}

func TestAdmin_System(t *testing.T) {
	srv, _, app := newAPIWith(t, nil)
	boss := signInAdmin(t, srv, "boss@example.com")
	turnOnTwoFactor(t, srv, boss)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		app.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	var body map[string]any
	for deadline := time.Now().Add(10 * time.Second); ; {
		resp := get(t, boss, srv.URL+"/api/v1/admin/system")
		wantStatus(t, resp, http.StatusOK)
		body = decode(t, resp)
		if jobRuns(body, "email") > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the email job never ran: %v", body)
		}
		time.Sleep(50 * time.Millisecond)
	}

	states := map[string]string{}
	for _, c := range body["checks"].([]any) {
		c := c.(map[string]any)
		states[c["name"].(string)] = c["state"].(string)
	}
	// The tests run without a mail server or a bot.
	if states["database"] != "ok" || states["mail"] != "off" || states["telegram"] != "off" {
		t.Fatalf("checks = %v", body["checks"])
	}
	if body["version"] != "dev" || body["go_version"] == "" || body["started_at"] == nil {
		t.Fatalf("build = %v", body)
	}
}

// jobRuns is how many times the named job ran, from a system report.
func jobRuns(body map[string]any, name string) float64 {
	for _, j := range body["jobs"].([]any) {
		j := j.(map[string]any)
		if j["name"] == name {
			return j["runs"].(float64)
		}
	}
	return 0
}
