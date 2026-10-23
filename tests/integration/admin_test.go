package integration

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pmaxis/helpic/internal/config"
)

// newAdminAPI is newAPI with boss@example.com as the admin, written the way
// people write emails in an env file.
func newAdminAPI(t *testing.T) *httptest.Server {
	t.Helper()
	srv, _, _ := newAPIWith(t, func(c *config.Config) { c.AdminEmails = []string{"boss@example.com"} })
	return srv
}

func adminAccess(t *testing.T, srv *httptest.Server, c *http.Client) map[string]any {
	t.Helper()
	resp := get(t, c, srv.URL+"/api/v1/admin/access")
	wantStatus(t, resp, http.StatusOK)
	return decode(t, resp)
}

func TestAdmin_OnlyListedEmailsWithTwoFactor(t *testing.T) {
	srv := newAdminAPI(t)
	someone := signUpFreelancer(t, srv, "someone@example.com")
	// The account's email is matched whatever its case.
	boss := signUpFreelancer(t, srv, "Boss@Example.com")

	// Not on the list: no panel.
	if a := adminAccess(t, srv, someone); a["admin"] != false || a["two_factor"] != false {
		t.Fatalf("access = %v", a)
	}
	wantStatus(t, get(t, someone, srv.URL+"/api/v1/admin/stats"), http.StatusForbidden)
	wantStatus(t, get(t, someone, srv.URL+"/api/v1/admin/users"), http.StatusForbidden)
	wantStatus(t, get(t, newClient(), srv.URL+"/api/v1/admin/stats"), http.StatusUnauthorized)

	// On the list but without two-factor: told so, still no panel.
	if a := adminAccess(t, srv, boss); a["admin"] != true || a["two_factor"] != false {
		t.Fatalf("access = %v", a)
	}
	wantStatus(t, get(t, boss, srv.URL+"/api/v1/admin/stats"), http.StatusForbidden)

	turnOnTwoFactor(t, srv, boss)
	if a := adminAccess(t, srv, boss); a["admin"] != true || a["two_factor"] != true {
		t.Fatalf("access = %v", a)
	}
	wantStatus(t, get(t, boss, srv.URL+"/api/v1/admin/stats"), http.StatusOK)
	wantStatus(t, get(t, someone, srv.URL+"/api/v1/admin/stats"), http.StatusForbidden)
}

func TestAdmin_NoAdminsWithoutTheList(t *testing.T) {
	srv, _ := newAPI(t)
	c := signUpFreelancer(t, srv, "boss@example.com")
	turnOnTwoFactor(t, srv, c)
	if a := adminAccess(t, srv, c); a["admin"] != false {
		t.Fatalf("access = %v", a)
	}
	wantStatus(t, get(t, c, srv.URL+"/api/v1/admin/stats"), http.StatusForbidden)
}

func TestAdmin_Stats(t *testing.T) {
	srv := newAdminAPI(t)
	boss := signUpFreelancer(t, srv, "boss@example.com")
	turnOnTwoFactor(t, srv, boss)
	// One freelancer goes all the way to a paid report, one only signs up.
	dev := signUpFreelancer(t, srv, "dev@example.com")
	client := acceptNew(t, srv, invite(t, srv, dev, "client@example.com"))
	signUpFreelancer(t, srv, "lazy@example.com")
	stats := func(query string) map[string]any {
		t.Helper()
		resp := get(t, boss, srv.URL+"/api/v1/admin/stats"+query)
		wantStatus(t, resp, http.StatusOK)
		return decode(t, resp)
	}
	// Accepting the invitation is not using the app yet; the first signed-in request is.
	if n := field(stats("?days=7"), "totals")["active_day"]; n != 2.0 {
		t.Fatalf("active before the client's first request = %v", n)
	}
	paidTask(t, srv, dev, client)
	// The developer was also around ten days ago: the period before.
	devID := myID(t, srv, dev)
	if _, err := testPool.Exec(context.Background(), `INSERT INTO user_activity (user_id, day) VALUES ($1, current_date - 10)`, devID); err != nil {
		t.Fatal(err)
	}

	body := stats("?days=7")
	check := func(group string, want map[string]float64) {
		t.Helper()
		got := field(body, group)
		for k, v := range want {
			if got[k] != v {
				t.Errorf("%s[%s] = %v, want %v", group, k, got[k], v)
			}
		}
	}
	check("totals", map[string]float64{
		"users": 4, "freelancers": 3, "clients": 1, "two_factor": 1, "tasks": 1,
		"active_day": 3, "active_week": 3, "active_month": 3,
	})
	check("period", map[string]float64{
		"active": 3, "new_users": 4, "tasks_created": 1, "tasks_completed": 1,
	})
	check("previous", map[string]float64{"active": 1, "new_users": 0, "tasks_created": 0})

	today := time.Now().UTC().Format("2006-01-02")
	days, previous := body["days"].([]any), body["previous_days"].([]any)
	if len(days) != 7 || len(previous) != 7 || body["to"] != today {
		t.Fatalf("%d days, %d before, to %v", len(days), len(previous), body["to"])
	}
	last := days[6].(map[string]any)
	if last["day"] != today || last["signups"] != 4.0 || last["active"] != 3.0 || last["tasks_completed"] != 1.0 {
		t.Fatalf("today = %v", last)
	}
	tenDaysAgo := time.Now().UTC().AddDate(0, 0, -10).Format("2006-01-02")
	if d := previous[3].(map[string]any); d["day"] != tenDaysAgo || d["active"] != 1.0 {
		t.Fatalf("ten days ago = %v", d)
	}

	for _, bad := range []string{"6", "366", "week"} {
		resp := get(t, boss, srv.URL+"/api/v1/admin/stats?days="+bad)
		wantStatus(t, resp, http.StatusBadRequest)
		resp.Body.Close()
	}
}

