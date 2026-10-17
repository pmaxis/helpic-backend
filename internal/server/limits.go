package server

import (
	"context"
	"net/http"
	"time"

	"github.com/pmaxis/helpic/internal/middleware"
	"github.com/pmaxis/helpic/internal/ratelimit"
)

// limits are the API's rate limits. Their numbers allow for an office behind
// one address; with rate limiting off they are all nil.
type limits struct {
	// signIn: sign-in, sign-up and invitation attempts per address.
	signIn *ratelimit.Limiter
	// signUp: accounts made per address.
	signUp *ratelimit.Limiter
	// refresh: session refreshes per address; a tab refreshes every 15 minutes.
	refresh *ratelimit.Limiter
	// loginFailures: wrong passwords per email, from anywhere.
	loginFailures *ratelimit.Limiter
	// passwordChecks: changes that check the current password, per user.
	passwordChecks *ratelimit.Limiter
}

func newLimits(on bool) limits {
	if !on {
		return limits{}
	}
	return limits{
		signIn:         ratelimit.New(20, 30*time.Second),
		signUp:         ratelimit.New(5, 10*time.Minute),
		refresh:        ratelimit.New(120, 5*time.Second),
		loginFailures:  ratelimit.New(10, 3*time.Minute),
		passwordChecks: ratelimit.New(5, time.Minute),
	}
}

// sweepEvery is how often full buckets are forgotten.
const sweepEvery = time.Minute

// sweep forgets full buckets until ctx ends.
func (l limits) sweep(ctx context.Context) {
	all := []*ratelimit.Limiter{l.signIn, l.signUp, l.refresh, l.loginFailures, l.passwordChecks}
	t := time.NewTicker(sweepEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			for _, lim := range all {
				if lim != nil {
					lim.Sweep()
				}
			}
		}
	}
}

// byIP limits requests per client address; a nil limiter lets all through.
func byIP(l *ratelimit.Limiter) func(http.Handler) http.Handler {
	return limit(l, middleware.ClientIP)
}

// byUser limits requests per signed-in user; must run after Auth.
func byUser(l *ratelimit.Limiter) func(http.Handler) http.Handler {
	return limit(l, func(r *http.Request) string {
		id, _ := middleware.UserID(r.Context())
		return id
	})
}

func limit(l *ratelimit.Limiter, key func(*http.Request) string) func(http.Handler) http.Handler {
	if l == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	return ratelimit.Middleware(l, key)
}
