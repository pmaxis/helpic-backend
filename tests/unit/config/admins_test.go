package config_test

import (
	"slices"
	"testing"

	"github.com/pmaxis/helpic/internal/config"
)

func devEnv(t *testing.T) {
	t.Helper()
	t.Setenv("APP_ENV", "development")
	t.Setenv("DATABASE_URL", "postgres://postgres:postgres@localhost/helpic?sslmode=disable")
	t.Setenv("JWT_SECRET", "change-me-in-production")
}

func TestLoad_AdminEmails(t *testing.T) {
	devEnv(t)
	t.Setenv("ADMIN_EMAILS", " Boss@Example.com, ,ops@example.com ")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cfg.AdminEmails, []string{"boss@example.com", "ops@example.com"}) {
		t.Fatalf("admins = %q", cfg.AdminEmails)
	}
}

func TestLoad_NoAdminsByDefault(t *testing.T) {
	devEnv(t)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.AdminEmails) != 0 {
		t.Fatalf("admins = %q", cfg.AdminEmails)
	}
}

func TestLoad_AdminEmailsRefusesNonsense(t *testing.T) {
	for _, list := range []string{"boss", "@example.com", "boss@", "boss@example.com;ops@example.com"} {
		devEnv(t)
		t.Setenv("ADMIN_EMAILS", list)
		if _, err := config.Load(); err == nil {
			t.Errorf("ADMIN_EMAILS=%q accepted", list)
		}
	}
}
