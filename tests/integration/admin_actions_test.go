package integration

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// adminAct does an action on an account and returns the account as it is
// after.
func adminAct(t *testing.T, srv *httptest.Server, admin *http.Client, id, action, reason string) map[string]any {
	t.Helper()
	resp := post(t, admin, srv.URL+"/api/v1/admin/users/"+id+"/"+action, map[string]string{"reason": reason})
	wantStatus(t, resp, http.StatusOK)
	return field(decode(t, resp), "user")
}

func adminActions(t *testing.T, srv *httptest.Server, admin *http.Client, query string) map[string]any {
	t.Helper()
	resp := get(t, admin, srv.URL+"/api/v1/admin/actions"+query)
	wantStatus(t, resp, http.StatusOK)
	return decode(t, resp)
}

func TestAdmin_BlockAndUnblock(t *testing.T) {
	srv, _ := newAPI(t)
	boss := signInAdmin(t, srv, "boss@example.com")
	turnOnTwoFactor(t, srv, boss)
	dev := signUpFreelancer(t, srv, "dev@example.com")
	id := myID(t, srv, dev)

	u := adminAct(t, srv, boss, id, "block", "  Spam  ")
	if u["blocked_at"] == nil || u["sessions"] != 0.0 {
		t.Fatalf("blocked = %v", u)
	}
	// The session it had ends at once, and it can't start another.
	wantStatus(t, get(t, dev, srv.URL+"/api/v1/me"), http.StatusUnauthorized)
	resp := login(t, srv, newClient(), "dev@example.com", "supersecret1")
	wantStatus(t, resp, http.StatusForbidden)
	if code := errorCode(decode(t, resp)); code != "ACCOUNT_BLOCKED" {
		t.Fatalf("code = %q", code)
	}
	// A wrong password still says only that.
	wantStatus(t, login(t, srv, newClient(), "dev@example.com", "wrongwrong1"), http.StatusUnauthorized)

	resp = get(t, boss, srv.URL+"/api/v1/admin/users?activity=blocked")
	wantStatus(t, resp, http.StatusOK)
	if n := decode(t, resp)["total"]; n != 1.0 {
		t.Fatalf("blocked accounts = %v", n)
	}

	// Blocking again changes nothing and isn't recorded twice.
	adminAct(t, srv, boss, id, "block", "")
	if n := adminActions(t, srv, boss, "")["total"]; n != 1.0 {
		t.Fatalf("actions after blocking twice = %v", n)
	}

	if u := adminAct(t, srv, boss, id, "unblock", "Appealed"); u["blocked_at"] != nil {
		t.Fatalf("unblocked = %v", u)
	}
	wantStatus(t, login(t, srv, newClient(), "dev@example.com", "supersecret1"), http.StatusOK)

	list := adminActions(t, srv, boss, "?target="+id)
	actions := list["actions"].([]any)
	if list["total"] != 2.0 || len(actions) != 2 {
		t.Fatalf("actions = %v", list)
	}
	last, first := actions[0].(map[string]any), actions[1].(map[string]any)
	if last["action"] != "unblock" || first["action"] != "block" || first["reason"] != "Spam" {
		t.Fatalf("actions, the newest first = %v", actions)
	}
	if first["admin_email"] != "boss@example.com" || first["target_email"] != "dev@example.com" || first["target_id"] != id {
		t.Fatalf("who = %v", first)
	}
}

func TestAdmin_EndSessionsAndResetTwoFactor(t *testing.T) {
	srv, _ := newAPI(t)
	boss := signInAdmin(t, srv, "boss@example.com")
	turnOnTwoFactor(t, srv, boss)
	dev := signUpFreelancer(t, srv, "dev@example.com")
	id := myID(t, srv, dev)

	if u := adminAct(t, srv, boss, id, "end-sessions", ""); u["sessions"] != 0.0 {
		t.Fatalf("after ending sessions = %v", u)
	}
	wantStatus(t, get(t, dev, srv.URL+"/api/v1/me"), http.StatusUnauthorized)
	// No sessions left: nothing to end, nothing recorded.
	adminAct(t, srv, boss, id, "end-sessions", "")

	dev = signInAgain(t, srv, "dev@example.com")
	turnOnTwoFactor(t, srv, dev)
	u := adminAct(t, srv, boss, id, "reset-two-factor", "Lost the phone and the codes")
	if u["two_factor"] != false || u["sessions"] != 0.0 {
		t.Fatalf("after the reset = %v", u)
	}
	wantStatus(t, get(t, dev, srv.URL+"/api/v1/me"), http.StatusUnauthorized)
	// The password alone signs in again, and the owner is told.
	wantStatus(t, login(t, srv, newClient(), "dev@example.com", "supersecret1"), http.StatusOK)
	if e := lastEmailTo(t, "dev@example.com"); !strings.Contains(e.Subject, "Two-factor sign-in was reset") {
		t.Fatalf("last email = %q", e.Subject)
	}
	// Two-factor is off now: resetting it again changes nothing.
	adminAct(t, srv, boss, id, "reset-two-factor", "")

	got := fmt.Sprint(actionNames(adminActions(t, srv, boss, "")))
	if got != "[reset_two_factor end_sessions]" {
		t.Fatalf("actions = %v", got)
	}
}

func actionNames(page map[string]any) []string {
	var out []string
	for _, a := range page["actions"].([]any) {
		out = append(out, a.(map[string]any)["action"].(string))
	}
	return out
}

func TestAdmin_ActionsAreTheAdminsOnly(t *testing.T) {
	srv, _ := newAPI(t)
	boss := signInAdmin(t, srv, "boss@example.com")
	turnOnTwoFactor(t, srv, boss)
	other := signInAdmin(t, srv, "other@example.com")
	dev := signUpFreelancer(t, srv, "dev@example.com")
	id := myID(t, srv, dev)

	// Not an admin, or an admin without two-factor: no.
	for _, c := range []*http.Client{dev, other} {
		resp := post(t, c, srv.URL+"/api/v1/admin/users/"+id+"/block", map[string]string{})
		wantStatus(t, resp, http.StatusForbidden)
		resp.Body.Close()
	}
	// Admins' accounts and ones that don't exist can't be acted on.
	for _, target := range []string{myID(t, srv, other), "00000000-0000-0000-0000-000000000000", "nope"} {
		resp := post(t, boss, srv.URL+"/api/v1/admin/users/"+target+"/block", map[string]string{})
		wantStatus(t, resp, http.StatusNotFound)
		resp.Body.Close()
	}
	resp := post(t, boss, srv.URL+"/api/v1/admin/users/"+id+"/block", map[string]string{"reason": strings.Repeat("я", 501)})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()
	wantStatus(t, get(t, dev, srv.URL+"/api/v1/admin/actions"), http.StatusForbidden)
	if n := adminActions(t, srv, boss, "")["total"]; n != 0.0 {
		t.Fatalf("actions = %v", n)
	}
}
