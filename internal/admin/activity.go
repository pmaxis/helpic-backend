package admin

import (
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/pmaxis/helpic/internal/middleware"
	"github.com/pmaxis/helpic/internal/user"
)

// Activity records the days people use Helpic: the first signed-in request
// of a user's day writes a row, the rest of the day only checks a set in
// memory.
type Activity struct {
	repo *Repository
	svc  *Service

	mu   sync.Mutex
	day  time.Time
	seen map[string]bool
}

// NewActivity creates the activity recorder.
func NewActivity(repo *Repository, svc *Service) *Activity {
	return &Activity{repo: repo, svc: svc, seen: map[string]bool{}}
}

// Track is middleware that marks the signed-in user active today; admins
// aren't counted. It never fails the request: activity is for statistics
// only. Must run after Auth.
func (a *Activity) Track(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if userID, ok := middleware.UserID(r.Context()); ok && middleware.Role(r.Context()) != user.RoleAdmin {
			a.mark(r, userID)
		}
		next.ServeHTTP(w, r)
	})
}

func (a *Activity) mark(r *http.Request, userID string) {
	today := a.svc.Today()
	a.mu.Lock()
	if !a.day.Equal(today) {
		a.day, a.seen = today, map[string]bool{}
	}
	seen := a.seen[userID]
	a.mu.Unlock()
	if seen {
		return
	}
	if err := a.repo.MarkActive(r.Context(), userID, today); err != nil {
		slog.Warn("activity not recorded", "user_id", userID, "error", err)
		return
	}
	a.mu.Lock()
	if a.day.Equal(today) {
		a.seen[userID] = true
	}
	a.mu.Unlock()
}
