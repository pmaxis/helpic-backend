package integration

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pmaxis/helpic/internal/config"
	"github.com/pmaxis/helpic/internal/totp"
)

// appCode is the code an authenticator app shows for the secret, offset
// steps from now.
func appCode(t *testing.T, secret string, offset int64) string {
	t.Helper()
	code, err := totp.Code(secret, totp.Step(time.Now())+offset)
	if err != nil {
		t.Fatal(err)
	}
	return code
}

// turnOnTwoFactor turns two-factor on for the signed-in user with a code of
// the current step, and returns the secret and the recovery codes.
func turnOnTwoFactor(t *testing.T, srv *httptest.Server, c *http.Client) (string, []string) {
	t.Helper()
	resp := post(t, c, srv.URL+"/api/v1/me/two-factor/setup", map[string]string{"current_password": "supersecret1"})
	wantStatus(t, resp, http.StatusOK)
	secret := decode(t, resp)["secret"].(string)
	resp = post(t, c, srv.URL+"/api/v1/me/two-factor/enable", map[string]string{"code": appCode(t, secret, 0)})
	wantStatus(t, resp, http.StatusOK)
	var codes []string
	for _, c := range decode(t, resp)["recovery_codes"].([]any) {
		codes = append(codes, c.(string))
	}
	return secret, codes
}

func loginWithCode(t *testing.T, c *http.Client, srv *httptest.Server, email, code string) *http.Response {
	t.Helper()
	return post(t, c, srv.URL+"/api/v1/auth/login", map[string]string{"email": email, "password": "supersecret1", "code": code})
}

// wantCodeRefused checks a 400 with the field "code".
func wantCodeRefused(t *testing.T, resp *http.Response) {
	t.Helper()
	wantStatus(t, resp, http.StatusBadRequest)
	if _, ok := decode(t, resp)["error"].(map[string]any)["fields"].(map[string]any)["code"]; !ok {
		t.Fatal("no error on the field code")
	}
}

func TestTwoFactor_TurnOnAndSignIn(t *testing.T) {
	srv, _ := newAPI(t)
	c := signUpFreelancer(t, srv, "two@example.com")
	elsewhere := signInAgain(t, srv, "two@example.com")

	// Starting takes the password.
	resp := post(t, c, srv.URL+"/api/v1/me/two-factor/setup", map[string]string{"current_password": "wrong-password1"})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()
	resp = post(t, c, srv.URL+"/api/v1/me/two-factor/setup", map[string]string{"current_password": "supersecret1"})
	wantStatus(t, resp, http.StatusOK)
	setup := decode(t, resp)
	secret := setup["secret"].(string)
	if !strings.HasPrefix(setup["qr"].(string), "data:image/svg+xml;base64,") || !strings.Contains(setup["uri"].(string), "secret="+secret) {
		t.Fatalf("setup = %v", setup)
	}
	// Until a code confirms it, signing in doesn't change.
	resp = get(t, c, srv.URL+"/api/v1/me/two-factor")
	wantStatus(t, resp, http.StatusOK)
	if decode(t, resp)["enabled"] != false {
		t.Fatal("on before it was confirmed")
	}

	wantCodeRefused(t, post(t, c, srv.URL+"/api/v1/me/two-factor/enable", map[string]string{"code": "000000"}))
	resp = post(t, c, srv.URL+"/api/v1/me/two-factor/enable", map[string]string{"code": appCode(t, secret, 0)})
	wantStatus(t, resp, http.StatusOK)
	on := decode(t, resp)
	if field(on, "user")["two_factor"] != true || len(on["recovery_codes"].([]any)) != 10 {
		t.Fatalf("enable = %v", on)
	}
	recovery := on["recovery_codes"].([]any)[0].(string)

	// Whoever was signed in with the password alone is out; this device stays.
	wantStatus(t, get(t, elsewhere, srv.URL+"/api/v1/me"), http.StatusUnauthorized)
	wantStatus(t, get(t, c, srv.URL+"/api/v1/me"), http.StatusOK)
	if e := lastEmailTo(t, "two@example.com"); e.Subject != "Helpic: Two-factor sign-in is on" {
		t.Fatalf("email = %q", e.Subject)
	}

	// The password alone isn't enough now, and sets no cookie.
	d := newClient()
	resp = post(t, d, srv.URL+"/api/v1/auth/login", map[string]string{"email": "two@example.com", "password": "supersecret1"})
	wantStatus(t, resp, http.StatusForbidden)
	if cookieNamed(resp, "auth_token") != nil || errorCode(decode(t, resp)) != "TWO_FACTOR_REQUIRED" {
		t.Fatal("signed in without a code")
	}
	// A wrong password says nothing about two-factor.
	resp = post(t, d, srv.URL+"/api/v1/auth/login", map[string]string{"email": "two@example.com", "password": "wrong-password1"})
	wantStatus(t, resp, http.StatusUnauthorized)
	resp.Body.Close()

	// The code that turned it on is spent; the next one works.
	wantCodeRefused(t, loginWithCode(t, d, srv, "two@example.com", appCode(t, secret, 0)))
	resp = loginWithCode(t, d, srv, "two@example.com", appCode(t, secret, 1))
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	// And it doesn't work twice either.
	wantCodeRefused(t, loginWithCode(t, newClient(), srv, "two@example.com", appCode(t, secret, 1)))

	// A recovery code works once, typed any which way.
	resp = loginWithCode(t, newClient(), srv, "two@example.com", " "+strings.ToUpper(recovery)+" ")
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	wantCodeRefused(t, loginWithCode(t, newClient(), srv, "two@example.com", recovery))

	resp = get(t, c, srv.URL+"/api/v1/me/two-factor")
	wantStatus(t, resp, http.StatusOK)
	if st := decode(t, resp); st["enabled"] != true || st["recovery_codes_left"] != float64(9) {
		t.Fatalf("status = %v", st)
	}
	// Starting again while it's on is refused.
	resp = post(t, c, srv.URL+"/api/v1/me/two-factor/setup", map[string]string{"current_password": "supersecret1"})
	wantStatus(t, resp, http.StatusConflict)
	resp.Body.Close()
}

