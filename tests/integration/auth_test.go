// Package integration contains integration tests that exercise the real HTTP stack
// against a throwaway PostgreSQL instance started with Testcontainers.
//
// Run: go test ./...   (requires a running Docker daemon)
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/pmaxis/helpic/internal/config"
	"github.com/pmaxis/helpic/internal/database"
	"github.com/pmaxis/helpic/internal/server"
	"github.com/pmaxis/helpic/internal/token"
)

var (
	testPool *pgxpool.Pool
	testCfg  config.Config
)

func TestMain(m *testing.M) {
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("app_test"),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("postgres"),
		testcontainers.WithWaitStrategy(
			wait.ForListeningPort("5432/tcp").WithStartupTimeout(90*time.Second),
		),
	)
	if err != nil {
		fmt.Println("SKIP: cannot start postgres test container:", err)
		os.Exit(0)
	}

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Println("SKIP: cannot build connection string:", err)
		_ = container.Terminate(ctx)
		os.Exit(0)
	}

	testCfg = config.Config{
		AppEnv:            "test",
		AppPort:           "8080",
		DatabaseURL:       dsn,
		MigrationsDir:     "../../migrations",
		JWTSecret:         "test-secret-please-change",
		JWTExpiration:     time.Hour,
		RefreshExpiration: 24 * time.Hour,
		CORSOrigin:        "http://localhost:5173",
		CookieName:        "auth_token",
		CookiePath:        "/",
		RefreshCookieName: "refresh_token",
		RefreshCookiePath: "/api/v1/auth",
		CookieSecure:      false,
		CookieSameSite:    http.SameSiteLaxMode,
		AppURL:            "http://app.test",
		Location:          time.UTC,
		ReminderHour:      9,
	}

	if err := database.Migrate(dsn, "../../migrations", database.Up); err != nil {
		fmt.Println("migration failed:", err)
		_ = container.Terminate(ctx)
		os.Exit(1)
	}

	pool, err := database.Connect(ctx, dsn)
	if err != nil {
		fmt.Println("connect failed:", err)
		_ = container.Terminate(ctx)
		os.Exit(1)
	}
	testPool = pool

	code := m.Run()

	pool.Close()
	_ = container.Terminate(ctx)
	os.Exit(code)
}

// --- helpers ---------------------------------------------------------------

func newAPI(t *testing.T) (*httptest.Server, *http.Client) {
	t.Helper()
	srv, c, _ := newAPIWith(t, nil)
	return srv, c
}

// newAPIWith is newAPI with the config adjusted first, also returning the app
// (to run its background work).
func newAPIWith(t *testing.T, adjust func(*config.Config)) (*httptest.Server, *http.Client, *server.App) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(),
		"TRUNCATE users, emails, rate_limit_buckets RESTART IDENTITY CASCADE"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	cfg := testCfg
	cfg.UploadsDir = t.TempDir()
	cfg.MaxUploadBytes = 1 << 20
	cfg.SwaggerEnabled = true
	if adjust != nil {
		adjust(&cfg)
	}
	app, err := server.New(cfg, testPool)
	if err != nil {
		t.Fatalf("build app: %v", err)
	}
	srv := httptest.NewServer(app.Handler)
	t.Cleanup(srv.Close)

	jar, _ := cookiejar.New(nil)
	return srv, &http.Client{Jar: jar}, app
}

func post(t *testing.T, c *http.Client, url string, body any) *http.Response {
	t.Helper()
	buf, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	return resp
}

func get(t *testing.T, c *http.Client, url string) *http.Response {
	t.Helper()
	resp, err := c.Get(url)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	return resp
}

func decode(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if len(raw) == 0 {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode body %q: %v", string(raw), err)
	}
	return out
}

func wantStatus(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	if resp.StatusCode != want {
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want %d; body: %s", resp.StatusCode, want, raw)
	}
}

func errorCode(body map[string]any) string {
	e, ok := body["error"].(map[string]any)
	if !ok {
		return ""
	}
	code, _ := e["code"].(string)
	return code
}

func register(t *testing.T, c *http.Client, base, email, password string) *http.Response {
	return post(t, c, base+"/api/v1/auth/register", map[string]string{
		"email":                 email,
		"name":                  "Test User",
		"password":              password,
		"password_confirmation": password,
	})
}

// confirmSignup opens the link the sign-up email carried and creates the account.
func confirmSignup(t *testing.T, c *http.Client, base, email string) *http.Response {
	t.Helper()
	token := linkToken(t, lastEmailTo(t, strings.ToLower(email)), "/confirm-signup/")
	return post(t, c, base+"/api/v1/auth/signups/confirm", map[string]string{"token": token})
}

// confirmed registers and opens the confirmation link, so the account exists.
func confirmed(t *testing.T, c *http.Client, base, email, password string) {
	t.Helper()
	resp := register(t, c, base, email, password)
	wantStatus(t, resp, http.StatusAccepted)
	resp.Body.Close()
	resp = confirmSignup(t, c, base, email)
	wantStatus(t, resp, http.StatusCreated)
	resp.Body.Close()
}

