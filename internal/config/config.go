// Package config loads application configuration from environment variables.
// No secrets are hardcoded; every deployment-specific value comes from the
// environment (see .env.example).
package config

import (
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime configuration for the server.
type Config struct {
	AppEnv        string
	AppPort       string
	DatabaseURL   string
	MigrationsDir string

	// JWTExpiration is how long an access token lasts; RefreshExpiration is
	// how long a session lasts unused.
	JWTSecret         string
	JWTExpiration     time.Duration
	RefreshExpiration time.Duration

	// TwoFactorKey seals the secrets of authenticator apps. Without it,
	// development seals them with a key made from JWTSecret.
	TwoFactorKey string

	CORSOrigin string

	CookieName     string
	CookieSecure   bool
	CookieSameSite http.SameSite
	CookiePath     string

	// The refresh token's cookie; its path keeps it to the auth endpoints.
	RefreshCookieName string
	RefreshCookiePath string

	// SwaggerEnabled serves the API docs at /api/docs; off in production
	// unless SWAGGER_ENABLED says otherwise.
	SwaggerEnabled bool

	// UploadsDir holds task attachments; MaxUploadBytes caps one file, and
	// UploadQuotaBytes everything one user has uploaded.
	UploadsDir       string
	MaxUploadBytes   int64
	UploadQuotaBytes int64

	// AppURL is the frontend's address, for links sent outside the app.
	AppURL string

	// TelegramBotToken turns on notifications in Telegram; empty leaves them
	// off. TelegramAPIURL is the Bot API.
	TelegramBotToken string
	TelegramAPIURL   string

	// NBUURL is where the National Bank of Ukraine's official rates are
	// read, for reports' totals in hryvnias; "" (NBU_API_URL=off) turns
	// that off.
	NBUURL string

	// RateLimit slows down password guessing and sign-up spam (on unless
	// RATE_LIMIT says otherwise). TrustedProxies are the proxies whose
	// X-Forwarded-For names the client; without any, the peer is the client.
	RateLimit      bool
	TrustedProxies []netip.Prefix

	// SMTP sends email; without SMTPHost nothing is sent, only logged.
	SMTPHost     string
	SMTPPort     int
	SMTPUsername string
	SMTPPassword string
	// MailFrom is the sender, e.g. "Helpic <no-reply@helpic.app>".
	MailFrom string

	// Location is the app's time zone: due dates are days there, and
	// deadline reminders go out from ReminderHour on.
	Location     *time.Location
	ReminderHour int
}

// IsProduction reports whether the app runs in a production environment.
func (c Config) IsProduction() bool {
	return strings.EqualFold(c.AppEnv, "production")
}

// Load reads configuration from the environment, applying safe development
// defaults and validating required values.
func Load() (Config, error) {
	cfg := Config{
		AppEnv:        getEnv("APP_ENV", "development"),
		AppPort:       getEnv("APP_PORT", "8080"),
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		MigrationsDir: getEnv("MIGRATIONS_DIR", "migrations"),
		JWTSecret:     os.Getenv("JWT_SECRET"),
		TwoFactorKey:  os.Getenv("TWO_FACTOR_KEY"),
		CORSOrigin:    getEnv("CORS_ORIGIN", "http://localhost:5180"),
		CookieName:    getEnv("COOKIE_NAME", "auth_token"),
		CookiePath:    getEnv("COOKIE_PATH", "/"),

		RefreshCookieName: getEnv("REFRESH_COOKIE_NAME", "refresh_token"),
		RefreshCookiePath: getEnv("REFRESH_COOKIE_PATH", "/api/v1/auth"),
	}

	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	if cfg.JWTSecret == "" {
		return Config{}, fmt.Errorf("JWT_SECRET is required")
	}

	exp, err := time.ParseDuration(getEnv("JWT_EXPIRATION", "15m"))
	if err != nil {
		return Config{}, fmt.Errorf("invalid JWT_EXPIRATION: %w", err)
	}
	cfg.JWTExpiration = exp

	refresh, err := time.ParseDuration(getEnv("REFRESH_EXPIRATION", "720h"))
	if err != nil || refresh <= exp {
		return Config{}, fmt.Errorf("invalid REFRESH_EXPIRATION: must be a duration longer than JWT_EXPIRATION")
	}
	cfg.RefreshExpiration = refresh

	cfg.CookieSecure = getEnvBool("COOKIE_SECURE", false)
	cfg.CookieSameSite = parseSameSite(getEnv("COOKIE_SAME_SITE", "lax"))
	cfg.SwaggerEnabled = getEnvBool("SWAGGER_ENABLED", !cfg.IsProduction())
	cfg.UploadsDir = getEnv("UPLOADS_DIR", "uploads")
	maxMB, err := strconv.Atoi(getEnv("MAX_UPLOAD_MB", "25"))
	if err != nil || maxMB <= 0 {
		return Config{}, fmt.Errorf("invalid MAX_UPLOAD_MB")
	}
	cfg.MaxUploadBytes = int64(maxMB) << 20
	quotaMB, err := strconv.Atoi(getEnv("UPLOAD_QUOTA_MB", "1024"))
	if err != nil || quotaMB < maxMB {
		return Config{}, fmt.Errorf("invalid UPLOAD_QUOTA_MB: at least MAX_UPLOAD_MB")
	}
	cfg.UploadQuotaBytes = int64(quotaMB) << 20
	cfg.AppURL = getEnv("APP_URL", cfg.CORSOrigin)
	cfg.TelegramBotToken = os.Getenv("TELEGRAM_BOT_TOKEN")
	cfg.TelegramAPIURL = getEnv("TELEGRAM_API_URL", "https://api.telegram.org")
	if cfg.NBUURL = strings.TrimRight(getEnv("NBU_API_URL", "https://bank.gov.ua"), "/"); strings.EqualFold(cfg.NBUURL, "off") {
		cfg.NBUURL = ""
	}
	cfg.RateLimit = getEnvBool("RATE_LIMIT", true)
	cfg.SMTPHost = os.Getenv("SMTP_HOST")
	if cfg.SMTPPort, err = strconv.Atoi(getEnv("SMTP_PORT", "587")); err != nil || cfg.SMTPPort <= 0 {
		return Config{}, fmt.Errorf("invalid SMTP_PORT")
	}
	cfg.SMTPUsername = os.Getenv("SMTP_USERNAME")
	cfg.SMTPPassword = os.Getenv("SMTP_PASSWORD")
	cfg.MailFrom = getEnv("MAIL_FROM", "Helpic <no-reply@helpic.local>")
	if cfg.Location, err = time.LoadLocation(getEnv("APP_TIMEZONE", "UTC")); err != nil {
		return Config{}, fmt.Errorf("invalid APP_TIMEZONE: %w", err)
	}
	if cfg.ReminderHour, err = strconv.Atoi(getEnv("REMINDER_HOUR", "9")); err != nil || cfg.ReminderHour < 0 || cfg.ReminderHour > 23 {
		return Config{}, fmt.Errorf("invalid REMINDER_HOUR: a whole hour, 0 to 23")
	}
	if cfg.TrustedProxies, err = parsePrefixes(os.Getenv("TRUSTED_PROXIES")); err != nil {
		return Config{}, fmt.Errorf("invalid TRUSTED_PROXIES: %w", err)
	}
	if err := cfg.validateProduction(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// validateProduction refuses a production process whose accounts, mail or
// database would be exposed the way the development stack is.
func (c Config) validateProduction() error {
	if !c.IsProduction() {
		return nil
	}
	var problems []string
	if len(c.JWTSecret) < 32 || strings.EqualFold(c.JWTSecret, "change-me-in-production") {
		problems = append(problems, "JWT_SECRET must be at least 32 characters and not the placeholder")
	}
	// Its own key, so a new JWT_SECRET doesn't lock everyone with two-factor out.
	if len(c.TwoFactorKey) < 32 || c.TwoFactorKey == c.JWTSecret {
		problems = append(problems, "TWO_FACTOR_KEY must be at least 32 characters and not the JWT_SECRET")
	}
	if !c.CookieSecure {
		problems = append(problems, "COOKIE_SECURE must be true")
	}
	if c.CookieSameSite == http.SameSiteNoneMode {
		problems = append(problems, "COOKIE_SAME_SITE=none is refused; serve the site and the API as one site and keep lax")
	}
	if !c.RateLimit {
		problems = append(problems, "RATE_LIMIT must stay on")
	}
	if c.SwaggerEnabled {
		problems = append(problems, "SWAGGER_ENABLED must be false")
	}
	if reason := insecureDatabase(c.DatabaseURL); reason != "" {
		problems = append(problems, reason)
	}
	if c.SMTPHost == "" || isMailpit(c.SMTPHost) {
		problems = append(problems, "SMTP_HOST must be a real mail server, not Mailpit or empty")
	}
	if strings.Contains(strings.ToLower(c.MailFrom), "helpic.local") {
		problems = append(problems, "MAIL_FROM must be a real sender address")
	}
	if reason := localAppURL(c.AppURL); reason != "" {
		problems = append(problems, "APP_URL "+reason)
	}
	if reason := localAppURL(c.CORSOrigin); reason != "" {
		problems = append(problems, "CORS_ORIGIN "+reason)
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("refusing to start in production: %s", strings.Join(problems, "; "))
}

// insecureDatabase reports why this URL is not fit for production.
func insecureDatabase(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return databaseProblem("is not a URL")
	}
	if pass, _ := u.User.Password(); pass == "" || pass == "postgres" {
		return databaseProblem("must not use the default postgres password")
	}
	switch strings.ToLower(u.Query().Get("sslmode")) {
	case "verify-full", "verify-ca":
		return ""
	default:
		return databaseProblem("must set sslmode to verify-full or verify-ca")
	}
}

func databaseProblem(detail string) string {
	return "DATABASE_URL " + detail
}

func isMailpit(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	return host == "mailpit" || strings.HasPrefix(host, "mailpit.")
}

// localAppURL reports why a link base is not a public https address.
func localAppURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "must be the public https address"
	}
	host := strings.ToLower(u.Hostname())
	if u.Scheme != "https" || host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return "must be the public https address, not " + raw
	}
	return ""
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func getEnvBool(key string, fallback bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func parseSameSite(v string) http.SameSite {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "strict":
		return http.SameSiteStrictMode
	case "none":
		return http.SameSiteNoneMode
	default:
		return http.SameSiteLaxMode
	}
}

// parsePrefixes reads a comma-separated list of IPs and CIDRs.
func parsePrefixes(list string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, item := range strings.Split(list, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if !strings.Contains(item, "/") {
			ip, err := netip.ParseAddr(item)
			if err != nil {
				return nil, err
			}
			ip = ip.Unmap()
			out = append(out, netip.PrefixFrom(ip, ip.BitLen()))
			continue
		}
		p, err := netip.ParsePrefix(item)
		if err != nil {
			return nil, err
		}
		out = append(out, p.Masked())
	}
	return out, nil
}
