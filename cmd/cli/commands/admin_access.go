package commands

import (
	"context"
	"errors"
	"flag"
	"fmt"

	"github.com/pmaxis/helpic/internal/admin"
	"github.com/pmaxis/helpic/internal/alerts"
	"github.com/pmaxis/helpic/internal/config"
	"github.com/pmaxis/helpic/internal/database"
)

// For an admin account in the wrong hands: the panel can't lock an admin
// out, so a stolen account can't lock out the rest; whoever has the server
// can. Each is recorded in the action log as the cli's and told to the
// admins' alert chat.

func init() {
	All = append(All,
		Command{Name: "lock-admin", Summary: "stop an admin signing in and sign them out everywhere", Run: lockAdmin},
		Command{Name: "unlock-admin", Summary: "let a locked admin sign in again", Run: unlockAdmin},
		Command{Name: "reset-admin", Summary: "give an admin a new password, turn their two-factor off and sign them out", Run: resetAdmin},
	)
}

// adminFlags reads -email and opens the admin service on the database.
func adminFlags(ctx context.Context, cfg config.Config, name string, args []string) (*admin.Service, string, func(), error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	email := fs.String("email", "", "the admin's email (required)")
	if err := fs.Parse(args); err != nil {
		return nil, "", nil, err
	}
	if *email == "" {
		fs.Usage()
		return nil, "", nil, errors.New("-email is required")
	}
	pool, err := database.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, "", nil, err
	}
	svc := admin.NewService(admin.NewRepository(pool), cfg.Location)
	if cfg.AlertBotToken != "" {
		svc.UseAlerts(alerts.NewWatcher(alerts.NewTelegram(cfg.TelegramAPIURL, cfg.AlertBotToken, cfg.AlertChatID), cfg.AlertLocale), cfg.AlertLocale)
	}
	return svc, *email, pool.Close, nil
}

func lockAdmin(ctx context.Context, cfg config.Config, args []string) error {
	svc, email, done, err := adminFlags(ctx, cfg, "lock-admin", args)
	if err != nil {
		return err
	}
	defer done()
	if err := svc.LockAdmin(ctx, email); err != nil {
		return err
	}
	fmt.Printf("Admin %s locked: they can't sign in, and every device is signed out.\nunlock-admin lets them in again; reset-admin also gives a new password and turns two-factor off.\n", email)
	return nil
}

func unlockAdmin(ctx context.Context, cfg config.Config, args []string) error {
	svc, email, done, err := adminFlags(ctx, cfg, "unlock-admin", args)
	if err != nil {
		return err
	}
	defer done()
	if err := svc.UnlockAdmin(ctx, email); err != nil {
		return err
	}
	fmt.Printf("Admin %s unlocked: they can sign in again.\n", email)
	return nil
}

func resetAdmin(ctx context.Context, cfg config.Config, args []string) error {
	svc, email, done, err := adminFlags(ctx, cfg, "reset-admin", args)
	if err != nil {
		return err
	}
	defer done()
	password, err := svc.ResetAdmin(ctx, email)
	if err != nil {
		return err
	}
	fmt.Printf("Admin %s reset: signed out everywhere, two-factor off.\nPassword: %s\nGive it to the owner in person: they sign in, change it and turn on two-factor again, which the panel needs.\n", email, password)
	return nil
}
