// Package config loads application configuration from environment variables.
// No secrets are hardcoded; every deployment-specific value comes from the
// environment (see .env.example).
package config

import (
	"fmt"
	"net/http"
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

	JWTSecret     string
	JWTExpiration time.Duration

	CORSOrigin string

	CookieName     string
	CookieSecure   bool
	CookieSameSite http.SameSite
	CookiePath     string

	// ChromePath points at the Chrome/Chromium that prints report PDFs; empty
	// means auto-detect.
	ChromePath string

	// SwaggerEnabled serves the API docs at /api/docs; off in production
	// unless SWAGGER_ENABLED says otherwise.
	SwaggerEnabled bool

	// UploadsDir holds task attachments; MaxUploadBytes caps one file.
	UploadsDir     string
	MaxUploadBytes int64
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
		CORSOrigin:    getEnv("CORS_ORIGIN", "http://localhost:5180"),
		CookieName:    getEnv("COOKIE_NAME", "auth_token"),
		CookiePath:    getEnv("COOKIE_PATH", "/"),
	}

	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	if cfg.JWTSecret == "" {
		return Config{}, fmt.Errorf("JWT_SECRET is required")
	}

	exp, err := time.ParseDuration(getEnv("JWT_EXPIRATION", "24h"))
	if err != nil {
		return Config{}, fmt.Errorf("invalid JWT_EXPIRATION: %w", err)
	}
	cfg.JWTExpiration = exp

	cfg.CookieSecure = getEnvBool("COOKIE_SECURE", false)
	cfg.CookieSameSite = parseSameSite(getEnv("COOKIE_SAME_SITE", "lax"))
	cfg.ChromePath = os.Getenv("CHROME_PATH")
	cfg.SwaggerEnabled = getEnvBool("SWAGGER_ENABLED", !cfg.IsProduction())
	cfg.UploadsDir = getEnv("UPLOADS_DIR", "uploads")
	maxMB, err := strconv.Atoi(getEnv("MAX_UPLOAD_MB", "25"))
	if err != nil || maxMB <= 0 {
		return Config{}, fmt.Errorf("invalid MAX_UPLOAD_MB")
	}
	cfg.MaxUploadBytes = int64(maxMB) << 20

	return cfg, nil
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
