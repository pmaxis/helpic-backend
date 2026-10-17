package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	notifs "github.com/pmaxis/helpic/internal/notifications"
	"github.com/pmaxis/helpic/internal/reminders"
)

// remindersAt is the reminders service with its clock stopped at now.
func remindersAt(now *time.Time) *reminders.Service {
	notifier := notifs.NewService(notifs.NewRepository(testPool))
	return reminders.NewService(reminders.NewRepository(testPool), notifier, time.UTC, 9).WithClock(func() time.Time { return *now })
}

// reminded lists the user's reminders as "kind subject detail", sorted.
func reminded(t *testing.T, srv *httptest.Server, c *http.Client) []string {
	t.Helper()
	list, _ := notifications(t, srv, c)
	var out []string
	for _, n := range list {
		if n.Kind == "task_due_soon" || n.Kind == "task_overdue" {
			if n.Actor != "" {
				t.Fatalf("a reminder has an actor: %+v", n)
			}
			out = append(out, n.Kind+" "+n.Subject+" "+n.Detail)
		}
	}
	sort.Strings(out)
	return out
}

func wantReminded(t *testing.T, srv *httptest.Server, c *http.Client, want ...string) {
	t.Helper()
	if got := reminded(t, srv, c); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("reminders = %q, want %q", got, want)
	}
}

func TestReminders(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	member := joinTeam(t, srv, client, "member@example.com")
	freelancerID := myID(t, srv, freelancer)

	// Today is 10 March 2030 in the app's time zone.
	task := func(c *http.Client, title, due string, forFreelancer bool) map[string]any {
		body := map[string]any{"title": title, "due_on": due}
		if forFreelancer {
			body["freelancer_id"] = freelancerID
		}
		return newTask(t, srv, c, body)
	}
	task(client, "Logo", "2030-03-11", true)       // due tomorrow
	task(freelancer, "Taxes", "2030-03-11", false) // personal, due tomorrow
	task(member, "Banner", "2030-03-09", true)     // overdue since yesterday, set by a member
	done := task(client, "Done already", "2030-03-11", true)
	later := task(client, "Later", "2030-03-12", true)
	task(client, "Long gone", "2030-03-01", true) // overdue for days: not reminded again
	if _, err := testPool.Exec(context.Background(), `UPDATE tasks SET status = 'done' WHERE id = $1`, done["id"]); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2030, 3, 10, 8, 30, 0, 0, time.UTC)
	svc := remindersAt(&now)

	// Before the reminder hour: nothing.
	if err := svc.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantReminded(t, srv, freelancer)

	now = now.Add(time.Hour)
	if err := svc.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The freelancer hears about both deadlines; both sides about the late task.
	wantReminded(t, srv, freelancer,
		"task_due_soon Logo 2030-03-11", "task_due_soon Taxes 2030-03-11", "task_overdue Banner 2030-03-09")
	wantReminded(t, srv, client, "task_overdue Banner 2030-03-09")
	wantReminded(t, srv, member, "task_overdue Banner 2030-03-09")

	// Each reminder goes once.
	if err := svc.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := reminded(t, srv, freelancer); len(got) != 3 {
		t.Fatalf("after another run = %q", got)
	}

	// A new due date is a new reminder.
	resp := do(t, client, http.MethodPut, srv.URL+"/api/v1/tasks/"+later["id"].(string), map[string]any{"title": "Later", "due_on": "2030-03-11"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	if err := svc.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantReminded(t, srv, freelancer,
		"task_due_soon Later 2030-03-11", "task_due_soon Logo 2030-03-11", "task_due_soon Taxes 2030-03-11", "task_overdue Banner 2030-03-09")

	// Old sent reminders are cleaned up; it doesn't bring them back.
	now = now.Add(8 * 24 * time.Hour)
	if err := svc.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM task_reminders`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("reminders left after cleanup = %d, %v", left, err)
	}
}

func TestReminders_TimeZone(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "kyiv@example.com")
	newTask(t, srv, freelancer, map[string]any{"title": "Call", "due_on": "2030-03-11"})

	// 22:30 UTC on the 9th is already 00:30 on the 10th in Kyiv: the task is
	// due tomorrow there, and it's too early to say so. At 07:00 UTC it's 09:00
	// in Kyiv.
	kyiv, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2030, 3, 9, 22, 30, 0, 0, time.UTC)
	notifier := notifs.NewService(notifs.NewRepository(testPool))
	svc := reminders.NewService(reminders.NewRepository(testPool), notifier, kyiv, 9).WithClock(func() time.Time { return now })

	if err := svc.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantReminded(t, srv, freelancer)
	now = time.Date(2030, 3, 10, 7, 0, 0, 0, time.UTC)
	if err := svc.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantReminded(t, srv, freelancer, "task_due_soon Call 2030-03-11")
}
