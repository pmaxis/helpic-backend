package integration

import (
	"net/http"
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

	resp := do(t, c, http.MethodPut, srv.URL+"/api/v1/me/email", map[string]string{"email": "new@example.com", "current_password": "wrong-pass"})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()

	resp = do(t, c, http.MethodPut, srv.URL+"/api/v1/me/email", map[string]string{"email": "taken@example.com", "current_password": "supersecret"})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()

	resp = do(t, c, http.MethodPut, srv.URL+"/api/v1/me/email", map[string]string{"email": "New@Example.com", "current_password": "supersecret"})
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "user")["email"]; got != "new@example.com" {
		t.Fatalf("email = %v", got)
	}

	resp = post(t, newClient(), srv.URL+"/api/v1/auth/login", map[string]string{"email": "new@example.com", "password": "supersecret"})
	wantStatus(t, resp, http.StatusOK)
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
}
