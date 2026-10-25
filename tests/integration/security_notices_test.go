package integration

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/pmaxis/helpic/internal/auth"
)

const newSignIn = "New sign-in to your Helpic account"

// subjectsTo lists the subjects of the emails to the address, oldest first.
func subjectsTo(t *testing.T, to string) []string {
	t.Helper()
	var out []string
	for _, e := range emailsTo(t, to) {
		out = append(out, e.Subject)
	}
	return out
}

// hasEmail reports whether the address got an email whose subject contains
// part, wherever it is among them: signing in from a new browser may have
// sent another since.
func hasEmail(t *testing.T, to, part string) bool {
	t.Helper()
	for _, s := range subjectsTo(t, to) {
		if strings.Contains(s, part) {
			return true
		}
	}
	return false
}

func countSubject(t *testing.T, to, subject string) int {
	t.Helper()
	n := 0
	for _, s := range subjectsTo(t, to) {
		if s == subject {
			n++
		}
	}
	return n
}

func signInFrom(t *testing.T, srv string, c *http.Client, email, userAgent string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv+"/api/v1/auth/login", strings.NewReader(`{"email":"`+email+`","password":"supersecret1"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
}

func deviceCookie(t *testing.T, c *http.Client, srv string) *http.Cookie {
	t.Helper()
	u, _ := url.Parse(srv)
	for _, ck := range c.Jar.Cookies(u) {
		if ck.Name == auth.DeviceCookie {
			return ck
		}
	}
	return nil
}

const macChrome = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"

func TestNewDevice_SignInFromAnotherBrowserIsEmailed(t *testing.T) {
	srv, _ := newAPI(t)
	const email = "devices@example.com"
	home := signUpFreelancer(t, srv, email)

	// The browser the account was made in is known; signing in again there
	// tells nobody.
	if deviceCookie(t, home, srv.URL) == nil {
		t.Fatal("signing up left no device cookie")
	}
	signInFrom(t, srv.URL, home, email, macChrome)
	if n := countSubject(t, email, newSignIn); n != 0 {
		t.Fatalf("%d new sign-in emails for the known browser", n)
	}

	// Another browser is emailed about, once.
	other := newClient()
	signInFrom(t, srv.URL, other, email, macChrome)
	if n := countSubject(t, email, newSignIn); n != 1 {
		t.Fatalf("%d new sign-in emails, want 1", n)
	}
	e := lastEmailTo(t, email)
	for _, want := range []string{"Chrome on macOS", "127.0.0.1", "http://app.test/settings?tab=security"} {
		if !strings.Contains(e.Text, want) {
			t.Errorf("the email lacks %q:\n%s", want, e.Text)
		}
	}

	// That browser is known from now on, even after signing out.
	resp := post(t, other, srv.URL+"/api/v1/auth/logout", nil)
	resp.Body.Close()
	signInFrom(t, srv.URL, other, email, macChrome)
	if n := countSubject(t, email, newSignIn); n != 1 {
		t.Fatalf("%d new sign-in emails after signing in again, want 1", n)
	}
}

// An account with no known browser yet, as every account is on the day
// devices start being kept, learns its first one without an email.
func TestNewDevice_FirstBrowserIsQuiet(t *testing.T) {
	srv, _ := newAPI(t)
	const email = "quiet@example.com"
	signUpFreelancer(t, srv, email)
	if _, err := testPool.Exec(t.Context(), `DELETE FROM known_devices`); err != nil {
		t.Fatal(err)
	}

	signInFrom(t, srv.URL, newClient(), email, macChrome)
	if n := countSubject(t, email, newSignIn); n != 0 {
		t.Fatalf("%d new sign-in emails for the first browser", n)
	}
	signInFrom(t, srv.URL, newClient(), email, macChrome)
	if n := countSubject(t, email, newSignIn); n != 1 {
		t.Fatalf("%d new sign-in emails for the second browser, want 1", n)
	}
}

// Two accounts signed in on one browser share its token: neither is told
// about the other's sign-ins there.
func TestNewDevice_SharedBrowser(t *testing.T) {
	srv, _ := newAPI(t)
	shared := signUpFreelancer(t, srv, "first@example.com")
	signUpFreelancer(t, srv, "second@example.com")
	token := deviceCookie(t, shared, srv.URL).Value

	signInFrom(t, srv.URL, shared, "second@example.com", macChrome)
	if got := deviceCookie(t, shared, srv.URL).Value; got != token {
		t.Fatal("the browser's token changed when another account signed in")
	}
	signInFrom(t, srv.URL, shared, "first@example.com", macChrome)
	if n := countSubject(t, "first@example.com", newSignIn); n != 0 {
		t.Fatalf("%d new sign-in emails to the browser's first account", n)
	}
	if n := countSubject(t, "second@example.com", newSignIn); n != 1 {
		t.Fatalf("%d new sign-in emails to the second account, want 1", n)
	}
}

func TestRecoveryCode_UseIsEmailed(t *testing.T) {
	srv, _ := newAPI(t)
	const email = "recovery@example.com"
	c := signUpFreelancer(t, srv, email)
	_, codes := turnOnTwoFactor(t, srv, c)

	resp := loginWithCode(t, c, srv, email, codes[0])
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	e := lastEmailTo(t, email)
	if e.Subject != "A recovery code was used on your Helpic account" {
		t.Fatalf("last email = %q, want the recovery code notice", e.Subject)
	}
	if want := "Recovery codes left: " + strconv.Itoa(len(codes)-1); !strings.Contains(e.Text, want) {
		t.Fatalf("the email lacks %q:\n%s", want, e.Text)
	}
}