func TestTwoFactor_NewRecoveryCodesAndTurningOff(t *testing.T) {
	srv, _ := newAPI(t)
	c := signUpFreelancer(t, srv, "codes@example.com")
	secret, old := turnOnTwoFactor(t, srv, c)

	// Both take the password and a code.
	wantCodeRefused(t, post(t, c, srv.URL+"/api/v1/me/two-factor/recovery-codes", map[string]string{"current_password": "supersecret1"}))
	resp := post(t, c, srv.URL+"/api/v1/me/two-factor/recovery-codes", map[string]string{"current_password": "wrong-password1", "code": appCode(t, secret, 1)})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()

	resp = post(t, c, srv.URL+"/api/v1/me/two-factor/recovery-codes", map[string]string{"current_password": "supersecret1", "code": appCode(t, secret, 1)})
	wantStatus(t, resp, http.StatusOK)
	fresh := decode(t, resp)["recovery_codes"].([]any)
	if len(fresh) != 10 || fresh[0] == old[0] {
		t.Fatalf("new codes = %v", fresh)
	}
	if e := lastEmailTo(t, "codes@example.com"); e.Subject != "Helpic: New recovery codes" {
		t.Fatalf("email = %q", e.Subject)
	}
	// The old codes are gone.
	wantCodeRefused(t, loginWithCode(t, newClient(), srv, "codes@example.com", old[1]))

	// Turned off with a new recovery code, signing in takes the password alone.
	resp = post(t, c, srv.URL+"/api/v1/me/two-factor/disable", map[string]string{"current_password": "supersecret1", "code": fresh[0].(string)})
	wantStatus(t, resp, http.StatusOK)
	if field(decode(t, resp), "user")["two_factor"] != false {
		t.Fatal("still on")
	}
	resp = login(t, srv, newClient(), "codes@example.com", "supersecret1")
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	resp = post(t, c, srv.URL+"/api/v1/me/two-factor/disable", map[string]string{"current_password": "supersecret1", "code": "123456"})
	wantStatus(t, resp, http.StatusConflict)
	resp.Body.Close()
}

func TestTwoFactor_ExistingClientAcceptsInvitation(t *testing.T) {
	srv, _ := newAPI(t)
	first := signUpFreelancer(t, srv, "first@example.com")
	client := newClient()
	resp := acceptInvite(t, srv, client, invite(t, srv, first, "client@example.com"), map[string]string{
		"name": "Acme Inc", "password": "supersecret1", "password_confirmation": "supersecret1",
	})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	secret, _ := turnOnTwoFactor(t, srv, client)

	second := signUpFreelancer(t, srv, "second@example.com")
	token := invite(t, srv, second, "client@example.com")
	resp = acceptInvite(t, srv, newClient(), token, map[string]string{"password": "supersecret1"})
	wantStatus(t, resp, http.StatusForbidden)
	if errorCode(decode(t, resp)) != "TWO_FACTOR_REQUIRED" {
		t.Fatal("accepted without a code")
	}
	wantCodeRefused(t, acceptInvite(t, srv, newClient(), token, map[string]string{"password": "supersecret1", "code": "000000"}))
	resp = acceptInvite(t, srv, newClient(), token, map[string]string{"password": "supersecret1", "code": appCode(t, secret, 1)})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
}

func TestRateLimit_WrongTwoFactorCodes(t *testing.T) {
	srv, _, _ := newAPIWith(t, func(cfg *config.Config) { cfg.RateLimit = true })
	c := signUpFreelancer(t, srv, "guess@example.com")
	secret, _ := turnOnTwoFactor(t, srv, c)

	for i := range 10 {
		resp := loginWithCode(t, newClient(), srv, "guess@example.com", "000000")
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("wrong code %d = %d, want 400", i+1, resp.StatusCode)
		}
		resp.Body.Close()
	}
	// Then even the right one waits, from anywhere.
	wantLimited(t, loginWithCode(t, newClient(), srv, "guess@example.com", appCode(t, secret, 1)))
}
