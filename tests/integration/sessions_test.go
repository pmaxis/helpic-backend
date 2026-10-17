package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// refreshToken is the refresh cookie the client's jar holds.
func refreshToken(t *testing.T, srv *httptest.Server, c *http.Client) string {
	t.Helper()
	u, _ := url.Parse(srv.URL + "/api/v1/auth/refresh")
	for _, ck := range c.Jar.Cookies(u) {
		if ck.Name == "refresh_token" {
			return ck.Value
		}
	}
	t.Fatal("no refresh_token cookie in the jar")
	return ""
}

// refreshWith calls /auth/refresh with just this refresh token.
func refreshWith(t *testing.T, srv *httptest.Server, raw string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/auth/refresh", nil)
	req.AddCookie(&http.Cookie{Name: "refresh_token", Value: raw})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	return resp
}

func cookieNamed(resp *http.Response, name string) *http.Cookie {
	for _, ck := range resp.Cookies() {
		if ck.Name == name {
			return ck
		}
	}
	return nil
}

func sessionsOf(t *testing.T, srv *httptest.Server, c *http.Client) []map[string]any {
	t.Helper()
	resp := get(t, c, srv.URL+"/api/v1/me/sessions")
	wantStatus(t, resp, http.StatusOK)
	var out []map[string]any
	for _, s := range decode(t, resp)["sessions"].([]any) {
		out = append(out, s.(map[string]any))
	}
	return out
}

