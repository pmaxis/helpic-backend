// Package jobs runs background work on a schedule beside the HTTP server. It
// is application infrastructure, not a feature.
package jobs

import (
	"context"
	"log/slog"
	"time"
)

// Every runs fn at once and then every interval until ctx ends. An error is
// logged and the next run goes ahead as usual.
func Every(ctx context.Context, name string, interval time.Duration, fn func(context.Context) error) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if err := fn(ctx); err != nil && ctx.Err() == nil {
			slog.Error("background job failed", "job", name, "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
