package commands

import (
	"context"
	"errors"
	"flag"
	"fmt"

	"github.com/pmaxis/helpic/internal/admin"
	"github.com/pmaxis/helpic/internal/config"
	"github.com/pmaxis/helpic/internal/database"
	"github.com/pmaxis/helpic/internal/i18n"
)

func init() {
	All = append(All, Command{
		Name:    "create-admin",
		Summary: "make an admin account and print its password",
		Run:     createAdmin,
	})
}

// createAdmin makes an account with the admin role. The password is printed
// once; the admin changes it and turns on two-factor in the settings, which
// the panel needs.
func createAdmin(ctx context.Context, cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("create-admin", flag.ContinueOnError)
	email := fs.String("email", "", "the admin's email (required)")
	name := fs.String("name", "", "the admin's name")
	locale := fs.String("locale", i18n.Default, "the interface's language: en or uk")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *email == "" {
		fs.Usage()
		return errors.New("-email is required")
	}
	if !i18n.Valid(*locale) {
		return fmt.Errorf("-locale must be en or uk, not %q", *locale)
	}

	pool, err := database.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	password, err := admin.NewService(admin.NewRepository(pool), cfg.Location).CreateAdmin(ctx, *email, *name, *locale)
	if err != nil {
		return err
	}
	fmt.Printf("Admin %s created.\nPassword: %s\nSign in, change the password and turn on two-factor in the settings: the admin panel needs it.\n", *email, password)
	return nil
}