// --- registration --------------------------------------------------------

func TestRegister_Success(t *testing.T) {
	srv, c := newAPI(t)

	resp := register(t, c, srv.URL, "User@Example.com", "supersecret1")
	wantStatus(t, resp, http.StatusAccepted)
	if got := decode(t, resp)["email"]; got != "user@example.com" {
		t.Fatalf("email = %v, want normalized user@example.com", got)
	}

	// The inbox has not confirmed it, so there is no account to sign in to.
	resp = post(t, c, srv.URL+"/api/v1/auth/login", map[string]string{
		"email": "user@example.com", "password": "supersecret1",
	})
	wantStatus(t, resp, http.StatusUnauthorized)
	resp.Body.Close()

	resp = confirmSignup(t, c, srv.URL, "user@example.com")
	wantStatus(t, resp, http.StatusCreated)
	u, _ := decode(t, resp)["user"].(map[string]any)
	if u == nil || u["email"] != "user@example.com" || u["id"] == "" || u["id"] == nil {
		t.Fatalf("confirmed account = %v", u)
	}
	if u["role"] != "freelancer" {
		t.Fatalf("role = %v, want freelancer: public sign-up always creates a freelancer", u["role"])
	}
	if _, exists := u["password"]; exists {
		t.Fatal("response leaked password")
	}
	if _, exists := u["password_hash"]; exists {
		t.Fatal("response leaked password_hash")
	}
}

func TestRegister_NewRequestReplacesTheLink(t *testing.T) {
	srv, c := newAPI(t)
	resp := register(t, c, srv.URL, "again@example.com", "supersecret1")
	wantStatus(t, resp, http.StatusAccepted)
	resp.Body.Close()
	first := linkToken(t, lastEmailTo(t, "again@example.com"), "/confirm-signup/")

	resp = register(t, c, srv.URL, "again@example.com", "supersecret1")
	wantStatus(t, resp, http.StatusAccepted)
	resp.Body.Close()

	resp = post(t, c, srv.URL+"/api/v1/auth/signups/confirm", map[string]string{"token": first})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()

	resp = confirmSignup(t, c, srv.URL, "again@example.com")
	wantStatus(t, resp, http.StatusCreated)
	resp.Body.Close()
}

func TestRegister_InvalidEmail(t *testing.T) {
	srv, c := newAPI(t)
	resp := register(t, c, srv.URL, "not-an-email", "supersecret1")
	wantStatus(t, resp, http.StatusBadRequest)
	if got := errorCode(decode(t, resp)); got != "VALIDATION_ERROR" {
		t.Fatalf("code = %q, want VALIDATION_ERROR", got)
	}
}

func TestRegister_WeakPassword(t *testing.T) {
	srv, c := newAPI(t)
	for _, password := range []string{"short", "longenough", "12345678"} {
		resp := register(t, c, srv.URL, "weak@example.com", password)
		wantStatus(t, resp, http.StatusBadRequest)
		if got := errorCode(decode(t, resp)); got != "VALIDATION_ERROR" {
			t.Fatalf("%q: code = %q, want VALIDATION_ERROR", password, got)
		}
	}
}

func TestRegister_PasswordMismatch(t *testing.T) {
	srv, c := newAPI(t)
	resp := post(t, c, srv.URL+"/api/v1/auth/register", map[string]string{
		"email":                 "mismatch@example.com",
		"password":              "supersecret1",
		"password_confirmation": "different-secret",
	})
	wantStatus(t, resp, http.StatusBadRequest)
	if got := errorCode(decode(t, resp)); got != "VALIDATION_ERROR" {
		t.Fatalf("code = %q, want VALIDATION_ERROR", got)
	}
}

func TestRegister_DuplicateEmail(t *testing.T) {
	srv, c := newAPI(t)

	confirmed(t, c, srv.URL, "dupe@example.com", "supersecret1")

	second := register(t, c, srv.URL, "Dupe@example.com", "supersecret1")
	wantStatus(t, second, http.StatusConflict)
	if got := errorCode(decode(t, second)); got != "EMAIL_ALREADY_EXISTS" {
		t.Fatalf("code = %q, want EMAIL_ALREADY_EXISTS", got)
	}
}

// --- login ---------------------------------------------------------------

func TestLogin_Success(t *testing.T) {
	srv, c := newAPI(t)
	confirmed(t, c, srv.URL, "login@example.com", "supersecret1")

	resp := post(t, c, srv.URL+"/api/v1/auth/login", map[string]string{
		"email":    "login@example.com",
		"password": "supersecret1",
	})
	wantStatus(t, resp, http.StatusOK)

	body := decode(t, resp)
	if _, exists := body["token"]; exists {
		t.Fatal("login response must not contain a JWT in JSON")
	}

	var authCookie *http.Cookie
	for _, ck := range resp.Cookies() {
		if ck.Name == "auth_token" {
			authCookie = ck
		}
	}
	if authCookie == nil {
		t.Fatal("no auth_token cookie set")
	}
	if !authCookie.HttpOnly {
		t.Fatal("auth cookie is not HttpOnly")
	}
	if authCookie.Value == "" {
		t.Fatal("auth cookie has empty value")
	}
}

