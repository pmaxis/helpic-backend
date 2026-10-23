package alerts_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/pmaxis/helpic/internal/alerts"
)

type fakeSender struct {
	sent []string
	fail bool
}

func (f *fakeSender) Send(_ context.Context, html string) error {
	if f.fail {
		return errors.New("telegram is down")
	}
	f.sent = append(f.sent, html)
	return nil
}

// take returns what was sent since the last take.
func (f *fakeSender) take() []string {
	out := f.sent
	f.sent = nil
	return out
}

// world is what the checks see: whether the database is down, a check
// can't tell, and an event to report.
type world struct {
	down, unsure, event bool
}

func (w *world) check(_ context.Context, t alerts.Translator) ([]alerts.Problem, error) {
	if w.unsure {
		return nil, errors.New("can't tell")
	}
	var out []alerts.Problem
	if w.down {
		out = append(out, alerts.Problem{Key: "db", Title: "DB down", Fixed: "DB up", Detail: "connection refused"})
	}
	if w.event {
		out = append(out, alerts.Problem{Key: "gave-up", Title: "Gave up on an email", Event: true})
	}
	return out, nil
}

func run(t *testing.T, w *alerts.Watcher) {
	t.Helper()
	if err := w.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
}

func want(t *testing.T, got []string, parts ...string) {
	t.Helper()
	if len(got) != len(parts) {
		t.Fatalf("sent %d messages, want %d: %q", len(got), len(parts), got)
	}
	for i, p := range parts {
		if !strings.Contains(got[i], p) {
			t.Fatalf("message %d = %q, want it to contain %q", i, got[i], p)
		}
	}
}

func TestWatcher_TellsStartRemindersAndEnd(t *testing.T) {
	sender := &fakeSender{}
	state := &world{}
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	w := alerts.NewWatcher(sender, "en", state.check)
	w.UseClock(func() time.Time { return now })

	run(t, w)
	want(t, sender.take())

	state.down = true
	run(t, w)
	want(t, sender.take(), "🔴 <b>Helpic: DB down</b>\n<code>connection refused</code>")

	// Still down, within the hour: quiet.
	now = now.Add(30 * time.Minute)
	run(t, w)
	want(t, sender.take())

	now = now.Add(31 * time.Minute)
	run(t, w)
	want(t, sender.take(), "Still: DB down")

	// A check that can't tell neither ends a problem nor starts one.
	state.unsure = true
	run(t, w)
	want(t, sender.take())

	state.unsure, state.down = false, false
	now = now.Add(4 * time.Minute)
	run(t, w)
	want(t, sender.take(), "🟢 <b>Helpic: DB up</b>\n<code>It lasted 1 h 5 min</code>")
}

func TestWatcher_EventsAreToldEachTime(t *testing.T) {
	sender := &fakeSender{}
	state := &world{event: true}
	w := alerts.NewWatcher(sender, "en", state.check)

	run(t, w)
	run(t, w)
	want(t, sender.take(), "Gave up on an email", "Gave up on an email")
}

func TestWatcher_TriesAgainWhenSendingFails(t *testing.T) {
	sender := &fakeSender{fail: true}
	state := &world{down: true}
	w := alerts.NewWatcher(sender, "uk", state.check)

	if err := w.Run(context.Background()); err == nil {
		t.Fatal("no error when the alert couldn't be sent")
	}
	sender.fail = false
	run(t, w)
	want(t, sender.take(), "DB down")
}

func TestWatcher_Escapes(t *testing.T) {
	sender := &fakeSender{}
	w := alerts.NewWatcher(sender, "en", func(context.Context, alerts.Translator) ([]alerts.Problem, error) {
		return []alerts.Problem{{Key: "x", Title: "a <b> & c", Detail: "<script>"}}, nil
	})
	run(t, w)
	want(t, sender.take(), "a &lt;b&gt; &amp; c</b>\n<code>&lt;script&gt;")
}
