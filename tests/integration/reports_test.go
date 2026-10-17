package integration

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestReports_MakeEditSend(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	clientID, freelancerID := myID(t, srv, client), myID(t, srv, freelancer)

	// Two finished tasks, one still open, one personal (never reported).
	done1 := newTask(t, srv, client, map[string]any{"freelancer_id": freelancerID, "title": "Logo"})
	done2 := newTask(t, srv, freelancer, map[string]any{"client_id": clientID, "title": "Landing"})
	newTask(t, srv, client, map[string]any{"freelancer_id": freelancerID, "title": "Still open"})
	newTask(t, srv, freelancer, map[string]any{"title": "Personal", "status_id": column(getBoard(t, srv, freelancer, "?with=personal"), "Done").ID})
	for _, task := range []map[string]any{done1, done2} {
		resp := post(t, freelancer, srv.URL+"/api/v1/tasks/"+task["id"].(string)+"/status", map[string]string{"status": "done"})
		wantStatus(t, resp, http.StatusOK)
		resp.Body.Close()
	}

	today := time.Now().Format("2006-01-02")
	resp := post(t, freelancer, srv.URL+"/api/v1/reports", map[string]string{"client_id": clientID, "from": today, "to": today, "summary": "Good week"})
	wantStatus(t, resp, http.StatusCreated)
	rep := field(decode(t, resp), "report")
	id := rep["id"].(string)
	items, _ := rep["items"].([]any)
	if rep["status"] != "draft" || len(items) != 2 || rep["title"] == "" || rep["can_edit"] != true {
		t.Fatalf("draft = %v", rep)
	}

	// A draft is the freelancer's alone.
	wantStatus(t, get(t, client, srv.URL+"/api/v1/reports/"+id), http.StatusNotFound)
	resp = get(t, client, srv.URL+"/api/v1/reports")
	wantStatus(t, resp, http.StatusOK)
	if list, _ := decode(t, resp)["reports"].([]any); len(list) != 0 {
		t.Fatalf("client sees drafts: %v", list)
	}

	// Edit, drop a task, send.
	resp = do(t, freelancer, http.MethodPut, srv.URL+"/api/v1/reports/"+id, map[string]string{"title": "October", "summary": "Done and dusted"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	itemID := items[0].(map[string]any)["id"].(string)
	resp = do(t, freelancer, http.MethodDelete, srv.URL+"/api/v1/reports/"+id+"/items/"+itemID, nil)
	wantStatus(t, resp, http.StatusOK)
	if got, _ := field(decode(t, resp), "report")["items"].([]any); len(got) != 1 {
		t.Fatalf("items after removing one = %v", got)
	}
	resp = post(t, freelancer, srv.URL+"/api/v1/reports/"+id+"/send", nil)
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "report"); got["status"] != "sent" || got["can_edit"] != false {
		t.Fatalf("sent = %v", got)
	}

	// The client sees it and is told.
	resp = get(t, client, srv.URL+"/api/v1/reports/"+id)
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "report"); got["title"] != "October" || got["summary"] != "Done and dusted" {
		t.Fatalf("client view = %v", got)
	}
	list, _ := notifications(t, srv, client)
	found := false
	for _, n := range list {
		if n.Kind == "report_sent" && n.Subject == "October" {
			found = true
		}
	}
	if !found {
		t.Fatalf("client notifications = %+v", list)
	}

	// Sent reports don't change, and only freelancers make reports.
	resp = do(t, freelancer, http.MethodPut, srv.URL+"/api/v1/reports/"+id, map[string]string{"title": "Changed"})
	wantStatus(t, resp, http.StatusConflict)
	resp.Body.Close()
	resp = post(t, client, srv.URL+"/api/v1/reports", map[string]string{"client_id": clientID, "from": today, "to": today})
	wantStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()

	// The freelancer may still delete a sent report; the client loses it too.
	resp = do(t, client, http.MethodDelete, srv.URL+"/api/v1/reports/"+id, nil)
	wantStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()
	resp = get(t, freelancer, srv.URL+"/api/v1/reports/"+id)
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "report"); got["can_delete"] != true || got["can_edit"] != false {
		t.Fatalf("sent report for its freelancer = %v", got)
	}
	resp = do(t, freelancer, http.MethodDelete, srv.URL+"/api/v1/reports/"+id, nil)
	wantStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
	wantStatus(t, get(t, client, srv.URL+"/api/v1/reports/"+id), http.StatusNotFound)
}

func TestReports_Validation(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	stranger := acceptNew(t, srv, invite(t, srv, signUpFreelancer(t, srv, "other@example.com"), "c@example.com"))

	for name, body := range map[string]map[string]string{
		"backwards":     {"client_id": myID(t, srv, stranger), "from": "2026-10-31", "to": "2026-10-01"},
		"not my client": {"client_id": myID(t, srv, stranger), "from": "2026-10-01", "to": "2026-10-31"},
	} {
		resp := post(t, freelancer, srv.URL+"/api/v1/reports", body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, resp.StatusCode)
		}
		resp.Body.Close()
	}
}

