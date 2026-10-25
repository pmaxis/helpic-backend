package integration

import (
	"context"
	"crypto/sha1" //nolint:gosec // the range API's key
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/pmaxis/helpic/internal/auth"
	"github.com/pmaxis/helpic/internal/config"
)

const breachedPassword = "password123"

// fakePwned is Pwned Passwords knowing one breached password, among padding.
// It keeps the paths it was asked for.
func fakePwned(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()
	sum := sha1.Sum([]byte(breachedPassword)) //nolint:gosec
	hash := strings.ToUpper(hex.EncodeToString(sum[:]))
	var (
		mu    sync.Mutex
		paths []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		if r.Header.Get("Add-Padding") != "true" {
			t.Errorf("asked without padding")
		}
		w.Write([]byte("0018A45C4D1DEF81644B54AB7F969B88D65:0\r\n")) //nolint:errcheck
		if r.URL.Path == "/range/"+hash[:5] {
			w.Write([]byte(hash[5:] + ":3861493\r\n")) //nolint:errcheck
		}
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), paths...)
	}
}

func passwordError(t *testing.T, resp *http.Response) any {
	t.Helper()
	wantStatus(t, resp, http.StatusBadRequest)
	return decode(t, resp)["error"].(map[string]any)["fields"].(map[string]any)["password"]
}

func TestRegister_BreachedPassword(t *testing.T) {
	pwned, asked := fakePwned(t)
	srv, c, _ := newAPIWith(t, func(cfg *config.Config) { cfg.PwnedPasswordsURL = pwned.URL })

	resp := register(t, c, srv.URL, "breached@example.com", breachedPassword)
	if got := passwordError(t, resp); got != "This password has appeared in a data breach. Choose another one." {
		t.Fatalf("password error = %v", got)
	}

	resp = register(t, c, srv.URL, "breached@example.com", "violet-harbor-41")
	wantStatus(t, resp, http.StatusAccepted)
	resp.Body.Close()

	// Only five characters of each hash went out, never the password.
	for _, p := range asked() {
		if prefix := strings.TrimPrefix(p, "/range/"); len(prefix) != 5 || strings.Contains(p, "password") {
			t.Fatalf("asked for %q", p)
		}
	}
	if n := len(asked()); n != 2 {
		t.Fatalf("asked %d times, want 2", n)
	}
}

func TestRegister_InvalidPasswordIsNotSentOut(t *testing.T) {
	pwned, asked := fakePwned(t)
	srv, c, _ := newAPIWith(t, func(cfg *config.Config) { cfg.PwnedPasswordsURL = pwned.URL })

	resp := register(t, c, srv.URL, "short@example.com", "short1")
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()
	if n := len(asked()); n != 0 {
		t.Fatalf("asked %d times for a password refused anyway", n)
	}
}

func TestRegister_BreachCheckDownLetsThePasswordThrough(t *testing.T) {
	down := httptest.NewServer(http.NotFoundHandler())
	down.Close()
	srv, c, _ := newAPIWith(t, func(cfg *config.Config) { cfg.PwnedPasswordsURL = down.URL })

	resp := register(t, c, srv.URL, "offline@example.com", breachedPassword)
	wantStatus(t, resp, http.StatusAccepted)
	resp.Body.Close()
}

func TestChangePassword_BreachedPassword(t *testing.T) {
	pwned, _ := fakePwned(t)
	srv, _, _ := newAPIWith(t, func(cfg *config.Config) { cfg.PwnedPasswordsURL = pwned.URL })
	c := signUpFreelancer(t, srv, "change@example.com")

	resp := do(t, c, http.MethodPut, srv.URL+"/api/v1/me/password", map[string]string{
		"current_password": "supersecret1", "password": breachedPassword, "password_confirmation": breachedPassword,
	})
	if got := passwordError(t, resp); got == nil {
		t.Fatal("a breached password was accepted")
	}
}

// A hash made with less work than new ones is redone when its owner signs in.
func TestLogin_UpgradesAnOutdatedHash(t *testing.T) {
	srv, _ := newAPI(t)
	signUpFreelancer(t, srv, "upgrade@example.com")

	cost := func() int {
		t.Helper()
		var hash string
		if err := testPool.QueryRow(context.Background(), `SELECT password_hash FROM users WHERE email = 'upgrade@example.com'`).Scan(&hash); err != nil {
			t.Fatal(err)
		}
		c, err := bcrypt.Cost([]byte(hash))
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	if got := cost(); got != auth.PasswordCost {
		t.Fatalf("new hash cost = %d, want %d", got, auth.PasswordCost)
	}

	before := auth.PasswordCost
	auth.PasswordCost = before + 1
	t.Cleanup(func() { auth.PasswordCost = before })

	resp := post(t, newClient(), srv.URL+"/api/v1/auth/login", map[string]string{"email": "upgrade@example.com", "password": "supersecret1"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	if got := cost(); got != before+1 {
		t.Fatalf("hash cost after sign-in = %d, want %d", got, before+1)
	}

	// The upgraded hash still opens the account.
	resp = post(t, newClient(), srv.URL+"/api/v1/auth/login", map[string]string{"email": "upgrade@example.com", "password": "supersecret1"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
}
