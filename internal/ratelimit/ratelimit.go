// Package ratelimit slows down guessing and abuse: token buckets kept in
// memory, one per key (an IP, an email, a user). Each bucket holds up to
// burst tokens and gains one every interval; a request that finds it empty is
// refused with 429 and told when to come back.
//
// Buckets live in this process, so with several instances each counts on its
// own. It is application infrastructure, not a feature.
package ratelimit

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/pmaxis/helpic/internal/response"
)

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
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.refill(key)
	if b.tokens < 1 {
		return false, l.wait(b)
	}
	b.tokens--
	return true, 0
}

// Check reports whether key has a token, without taking it, and if not how
// long until it has. With Take it counts only some requests, e.g. failed
// sign-ins.
func (l *Limiter) Check(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.refill(key)
	return b.tokens >= 1, l.wait(b)
}

// Take uses up a token for key, if it has one.
func (l *Limiter) Take(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.refill(key)
	b.tokens = math.Max(0, b.tokens-1)
}

// Sweep forgets the buckets that have filled up again: a new one would be the
// same. Run it now and then so the map doesn't grow without end.
func (l *Limiter) Sweep() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for key := range l.buckets {
		if l.refill(key).tokens >= l.burst {
			delete(l.buckets, key)
		}
	}
}

// Middleware refuses a request once its key's bucket is empty. key picks the
// bucket; an empty key is not limited.
func Middleware(l *Limiter, key func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if k := key(r); k != "" {
				if ok, retry := l.Allow(k); !ok {
					TooMany(w, retry)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// TooMany answers 429, saying when to try again.
func TooMany(w http.ResponseWriter, retry time.Duration) {
	seconds := int(math.Ceil(retry.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	response.Error(w, http.StatusTooManyRequests, response.CodeRateLimited, "Too many attempts. Try again in "+humanize(seconds)+".")
}

func humanize(seconds int) string {
	if seconds < 60 {
		return plural(seconds, "second")
	}
	return plural(int(math.Ceil(float64(seconds)/60)), "minute")
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}
