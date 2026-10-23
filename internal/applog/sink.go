package applog

import (
	"context"
	"log/slog"
	"time"
)

// Sink queues entries and writes them in batches. A full queue drops new
// entries rather than slow the request that logged them.
type Sink struct {
	repo  *Repository
	queue chan Entry
	// log reports the sink's own failures, past the Handler so they don't
	// loop back into the queue.
	log *slog.Logger
}

// queueSize and batchSize bound the memory and each write.
const (
	queueSize = 1024
	batchSize = 100
	flushEach = time.Second
)

// NewSink creates a Sink writing to repo; log reports its failures.
func NewSink(repo *Repository, log *slog.Logger) *Sink {
	return &Sink{repo: repo, queue: make(chan Entry, queueSize), log: log}
}

func (s *Sink) add(e Entry) {
	select {
	case s.queue <- e:
	default:
	}
}

// Run writes the queue until ctx ends, then what is left of it.
func (s *Sink) Run(ctx context.Context) {
	t := time.NewTicker(flushEach)
	defer t.Stop()
	batch := make([]Entry, 0, batchSize)
	flush := func(ctx context.Context) {
		if len(batch) == 0 {
			return
		}
		if err := s.repo.Insert(ctx, batch); err != nil {
			s.log.Warn("app logs not stored", "entries", len(batch), "error", err)
		}
		batch = batch[:0]
	}
	for {
		select {
		case e := <-s.queue:
			batch = append(batch, e)
			if len(batch) >= batchSize {
				flush(ctx)
			}
		case <-t.C:
			flush(ctx)
		case <-ctx.Done():
			for len(s.queue) > 0 {
				batch = append(batch, <-s.queue)
			}
			done, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			flush(done)
			cancel()
			return
		}
	}
}
