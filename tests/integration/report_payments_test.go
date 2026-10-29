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

	// Only a sent report is paid, by either side.
	resp = post(t, client, reportURL+"/paid", nil)
	wantStatus(t, resp, http.StatusNotFound) // the client can't see a draft
	resp.Body.Close()
	resp = post(t, freelancer, reportURL+"/paid", nil)
	wantStatus(t, resp, http.StatusConflict)
	resp.Body.Close()
	resp = post(t, freelancer, reportURL+"/send", nil)
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	for _, c := range []*http.Client{freelancer, client} {
		resp = get(t, c, reportURL)
		wantStatus(t, resp, http.StatusOK)
		if got := field(decode(t, resp), "report"); got["can_mark_paid"] != true {
			t.Fatalf("view of a sent report = %v", got)
		}
	}
	resp = post(t, client, reportURL+"/paid", nil)
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "report"); got["status"] != "paid" || got["paid_at"] == nil || got["can_mark_paid"] != false {
		t.Fatalf("paid report = %v", got)
	}
	resp = post(t, client, reportURL+"/paid", nil)
	wantStatus(t, resp, http.StatusConflict)
	resp.Body.Close()

	// The report's task left the board for the archive; no Paid column is
	// made, and the dropped one stayed where it was.
	b := getBoard(t, srv, freelancer, "?with="+clientID)
	if len(b.Columns) != 3 || column(b, "Paid").ID != "" || len(column(b, "Done").Tasks) != 1 || column(b, "Done").Tasks[0] != "Landing" {
		t.Fatalf("board after payment = %+v", b.Columns)
	}
	resp = get(t, freelancer, srv.URL+"/api/v1/tasks/"+logo)
	wantStatus(t, resp, http.StatusOK)
	archived := field(decode(t, resp), "task")
	if archived["archived_at"] == nil || field(archived, "archived_by")["title"] != "October" || archived["can_edit"] != false {
		t.Fatalf("paid task = %v", archived)
	}
	resp = get(t, freelancer, srv.URL+"/api/v1/tasks/"+landing)
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "task"); got["status_name"] != "Done" || got["archived_at"] != nil {
		t.Fatalf("dropped task = %v", got)
	}

	// The task's history says who archived it, and for which report.
	resp = get(t, freelancer, srv.URL+"/api/v1/tasks/"+logo+"/history")
	wantStatus(t, resp, http.StatusOK)
	events := decode(t, resp)["events"].([]any)
	last := events[len(events)-1].(map[string]any)
	if last["kind"] != "archived" || last["data"].(map[string]any)["report"] != "October" || last["actor"].(map[string]any)["email"] != "client@example.com" {
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

	// A second report leaves out what was paid for; the freelancer marks it
	// paid when the money comes, which archives the rest.
	reportURL, items = report()
	if len(items) != 1 || items[0].(map[string]any)["title"] != "Landing" {
		t.Fatalf("second report = %v", items)
	}
	resp = post(t, freelancer, reportURL+"/send", nil)
	wantStatus(t, resp, http.StatusOK)
	second := field(decode(t, resp), "report")["title"]
	resp = post(t, freelancer, reportURL+"/paid", nil)
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "report"); got["status"] != "paid" || got["can_mark_paid"] != false {
		t.Fatalf("report the freelancer marked paid = %v", got)
	}
	b = getBoard(t, srv, freelancer, "?with="+clientID)
	if len(b.Columns) != 3 || len(column(b, "Done").Tasks) != 0 {
		t.Fatalf("board after the second payment = %+v", b.Columns)
	}

	// This time the client hears about it.
	notes, _ = notifications(t, srv, client)
	found = false
	for _, n := range notes {
		if n.Kind == "report_paid" && n.Subject == second {
			found = true
		}
	}
	if !found {
		t.Fatalf("client notifications = %+v", notes)
	}
}

func TestReports_PaidOnSharedProject(t *testing.T) {
	srv, _ := newAPI(t)
	p := newSharedProject(t, srv)
	today := time.Now().Format("2006-01-02")
	task := newTask(t, srv, p.second, map[string]any{"project_id": p.id, "title": "Logo"})
	resp := post(t, p.second, srv.URL+"/api/v1/tasks/"+task["id"].(string)+"/status", map[string]string{"status": "done"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// The second freelancer bills the client for it, and the client pays.
	resp = post(t, p.second, srv.URL+"/api/v1/reports", map[string]string{"client_id": p.clientID, "from": today, "to": today})
	wantStatus(t, resp, http.StatusCreated)
	reportURL := srv.URL + "/api/v1/reports/" + field(decode(t, resp), "report")["id"].(string)
	resp = post(t, p.second, reportURL+"/send", nil)
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	resp = post(t, p.client, reportURL+"/paid", nil)
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// The task left the project's board for the archive, which the client
	// and the freelancer both see.
	b := getBoard(t, srv, p.client, "?project="+p.id)
	if len(b.Columns) != 3 || len(column(b, "Done").Tasks) != 0 {
		t.Fatalf("project board after payment = %+v", b.Columns)
	}
	for _, c := range []*http.Client{p.client, p.second} {
		if got := archiveTitles(t, srv, c, ""); len(got) != 1 || got[0] != "Logo" {
			t.Fatalf("archive = %v", got)
		}
	}
	if got := archiveTitles(t, srv, p.first, ""); len(got) != 0 {
		t.Fatalf("the other freelancer's archive = %v", got)
	}
}
