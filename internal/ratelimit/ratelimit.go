// Package ratelimit slows down guessing and abuse: token buckets, one per key
// (an IP, an email, a user). Each bucket holds up to burst tokens and gains
// one every interval; a request that finds it empty is refused with 429 and
// told when to come back.
//
// Limiter keeps its buckets in this process, for tests. Store keeps them in
// Postgres, so every copy of the API shares one budget. It is application
// infrastructure, not a feature.
package ratelimit

import (
	"context"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/pmaxis/helpic/internal/i18n"
	"github.com/pmaxis/helpic/internal/response"
)

// Gate is a set of token buckets. Allow and Check report whether a token is
// there; Take uses one up. An error means the buckets could not be read.
type Gate interface {
	Allow(ctx context.Context, key string) (bool, time.Duration, error)
	Check(ctx context.Context, key string) (bool, time.Duration, error)
	Take(ctx context.Context, key string) error
	Sweep(ctx context.Context) error
}

// Limiter is a set of token buckets, one per key.
type Limiter struct {
	burst float64
	every time.Duration
	// now is time.Now, replaceable in tests.
	now func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	at     time.Time
}

// New creates a Limiter whose buckets hold burst tokens and gain one every
// interval.
func New(burst int, every time.Duration) *Limiter {
	return &Limiter{burst: float64(burst), every: every, now: time.Now, buckets: map[string]*bucket{}}
}

// WithClock makes the Limiter read the time from now; for tests.
func (l *Limiter) WithClock(now func() time.Time) *Limiter {
	l.now = now
	return l
}

// refill returns the key's bucket, topped up for the time since it was last
// seen. The caller holds mu.
func (l *Limiter) refill(key string) *bucket {
	now := l.now()
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, at: now}
		l.buckets[key] = b
		return b
	}
	b.tokens = math.Min(l.burst, b.tokens+float64(now.Sub(b.at))/float64(l.every))
	b.at = now
	return b
}

// wait is how long until the bucket has a token. The caller holds mu.
func (l *Limiter) wait(b *bucket) time.Duration {
	if b.tokens >= 1 {
		return 0
	}
	return time.Duration((1 - b.tokens) * float64(l.every))
}

// Allow takes a token for key. When there is none it takes nothing and
// returns how long until there is.
func (l *Limiter) Allow(_ context.Context, key string) (bool, time.Duration, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.refill(key)
	if b.tokens < 1 {
		return false, l.wait(b), nil
	}
	b.tokens--
	return true, 0, nil
}

// Check reports whether key has a token, without taking it, and if not how
// long until it has. With Take it counts only some requests, e.g. failed
// sign-ins.
func (l *Limiter) Check(_ context.Context, key string) (bool, time.Duration, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.refill(key)
	return b.tokens >= 1, l.wait(b), nil
}

// Take uses up a token for key, if it has one.
func (l *Limiter) Take(_ context.Context, key string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.refill(key)
	b.tokens = math.Max(0, b.tokens-1)
	return nil
}

// Sweep forgets the buckets that have filled up again: a new one would be the
// same. Run it now and then so the map doesn't grow without end.
func (l *Limiter) Sweep(context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	for key := range l.buckets {
		if l.refill(key).tokens >= l.burst {
			delete(l.buckets, key)
		}
	}
	return nil
}

// Middleware refuses a request once its key's bucket is empty. key picks the
// bucket; an empty key is not limited.
func Middleware(l Gate, key func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if k := key(r); k != "" {
				ok, retry, err := l.Allow(r.Context(), k)
				if err != nil {
					response.Internal(w, err)
					return
				}
				if !ok {
					TooMany(w, retry)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// TooMany answers 429, saying when to try again, in the response's language.
func TooMany(w http.ResponseWriter, retry time.Duration) {
	seconds := int(math.Ceil(retry.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	locale := i18n.Default
	if l, ok := w.(interface{ Locale() string }); ok {
		locale = l.Locale()
	}
	response.Error(w, http.StatusTooManyRequests, response.CodeRateLimited,
		i18n.T(locale, "Too many attempts. Try again in %s.", i18n.Duration(locale, time.Duration(seconds)*time.Second)))
}
