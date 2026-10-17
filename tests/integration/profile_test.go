package integration

import (
	"net/http"
	"strings"
	"testing"
)

func TestProfile_UpdateName(t *testing.T) {
	srv, _ := newAPI(t)
	c := signUpFreelancer(t, srv, "name@example.com")

	resp := do(t, c, http.MethodPut, srv.URL+"/api/v1/me", map[string]string{"first_name": "  Olena ", "last_name": " Petrenko "})
	wantStatus(t, resp, http.StatusOK)
	u := field(decode(t, resp), "user")
	if u["first_name"] != "Olena" || u["last_name"] != "Petrenko" || u["name"] != "Olena Petrenko" {
		t.Fatalf("user = %v", u)
	}

	// The last name is optional; the full name follows.
	resp = do(t, c, http.MethodPut, srv.URL+"/api/v1/me", map[string]string{"first_name": "Acme Inc", "last_name": ""})
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "user")["name"]; got != "Acme Inc" {
		t.Fatalf("name = %v, want Acme Inc", got)
	}

	resp = do(t, c, http.MethodPut, srv.URL+"/api/v1/me", map[string]string{"first_name": " ", "last_name": "Petrenko"})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()
}

func TestProfile_ChangeEmail(t *testing.T) {
	srv, _ := newAPI(t)
	c := signUpFreelancer(t, srv, "old@example.com")
	signUpFreelancer(t, srv, "taken@example.com")
	change := func(email, password string) *http.Response {
		return do(t, c, http.MethodPut, srv.URL+"/api/v1/me/email", map[string]string{"email": email, "current_password": password})
	}
	confirm := func(token string) *http.Response {
		return post(t, newClient(), srv.URL+"/api/v1/email-changes/confirm", map[string]string{"token": token})
	}

	for _, bad := range []struct{ email, password, field string }{
		{"new@example.com", "wrong-pass", "current_password"},
		{"taken@example.com", "supersecret", "email"},
		{"OLD@example.com", "supersecret", "email"},
	} {
		resp := change(bad.email, bad.password)
		wantStatus(t, resp, http.StatusBadRequest)
		if fields := decode(t, resp)["error"].(map[string]any)["fields"].(map[string]any); fields[bad.field] == nil {
			t.Fatalf("%s: fields = %v, want %s", bad.email, fields, bad.field)
		}
	}

	// Asking sends a link to the new address; the email doesn't change yet.
	resp := change("New@Example.com", "supersecret")
	wantStatus(t, resp, http.StatusAccepted)
	if got := decode(t, resp)["pending_email"]; got != "new@example.com" {
		t.Fatalf("pending_email = %v", got)
	}
	if got := field(decode(t, get(t, c, srv.URL+"/api/v1/me")), "user")["email"]; got != "old@example.com" {
		t.Fatalf("email changed before confirming: %v", got)
	}
	token := linkToken(t, lastEmailTo(t, "new@example.com"), "/confirm-email/")

	resp = confirm("not-a-token")
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()

	// The link works from anywhere, signed in or not, and once.
	resp = confirm(token)
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "user")["email"]; got != "new@example.com" {
		t.Fatalf("email after confirming = %v", got)
	}
	resp = confirm(token)
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()

	resp = post(t, newClient(), srv.URL+"/api/v1/auth/login", map[string]string{"email": "new@example.com", "password": "supersecret"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	if notice := lastEmailTo(t, "old@example.com"); !strings.Contains(notice.Text, "now signs in with new@example.com") {
		t.Fatalf("notice to the old address = %+v", notice)
	}

	// A newer request replaces the older link.
	resp = change("first@example.com", "supersecret")
	wantStatus(t, resp, http.StatusAccepted)
	resp.Body.Close()
	first := linkToken(t, lastEmailTo(t, "first@example.com"), "/confirm-email/")
	resp = change("second@example.com", "supersecret")
	wantStatus(t, resp, http.StatusAccepted)
	resp.Body.Close()
	resp = confirm(first)
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()
}

func TestProfile_ChangePassword(t *testing.T) {
	srv, _ := newAPI(t)
	c := signUpFreelancer(t, srv, "pw@example.com")

	resp := do(t, c, http.MethodPut, srv.URL+"/api/v1/me/password", map[string]string{
		"current_password": "wrong-pass", "password": "brand-new-pw", "password_confirmation": "brand-new-pw",
	})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()

	resp = do(t, c, http.MethodPut, srv.URL+"/api/v1/me/password", map[string]string{
		"current_password": "supersecret", "password": "brand-new-pw", "password_confirmation": "brand-new-pw",
	})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	old := post(t, newClient(), srv.URL+"/api/v1/auth/login", map[string]string{"email": "pw@example.com", "password": "supersecret"})
	wantStatus(t, old, http.StatusUnauthorized)
	old.Body.Close()
	fresh := post(t, newClient(), srv.URL+"/api/v1/auth/login", map[string]string{"email": "pw@example.com", "password": "brand-new-pw"})
	wantStatus(t, fresh, http.StatusOK)
	fresh.Body.Close()
	if notice := lastEmailTo(t, "pw@example.com"); notice.Subject != "Your Helpic password was changed" {
		t.Fatalf("notice = %+v", notice)
	}
}
