package config_test

import (
	"strings"
	"testing"

	"github.com/pmaxis/helpic/internal/config"
)

func TestLoad_DevelopmentAllowsLocalDefaults(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("DATABASE_URL", "postgres://postgres:postgres@localhost/helpic?sslmode=disable")
	t.Setenv("JWT_SECRET", "change-me-in-production")

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.IsProduction() || cfg.CookieSecure {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestLoad_ProductionRefusesTheDevelopmentStack(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("DATABASE_URL", "postgres://postgres:postgres@localhost/helpic?sslmode=disable")
	t.Setenv("JWT_SECRET", "change-me-in-production")
	t.Setenv("SMTP_HOST", "mailpit")
	t.Setenv("APP_URL", "http://localhost:5180")
	t.Setenv("CORS_ORIGIN", "http://localhost:5180")

	_, err := config.Load()
	if err == nil || !strings.Contains(err.Error(), "refusing to start in production") {
		t.Fatal(err)
	}
}

func TestLoad_ProductionAcceptsAClosedSetup(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("DATABASE_URL", "postgres://helpic:a-real-password@db.internal/helpic?sslmode=verify-full")
	t.Setenv("JWT_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("TWO_FACTOR_KEY", "fedcba9876543210fedcba9876543210")
	t.Setenv("COOKIE_SECURE", "true")
	t.Setenv("COOKIE_SAME_SITE", "lax")
	t.Setenv("SWAGGER_ENABLED", "false")
	t.Setenv("RATE_LIMIT", "true")
	t.Setenv("SMTP_HOST", "smtp.example.com")
	t.Setenv("MAIL_FROM", "Helpic <no-reply@example.com>")
	t.Setenv("APP_URL", "https://helpic.example")
	t.Setenv("CORS_ORIGIN", "https://helpic.example")

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.IsProduction() || !cfg.CookieSecure || cfg.SwaggerEnabled {
		t.Fatalf("cfg = %+v", cfg)
	}
}