func TestReports_PersonalWork(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	done := column(getBoard(t, srv, freelancer, "?with=personal"), "Done").ID
	newTask(t, srv, freelancer, map[string]any{"title": "Portfolio site", "status_id": done})
	newTask(t, srv, freelancer, map[string]any{"title": "Not finished"})

	today := time.Now().Format("2006-01-02")
	resp := post(t, freelancer, srv.URL+"/api/v1/reports", map[string]string{"from": today, "to": today})
	wantStatus(t, resp, http.StatusCreated)
	rep := field(decode(t, resp), "report")
	items, _ := rep["items"].([]any)
	if rep["client"] != nil || len(items) != 1 || items[0].(map[string]any)["title"] != "Portfolio site" {
		t.Fatalf("personal report = %v", rep)
	}

	// Nothing to send it to.
	resp = post(t, freelancer, srv.URL+"/api/v1/reports/"+rep["id"].(string)+"/send", nil)
	wantStatus(t, resp, http.StatusConflict)
	resp.Body.Close()
}

func TestReports_Documents(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	today := time.Now().Format("2006-01-02")
	resp := post(t, freelancer, srv.URL+"/api/v1/reports", map[string]string{"client_id": myID(t, srv, client), "from": today, "to": today, "title": "Weekly"})
	wantStatus(t, resp, http.StatusCreated)
	id := field(decode(t, resp), "report")["id"].(string)

	resp = get(t, freelancer, srv.URL+"/api/v1/reports/"+id+"/html")
	wantStatus(t, resp, http.StatusOK)
	if ct := resp.Header.Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Fatalf("html content type = %q", ct)
	}
	resp.Body.Close()

	// The client can't read a draft in any form.
	wantStatus(t, get(t, client, srv.URL+"/api/v1/reports/"+id+"/html"), http.StatusNotFound)
	wantStatus(t, get(t, client, srv.URL+"/api/v1/reports/"+id+"/pdf"), http.StatusNotFound)

	// No Chrome in tests: the PDF says so instead of failing.
	resp = get(t, freelancer, srv.URL+"/api/v1/reports/"+id+"/pdf")
	wantStatus(t, resp, http.StatusServiceUnavailable)
	resp.Body.Close()
	resp = get(t, freelancer, srv.URL+"/api/v1/reports")
	wantStatus(t, resp, http.StatusOK)
	if got := decode(t, resp)["pdf_available"]; got != false {
		t.Fatalf("pdf_available = %v, want false", got)
	}
}

func TestReports_SeveralProjects(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	clientID := myID(t, srv, client)
	website := createProject(t, srv, freelancer, map[string]any{"client_id": clientID, "name": "Website"})
	branding := createProject(t, srv, freelancer, map[string]any{"client_id": clientID, "name": "Branding"})
	other := createProject(t, srv, freelancer, map[string]any{"client_id": clientID, "name": "Other"})
	personal := createProject(t, srv, freelancer, map[string]any{"name": "Mine"})

	for _, p := range []map[string]any{website, branding, other} {
		task := newTask(t, srv, freelancer, map[string]any{"project_id": p["id"], "title": "Work on " + p["name"].(string)})
		resp := post(t, freelancer, srv.URL+"/api/v1/tasks/"+task["id"].(string)+"/status", map[string]string{"status": "done"})
		wantStatus(t, resp, http.StatusOK)
		resp.Body.Close()
	}
	today := time.Now().Format("2006-01-02")

	// Two of the three projects: their tasks only, both named.
	resp := post(t, freelancer, srv.URL+"/api/v1/reports", map[string]any{
		"client_id": clientID, "from": today, "to": today, "project_ids": []any{website["id"], branding["id"], website["id"]},
	})
	wantStatus(t, resp, http.StatusCreated)
	rep := field(decode(t, resp), "report")
	projects, _ := rep["projects"].([]any)
	items, _ := rep["items"].([]any)
	if len(projects) != 2 || projects[1].(map[string]any)["name"] != "Branding" || len(items) != 2 {
		t.Fatalf("report on two projects = %v", rep)
	}
	if title, _ := rep["title"].(string); !strings.HasPrefix(title, "Work report") {
		t.Fatalf("title = %q", title)
	}

	// One project still names the report; none covers them all.
	resp = post(t, freelancer, srv.URL+"/api/v1/reports", map[string]any{"client_id": clientID, "from": today, "to": today, "project_id": other["id"]})
	wantStatus(t, resp, http.StatusCreated)
	if got := field(decode(t, resp), "report"); !strings.HasPrefix(got["title"].(string), "Other") {
		t.Fatalf("one-project report = %v", got)
	}
	resp = post(t, freelancer, srv.URL+"/api/v1/reports", map[string]any{"client_id": clientID, "from": today, "to": today})
	wantStatus(t, resp, http.StatusCreated)
	if got := field(decode(t, resp), "report"); len(got["items"].([]any)) != 3 || len(got["projects"].([]any)) != 0 {
		t.Fatalf("all-projects report = %v", got)
	}

	// A project of another pair is refused.
	resp = post(t, freelancer, srv.URL+"/api/v1/reports", map[string]any{
		"client_id": clientID, "from": today, "to": today, "project_ids": []any{website["id"], personal["id"]},
	})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()
}
