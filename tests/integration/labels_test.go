package integration

import (
	"net/http"
	"testing"
)

func TestPriorityAndLabels(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	clientID, freelancerID := myID(t, srv, client), myID(t, srv, freelancer)

	// Priority comes with the task, and either side changes it.
	task := newTask(t, srv, client, map[string]any{"freelancer_id": freelancerID, "title": "Logo", "priority": "high"})
	taskURL := srv.URL + "/api/v1/tasks/" + task["id"].(string)
	if task["priority"] != "high" || len(task["labels"].([]any)) != 0 {
		t.Fatalf("new task = %v", task)
	}
	if plain := newTask(t, srv, freelancer, map[string]any{"client_id": clientID, "title": "Copy"}); plain["priority"] != "none" {
		t.Fatalf("default priority = %v", plain["priority"])
	}
	resp := do(t, freelancer, http.MethodPut, taskURL+"/priority", map[string]string{"priority": "urgent"})
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "task"); got["priority"] != "urgent" {
		t.Fatalf("after priority = %v", got)
	}
	resp = do(t, client, http.MethodPut, taskURL+"/priority", map[string]string{"priority": "asap"})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()

	// Both sides make labels on their board; names are unique whatever the case.
	labelsURL := srv.URL + "/api/v1/board/labels"
	resp = post(t, freelancer, labelsURL, map[string]string{"with": clientID, "name": "  Design  ", "color": "violet"})
	wantStatus(t, resp, http.StatusCreated)
	design := field(decode(t, resp), "label")
	resp = post(t, client, labelsURL, map[string]string{"with": freelancerID, "name": "Bug", "color": "red"})
	wantStatus(t, resp, http.StatusCreated)
	bug := field(decode(t, resp), "label")
	resp = post(t, client, labelsURL, map[string]string{"with": freelancerID, "name": "design", "color": "blue"})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()
	resp = post(t, client, labelsURL, map[string]string{"with": freelancerID, "name": "Ok", "color": "neon"})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()
	if design["name"] != "Design" {
		t.Fatalf("label = %v", design)
	}

	// A personal label can't go on a shared task.
	resp = post(t, freelancer, labelsURL, map[string]string{"with": "personal", "name": "Mine", "color": "gray"})
	wantStatus(t, resp, http.StatusCreated)
	mine := field(decode(t, resp), "label")
	resp = do(t, freelancer, http.MethodPut, taskURL+"/labels", map[string]any{"label_ids": []any{mine["id"]}})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()

	// The client tags the task; it comes back sorted by name.
	resp = do(t, client, http.MethodPut, taskURL+"/labels", map[string]any{"label_ids": []any{design["id"], bug["id"], bug["id"]}})
	wantStatus(t, resp, http.StatusOK)
	labels := field(decode(t, resp), "task")["labels"].([]any)
	if len(labels) != 2 || labels[0].(map[string]any)["name"] != "Bug" || labels[1].(map[string]any)["color"] != "violet" {
		t.Fatalf("task labels = %v", labels)
	}
	resp = get(t, freelancer, labelsURL+"?with="+clientID)
	wantStatus(t, resp, http.StatusOK)
	if list, _ := decode(t, resp)["labels"].([]any); len(list) != 2 || list[0].(map[string]any)["task_count"] != float64(1) {
		t.Fatalf("board labels = %v", list)
	}

	// Renaming shows on the task; deleting takes the label off it.
	resp = do(t, freelancer, http.MethodPut, labelsURL+"/"+bug["id"].(string), map[string]string{"name": "Fix", "color": "orange"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	resp = do(t, client, http.MethodDelete, labelsURL+"/"+design["id"].(string), nil)
	wantStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
	resp = get(t, client, taskURL)
	wantStatus(t, resp, http.StatusOK)
	labels = field(decode(t, resp), "task")["labels"].([]any)
	if len(labels) != 1 || labels[0].(map[string]any)["name"] != "Fix" {
		t.Fatalf("labels after rename and delete = %v", labels)
	}

	// Someone else's labels stay out of reach.
	outsider := signUpFreelancer(t, srv, "other@example.com")
	resp = do(t, outsider, http.MethodDelete, labelsURL+"/"+bug["id"].(string), nil)
	wantStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()

	// The history has both kinds of change.
	resp = get(t, freelancer, taskURL+"/history")
	wantStatus(t, resp, http.StatusOK)
	kinds := map[string]bool{}
	for _, e := range decode(t, resp)["events"].([]any) {
		kinds[e.(map[string]any)["kind"].(string)] = true
	}
	if !kinds["priority"] || !kinds["labels"] {
		t.Fatalf("history kinds = %v", kinds)
	}
}
