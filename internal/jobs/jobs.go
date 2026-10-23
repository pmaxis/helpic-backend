// Package jobs runs background work on a schedule beside the HTTP server. It
// is application infrastructure, not a feature.
package jobs

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Status is how a job's runs on this server went so far.
type Status struct {
	Name     string
	Interval time.Duration
	// LastRun is when the last run began; zero before the first.
	LastRun time.Time
	// LastSuccess is when a run last ended without an error.
	LastSuccess time.Time
	// LastError is the last run's error; empty when it went well.
	LastError string
	Running   bool
	Runs      int
	Failures  int
}

// Registry runs jobs and remembers how each went, for the admin panel. It
// knows only this server's jobs, since the server started.
type Registry struct {
	mu   sync.Mutex
	jobs []*Status
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{}
}

// Every runs fn at once and then every interval until ctx ends. An error is
// logged and the next run goes ahead as usual.
func (r *Registry) Every(ctx context.Context, name string, interval time.Duration, fn func(context.Context) error) {
	st := &Status{Name: name, Interval: interval}
	r.mu.Lock()
	r.jobs = append(r.jobs, st)
	r.mu.Unlock()

	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		r.mu.Lock()
		st.Running, st.LastRun = true, time.Now()
		r.mu.Unlock()

		err := fn(ctx)

		r.mu.Lock()
		st.Running = false
		st.Runs++
		if err != nil && ctx.Err() == nil {
			st.Failures++
			st.LastError = err.Error()
		} else if err == nil {
			st.LastError = ""
			st.LastSuccess = time.Now()
		}
		r.mu.Unlock()
		if err != nil && ctx.Err() == nil {
			slog.Error("background job failed", "job", name, "error", err)
		}

		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Statuses returns every job's status, in the order they started.
func (r *Registry) Statuses() []Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Status, 0, len(r.jobs))
	for _, st := range r.jobs {
		out = append(out, *st)
	}
	return out
}
