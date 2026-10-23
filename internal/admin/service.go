package admin

import (
	"context"
	"slices"
	"strings"
	"time"
)

// Service decides who is an admin and gathers the statistics.
type Service struct {
	repo   *Repository
	admins []string
	loc    *time.Location
}

// NewService creates an admin Service. admins are the lowercased emails
// that may open the panel; loc is the app's time zone, whose days the
// statistics count.
func NewService(repo *Repository, admins []string, loc *time.Location) *Service {
	if loc == nil {
		loc = time.UTC
	}
	return &Service{repo: repo, admins: admins, loc: loc}
}

// Access tells whether the user is an admin, and whether they sign in with
// two-factor, which the panel requires.
func (s *Service) Access(ctx context.Context, userID string) (admin, twoFactor bool, err error) {
	if len(s.admins) == 0 {
		return false, false, nil
	}
	email, twoFactor, found, err := s.repo.Account(ctx, userID)
	if err != nil || !found {
		return false, false, err
	}
	return slices.Contains(s.admins, strings.ToLower(email)), twoFactor, nil
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
