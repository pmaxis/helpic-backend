package integration

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/pmaxis/helpic/internal/config"
)

func TestAdmin_ClearLimit(t *testing.T) {
	srv, _, _ := newAPIWith(t, func(cfg *config.Config) { cfg.RateLimit = true })
	boss := signInAdmin(t, srv, "boss@example.com")
	turnOnTwoFactor(t, srv, boss)
	dev := signUpFreelancer(t, srv, "dev@example.com")
	id := myID(t, srv, dev)

	// Ten wrong passwords from one address lock the email there.
	for range 10 {
		wantStatus(t, login(t, srv, newClient(), "dev@example.com", "wrongwrong1"), http.StatusUnauthorized)
	}
	wantStatus(t, login(t, srv, newClient(), "dev@example.com", "supersecret1"), http.StatusTooManyRequests)

	resp := get(t, boss, srv.URL+"/api/v1/admin/limits?account="+id)
	wantStatus(t, resp, http.StatusOK)
	limits := decode(t, resp)["limits"].([]any)
	if len(limits) != 1 {
		t.Fatalf("limits = %v", limits)
	}
	l := limits[0].(map[string]any)
	if l["scope"] != "login-failures" || l["kind"] != "email_ip" || l["email"] != "dev@example.com" || l["ip"] != "127.0.0.1" ||
		l["account_id"] != id || l["account_email"] != "dev@example.com" || l["opens_at"] == nil {
		t.Fatalf("limit = %v", l)
	}
	resp = get(t, boss, srv.URL+"/api/v1/admin/limits?q=nobody")
	wantStatus(t, resp, http.StatusOK)
	if n := len(decode(t, resp)["limits"].([]any)); n != 0 {
		t.Fatalf("limits for nobody = %d", n)
	}

	clear := map[string]string{"scope": "login-failures", "key": l["key"].(string)}
	wantStatus(t, post(t, boss, srv.URL+"/api/v1/admin/limits/clear", clear), http.StatusNoContent)
	wantStatus(t, login(t, srv, newClient(), "dev@example.com", "supersecret1"), http.StatusOK)
	wantStatus(t, post(t, boss, srv.URL+"/api/v1/admin/limits/clear", clear), http.StatusNotFound)

	a := adminActions(t, srv, boss, "?target="+id)["actions"].([]any)[0].(map[string]any)
	if a["action"] != "clear_limit" || a["target_email"] != "dev@example.com" || a["reason"] != "login-failures" {
		t.Fatalf("action = %v", a)
	}
}

func TestAdmin_EndOneSession(t *testing.T) {
	srv, _ := newAPI(t)
	boss, codes := stepUpAdmin(t, srv, "boss@example.com")
	laptop := signUpFreelancer(t, srv, "dev@example.com")
	id := myID(t, srv, laptop)
	phone := signInAgain(t, srv, "dev@example.com")

	resp := get(t, boss, srv.URL+"/api/v1/admin/users/"+id+"/sessions")
	wantStatus(t, resp, http.StatusOK)
	sessions := decode(t, resp)["sessions"].([]any)
	if len(sessions) != 2 {
		t.Fatalf("sessions = %v", sessions)
	}
	// The newest is the phone's.
	newest := sessions[0].(map[string]any)
	if newest["ip"] != "127.0.0.1" || newest["last_used_at"] == nil {
		t.Fatalf("session = %v", newest)
	}

	end := srv.URL + "/api/v1/admin/users/" + id + "/sessions/" + newest["id"].(string) + "/end"
	// Without the code, or with a wrong one, nothing ends.
	wantStatus(t, post(t, boss, end, map[string]string{"reason": "Lost phone"}), http.StatusBadRequest)
	wantStatus(t, post(t, boss, end, map[string]string{"reason": "Lost phone", "code": "000000"}), http.StatusBadRequest)
	resp = post(t, boss, end, map[string]string{"reason": "Lost phone", "code": codes.next(t)})
	wantStatus(t, resp, http.StatusOK)
	if u := field(decode(t, resp), "user"); u["sessions"] != 1.0 {
		t.Fatalf("after ending one = %v", u)
	}
	wantStatus(t, get(t, phone, srv.URL+"/api/v1/me"), http.StatusUnauthorized)
	wantStatus(t, get(t, laptop, srv.URL+"/api/v1/me"), http.StatusOK)

	// Gone, or another account's: not found.
	wantStatus(t, post(t, boss, end, map[string]string{"code": codes.next(t)}), http.StatusNotFound)
	wantStatus(t, get(t, boss, srv.URL+"/api/v1/admin/users/not-an-id/sessions"), http.StatusNotFound)

	if got := fmt.Sprint(actionNames(adminActions(t, srv, boss, "?target="+id))); got != "[end_session]" {
		t.Fatalf("actions = %v", got)
	}
}

func TestAdmin_ListAndSignIns(t *testing.T) {
	srv, _ := newAPI(t)
	boss := signInAdmin(t, srv, "boss@example.com")
	turnOnTwoFactor(t, srv, boss)
	signInAdmin(t, srv, "second@example.com")

	// A wrong password and a wrong code to the boss's account are noted; a
	// freelancer's sign-in isn't.
	wantStatus(t, login(t, srv, newClient(), "boss@example.com", "wrong-password"), http.StatusUnauthorized)
	wantStatus(t, loginWithCode(t, newClient(), srv, "boss@example.com", "000000"), http.StatusBadRequest)
	signUpFreelancer(t, srv, "dev@example.com")

	resp := get(t, boss, srv.URL+"/api/v1/admin/admins")
	wantStatus(t, resp, http.StatusOK)
	admins := decode(t, resp)["admins"].([]any)
	if len(admins) != 2 {
		t.Fatalf("admins = %v", admins)
	}
	first := admins[0].(map[string]any)
	if first["email"] != "boss@example.com" || first["two_factor"] != true || first["last_sign_in"] == nil || len(first["sessions"].([]any)) != 1 {
		t.Fatalf("boss = %v", first)
	}

	resp = get(t, boss, srv.URL+"/api/v1/admin/sign-ins")
	wantStatus(t, resp, http.StatusOK)
	page := decode(t, resp)
	var got []string
	for _, s := range page["sign_ins"].([]any) {
		s := s.(map[string]any)
		got = append(got, s["admin_email"].(string)+" "+s["outcome"].(string))
	}
	if fmt.Sprint(got) != "[boss@example.com wrong_code boss@example.com wrong_password second@example.com ok boss@example.com ok]" {
		t.Fatalf("sign-ins = %v", got)
	}

	resp = get(t, boss, srv.URL+"/api/v1/admin/sign-ins?failed=true")
	wantStatus(t, resp, http.StatusOK)
	if n := decode(t, resp)["total"]; n != 2.0 {
		t.Fatalf("failed sign-ins = %v", n)
	}
}
