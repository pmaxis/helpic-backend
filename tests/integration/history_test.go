package integration

import (
	"net/http"
	"testing"
)

func TestTaskHistory(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	outsider := signUpFreelancer(t, srv, "other@example.com")
	project := createProject(t, srv, freelancer, map[string]any{"client_id": myID(t, srv, client), "name": "Website"})

	task := newTask(t, srv, client, map[string]any{"freelancer_id": myID(t, srv, freelancer), "title": "Logo"})
	taskURL := srv.URL + "/api/tasks/" + task["id"].(string)

	// The client edits; an unchanged field leaves no trace.
	resp := do(t, client, http.MethodPut, taskURL, map[string]any{
		"title": "Logo v2", "description": "Three options", "due_on": "2030-01-15", "project_id": project["id"],
	})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	resp = do(t, client, http.MethodPut, taskURL, map[string]any{"title": "Logo v2", "description": "Three options", "due_on": "2030-01-15"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// The freelancer moves it and prices it; a file comes and goes.
	resp = post(t, freelancer, taskURL+"/status", map[string]string{"status": "in_progress"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	resp = do(t, freelancer, http.MethodPut, taskURL+"/price", map[string]any{"price": "300"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	resp = upload(t, srv, freelancer, task["id"].(string), "draft.png", pngBytes)
	wantStatus(t, resp, http.StatusCreated)
	file := field(decode(t, resp), "attachment")
	resp = do(t, freelancer, http.MethodDelete, srv.URL+"/api/attachments/"+file["id"].(string), nil)
	wantStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()

	resp = get(t, freelancer, taskURL+"/history")
	wantStatus(t, resp, http.StatusOK)
	events, _ := decode(t, resp)["events"].([]any)
	type want struct{ kind, actor, from, to string }
	wants := []want{
		{"created", "client@example.com", "", ""},
		{"title", "client@example.com", "Logo", "Logo v2"},
		{"description", "client@example.com", "", ""},
		{"due_on", "client@example.com", "", "2030-01-15"},
		{"project", "client@example.com", "", "Website"},
		{"status", "free@example.com", "New", "In progress"},
		{"price", "free@example.com", "", ""},
		{"attachment_added", "free@example.com", "", ""},
		{"attachment_removed", "free@example.com", "", ""},
	}
	if len(events) != len(wants) {
		t.Fatalf("history = %v", events)
	}
	for i, w := range wants {
		e := events[i].(map[string]any)
		data := e["data"].(map[string]any)
		actor := e["actor"].(map[string]any)["email"]
		from, _ := data["from"].(string)
		to, _ := data["to"].(string)
		if e["kind"] != w.kind || actor != w.actor || (w.from != "" && from != w.from) || (w.to != "" && to != w.to) {
			t.Errorf("event %d = %v, want %+v", i, e, w)
		}
	}
	if p := events[6].(map[string]any)["data"].(map[string]any); p["from"] != nil || p["to"] != float64(30000) || p["currency"] != "USD" {
		t.Errorf("price event = %v", p)
	}
	if f := events[7].(map[string]any)["data"].(map[string]any); f["name"] != "draft.png" {
		t.Errorf("file event = %v", f)
	}

	// The history is the task's sides' only.
	wantStatus(t, get(t, outsider, taskURL+"/history"), http.StatusNotFound)
}
