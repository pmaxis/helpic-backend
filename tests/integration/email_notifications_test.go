package integration

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/pmaxis/helpic/internal/mail"
	notifs "github.com/pmaxis/helpic/internal/notifications"
)

// emailNotifications runs the notification emails once, with what's waiting
// aged past the delay first.
func emailNotifications(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	if _, err := testPool.Exec(ctx, `UPDATE notifications SET created_at = created_at - interval '6 minutes' WHERE emailed_at IS NULL`); err != nil {
		t.Fatal(err)
	}
	emailer := notifs.NewEmailer(testPool, mail.NewOutbox(testPool, mail.Log{}), "http://app.test")
	if err := emailer.Run(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestEmailNotifications(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	freelancerID := myID(t, srv, freelancer)
	invitations := len(emailsTo(t, "free@example.com"))

	// Too fresh: nothing yet.
	emailer := notifs.NewEmailer(testPool, mail.NewOutbox(testPool, mail.Log{}), "http://app.test")
	if err := emailer.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(emailsTo(t, "free@example.com")); n != invitations {
		t.Fatalf("emailed before the delay: %d", n)
	}

	// What happened together arrives together.
	task := newTask(t, srv, client, map[string]any{"freelancer_id": freelancerID, "title": "Logo"})
	resp := post(t, client, srv.URL+"/api/v1/comments?task="+task["id"].(string), map[string]string{"body": "Darker blue?"})
	wantStatus(t, resp, http.StatusCreated)
	resp.Body.Close()
	emailNotifications(t)
	e := lastEmailTo(t, "free@example.com")
	if e.Subject != "3 updates on Helpic" {
		t.Fatalf("subject = %q", e.Subject)
	}
	for _, want := range []string{"Acme Inc accepted your invitation", "Acme Inc added a task “Logo”", "Acme Inc commented on “Logo”: “Darker blue?”", "http://app.test/dashboard"} {
		if !strings.Contains(e.Text, want) {
			t.Fatalf("email lacks %q:\n%s", want, e.Text)
		}
	}

	// Each goes once.
	before := len(emailsTo(t, "free@example.com"))
	emailNotifications(t)
	if n := len(emailsTo(t, "free@example.com")); n != before {
		t.Fatalf("emailed again: %d emails", n)
	}

	// One on its own is its own email, linking to what it's about.
	resp = do(t, freelancer, http.MethodPost, srv.URL+"/api/v1/tasks/"+task["id"].(string)+"/status", map[string]string{"status": "in_progress"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	emailNotifications(t)
	e = lastEmailTo(t, "client@example.com")
	if !strings.HasPrefix(e.Subject, "Test User moved “Logo” to ") || !strings.Contains(e.Text, "http://app.test/tasks/"+task["id"].(string)) {
		t.Fatalf("single email = %q\n%s", e.Subject, e.Text)
	}

	// Read in the app first: no email.
	before = len(emailsTo(t, "free@example.com"))
	newTask(t, srv, client, map[string]any{"freelancer_id": freelancerID, "title": "Seen already"})
	resp = post(t, freelancer, srv.URL+"/api/v1/notifications/read-all", nil)
	wantStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
	emailNotifications(t)
	if n := len(emailsTo(t, "free@example.com")); n != before {
		t.Fatal("emailed a notification already read in the app")
	}
}

func TestEmailNotifications_Kinds(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	freelancerID := myID(t, srv, freelancer)
	prefs := func(resp *http.Response) string {
		t.Helper()
		wantStatus(t, resp, http.StatusOK)
		return fmt.Sprint(field(decode(t, resp), "email_notifications")["kinds"])
	}

	// The defaults until the user chooses.
	if got := prefs(get(t, freelancer, srv.URL+"/api/v1/me/email-notifications")); !strings.Contains(got, "task_created") || strings.Contains(got, "task_updated") {
		t.Fatalf("default kinds = %s", got)
	}
	resp := do(t, freelancer, http.MethodPut, srv.URL+"/api/v1/me/email-notifications", map[string]any{"kinds": []string{"nope"}})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()

	// Only comments: a new task stays in the app.
	if got := prefs(do(t, freelancer, http.MethodPut, srv.URL+"/api/v1/me/email-notifications", map[string]any{"kinds": []string{"comment_added", "comment_added"}})); got != "[comment_added]" {
		t.Fatalf("kinds = %s", got)
	}
	emailNotifications(t) // the invitation
	before := len(emailsTo(t, "free@example.com"))
	newTask(t, srv, client, map[string]any{"freelancer_id": freelancerID, "title": "Quiet"})
	emailNotifications(t)
	if n := len(emailsTo(t, "free@example.com")); n != before {
		t.Fatal("emailed a kind the user turned off")
	}

	// None: no email at all.
	if got := prefs(do(t, freelancer, http.MethodPut, srv.URL+"/api/v1/me/email-notifications", map[string]any{"kinds": []string{}})); got != "[]" {
		t.Fatalf("kinds = %s", got)
	}
	task := newTask(t, srv, client, map[string]any{"freelancer_id": freelancerID, "title": "Also quiet"})
	resp = post(t, client, srv.URL+"/api/v1/comments?task="+task["id"].(string), map[string]string{"body": "Hello?"})
	wantStatus(t, resp, http.StatusCreated)
	resp.Body.Close()
	emailNotifications(t)
	if n := len(emailsTo(t, "free@example.com")); n != before {
		t.Fatal("emailed with email turned off")
	}
}
