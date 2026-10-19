package server

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/pmaxis/helpic/internal/middleware"
	"github.com/pmaxis/helpic/internal/ratelimit"
)

// limits are the API's rate limits. Their numbers allow for an office behind
// one address; with rate limiting off they are all nil.
type limits struct {
	// signIn: sign-in, sign-up and invitation attempts per address.
	signIn ratelimit.Gate
	// signUp: accounts made per address.
	signUp ratelimit.Gate
	// refresh: session refreshes per address; a tab refreshes every 15 minutes.
	refresh ratelimit.Gate
	// loginFailures: wrong passwords per email, from anywhere.
	loginFailures ratelimit.Gate
	// passwordChecks: changes that check the current password, per user.
	passwordChecks ratelimit.Gate
	// resetRequests: password reset emails per address asked for.
	resetRequests ratelimit.Gate
}

func newLimits(pool *pgxpool.Pool, on bool) limits {
	if !on {
		return limits{}
	}
	return limits{
		signIn:         ratelimit.NewStore(pool, "sign-in", 20, 30*time.Second),
		signUp:         ratelimit.NewStore(pool, "sign-up", 5, 10*time.Minute),
		refresh:        ratelimit.NewStore(pool, "refresh", 120, 5*time.Second),
		loginFailures:  ratelimit.NewStore(pool, "login-failures", 10, 3*time.Minute),
		passwordChecks: ratelimit.NewStore(pool, "password-checks", 5, time.Minute),
		resetRequests:  ratelimit.NewStore(pool, "reset", 3, 10*time.Minute),
	}
}

// sweep forgets full buckets, so the maps don't grow without end.
func (l limits) sweep(ctx context.Context) error {
	for _, lim := range []ratelimit.Gate{l.signIn, l.signUp, l.refresh, l.loginFailures, l.passwordChecks, l.resetRequests} {
		if lim != nil {
			if err := lim.Sweep(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

// byIP limits requests per client address; a nil limiter lets all through.
func byIP(l ratelimit.Gate) func(http.Handler) http.Handler {
	return limit(l, middleware.ClientIP)
}

// byUser limits requests per signed-in user; must run after Auth.
func byUser(l ratelimit.Gate) func(http.Handler) http.Handler {
	return limit(l, func(r *http.Request) string {
		id, _ := middleware.UserID(r.Context())
		return id
	})
}

func limit(l ratelimit.Gate, key func(*http.Request) string) func(http.Handler) http.Handler {
	if l == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	return ratelimit.Middleware(l, key)
}