func TestLogin_WrongPassword(t *testing.T) {
	srv, c := newAPI(t)
	confirmed(t, c, srv.URL, "wrongpw@example.com", "supersecret1")

	resp := post(t, c, srv.URL+"/api/v1/auth/login", map[string]string{
		"email":    "wrongpw@example.com",
		"password": "not-the-password",
	})
	wantStatus(t, resp, http.StatusUnauthorized)
	if got := errorCode(decode(t, resp)); got != "INVALID_CREDENTIALS" {
		t.Fatalf("code = %q, want INVALID_CREDENTIALS", got)
	}
}

func TestLogin_NonExistingEmail(t *testing.T) {
	srv, c := newAPI(t)
	resp := post(t, c, srv.URL+"/api/v1/auth/login", map[string]string{
		"email":    "ghost@example.com",
		"password": "supersecret1",
	})
	wantStatus(t, resp, http.StatusUnauthorized)
	if got := errorCode(decode(t, resp)); got != "INVALID_CREDENTIALS" {
		t.Fatalf("code = %q, want INVALID_CREDENTIALS", got)
	}
}

// --- /api/v1/me -----------------------------------------------------------------

func TestMe_ValidSession(t *testing.T) {
	srv, c := newAPI(t)
	confirmed(t, c, srv.URL, "me@example.com", "supersecret1")
	post(t, c, srv.URL+"/api/v1/auth/login", map[string]string{
		"email":    "me@example.com",
		"password": "supersecret1",
	}).Body.Close()

	resp := get(t, c, srv.URL+"/api/v1/me")
	wantStatus(t, resp, http.StatusOK)

	body := decode(t, resp)
	u, _ := body["user"].(map[string]any)
	if u == nil || u["email"] != "me@example.com" {
		t.Fatalf("unexpected /api/v1/me body: %v", body)
	}
}

func TestMe_NoCookie(t *testing.T) {
	srv, _ := newAPI(t)
	resp, err := http.Get(srv.URL + "/api/v1/me")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	wantStatus(t, resp, http.StatusUnauthorized)
	if got := errorCode(decode(t, resp)); got != "UNAUTHORIZED" {
		t.Fatalf("code = %q, want UNAUTHORIZED", got)
	}
}

func TestMe_InvalidJWT(t *testing.T) {
	srv, _ := newAPI(t)
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/me", nil)
	req.AddCookie(&http.Cookie{Name: "auth_token", Value: "this.is.not.a.jwt"})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	wantStatus(t, resp, http.StatusUnauthorized)
}

func TestMe_ExpiredJWT(t *testing.T) {
	srv, c := newAPI(t)
	confirmed(t, c, srv.URL, "expired@example.com", "supersecret1")

	var userID string
	if err := testPool.QueryRow(context.Background(),
		"SELECT id FROM users WHERE email = $1", "expired@example.com").Scan(&userID); err != nil {
		t.Fatalf("load user id: %v", err)
	}

	expiredManager := token.NewManager(testCfg.JWTSecret, -time.Hour)
	expired, _, err := expiredManager.Generate(userID, "00000000-0000-0000-0000-000000000000")
	if err != nil {
		t.Fatalf("generate expired token: %v", err)
	}

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/me", nil)
	req.AddCookie(&http.Cookie{Name: "auth_token", Value: expired})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	wantStatus(t, resp, http.StatusUnauthorized)
}

// --- logout ----------------------------------------------------------------

func TestLogout_ClearsCookie(t *testing.T) {
	srv, c := newAPI(t)
	confirmed(t, c, srv.URL, "logout@example.com", "supersecret1")
	post(t, c, srv.URL+"/api/v1/auth/login", map[string]string{
		"email":    "logout@example.com",
		"password": "supersecret1",
	}).Body.Close()

	// Session works before logout.
	pre := get(t, c, srv.URL+"/api/v1/me")
	wantStatus(t, pre, http.StatusOK)
	pre.Body.Close()

	resp := post(t, c, srv.URL+"/api/v1/auth/logout", nil)
	wantStatus(t, resp, http.StatusNoContent)

	var cleared bool
	for _, ck := range resp.Cookies() {
		if ck.Name == "auth_token" && ck.Value == "" && ck.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("logout did not clear the auth cookie")
	}

	// Session is rejected after logout (cookie jar dropped the cookie).
	postLogout := get(t, c, srv.URL+"/api/v1/me")
	wantStatus(t, postLogout, http.StatusUnauthorized)
	postLogout.Body.Close()
}
