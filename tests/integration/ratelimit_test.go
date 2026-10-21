package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/pmaxis/helpic/internal/config"
)

func newLimitedAPI(t *testing.T) *httptest.Server {
	t.Helper()
	srv, _, _ := newAPIWith(t, func(cfg *config.Config) { cfg.RateLimit = true })
	return srv
}

func login(t *testing.T, srv *httptest.Server, c *http.Client, email, password string) *http.Response {
	t.Helper()
	return post(t, c, srv.URL+"/api/v1/auth/login", map[string]string{"email": email, "password": password})
}

func wantLimited(t *testing.T, resp *http.Response) {
	t.Helper()
	wantStatus(t, resp, http.StatusTooManyRequests)
	if resp.Header.Get("Retry-After") == "" {
		t.Fatal("429 without Retry-After")
	}
	if code := errorCode(decode(t, resp)); code != "RATE_LIMITED" {
		t.Fatalf("code = %q", code)
	}
}

func TestRateLimit_WrongPasswordsPerEmail(t *testing.T) {
	srv := newLimitedAPI(t)
	c := newClient()
	confirmed(t, c, srv.URL, "guessed@example.com", "supersecret1")

	// A right password doesn't count.
	for range 3 {
		resp := login(t, srv, c, "guessed@example.com", "supersecret1")
		wantStatus(t, resp, http.StatusOK)
		resp.Body.Close()
	}
	for i := range 10 {
		resp := login(t, srv, c, "guessed@example.com", "wrong-password")
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("wrong password %d = %d, want 401", i+1, resp.StatusCode)
		}
		resp.Body.Close()
	}
	// Then even the right one waits.
	wantLimited(t, login(t, srv, c, "guessed@example.com", "supersecret1"))
	// Other emails are fine.
	resp := login(t, srv, c, "someone-else@example.com", "whatever1")
	wantStatus(t, resp, http.StatusUnauthorized)
	resp.Body.Close()
}

func TestRateLimit_WrongPasswordsElsewhereDontLockTheOwner(t *testing.T) {
	srv, _, _ := newAPIWith(t, func(cfg *config.Config) {
		cfg.RateLimit = true
		cfg.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("::1/128")}
	})
	c := newClient()
	confirmed(t, c, srv.URL, "owner@example.com", "supersecret1")
	from := func(ip, password string) *http.Response {
		body, _ := json.Marshal(map[string]string{"email": "owner@example.com", "password": password})
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/auth/login", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", ip)
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	for range 10 {
		resp := from("203.0.113.66", "wrong-password1")
		wantStatus(t, resp, http.StatusUnauthorized)
		resp.Body.Close()
	}
	// The guesser waits; the owner, somewhere else, signs in.
	wantLimited(t, from("203.0.113.66", "supersecret1"))
	resp := from("198.51.100.7", "supersecret1")
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
}

func TestRateLimit_InvitationsPerUser(t *testing.T) {
	srv := newLimitedAPI(t)
	freelancer := signUpFreelancer(t, srv, "inviter@example.com")
	for i := range 20 {
		resp := post(t, freelancer, srv.URL+"/api/v1/invitations", map[string]string{"email": fmt.Sprintf("client%d@example.com", i), "name": "Acme"})
		wantStatus(t, resp, http.StatusCreated)
		resp.Body.Close()
	}
	wantLimited(t, post(t, freelancer, srv.URL+"/api/v1/invitations", map[string]string{"email": "one-more@example.com", "name": "Acme"}))
	if n := len(emailsTo(t, "one-more@example.com")); n != 0 {
		t.Fatalf("emailed past the limit %d times", n)
	}
}

func TestRateLimit_SignInAttemptsPerAddress(t *testing.T) {
	srv := newLimitedAPI(t)
	c := newClient()
	// 20 attempts; emails differ so the per-email limit doesn't kick in first.
	for i := range 20 {
		resp := login(t, srv, c, "nobody"+string(rune('a'+i))+"@example.com", "whatever1")
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d = %d, want 401", i+1, resp.StatusCode)
		}
		resp.Body.Close()
	}
	wantLimited(t, login(t, srv, c, "late@example.com", "whatever1"))

	// Invitations share the budget: they sign in or make accounts too.
	wantLimited(t, post(t, c, srv.URL+"/api/v1/invites/lookup", map[string]string{"token": "some-token"}))

	// Signing out and refreshing are not sign-in attempts.
	resp := post(t, c, srv.URL+"/api/v1/auth/logout", nil)
	wantStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
}

func TestRateLimit_SignUpsPerAddress(t *testing.T) {
	srv := newLimitedAPI(t)
	c := newClient()
	for i := range 5 {
		resp := register(t, c, srv.URL, "new"+string(rune('a'+i))+"@example.com", "supersecret1")
		wantStatus(t, resp, http.StatusAccepted)
		resp.Body.Close()
	}
	wantLimited(t, register(t, c, srv.URL, "one-too-many@example.com", "supersecret1"))
}

func TestRateLimit_PasswordChecksPerUser(t *testing.T) {
	srv := newLimitedAPI(t)
	c := signUpFreelancer(t, srv, "checker@example.com")
	for i := range 5 {
		resp := do(t, c, http.MethodPut, srv.URL+"/api/v1/me/password", map[string]string{
			"current_password": "not-it-at-all", "password": "evenmoresecret1", "password_confirmation": "evenmoresecret1",
		})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("check %d = %d, want 400", i+1, resp.StatusCode)
		}
		resp.Body.Close()
	}
	wantLimited(t, do(t, c, http.MethodPut, srv.URL+"/api/v1/me/email", map[string]string{
		"email": "new@example.com", "current_password": "supersecret1",
	}))
	// Other settings aren't limited.
	resp := do(t, c, http.MethodPut, srv.URL+"/api/v1/me", map[string]string{"first_name": "Still", "last_name": "Here"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
}

func TestRealIP_FromTrustedProxy(t *testing.T) {
	srv, _, _ := newAPIWith(t, func(cfg *config.Config) {
		cfg.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("::1/128")}
	})
	c := newClient()
	confirmed(t, c, srv.URL, "proxied@example.com", "supersecret1")

	body, _ := json.Marshal(map[string]string{"email": "proxied@example.com", "password": "supersecret1"})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", "198.51.100.23")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	if ip := sessionsOf(t, srv, c)[0]["ip"]; ip != "198.51.100.23" {
		t.Fatalf("session IP = %v, want the forwarded client", ip)
	}
}
