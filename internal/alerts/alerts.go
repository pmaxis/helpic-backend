// Package alerts tells the admins in Telegram when something goes wrong on
// the server: a service stops answering, a background job keeps failing,
// email can't go out or errors pile up. Each problem is told when it starts,
// again every hour while it lasts, and once more when it is over.
//
// The watcher runs on every server and remembers only what it saw itself.
package alerts

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/pmaxis/helpic/internal/i18n"
	"github.com/pmaxis/helpic/internal/telegram"
)

// remindEvery is how often a problem that goes on is told again.
const remindEvery = time.Hour

// sendTimeout bounds sending one alert.
const sendTimeout = 15 * time.Second

// Sender posts a message, HTML-formatted, to the admins.
type Sender interface {
	Send(ctx context.Context, html string) error
}

// Telegram posts to one chat through the alert bot.
type Telegram struct {
	bot    *telegram.Client
	chatID int64
}

// NewTelegram creates a Telegram sender for the bot with this token.
func NewTelegram(apiURL, token string, chatID int64) *Telegram {
	return &Telegram{bot: telegram.NewClient(apiURL, token), chatID: chatID}
}

// Send posts the message to the chat.
func (t *Telegram) Send(ctx context.Context, html string) error {
	return t.bot.SendMessage(ctx, t.chatID, html, nil)
}

// Check tells whether the bot's token works.
func (t *Telegram) Check(ctx context.Context) error {
	_, err := t.bot.GetMe(ctx)
	return err
}

// Problem is one thing wrong right now.
type Problem struct {
	// Key tells the same problem apart from one check to the next.
	Key string
	// Title says what is wrong; Fixed what it is like once it's over.
	Title string
	Fixed string
	// Detail is the error or the numbers, shown as code.
	Detail string
	// Event is a problem told once, with no "fixed" later: an email given
	// up on stays given up on.
	Event bool
}

// Check looks for problems. An error means it couldn't tell, which says
// nothing either way.
type Check func(ctx context.Context, t Translator) ([]Problem, error)

// Translator puts a text in the alerts' language.
type Translator func(text string, args ...any) string

// open is a problem the admins have been told about.
type open struct {
	problem Problem
	since   time.Time
	told    time.Time
}

// Watcher runs the checks and tells the admins what changed.
type Watcher struct {
	sender Sender
	locale string
	checks []Check

	mu   sync.Mutex
	open map[string]*open
	// now is time.Now, or a clock in tests.
	now func() time.Time
}

// NewWatcher creates a Watcher that sends alerts in locale.
func NewWatcher(sender Sender, locale string, checks ...Check) *Watcher {
	return &Watcher{sender: sender, locale: locale, checks: checks, open: map[string]*open{}, now: time.Now}
}

// UseClock makes the watcher read the time from now, for tests.
func (w *Watcher) UseClock(now func() time.Time) {
	w.now = now
}

func (w *Watcher) t(text string, args ...any) string {
	return i18n.T(w.locale, text, args...)
}

// Run checks once and sends what is new, what goes on past the hour and
// what is over. For jobs.Registry.Every.
func (w *Watcher) Run(ctx context.Context) error {
	now := w.now()
	seen := map[string]bool{}
	// A check that couldn't tell keeps its problems as they were.
	unsure := map[int]bool{}
	var found [][]Problem
	for i, check := range w.checks {
		problems, err := check(ctx, w.t)
		if err != nil {
			unsure[i] = true
			slog.Warn("alert check failed", "error", err)
		}
		for j := range problems {
			problems[j].Key = fmt.Sprintf("%d:%s", i, problems[j].Key)
		}
		found = append(found, problems)
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	// undo puts a change back when its message couldn't be sent, so the
	// next run tries again.
	type message struct {
		text string
		undo func()
	}
	var messages []message
	for _, problems := range found {
		for _, p := range problems {
			if p.Event {
				messages = append(messages, message{text: w.format("🔴", p.Title, p.Detail)})
				continue
			}
			seen[p.Key] = true
			o, ok := w.open[p.Key]
			switch {
			case !ok:
				w.open[p.Key] = &open{problem: p, since: now, told: now}
				key := p.Key
				messages = append(messages, message{text: w.format("🔴", p.Title, p.Detail), undo: func() { delete(w.open, key) }})
			case now.Sub(o.told) >= remindEvery:
				told := o.told
				o.problem, o.told = p, now
				messages = append(messages, message{
					text: w.format("🟠", w.t("Still: %s", p.Title), strings.TrimSpace(p.Detail+"\n"+w.t("For %s now", ago(now.Sub(o.since), w.t)))),
					undo: func() { o.told = told },
				})
			default:
				o.problem = p
			}
		}
	}
	for key, o := range w.open {
		if seen[key] || w.fromUnsure(key, unsure) {
			continue
		}
		delete(w.open, key)
		messages = append(messages, message{
			text: w.format("🟢", o.problem.Fixed, w.t("It lasted %s", ago(now.Sub(o.since), w.t))),
			undo: func() { w.open[key] = o },
		})
	}

	var errs []error
	for _, m := range messages {
		if err := w.send(ctx, m.text); err != nil {
			errs = append(errs, err)
			if m.undo != nil {
				m.undo()
			}
		}
	}
	return errors.Join(errs...)
}

// fromUnsure tells whether the problem belongs to a check that couldn't
// tell this time, whose problems stay open. Keys start with the check's
// index, as Run makes them.
func (w *Watcher) fromUnsure(key string, unsure map[int]bool) bool {
	for i := range unsure {
		if strings.HasPrefix(key, fmt.Sprintf("%d:", i)) {
			return true
		}
	}
	return false
}

// Tell sends a message at once, for what the admins should hear about as it
// happens rather than on the next check.
func (w *Watcher) Tell(ctx context.Context, title, detail string) error {
	return w.send(ctx, w.format("🔐", title, detail))
}

// Test sends a message saying who asked for it, to see alerts arrive.
func (w *Watcher) Test(ctx context.Context, by string) error {
	return w.send(ctx, w.format("✅", w.t("Alerts work"), w.t("A test alert, sent by %s.", by)))
}

func (w *Watcher) send(ctx context.Context, message string) error {
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	if err := w.sender.Send(ctx, message); err != nil {
		return fmt.Errorf("send alert: %w", err)
	}
	return nil
}

// format is the alert: a mark, the title in bold and the detail as code.
func (w *Watcher) format(mark, title, detail string) string {
	out := mark + " <b>Helpic: " + html.EscapeString(title) + "</b>"
	if detail != "" {
		out += "\n<code>" + html.EscapeString(detail) + "</code>"
	}
	return out
}

// ago is a duration a person reads: "4 min", "2 h 5 min".
func ago(d time.Duration, t Translator) string {
	m := int(d.Round(time.Minute) / time.Minute)
	if m < 60 {
		return t("%d min", max(m, 1))
	}
	if m%60 == 0 {
		return t("%d h", m/60)
	}
	return t("%d h %d min", m/60, m%60)
}