func TestAdmin_Users(t *testing.T) {
	srv := newAdminAPI(t)
	boss := signUpFreelancer(t, srv, "boss@example.com")
	turnOnTwoFactor(t, srv, boss)
	freelancer := signUpFreelancer(t, srv, "dev_one@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	resp := post(t, freelancer, srv.URL+"/api/v1/tasks", map[string]any{"title": "Logo"})
	wantStatus(t, resp, http.StatusCreated)
	resp.Body.Close()

	list := func(query string) map[string]any {
		t.Helper()
		resp := get(t, boss, srv.URL+"/api/v1/admin/users"+query)
		wantStatus(t, resp, http.StatusOK)
		return decode(t, resp)
	}
	emails := func(page map[string]any) []string {
		var out []string
		for _, u := range page["users"].([]any) {
			out = append(out, u.(map[string]any)["email"].(string))
		}
		return out
	}

	if never := list("?activity=never"); fmt.Sprint(emails(never)) != "[client@example.com]" || never["users"].([]any)[0].(map[string]any)["last_active"] != nil {
		t.Fatalf("never active = %v", never)
	}
	myID(t, srv, client)
	if n := list("?activity=never")["total"]; n != 0.0 {
		t.Fatalf("never active after a request = %v", n)
	}
	if n := list("?activity=active")["total"]; n != 3.0 {
		t.Fatalf("active = %v", n)
	}

	all := list("")
	if all["total"] != 3.0 || fmt.Sprint(emails(all)) != "[client@example.com dev_one@example.com boss@example.com]" {
		t.Fatalf("all, the newest first = %v", emails(all))
	}
	first := all["users"].([]any)[0].(map[string]any)
	if first["role"] != "client" || first["tasks"] != 0.0 || first["active_days"] != 1.0 || first["last_active"] == nil {
		t.Fatalf("first = %v", first)
	}
	if got := emails(list("?sort=tasks"))[0]; got != "dev_one@example.com" {
		t.Fatalf("most tasks = %v", got)
	}

	// "_" is matched as itself, not as any character.
	found := list("?q=V_ONE")
	if found["total"] != 1.0 {
		t.Fatalf("search = %v", found)
	}
	dev := found["users"].([]any)[0].(map[string]any)
	if dev["email"] != "dev_one@example.com" || dev["tasks"] != 1.0 {
		t.Fatalf("dev = %v", dev)
	}
	if n := list("?q=dev%25one")["total"]; n != 0.0 {
		t.Fatalf("%% matched anything: %v", n)
	}

	if n := list("?role=client")["total"]; n != 1.0 {
		t.Fatalf("clients = %v", n)
	}
	page := list("?limit=2&page=2")
	if page["total"] != 3.0 || len(page["users"].([]any)) != 1 {
		t.Fatalf("page 2 = %v", page)
	}

	for _, bad := range []string{"?role=admin", "?activity=sometimes", "?sort=email", "?limit=101", "?page=0"} {
		resp := get(t, boss, srv.URL+"/api/v1/admin/users"+bad)
		wantStatus(t, resp, http.StatusBadRequest)
		resp.Body.Close()
	}

	// One account in full.
	resp = get(t, boss, srv.URL+"/api/v1/admin/users/"+dev["id"].(string))
	wantStatus(t, resp, http.StatusOK)
	d := field(decode(t, resp), "user")
	if d["email"] != "dev_one@example.com" || d["partners"] != 1.0 || d["sessions"] != 1.0 || d["telegram"] != false || d["team"] != nil {
		t.Fatalf("detail = %v", d)
	}
	today := time.Now().UTC().Format("2006-01-02")
	if on := d["active_on"].([]any); len(on) != 1 || on[0] != today {
		t.Fatalf("active on = %v", on)
	}
	since, _ := time.Parse("2006-01-02", d["active_since"].(string))
	if since.Weekday() != time.Monday || time.Since(since) < 25*7*24*time.Hour || time.Since(since) > 26*7*24*time.Hour {
		t.Fatalf("active since = %v", d["active_since"])
	}
	for _, id := range []string{"00000000-0000-0000-0000-000000000000", "nope"} {
		resp := get(t, boss, srv.URL+"/api/v1/admin/users/"+id)
		wantStatus(t, resp, http.StatusNotFound)
		resp.Body.Close()
	}
	wantStatus(t, get(t, freelancer, srv.URL+"/api/v1/admin/users/"+dev["id"].(string)), http.StatusForbidden)
}
