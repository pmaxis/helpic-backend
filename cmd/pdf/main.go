// Command pdf is the service that prints report PDFs with headless Chrome,
// so the browser never runs next to the API. It is internal: the API calls
// POST /render with an HTML document and gets the PDF back.
//
// Environment: PDF_PORT (default 8081), CHROME_PATH (empty auto-detects).
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

	"github.com/pmaxis/helpic/internal/pdf"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))
	if err := run(); err != nil {
		slog.Error("pdf service stopped with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	chrome := pdf.NewChrome(os.Getenv("CHROME_PATH"))
	if !chrome.Available() {
		return errors.New("chrome not found: install Chrome/Chromium or set CHROME_PATH")
	}
	port := os.Getenv("PDF_PORT")
	if port == "" {
		port = "8081"
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           pdf.NewHandler(chrome),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("pdf service listening", "port", port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}
