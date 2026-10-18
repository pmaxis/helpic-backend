package integration

import (
	"net/http"
	"testing"
	"time"
)

func TestReports_PaidAndHistory(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	clientID := myID(t, srv, client)
	today := time.Now().Format("2006-01-02")

	finish := func(title string) string {
		task := newTask(t, srv, freelancer, map[string]any{"client_id": clientID, "title": title})
		resp := post(t, freelancer, srv.URL+"/api/v1/tasks/"+task["id"].(string)+"/status", map[string]string{"status": "done"})
		wantStatus(t, resp, http.StatusOK)
		resp.Body.Close()
		return task["id"].(string)
	}
	logo, landing := finish("Logo"), finish("Landing")
	report := func() (string, []any) {
		resp := post(t, freelancer, srv.URL+"/api/v1/reports", map[string]string{"client_id": clientID, "from": today, "to": today})
		wantStatus(t, resp, http.StatusCreated)
		rep := field(decode(t, resp), "report")
		return srv.URL + "/api/v1/reports/" + rep["id"].(string), rep["items"].([]any)
	}

	// Draft, edit, drop a task, send.
	reportURL, items := report()
	resp := do(t, freelancer, http.MethodPut, reportURL, map[string]any{"title": "October", "summary": ""})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	var dropped string
	for _, it := range items {
		if m := it.(map[string]any); m["title"] == "Landing" {
			dropped = m["id"].(string)
		}
	}
	resp = do(t, freelancer, http.MethodDelete, reportURL+"/items/"+dropped, nil)
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// Only a sent report is paid, and only by its client.
	resp = post(t, client, reportURL+"/paid", nil)
	wantStatus(t, resp, http.StatusNotFound) // the client can't see a draft
	resp.Body.Close()
	resp = post(t, freelancer, reportURL+"/send", nil)
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	resp = post(t, freelancer, reportURL+"/paid", nil)
	wantStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()

	resp = get(t, client, reportURL)
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "report"); got["can_mark_paid"] != true {
		t.Fatalf("client view of a sent report = %v", got)
	}
	resp = post(t, client, reportURL+"/paid", nil)
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "report"); got["status"] != "paid" || got["paid_at"] == nil || got["can_mark_paid"] != false {
		t.Fatalf("paid report = %v", got)
	}
	resp = post(t, client, reportURL+"/paid", nil)
	wantStatus(t, resp, http.StatusConflict)
	resp.Body.Close()

	// The report's task moved to a new Paid column, right after Done; the
	// dropped one stayed where it was.
	b := getBoard(t, srv, freelancer, "?with="+clientID)
	names := []string{}
	for _, c := range b.Columns {
		names = append(names, c.Name)
	}
	paid := column(b, "Paid")
	if len(names) != 4 || names[3] != "Paid" || paid.Category != "done" || len(paid.Tasks) != 1 || paid.Tasks[0] != "Logo" {
		t.Fatalf("board after payment = %+v", b.Columns)
	}
	resp = get(t, freelancer, srv.URL+"/api/v1/tasks/"+landing)
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "task"); got["status_name"] != "Done" {
		t.Fatalf("dropped task = %v", got)
	}

	// The task's history says who moved it.
	resp = get(t, freelancer, srv.URL+"/api/v1/tasks/"+logo+"/history")
	wantStatus(t, resp, http.StatusOK)
	events := decode(t, resp)["events"].([]any)
	last := events[len(events)-1].(map[string]any)
	if last["kind"] != "status" || last["data"].(map[string]any)["to"] != "Paid" || last["actor"].(map[string]any)["email"] != "client@example.com" {
		t.Fatalf("task's last event = %v", last)
	}

	// The freelancer hears about it.
	notes, _ := notifications(t, srv, freelancer)
	found := false
	for _, n := range notes {
		if n.Kind == "report_paid" && n.Subject == "October" {
			found = true
		}
	}
	if !found {
		t.Fatalf("freelancer notifications = %+v", notes)
	}

	// The freelancer sees the whole history; the client from the sending on.
	kinds := func(c *http.Client) []string {
		resp := get(t, c, reportURL+"/history")
		wantStatus(t, resp, http.StatusOK)
		out := []string{}
		for _, e := range decode(t, resp)["events"].([]any) {
			out = append(out, e.(map[string]any)["kind"].(string))
		}
		return out
	}
	if got := kinds(freelancer); len(got) != 5 || got[0] != "created" || got[1] != "title" || got[2] != "item_removed" || got[3] != "sent" || got[4] != "paid" {
		t.Fatalf("freelancer's report history = %v", got)
	}
	if got := kinds(client); len(got) != 2 || got[0] != "sent" || got[1] != "paid" {
		t.Fatalf("client's report history = %v", got)
	}

	// A second report leaves out what was paid for; paying it reuses the
	// Paid column.
	reportURL, items = report()
	if len(items) != 1 || items[0].(map[string]any)["title"] != "Landing" {
		t.Fatalf("second report = %v", items)
	}
	resp = post(t, freelancer, reportURL+"/send", nil)
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	resp = post(t, client, reportURL+"/paid", nil)
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	b = getBoard(t, srv, freelancer, "?with="+clientID)
	if len(b.Columns) != 4 || len(column(b, "Paid").Tasks) != 2 {
		t.Fatalf("board after the second payment = %+v", b.Columns)
	}
}
