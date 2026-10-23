package admin

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Service makes admins and gathers the statistics.
type Service struct {
	repo *Repository
	loc  *time.Location
	mail Mailer
	// emails and signupLinks are the outbox and sign-ups, for the panel.
	emails      EmailQueue
	signupLinks SignupLinks
	// alerts tell the admins' chat, in alertLocale; nil when alerts are off.
	alerts      Notifier
	alertLocale string
}

// NewService creates an admin Service. loc is the app's time zone, whose days
// the statistics count.
func NewService(repo *Repository, loc *time.Location) *Service {
	if loc == nil {
		loc = time.UTC
	}
	return &Service{repo: repo, loc: loc}
}

// TwoFactor tells whether the user signs in with two-factor, which the panel
// requires.
func (s *Service) TwoFactor(ctx context.Context, userID string) (bool, error) {
	return s.repo.TwoFactor(ctx, userID)
}

// CreateAdmin makes an admin account and returns the password it was given,
// to be changed in the settings on the first sign-in. ErrInvalidEmail or
// ErrEmailExists when the email won't do.
func (s *Service) CreateAdmin(ctx context.Context, email, name, locale string) (password string, err error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if at := strings.Index(email, "@"); at <= 0 || at == len(email)-1 || strings.Count(email, "@") > 1 || strings.ContainsAny(email, " \t;,") {
		return "", ErrInvalidEmail
	}
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate password: %w", err)
	}
	password = base64.RawURLEncoding.EncodeToString(b)
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	if err := s.repo.CreateAdmin(ctx, email, strings.TrimSpace(name), string(hash), locale); err != nil {
		return "", err
	}
	return password, nil
}

// Today is the current day in the app's time zone, at midnight UTC so it
// compares as a date.
func (s *Service) Today() time.Time {
	y, m, d := time.Now().In(s.loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// start is the moment day began in the app's time zone.
func (s *Service) start(day time.Time) time.Time {
	return time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, s.loc)
}

// The periods the statistics may cover, in days.
const (
	MinPeriod = 7
	MaxPeriod = 365
)

// Stats counts the last days days, today included, against the days days
// before them.
func (s *Service) Stats(ctx context.Context, days int) (Stats, error) {
	to := s.Today()
	from := to.AddDate(0, 0, -(days - 1))
	prevFrom := from.AddDate(0, 0, -days)
	prevTo := from.AddDate(0, 0, -1)

	st := Stats{From: from, To: to}
	var err error
	if st.Totals, err = s.repo.Totals(ctx, to); err != nil {
		return Stats{}, err
	}
	if st.Period, err = s.repo.Period(ctx, from, to.AddDate(0, 0, 1), s.start(from), s.start(to.AddDate(0, 0, 1))); err != nil {
		return Stats{}, err
	}
	if st.Previous, err = s.repo.Period(ctx, prevFrom, from, s.start(prevFrom), s.start(from)); err != nil {
		return Stats{}, err
	}
	if st.Days, err = s.repo.Days(ctx, from, to, s.start(from), s.loc.String()); err != nil {
		return Stats{}, err
	}
	if st.PreviousDays, err = s.repo.Days(ctx, prevFrom, prevTo, s.start(prevFrom), s.loc.String()); err != nil {
		return Stats{}, err
	}
	return st, nil
}

// Accounts returns a page of accounts and how many match in all.
func (s *Service) Accounts(ctx context.Context, f AccountFilter) ([]Account, int, error) {
	return s.repo.Accounts(ctx, f, s.Today())
}

// ActivityWeeks is how far back an account's activity goes, in whole weeks
// ending with the current one.
const ActivityWeeks = 26

// AccountDetail returns one account in full, or ErrNotFound.
func (s *Service) AccountDetail(ctx context.Context, id string) (AccountDetail, error) {
	today := s.Today()
	monday := today.AddDate(0, 0, -((int(today.Weekday()) + 6) % 7))
	return s.repo.AccountDetail(ctx, id, today, monday.AddDate(0, 0, -7*(ActivityWeeks-1)))
}
