// Command server is the entrypoint for the Helpic API.
//
// Usage:
//
//	server              start the HTTP server (runs pending migrations first)
//	server migrate up    apply all pending migrations and exit
//	server migrate down   roll back all migrations and exit
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/pmaxis/helpic/internal/config"
	"github.com/pmaxis/helpic/internal/database"
	"github.com/pmaxis/helpic/internal/server"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}

	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		runMigrate(cfg, os.Args[2:])
		return
	}

	if err := run(cfg); err != nil {
		slog.Error("server stopped with error", "error", err)
		os.Exit(1)
	}
}

func runMigrate(cfg config.Config, args []string) {
	if len(args) < 1 {
		slog.Error("usage: server migrate [up|down]")
		os.Exit(1)
	}
	dir := database.Direction(args[0])
	if err := database.Migrate(cfg.DatabaseURL, cfg.MigrationsDir, dir); err != nil {
		slog.Error("migration failed", "direction", args[0], "error", err)
		os.Exit(1)
	}
	slog.Info("migrations complete", "direction", args[0])
}

func run(cfg config.Config) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := database.Migrate(cfg.DatabaseURL, cfg.MigrationsDir, database.Up); err != nil {
		return err
	}

	pool, err := database.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	app, err := server.New(cfg, pool)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              ":" + cfg.AppPort,
		Handler:           app.Handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("server listening", "port", cfg.AppPort, "env", cfg.AppEnv)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		slog.Info("shutdown signal received")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}