// signInAgain signs the account in on another device.
func signInAgain(t *testing.T, srv *httptest.Server, email string) *http.Client {
	t.Helper()
	c := newClient()
	resp := post(t, c, srv.URL+"/api/v1/auth/login", map[string]string{"email": email, "password": "supersecret"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	return c
}

func TestLogin_SetsRefreshCookie(t *testing.T) {
	srv, c := newAPI(t)
	register(t, c, srv.URL, "refresh@example.com", "supersecret").Body.Close()
	resp := post(t, c, srv.URL+"/api/v1/auth/login", map[string]string{"email": "refresh@example.com", "password": "supersecret"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	ck := cookieNamed(resp, "refresh_token")
	if ck == nil || ck.Value == "" {
		t.Fatal("no refresh_token cookie set")
	}
	if !ck.HttpOnly || ck.Path != "/api/v1/auth" {
		t.Fatalf("refresh cookie: HttpOnly=%v Path=%q", ck.HttpOnly, ck.Path)
	}
}

func TestRefresh_RotatesTheToken(t *testing.T) {
	srv, _ := newAPI(t)
	c := signUpFreelancer(t, srv, "rotate@example.com")
	first := refreshToken(t, srv, c)

	resp := post(t, c, srv.URL+"/api/v1/auth/refresh", nil)
	wantStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
	if cookieNamed(resp, "auth_token") == nil {
		t.Fatal("refresh did not set a new access cookie")
	}
	second := refreshToken(t, srv, c)
	if second == first {
		t.Fatal("refresh did not rotate the refresh token")
	}

	me := get(t, c, srv.URL+"/api/v1/me")
	wantStatus(t, me, http.StatusOK)
	me.Body.Close()

	// The new token refreshes again.
	again := refreshWith(t, srv, second)
	wantStatus(t, again, http.StatusNoContent)
	again.Body.Close()
}

func TestRefresh_ConcurrentReuseGetsAccessOnly(t *testing.T) {
	srv, _ := newAPI(t)
	c := signUpFreelancer(t, srv, "tabs@example.com")
	old := refreshToken(t, srv, c)

	resp := post(t, c, srv.URL+"/api/v1/auth/refresh", nil)
	wantStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
	current := refreshToken(t, srv, c)

	// Another tab sent the old token a moment later.
	late := refreshWith(t, srv, old)
	wantStatus(t, late, http.StatusNoContent)
	late.Body.Close()
	if cookieNamed(late, "auth_token") == nil {
		t.Fatal("no access cookie for the late tab")
	}
	if cookieNamed(late, "refresh_token") != nil {
		t.Fatal("the late tab must keep the refresh token the browser has")
	}

	// The session lives on.
	ok := refreshWith(t, srv, current)
	wantStatus(t, ok, http.StatusNoContent)
	ok.Body.Close()
}

func TestRefresh_ReuseAfterGraceEndsTheSession(t *testing.T) {
	srv, _ := newAPI(t)
	c := signUpFreelancer(t, srv, "stolen@example.com")
	stolen := refreshToken(t, srv, c)

	resp := post(t, c, srv.URL+"/api/v1/auth/refresh", nil)
	wantStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
	current := refreshToken(t, srv, c)

	if _, err := testPool.Exec(context.Background(), `UPDATE sessions SET rotated_at = now() - interval '1 minute'`); err != nil {
		t.Fatalf("age rotation: %v", err)
	}

	reused := refreshWith(t, srv, stolen)
	wantStatus(t, reused, http.StatusUnauthorized)
	reused.Body.Close()

	// The owner's token is gone with the session.
	owner := refreshWith(t, srv, current)
	wantStatus(t, owner, http.StatusUnauthorized)
	owner.Body.Close()
}

func TestRefresh_Rejected(t *testing.T) {
	srv, c := newAPI(t)

	none := post(t, c, srv.URL+"/api/v1/auth/refresh", nil)
	wantStatus(t, none, http.StatusUnauthorized)
	none.Body.Close()

	unknown := refreshWith(t, srv, "not-a-token")
	wantStatus(t, unknown, http.StatusUnauthorized)
	unknown.Body.Close()

	user := signUpFreelancer(t, srv, "expired-session@example.com")
	if _, err := testPool.Exec(context.Background(), `UPDATE sessions SET expires_at = now() - interval '1 second'`); err != nil {
		t.Fatalf("expire session: %v", err)
	}
	expired := post(t, user, srv.URL+"/api/v1/auth/refresh", nil)
	wantStatus(t, expired, http.StatusUnauthorized)
	expired.Body.Close()
}

func TestLogout_EndsTheSession(t *testing.T) {
	srv, _ := newAPI(t)
	c := signUpFreelancer(t, srv, "bye@example.com")
	raw := refreshToken(t, srv, c)

	resp := post(t, c, srv.URL+"/api/v1/auth/logout", nil)
	wantStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
	if ck := cookieNamed(resp, "refresh_token"); ck == nil || ck.MaxAge >= 0 {
		t.Fatal("logout did not clear the refresh cookie")
	}

	after := refreshWith(t, srv, raw)
	wantStatus(t, after, http.StatusUnauthorized)
	after.Body.Close()
}

func TestSessions_ListAndEnd(t *testing.T) {
	srv, _ := newAPI(t)
	laptop := signUpFreelancer(t, srv, "devices@example.com")
	phone := signInAgain(t, srv, "devices@example.com")
	tablet := signInAgain(t, srv, "devices@example.com")

	list := sessionsOf(t, srv, laptop)
	if len(list) != 3 {
		t.Fatalf("sessions = %d, want 3", len(list))
	}
	var current int
	for _, s := range list {
		if s["current"] == true {
			current++
		}
	}
	if current != 1 {
		t.Fatalf("current sessions = %d, want 1", current)
	}

	// End the phone's session from the laptop.
	var phoneID string
	for _, s := range sessionsOf(t, srv, phone) {
		if s["current"] == true {
			phoneID = s["id"].(string)
		}
	}
	resp := do(t, laptop, http.MethodDelete, srv.URL+"/api/v1/me/sessions/"+phoneID, nil)
	wantStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
	gone := post(t, phone, srv.URL+"/api/v1/auth/refresh", nil)
	wantStatus(t, gone, http.StatusUnauthorized)
	gone.Body.Close()

	// Someone else's session, or none, is not found.
	other := signUpFreelancer(t, srv, "stranger@example.com")
	var laptopID string
	for _, s := range sessionsOf(t, srv, laptop) {
		if s["current"] == true {
			laptopID = s["id"].(string)
		}
	}
	for _, id := range []string{laptopID, "not-a-uuid"} {
		resp := do(t, other, http.MethodDelete, srv.URL+"/api/v1/me/sessions/"+id, nil)
		wantStatus(t, resp, http.StatusNotFound)
		resp.Body.Close()
	}

	// Sign out every other device: the tablet goes, the laptop stays.
	resp = do(t, laptop, http.MethodDelete, srv.URL+"/api/v1/me/sessions", nil)
	wantStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
	if n := len(sessionsOf(t, srv, laptop)); n != 1 {
		t.Fatalf("sessions after signing out others = %d, want 1", n)
	}
	out := post(t, tablet, srv.URL+"/api/v1/auth/refresh", nil)
	wantStatus(t, out, http.StatusUnauthorized)
	out.Body.Close()
	stays := post(t, laptop, srv.URL+"/api/v1/auth/refresh", nil)
	wantStatus(t, stays, http.StatusNoContent)
	stays.Body.Close()
}

func TestChangePassword_SignsOutOtherDevices(t *testing.T) {
	srv, _ := newAPI(t)
	here := signUpFreelancer(t, srv, "newpass@example.com")
	there := signInAgain(t, srv, "newpass@example.com")

	resp := do(t, here, http.MethodPut, srv.URL+"/api/v1/me/password", map[string]string{
		"current_password": "supersecret", "password": "evenmoresecret", "password_confirmation": "evenmoresecret",
	})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	out := post(t, there, srv.URL+"/api/v1/auth/refresh", nil)
	wantStatus(t, out, http.StatusUnauthorized)
	out.Body.Close()
	stays := post(t, here, srv.URL+"/api/v1/auth/refresh", nil)
	wantStatus(t, stays, http.StatusNoContent)
	stays.Body.Close()
}

func TestAcceptInvite_StartsASession(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "inviter@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "invited@example.com"))

	resp := post(t, client, srv.URL+"/api/v1/auth/refresh", nil)
	wantStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
	if n := len(sessionsOf(t, srv, client)); n != 1 {
		t.Fatalf("client sessions = %d, want 1", n)
	}
}
