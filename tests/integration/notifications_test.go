package integration

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

type note struct {
	ID, Kind, Subject, Detail, Actor string
	Read                             bool
	TaskID                           any
}

func notifications(t *testing.T, srv *httptest.Server, c *http.Client) ([]note, int) {
	t.Helper()
	resp := get(t, c, srv.URL+"/api/notifications")
	wantStatus(t, resp, http.StatusOK)
	body := decode(t, resp)
	var out []note
	for _, x := range body["notifications"].([]any) {
		m := x.(map[string]any)
		out = append(out, note{
			ID: m["id"].(string), Kind: m["kind"].(string), Subject: m["subject"].(string), Detail: m["detail"].(string),
			Actor: m["actor"].(map[string]any)["email"].(string), Read: m["read"] == true, TaskID: m["task_id"],
		})
	}
	return out, int(body["unread"].(float64))
}

func TestNotifications_SharedWork(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	freelancerID := myID(t, srv, freelancer)

	// Accepting the invitation tells the freelancer.
	list, unread := notifications(t, srv, freelancer)
	if unread != 1 || list[0].Kind != "client_joined" || list[0].Actor != "client@example.com" {
		t.Fatalf("after joining = %+v (unread %d)", list, unread)
	}

	// The client adds a task: the freelancer hears about it, the client doesn't.
	task := newTask(t, srv, client, map[string]any{"freelancer_id": freelancerID, "title": "Logo"})
	list, unread = notifications(t, srv, freelancer)
	if unread != 2 || list[0].Kind != "task_created" || list[0].Subject != "Logo" || list[0].TaskID != task["id"] {
		t.Fatalf("after task = %+v (unread %d)", list, unread)
	}
	if _, n := notifications(t, srv, client); n != 0 {
		t.Fatalf("client has %d unread, want 0", n)
	}

	// The freelancer moves it: the client hears the new status.
	resp := post(t, freelancer, srv.URL+"/api/tasks/"+task["id"].(string)+"/status", map[string]string{"status": "in_progress"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	list, _ = notifications(t, srv, client)
	if len(list) != 1 || list[0].Kind != "task_status" || list[0].Detail != "In progress" {
		t.Fatalf("client notifications = %+v", list)
	}

	// Reading.
	resp = post(t, freelancer, srv.URL+"/api/notifications/"+list[0].ID+"/read", nil)
	wantStatus(t, resp, http.StatusNotFound) // someone else's
	resp.Body.Close()
	resp = post(t, client, srv.URL+"/api/notifications/"+list[0].ID+"/read", nil)
	wantStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
	resp = get(t, client, srv.URL+"/api/notifications/unread-count")
	wantStatus(t, resp, http.StatusOK)
	if got := decode(t, resp)["unread"]; got != float64(0) {
		t.Fatalf("client unread = %v", got)
	}

	resp = post(t, freelancer, srv.URL+"/api/notifications/read-all", nil)
	wantStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
	if _, n := notifications(t, srv, freelancer); n != 0 {
		t.Fatalf("freelancer unread after read-all = %d", n)
	}

	// A shared project tells the other side too.
	createProject(t, srv, freelancer, map[string]any{"client_id": myID(t, srv, client), "name": "Website"})
	list, _ = notifications(t, srv, client)
	if list[0].Kind != "project_created" || list[0].Subject != "Website" {
		t.Fatalf("project notification = %+v", list[0])
	}
}

func TestNotifications_PersonalWorkIsSilent(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	task := newTask(t, srv, freelancer, map[string]any{"title": "Mine"})
	resp := post(t, freelancer, srv.URL+"/api/tasks/"+task["id"].(string)+"/status", map[string]string{"status": "done"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	createProject(t, srv, freelancer, map[string]any{"name": "Portfolio"})

	if list, n := notifications(t, srv, freelancer); len(list) != 0 || n != 0 {
		t.Fatalf("personal work made notifications: %+v", list)
	}
}
