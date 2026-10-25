package auth

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"golang.org/x/crypto/bcrypt"

	"github.com/pmaxis/helpic/internal/user"
)

// PasswordCost is bcrypt's work factor for new hashes: about a fifth of a
// second on a current server core, 4× the default. Hashes made with less are
// redone at the next sign-in. Tests may lower it to run faster.
var PasswordCost = 12

// HashPassword hashes a password for keeping.
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), PasswordCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(hash), nil
}

// outdated reports whether hash was made with less work than new ones are.
func outdated(hash string) bool {
	cost, err := bcrypt.Cost([]byte(hash))
	return err == nil && cost < PasswordCost
}

// dummies are hashes of no one's password, by cost.
var (
	dummiesMu sync.Mutex
	dummies   = map[int][]byte{}
)

// dummyAt returns a hash of no one's password made with cost.
func dummyAt(cost int) []byte {
	dummiesMu.Lock()
	defer dummiesMu.Unlock()
	if h, ok := dummies[cost]; ok {
		return h
	}
	h, _ := bcrypt.GenerateFromPassword([]byte("dummy-password-for-timing"), cost)
	dummies[cost] = h
	return h
}

// spendCheck takes as long as checking a password against a current hash,
// for a sign-in with an unknown email: the refusal then doesn't tell it
// from a wrong password.
func spendCheck(password string) {
	_ = bcrypt.CompareHashAndPassword(dummyAt(PasswordCost), []byte(password))
}

// padCheck tops up a check of a wrong password against an outdated hash to
// the time a current one takes. Cost c is 2^c rounds, and
// 2^c + (2^c + 2^(c+1) + … + 2^(PasswordCost-1)) = 2^PasswordCost, so an
// account not signed in since the cost went up looks like any other.
func padCheck(hash, password string) {
	cost, err := bcrypt.Cost([]byte(hash))
	if err != nil {
		return
	}
	for c := cost; c < PasswordCost; c++ {
		_ = bcrypt.CompareHashAndPassword(dummyAt(c), []byte(password))
	}
}

// upgradeHash redoes an outdated hash of u's password, now that the
// password is known to be right. A failure leaves the old hash, which still
// works.
func (s *Service) upgradeHash(ctx context.Context, u user.User, password string) {
	if !outdated(u.PasswordHash) {
		return
	}
	hash, err := HashPassword(password)
	if err == nil {
		err = s.repo.UpgradePasswordHash(ctx, u.ID, u.PasswordHash, hash)
	}
	if err != nil {
		slog.Warn("failed to upgrade a password hash", "user_id", u.ID, "error", err)
	}
}

// BreachChecker tells whether a password is known from a data breach.
// Implemented by *pwned.Client.
type BreachChecker interface {
	Breached(ctx context.Context, password string) (bool, error)
}

// BreachedPassword answers why a new password is refused when it is known
// from a breach, where attackers look first, or "". Ask it once the request
// passed every other check. A nil checker asks nothing, and one that can't
// be reached lets the password through.
func BreachedPassword(ctx context.Context, breaches BreachChecker, password string) string {
	if breaches == nil {
		return ""
	}
	breached, err := breaches.Breached(ctx, password)
	if err != nil {
		slog.Warn("breached password check failed; password allowed", "error", err)
		return ""
	}
	if breached {
		return "This password has appeared in a data breach. Choose another one."
	}
	return ""
}
