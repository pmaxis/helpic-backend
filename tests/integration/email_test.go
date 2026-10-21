package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/pmaxis/helpic/internal/config"
	"github.com/pmaxis/helpic/internal/mail"
)

type queuedEmail struct {
	Subject, Text, HTML string
}

// emailsTo lists the emails queued for an address, oldest first.
func emailsTo(t *testing.T, to string) []queuedEmail {
	t.Helper()
	rows, err := testPool.Query(context.Background(), `SELECT subject, text, html FROM emails WHERE to_address = $1 ORDER BY created_at`, to)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []queuedEmail
	for rows.Next() {
		var e queuedEmail
		if err := rows.Scan(&e.Subject, &e.Text, &e.HTML); err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

func lastEmailTo(t *testing.T, to string) queuedEmail {
	t.Helper()
	list := emailsTo(t, to)
	if len(list) == 0 {
		t.Fatalf("no email to %s", to)
	}
	return list[len(list)-1]
}

// linkToken finds http://app.test<path><token> in the email and returns the token.
func linkToken(t *testing.T, e queuedEmail, path string) string {
	t.Helper()
	m := regexp.MustCompile(`http://app\.test` + regexp.QuoteMeta(path) + `([A-Za-z0-9_-]+)`).FindStringSubmatch(e.Text)
	if m == nil {
		t.Fatalf("no %s link in %q", path, e.Text)
	}
	if !strings.Contains(e.HTML, "http://app.test"+path+m[1]) {
		t.Fatal("the HTML lacks the link the text has")
	}
	return m[1]
}

func forgot(t *testing.T, srv string, email string) {
	t.Helper()
	resp := post(t, newClient(), srv+"/api/v1/auth/password/forgot", map[string]string{"email": email})
	wantStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
}

func reset(t *testing.T, srv, token, password string) *http.Response {
	t.Helper()
	return post(t, newClient(), srv+"/api/v1/auth/password/reset", map[string]string{
		"token": token, "password": password, "password_confirmation": password,
	})
}

func TestPasswordReset_EndsWithTheEmail(t *testing.T) {
	srv, _ := newAPI(t)
	c := signUpFreelancer(t, srv, "old@example.com")
	forgot(t, srv.URL, "old@example.com")
	token := linkToken(t, lastEmailTo(t, "old@example.com"), "/reset-password/")

	resp := do(t, c, http.MethodPut, srv.URL+"/api/v1/me/email", map[string]string{"email": "new@example.com", "current_password": "supersecret1"})
	wantStatus(t, resp, http.StatusAccepted)
	resp.Body.Close()
	resp = post(t, newClient(), srv.URL+"/api/v1/email-changes/confirm", map[string]string{
		"token": linkToken(t, lastEmailTo(t, "new@example.com"), "/confirm-email/"),
	})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// A link sent to the old address no longer sets the password.
	resp = reset(t, srv.URL, token, "takenover1")
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()
}

func TestPasswordReset(t *testing.T) {
	srv, _ := newAPI(t)
	laptop := signUpFreelancer(t, srv, "forgetful@example.com")

	// An unknown email gets the same answer, and no email.
	forgot(t, srv.URL, "nobody@example.com")
	if n := len(emailsTo(t, "nobody@example.com")); n != 0 {
		t.Fatalf("emailed an unknown address %d times", n)
	}
	resp := post(t, newClient(), srv.URL+"/api/v1/auth/password/forgot", map[string]string{"email": "not-an-email"})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()

	forgot(t, srv.URL, "Forgetful@Example.com")
	token := linkToken(t, lastEmailTo(t, "forgetful@example.com"), "/reset-password/")

	resp = reset(t, srv.URL, token, "short")
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()
	resp = reset(t, srv.URL, "not-a-token", "brand-new-pw1")
	wantStatus(t, resp, http.StatusBadRequest)
	if fields := decode(t, resp)["error"].(map[string]any)["fields"].(map[string]any); fields["token"] == nil {
		t.Fatalf("fields = %v, want token", fields)
	}

	resp = reset(t, srv.URL, token, "brand-new-pw1")
	wantStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()

	// The new password works, the old one doesn't, every device is out and
	// the owner hears about it.
	resp = post(t, newClient(), srv.URL+"/api/v1/auth/login", map[string]string{"email": "forgetful@example.com", "password": "supersecret1"})
	wantStatus(t, resp, http.StatusUnauthorized)
	resp.Body.Close()
	resp = post(t, newClient(), srv.URL+"/api/v1/auth/login", map[string]string{"email": "forgetful@example.com", "password": "brand-new-pw1"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	resp = post(t, laptop, srv.URL+"/api/v1/auth/refresh", nil)
	wantStatus(t, resp, http.StatusUnauthorized)
	resp.Body.Close()
	resp = get(t, laptop, srv.URL+"/api/v1/me")
	wantStatus(t, resp, http.StatusUnauthorized)
	resp.Body.Close()
	if notice := lastEmailTo(t, "forgetful@example.com"); notice.Subject != "Your Helpic password was changed" {
		t.Fatalf("notice = %+v", notice)
	}

	// A link works once; a newer one replaces an older one.
	resp = reset(t, srv.URL, token, "another-pw-1")
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()
	forgot(t, srv.URL, "forgetful@example.com")
	older := linkToken(t, lastEmailTo(t, "forgetful@example.com"), "/reset-password/")
	forgot(t, srv.URL, "forgetful@example.com")
	resp = reset(t, srv.URL, older, "another-pw-1")
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()

	// An expired link doesn't work.
	forgot(t, srv.URL, "forgetful@example.com")
	expired := linkToken(t, lastEmailTo(t, "forgetful@example.com"), "/reset-password/")
	if _, err := testPool.Exec(context.Background(), `UPDATE password_resets SET expires_at = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	resp = reset(t, srv.URL, expired, "another-pw-1")
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()
}

func TestPasswordReset_LimitedPerEmail(t *testing.T) {
	srv, _, _ := newAPIWith(t, func(cfg *config.Config) { cfg.RateLimit = true })
	signUpFreelancer(t, srv, "spammed@example.com")
	for range 5 {
		forgot(t, srv.URL, "spammed@example.com")
	}
	// The answer never changes, but only three reset emails go out. The fourth
	// message is the sign-up confirmation.
	if n := len(emailsTo(t, "spammed@example.com")); n != 4 {
		t.Fatalf("emails = %d, want 4", n)
	}
}

func TestInvitationEmails(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")

	token := invite(t, srv, freelancer, "client@example.com")
	e := lastEmailTo(t, "client@example.com")
	if e.Subject != "Test User invited you to Helpic" || !strings.Contains(e.Text, "Hi Acme,") {
		t.Fatalf("invitation email = %+v", e)
	}
	// Only the link's hash is stored, and the freelancer's list holds no link.
	wantHashed(t, `SELECT token_hash FROM invitations`, token)
	resp := get(t, freelancer, srv.URL+"/api/v1/invitations")
	wantStatus(t, resp, http.StatusOK)
	if inv := decode(t, resp)["invitations"].([]any)[0].(map[string]any); inv["token"] != nil {
		t.Fatalf("listed invitation = %v", inv)
	}

	client := acceptNew(t, srv, token)
	teamToken := teamInvite(t, srv, client, map[string]string{"email": "member@example.com"})
	if e := lastEmailTo(t, "member@example.com"); !strings.HasPrefix(e.Subject, "Join ") {
		t.Fatalf("team invite email = %+v", e)
	}
	wantHashed(t, `SELECT token_hash FROM company_invites`, teamToken)
	resp = get(t, client, srv.URL+"/api/v1/team")
	wantStatus(t, resp, http.StatusOK)
	if inv := field(decode(t, resp), "team")["invites"].([]any)[0].(map[string]any); inv["token"] != nil {
		t.Fatalf("listed team invite = %v", inv)
	}
}

// wantHashed checks that the query's one row holds the SHA-256 of the token.
func wantHashed(t *testing.T, query, token string) {
	t.Helper()
	var stored []byte
	if err := testPool.QueryRow(context.Background(), query).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if sum := sha256.Sum256([]byte(token)); !bytes.Equal(stored, sum[:]) {
		t.Fatal("the stored token is not the link's hash")
	}
}

// fakeSender records what it sends, or fails while failing is set.
type fakeSender struct {
	mu      sync.Mutex
	sent    []mail.Message
	failing bool
}

func (f *fakeSender) Send(_ context.Context, m mail.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failing {
		return errors.New("mail server down")
	}
	f.sent = append(f.sent, m)
	return nil
}

func TestOutbox(t *testing.T) {
	newAPI(t) // a clean database
	ctx := context.Background()
	sender := &fakeSender{failing: true}
	outbox := mail.NewOutbox(testPool, sender)

	if err := outbox.Send(ctx, mail.Email{To: "a@example.com", Subject: "Hello", Heading: "Hi", Paragraphs: []string{"<b>raw</b>"},
		Button: &mail.Button{Text: "Go", URL: "http://app.test/x"}}); err != nil {
		t.Fatal(err)
	}
	if e := lastEmailTo(t, "a@example.com"); !strings.Contains(e.HTML, "&lt;b&gt;raw&lt;/b&gt;") || !strings.Contains(e.Text, "Go: http://app.test/x") {
		t.Fatalf("rendered = %+v", e)
	}

	// A failure is tried again later, not at once.
	if err := outbox.Run(ctx); err != nil {
		t.Fatal(err)
	}
	var attempts int
	var lastError string
	if err := testPool.QueryRow(ctx, `SELECT attempts, last_error FROM emails`).Scan(&attempts, &lastError); err != nil || attempts != 1 || lastError != "mail server down" {
		t.Fatalf("after a failure: attempts %d, error %q, %v", attempts, lastError, err)
	}
	sender.failing = false
	if err := outbox.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 0 {
		t.Fatal("retried before its time")
	}

	// When it's due it goes, once, and its bodies are cleared.
	if _, err := testPool.Exec(ctx, `UPDATE emails SET next_attempt_at = now()`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := outbox.Run(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if len(sender.sent) != 1 || sender.sent[0].To != "a@example.com" || sender.sent[0].Subject != "Hello" {
		t.Fatalf("sent = %+v", sender.sent)
	}
	if e := lastEmailTo(t, "a@example.com"); e.HTML != "" || e.Text != "" {
		t.Fatal("the bodies were kept after sending")
	}
}
