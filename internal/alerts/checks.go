package alerts

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/pmaxis/helpic/internal/applog"
	"github.com/pmaxis/helpic/internal/jobs"
	"github.com/pmaxis/helpic/internal/mail"
)

// probeTimeout bounds one service's check.
const probeTimeout = 10 * time.Second

// service is a check that a service answers.
func service(title, fixed string, check func(context.Context) error) Check {
	return func(ctx context.Context, _ Translator) ([]Problem, error) {
		ctx, cancel := context.WithTimeout(ctx, probeTimeout)
		defer cancel()
		if err := check(ctx); err != nil {
			return []Problem{{Key: "down", Title: title, Fixed: fixed, Detail: err.Error()}}, nil
		}
		return nil, nil
	}
}

// Database alerts when the database doesn't answer.
func Database(ping func(context.Context) error) Check {
	return func(ctx context.Context, t Translator) ([]Problem, error) {
		return service(t("The database isn't answering"), t("The database answers again"), ping)(ctx, t)
	}
}

// MailServer alerts when Helpic can't sign in to the mail server.
func MailServer(check func(context.Context) error) Check {
	return func(ctx context.Context, t Translator) ([]Problem, error) {
		return service(t("The mail server isn't answering"), t("The mail server answers again"), check)(ctx, t)
	}
}

// Bot alerts when the users' Telegram bot can't reach Telegram.
func Bot(check func(context.Context) error) Check {
	return func(ctx context.Context, t Translator) ([]Problem, error) {
		return service(t("The Telegram bot can't reach Telegram"), t("The Telegram bot reaches Telegram again"), check)(ctx, t)
	}
}

// Scanner alerts when ClamAV doesn't answer: new uploads wait unchecked
// and don't download.
func Scanner(check func(context.Context) error) Check {
	return func(ctx context.Context, t Translator) ([]Problem, error) {
		return service(t("The virus scanner isn't answering"), t("The virus scanner answers again"), check)(ctx, t)
	}
}

// JobStreak is how many failed runs in a row make a job worth an alert.
const JobStreak = 3

// Jobs alerts when a background job failed JobStreak times in a row.
func Jobs(reg *jobs.Registry) Check {
	return func(_ context.Context, t Translator) ([]Problem, error) {
		var out []Problem
		for _, st := range reg.Statuses() {
			if st.Streak < JobStreak {
				continue
			}
			out = append(out, Problem{
				Key:    "job:" + st.Name,
				Title:  t("The “%s” job keeps failing", st.Name),
				Fixed:  t("The “%s” job works again", st.Name),
				Detail: t("%d runs in a row failed: %s", st.Streak, st.LastError),
			})
		}
		return out, nil
	}
}

// EmailTrouble is the outbox's trouble.
type EmailTrouble interface {
	Trouble(ctx context.Context, since time.Time) (mail.Trouble, error)
}

// givenUpShown is how many given-up emails one alert names.
const givenUpShown = 5

// Email alerts while emails keep failing, and once for every batch of
// emails given up on.
func Email(q EmailTrouble) Check {
	var mu sync.Mutex
	since := time.Now()
	return func(ctx context.Context, t Translator) ([]Problem, error) {
		mu.Lock()
		defer mu.Unlock()
		now := time.Now()
		tr, err := q.Trouble(ctx, since)
		if err != nil {
			return nil, err
		}
		since = now

		var out []Problem
		if tr.Retrying > 0 {
			out = append(out, Problem{
				Key:    "retrying",
				Title:  t("Emails keep failing to send: %d waiting for another try", tr.Retrying),
				Fixed:  t("Emails go out again"),
				Detail: tr.LastError,
			})
		}
		if n := len(tr.GivenUp); n > 0 {
			var lines []string
			for _, e := range tr.GivenUp[:min(n, givenUpShown)] {
				lines = append(lines, fmt.Sprintf("%s — %s: %s", e.To, e.Subject, e.LastError))
			}
			if n > givenUpShown {
				lines = append(lines, t("and %d more", n-givenUpShown))
			}
			out = append(out, Problem{
				Key:    "given-up",
				Title:  t("Emails not sent after every try: %d. Send them again under Admin → Emails", n),
				Detail: strings.Join(lines, "\n"),
				Event:  true,
			})
		}
		return out, nil
	}
}

// ErrorWindow is how far back Errors counts; ErrorBurst how many errors in
// it are worth an alert.
const (
	ErrorWindow = 10 * time.Minute
	ErrorBurst  = 20
)

// LogSummary counts the logged warnings and errors.
type LogSummary interface {
	Summary(ctx context.Context, since time.Time) (applog.Summary, error)
}

// Errors alerts while the server logs ErrorBurst errors or more in
// ErrorWindow.
func Errors(logs LogSummary) Check {
	return func(ctx context.Context, t Translator) ([]Problem, error) {
		sum, err := logs.Summary(ctx, time.Now().Add(-ErrorWindow))
		if err != nil {
			return nil, err
		}
		if sum.Errors < ErrorBurst {
			return nil, nil
		}
		return []Problem{{
			Key:   "burst",
			Title: t("%d errors in the last 10 minutes. See Admin → Logs", sum.Errors),
			Fixed: t("Errors are back to normal"),
		}}, nil
	}
}
