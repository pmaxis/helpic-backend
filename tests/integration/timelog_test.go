package integration

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestTimelog_HourlyTasksAndReports(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	clientID := myID(t, srv, client)
	member := joinTeam(t, srv, client, "member@example.com")
	today := time.Now().Format("2006-01-02")

	// A rate is a freelancer's; it's an amount in their currency.
	resp := do(t, freelancer, http.MethodPut, srv.URL+"/api/v1/me/rate", map[string]any{"rate": "40"})
	wantStatus(t, resp, http.StatusOK)
	if u := field(decode(t, resp), "user"); u["hourly_rate_cents"] != float64(4000) {
		t.Fatalf("user = %v", u)
	}
	resp = do(t, freelancer, http.MethodPut, srv.URL+"/api/v1/me/rate", map[string]any{"rate": "forty"})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()
	resp = do(t, client, http.MethodPut, srv.URL+"/api/v1/me/rate", map[string]any{"rate": "10"})
	wantStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()

	// The task is billed by the hour: its price follows the time written on it.
	task := newTask(t, srv, freelancer, map[string]any{"client_id": clientID, "title": "Landing"})
	taskURL := srv.URL + "/api/v1/tasks/" + task["id"].(string)
	resp = do(t, freelancer, http.MethodPut, taskURL+"/price", map[string]any{"billing": "hourly"})
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "task"); got["billing"] != "hourly" || got["price_cents"] != nil || got["hourly_rate_cents"] != float64(4000) {
		t.Fatalf("hourly task = %v", got)
	}
	entriesURL := srv.URL + "/api/v1/time-entries?task=" + task["id"].(string)
	resp = post(t, freelancer, entriesURL, map[string]string{"spent_on": today, "duration": "1h 30m", "note": "Hero"})
	wantStatus(t, resp, http.StatusCreated)
	entry := field(decode(t, resp), "entry")
	if entry["seconds"] != float64(5400) || entry["can_edit"] != true {
		t.Fatalf("entry = %v", entry)
	}
	resp = post(t, freelancer, entriesURL, map[string]string{"spent_on": today, "duration": "soon"})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()
	wantTask := func(c *http.Client, price, seconds any) {
		t.Helper()
		resp := get(t, c, taskURL)
		wantStatus(t, resp, http.StatusOK)
		if got := field(decode(t, resp), "task"); got["price_cents"] != price || got["tracked_seconds"] != seconds {
			t.Fatalf("task = %v, want price %v and seconds %v", got, price, seconds)
		}
	}
	wantTask(freelancer, float64(6000), float64(5400))

	// The client sees the time like the price; their team member sees neither,
	// and nobody but the freelancer writes time.
	wantTask(client, float64(6000), float64(5400))
	wantTask(member, nil, nil)
	resp = get(t, member, entriesURL)
	wantStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()
	resp = post(t, client, entriesURL, map[string]string{"spent_on": today, "duration": "1:00"})
	wantStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()
	resp = do(t, client, http.MethodDelete, srv.URL+"/api/v1/time-entries/"+entry["id"].(string), nil)
	wantStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()

	// A timer adds its time when it stops; starting another stops the first.
	resp = post(t, freelancer, srv.URL+"/api/v1/timer?task="+task["id"].(string), nil)
	wantStatus(t, resp, http.StatusCreated)
	timer := field(decode(t, resp), "timer")
	if timer["running_since"] == nil || timer["task_title"] != "Landing" {
		t.Fatalf("timer = %v", timer)
	}
	if _, err := testPool.Exec(context.Background(), `UPDATE time_entries SET running_since = now() - interval '30 minutes' WHERE id = $1`, timer["id"]); err != nil {
		t.Fatal(err)
	}
	other := newTask(t, srv, freelancer, map[string]any{"client_id": clientID, "title": "Logo"})
	resp = post(t, freelancer, srv.URL+"/api/v1/timer?task="+other["id"].(string), nil)
	wantStatus(t, resp, http.StatusCreated)
	resp.Body.Close()
	wantTask(freelancer, float64(8000), float64(7200))
	resp = get(t, freelancer, srv.URL+"/api/v1/timer")
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "timer"); got["task_title"] != "Logo" {
		t.Fatalf("running = %v", got)
	}
	resp = post(t, freelancer, srv.URL+"/api/v1/timer/stop", nil)
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	resp = post(t, freelancer, srv.URL+"/api/v1/timer/stop", nil)
	wantStatus(t, resp, http.StatusConflict)
	resp.Body.Close()

	// A rate on the project bills its tasks instead of the freelancer's own.
	project := createProject(t, srv, freelancer, map[string]any{"client_id": clientID, "name": "Website"})
	if project["can_set_rate"] != true || project["my_hourly_rate_cents"] != nil {
		t.Fatalf("project = %v", project)
	}
	resp = do(t, freelancer, http.MethodPut, taskURL, map[string]any{"title": "Landing", "project_id": project["id"]})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	resp = do(t, freelancer, http.MethodPut, srv.URL+"/api/v1/projects/"+project["id"].(string)+"/rate", map[string]any{"rate": "50"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	wantTask(freelancer, float64(10000), float64(7200))
	// The client on the project doesn't set a rate there.
	resp = post(t, freelancer, srv.URL+"/api/v1/projects/"+project["id"].(string)+"/participants", map[string]string{"user_id": clientID})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	resp = do(t, client, http.MethodPut, srv.URL+"/api/v1/projects/"+project["id"].(string)+"/rate", map[string]any{"rate": "1"})
	wantStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()

	// A report shows the hours with the price, live in a draft and frozen
	// once sent.
	resp = post(t, freelancer, taskURL+"/status", map[string]string{"status": "done"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	resp = post(t, freelancer, srv.URL+"/api/v1/reports", map[string]string{"client_id": clientID, "from": today, "to": today})
	wantStatus(t, resp, http.StatusCreated)
	reportURL := srv.URL + "/api/v1/reports/" + field(decode(t, resp), "report")["id"].(string)
	wantItem := func(price, seconds float64) {
		t.Helper()
		resp := get(t, freelancer, reportURL)
		wantStatus(t, resp, http.StatusOK)
		items := field(decode(t, resp), "report")["items"].([]any)
		if len(items) != 1 || items[0].(map[string]any)["price_cents"] != price || items[0].(map[string]any)["seconds"] != seconds {
			t.Fatalf("items = %v, want price %v and seconds %v", items, price, seconds)
		}
	}
	wantItem(10000, 7200)
	resp = post(t, freelancer, entriesURL, map[string]string{"spent_on": today, "duration": "0:30"})
	wantStatus(t, resp, http.StatusCreated)
	resp.Body.Close()
	wantItem(12500, 9000)
	resp = post(t, freelancer, reportURL+"/send", nil)
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	resp = do(t, freelancer, http.MethodPut, srv.URL+"/api/v1/me/rate", map[string]any{"rate": "100"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	resp = do(t, freelancer, http.MethodPut, srv.URL+"/api/v1/projects/"+project["id"].(string)+"/rate", map[string]any{"rate": nil})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	wantItem(12500, 9000)
	wantTask(freelancer, float64(12500), float64(9000))
	resp = post(t, freelancer, entriesURL, map[string]string{"spent_on": today, "duration": "1:00"})
	wantStatus(t, resp, http.StatusConflict)
	resp.Body.Close()

	// A rate holds the currency like a price does.
	resp = do(t, freelancer, http.MethodPut, srv.URL+"/api/v1/me/currency", map[string]string{"currency": "EUR"})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()

	// Once paid, the task is archived: its time and price stay as billed.
	resp = post(t, client, reportURL+"/paid", nil)
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	resp = post(t, freelancer, entriesURL, map[string]string{"spent_on": today, "duration": "1:00"})
	wantStatus(t, resp, http.StatusConflict)
	resp.Body.Close()
	wantTask(freelancer, float64(12500), float64(9000))
}
