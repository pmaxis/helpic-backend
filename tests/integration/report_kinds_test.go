package integration

import (
	"net/http"
	"testing"
	"time"
)

func TestReports_DetailedNotes(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	clientID := myID(t, srv, client)

	task := newTask(t, srv, freelancer, map[string]any{"client_id": clientID, "title": "Logo", "description": "Draw three options"})
	resp := post(t, freelancer, srv.URL+"/api/v1/tasks/"+task["id"].(string)+"/status", map[string]string{"status": "done"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	today := time.Now().Format("2006-01-02")
	resp = post(t, freelancer, srv.URL+"/api/v1/reports", map[string]string{"client_id": clientID, "from": today, "to": today, "kind": "weekly"})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()

	// A detailed report's notes start as the task's description.
	resp = post(t, freelancer, srv.URL+"/api/v1/reports", map[string]string{"client_id": clientID, "from": today, "to": today, "kind": "detailed"})
	wantStatus(t, resp, http.StatusCreated)
	rep := field(decode(t, resp), "report")
	reportURL := srv.URL + "/api/v1/reports/" + rep["id"].(string)
	item := rep["items"].([]any)[0].(map[string]any)
	itemURL := reportURL + "/items/" + item["id"].(string)
	if rep["kind"] != "detailed" || item["notes"] != "Draw three options" {
		t.Fatalf("detailed report = %v", rep)
	}

	// The freelancer rewrites them; the task stays as it was.
	resp = do(t, freelancer, http.MethodPut, itemURL, map[string]string{"notes": "  Drew three options, the client picked one  "})
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "report")["items"].([]any)[0].(map[string]any); got["notes"] != "Drew three options, the client picked one" {
		t.Fatalf("item after edit = %v", got)
	}
	resp = get(t, freelancer, srv.URL+"/api/v1/tasks/"+task["id"].(string))
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "task"); got["description"] != "Draw three options" {
		t.Fatalf("task changed with the report: %v", got)
	}

	// The kind switches in a draft; leaving it out keeps it.
	resp = do(t, freelancer, http.MethodPut, reportURL, map[string]any{"title": "October", "kind": "basic"})
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "report"); got["kind"] != "basic" {
		t.Fatalf("after switching = %v", got)
	}
	resp = do(t, freelancer, http.MethodPut, reportURL, map[string]any{"title": "October"})
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "report"); got["kind"] != "basic" {
		t.Fatalf("kind left out = %v", got)
	}

	// Once sent, notes are fixed, and a client never edits them.
	resp = post(t, freelancer, reportURL+"/send", nil)
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	resp = do(t, freelancer, http.MethodPut, itemURL, map[string]string{"notes": "Changed"})
	wantStatus(t, resp, http.StatusConflict)
	resp.Body.Close()
	resp = do(t, client, http.MethodPut, itemURL, map[string]string{"notes": "Changed"})
	wantStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()
}
