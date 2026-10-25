package integration

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// subtaskTitles lists a task's subtasks as the user sees them.
func subtaskTitles(t *testing.T, srv *httptest.Server, c *http.Client, taskID string) []string {
	t.Helper()
	resp := get(t, c, srv.URL+"/api/v1/tasks/"+taskID+"/subtasks")
	wantStatus(t, resp, http.StatusOK)
	titles := []string{}
	for _, x := range decode(t, resp)["tasks"].([]any) {
		titles = append(titles, x.(map[string]any)["title"].(string))
	}
	return titles
}

func getTask(t *testing.T, srv *httptest.Server, c *http.Client, id string) map[string]any {
	t.Helper()
	resp := get(t, c, srv.URL+"/api/v1/tasks/"+id)
	wantStatus(t, resp, http.StatusOK)
	return field(decode(t, resp), "task")
}

func TestSubtasks_SplitASharedTask(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	freelancerID, clientID := myID(t, srv, freelancer), myID(t, srv, client)

	parent := newTask(t, srv, freelancer, map[string]any{"client_id": clientID, "title": "Landing"})
	parentID := parent["id"].(string)
	if parent["can_add_subtask"] != true || parent["parent_id"] != nil {
		t.Fatalf("parent = %v", parent)
	}

	// The client splits it without naming anyone: the subtask is with the
	// parent's freelancer, and its own task.
	sub := newTask(t, srv, client, map[string]any{"parent_id": parentID, "title": "Header"})
	if sub["parent_id"] != parentID || field(sub, "parent")["title"] != "Landing" ||
		field(sub, "freelancer")["id"] != freelancerID || field(sub, "client")["id"] != clientID || sub["can_add_subtask"] != false {
		t.Fatalf("subtask = %v", sub)
	}
	// The freelancer adds one too and finishes it.
	own := newTask(t, srv, freelancer, map[string]any{"parent_id": parentID, "title": "Form"})
	resp := post(t, freelancer, srv.URL+"/api/v1/tasks/"+own["id"].(string)+"/status", map[string]string{"status": "done"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	for _, c := range []*http.Client{freelancer, client} {
		if got := subtaskTitles(t, srv, c, parentID); len(got) != 2 || got[0] != "Header" || got[1] != "Form" {
			t.Fatalf("subtasks = %v", got)
		}
		counts := field(getTask(t, srv, c, parentID), "subtasks")
		if counts["total"] != 2.0 || counts["done"] != 1.0 {
			t.Fatalf("subtask counts = %v", counts)
		}
	}
	// Subtasks are on the task lists, like any task.
	if got := taskTitles(t, freelancer, srv.URL); len(got) != 3 {
		t.Fatalf("task list = %v", got)
	}

	// The parent's history tells who split it.
	resp = get(t, client, srv.URL+"/api/v1/tasks/"+parentID+"/history")
	wantStatus(t, resp, http.StatusOK)
	added := 0
	for _, e := range decode(t, resp)["events"].([]any) {
		ev := e.(map[string]any)
		if ev["kind"] == "subtask_added" {
			added++
		}
	}
	if added != 2 {
		t.Fatalf("subtask_added events = %d", added)
	}

	// One level deep.
	resp = post(t, client, srv.URL+"/api/v1/tasks", map[string]any{"parent_id": sub["id"], "title": "Deeper"})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()
	// The parent's client and project are the subtask's.
	resp = post(t, freelancer, srv.URL+"/api/v1/tasks", map[string]any{"parent_id": parentID, "client_id": clientID, "title": "Mixed"})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()
	// Nobody else adds to it.
	stranger := signUpFreelancer(t, srv, "other@example.com")
	resp = post(t, stranger, srv.URL+"/api/v1/tasks", map[string]any{"parent_id": parentID, "title": "Mine"})
	wantStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()
	resp = get(t, stranger, srv.URL+"/api/v1/tasks/"+parentID+"/subtasks")
	wantStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()
}

func TestSubtasks_DeleteParentAfterItsSubtasks(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")

	parent := newTask(t, srv, freelancer, map[string]any{"title": "Move"})
	parentID := parent["id"].(string)
	sub := newTask(t, srv, freelancer, map[string]any{"parent_id": parentID, "title": "Pack"})
	if sub["client"] != nil {
		t.Fatalf("a personal task's subtask = %v", sub)
	}
	if getTask(t, srv, freelancer, parentID)["can_delete"] != false {
		t.Fatal("a task with subtasks offers to be deleted")
	}

	resp := do(t, freelancer, http.MethodDelete, srv.URL+"/api/v1/tasks/"+parentID, nil)
	wantStatus(t, resp, http.StatusConflict)
	resp.Body.Close()

	resp = do(t, freelancer, http.MethodDelete, srv.URL+"/api/v1/tasks/"+sub["id"].(string), nil)
	wantStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
	resp = get(t, freelancer, srv.URL+"/api/v1/tasks/"+parentID+"/history")
	wantStatus(t, resp, http.StatusOK)
	events := decode(t, resp)["events"].([]any)
	last := events[len(events)-1].(map[string]any)
	if last["kind"] != "subtask_removed" || field(last, "data")["title"] != "Pack" {
		t.Fatalf("last event = %v", last)
	}

	resp = do(t, freelancer, http.MethodDelete, srv.URL+"/api/v1/tasks/"+parentID, nil)
	wantStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
}

func TestSubtasks_ArchiveTakesThemAlong(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")

	parent := newTask(t, srv, freelancer, map[string]any{"title": "Trip"})
	parentID := parent["id"].(string)
	newTask(t, srv, freelancer, map[string]any{"parent_id": parentID, "title": "Tickets"})

	resp := post(t, freelancer, srv.URL+"/api/v1/tasks/"+parentID+"/archive", nil)
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	if got := taskTitles(t, freelancer, srv.URL); len(got) != 0 {
		t.Fatalf("task list = %v", got)
	}
	if got := archiveTitles(t, srv, freelancer, ""); len(got) != 2 {
		t.Fatalf("archive = %v", got)
	}
	// Still listed under the parent, which takes no new ones.
	if got := subtaskTitles(t, srv, freelancer, parentID); len(got) != 1 {
		t.Fatalf("subtasks = %v", got)
	}
	resp = post(t, freelancer, srv.URL+"/api/v1/tasks", map[string]any{"parent_id": parentID, "title": "Late"})
	wantStatus(t, resp, http.StatusConflict)
	resp.Body.Close()
}

func TestSubtasks_ReportNamesTheParent(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	clientID := myID(t, srv, client)
	today := time.Now().Format("2006-01-02")

	parent := newTask(t, srv, freelancer, map[string]any{"client_id": clientID, "title": "Landing"})
	sub := newTask(t, srv, freelancer, map[string]any{"parent_id": parent["id"], "title": "Header", "price": "600"})
	resp := post(t, freelancer, srv.URL+"/api/v1/tasks/"+sub["id"].(string)+"/status", map[string]string{"status": "done"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// The subtask is billed on its own, while the parent is still open.
	resp = post(t, freelancer, srv.URL+"/api/v1/reports", map[string]string{"client_id": clientID, "from": today, "to": today})
	wantStatus(t, resp, http.StatusCreated)
	items := field(decode(t, resp), "report")["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items = %v", items)
	}
	item := items[0].(map[string]any)
	if item["title"] != "Header" || item["parent_title"] != "Landing" || item["price_cents"] != 60000.0 {
		t.Fatalf("item = %v", item)
	}
}

func TestSubtasks_MoveWithTheirParent(t *testing.T) {
	srv, _ := newAPI(t)
	first := signUpFreelancer(t, srv, "one@example.com")
	client := acceptNew(t, srv, invite(t, srv, first, "client@example.com"))
	second := signUpFreelancer(t, srv, "two@example.com")
	resp := acceptInvite(t, srv, newClient(), invite(t, srv, second, "client@example.com"), map[string]string{"password": "clientsecret1"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	firstID, secondID := myID(t, srv, first), myID(t, srv, second)

	both := createProject(t, srv, client, map[string]any{"freelancer_ids": []string{firstID, secondID}, "name": "Site"})
	onlyFirst := createProject(t, srv, client, map[string]any{"freelancer_ids": []string{firstID}, "name": "Blog"})
	bothID, onlyFirstID := both["id"].(string), onlyFirst["id"].(string)

	parent := newTask(t, srv, client, map[string]any{"project_id": bothID, "freelancer_id": firstID, "title": "Pages"})
	parentID := parent["id"].(string)
	// In a project the client hands a subtask to another freelancer, who
	// sees it but not the parent.
	sub := newTask(t, srv, client, map[string]any{"parent_id": parentID, "freelancer_id": secondID, "title": "Contact page"})
	if field(sub, "project")["id"] != bothID {
		t.Fatalf("subtask = %v", sub)
	}
	seen := getTask(t, srv, second, sub["id"].(string))
	if seen["parent_id"] != parentID || seen["parent"] != nil {
		t.Fatalf("the second freelancer sees %v", seen)
	}
	if counts := field(getTask(t, srv, first, parentID), "subtasks"); counts["total"] != 0.0 {
		t.Fatalf("the first freelancer counts %v", counts)
	}

	// A subtask doesn't leave its parent's project on its own.
	resp = do(t, client, http.MethodPut, srv.URL+"/api/v1/tasks/"+sub["id"].(string), map[string]any{"title": "Contact page", "project_id": onlyFirstID})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()
	// Nor does the parent go where its subtask's freelancer isn't.
	resp = do(t, client, http.MethodPut, srv.URL+"/api/v1/tasks/"+parentID, map[string]any{"title": "Pages", "project_id": onlyFirstID})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()

	// Out of projects the subtask follows, onto its own pair's board.
	resp = do(t, client, http.MethodPut, srv.URL+"/api/v1/tasks/"+parentID, map[string]any{"title": "Pages", "project_id": ""})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	moved := getTask(t, srv, second, sub["id"].(string))
	if moved["project"] != nil || moved["status"] != "new" {
		t.Fatalf("moved subtask = %v", moved)
	}
}
