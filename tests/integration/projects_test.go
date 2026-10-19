package integration

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func createProject(t *testing.T, srv *httptest.Server, c *http.Client, body map[string]any) map[string]any {
	t.Helper()
	resp := post(t, c, srv.URL+"/api/v1/projects", body)
	wantStatus(t, resp, http.StatusCreated)
	return field(decode(t, resp), "project")
}

func TestProjects_SharedAndPersonal(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	clientID := myID(t, srv, client)

	shared := createProject(t, srv, freelancer, map[string]any{"client_id": clientID, "name": "Website"})
	personal := createProject(t, srv, freelancer, map[string]any{"name": "Portfolio"})
	if personal["client"] != nil || shared["client"] == nil {
		t.Fatalf("shared = %v, personal = %v", shared, personal)
	}
	fromClient := createProject(t, srv, client, map[string]any{"freelancer_id": myID(t, srv, freelancer), "name": "Mobile app"})

	// Each person sees the projects they started. Choosing a freelancer puts them
	// on the project. The client joins a freelancer's project when invited.
	resp := get(t, freelancer, srv.URL+"/api/v1/projects")
	wantStatus(t, resp, http.StatusOK)
	if list, _ := decode(t, resp)["projects"].([]any); len(list) != 3 {
		t.Fatalf("freelancer projects = %d, want 3", len(list))
	}
	resp = get(t, client, srv.URL+"/api/v1/projects")
	wantStatus(t, resp, http.StatusOK)
	if list, _ := decode(t, resp)["projects"].([]any); len(list) != 1 {
		t.Fatalf("client projects = %d, want 1", len(list))
	}
	wantStatus(t, get(t, client, srv.URL+"/api/v1/projects/"+personal["id"].(string)), http.StatusNotFound)
	wantStatus(t, get(t, client, srv.URL+"/api/v1/projects/"+shared["id"].(string)), http.StatusNotFound)
	wantStatus(t, get(t, freelancer, srv.URL+"/api/v1/projects/"+fromClient["id"].(string)), http.StatusOK)

	resp = post(t, freelancer, srv.URL+"/api/v1/projects/"+shared["id"].(string)+"/participants", map[string]string{"user_id": clientID})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	wantStatus(t, get(t, client, srv.URL+"/api/v1/projects/"+shared["id"].(string)), http.StatusOK)

	// Only the creator changes a project.
	resp = do(t, client, http.MethodPut, srv.URL+"/api/v1/projects/"+shared["id"].(string), map[string]any{"name": "Hijack"})
	wantStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()
	resp = do(t, client, http.MethodPut, srv.URL+"/api/v1/projects/"+fromClient["id"].(string), map[string]any{"name": "Mobile app v2"})
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "project")["name"]; got != "Mobile app v2" {
		t.Fatalf("name = %v", got)
	}
}

func TestProjects_TasksFollowTheProject(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	clientID := myID(t, srv, client)

	shared := createProject(t, srv, freelancer, map[string]any{"client_id": clientID, "name": "Website"})
	personal := createProject(t, srv, freelancer, map[string]any{"name": "Portfolio"})
	sharedID, personalID := shared["id"].(string), personal["id"].(string)

	// A client's task in the project needs no freelancer_id: the project names it.
	resp := post(t, client, srv.URL+"/api/v1/tasks", map[string]any{"project_id": sharedID, "title": "Pricing page"})
	wantStatus(t, resp, http.StatusCreated)
	task := field(decode(t, resp), "task")
	if field(task, "project")["name"] != "Website" || field(task, "freelancer")["email"] != "free@example.com" {
		t.Fatalf("task = %v", task)
	}

	// A freelancer's task in a personal project is personal.
	resp = post(t, freelancer, srv.URL+"/api/v1/tasks", map[string]any{"project_id": personalID, "title": "Case study"})
	wantStatus(t, resp, http.StatusCreated)
	personalTask := field(decode(t, resp), "task")
	if personalTask["client"] != nil {
		t.Fatalf("task in a personal project has client %v", personalTask["client"])
	}

	// The client can't put a task into a project they aren't part of.
	resp = post(t, client, srv.URL+"/api/v1/tasks", map[string]any{"project_id": personalID, "title": "Nope"})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()

	// Moving a task to another pair's project is refused; out of a project works.
	ptID := personalTask["id"].(string)
	resp = do(t, freelancer, http.MethodPut, srv.URL+"/api/v1/tasks/"+ptID, map[string]any{"title": "Case study", "project_id": sharedID})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()
	resp = do(t, freelancer, http.MethodPut, srv.URL+"/api/v1/tasks/"+ptID, map[string]any{"title": "Case study", "project_id": ""})
	wantStatus(t, resp, http.StatusOK)
	if field(decode(t, resp), "task")["project"] != nil {
		t.Fatal("task still in a project")
	}

	resp = get(t, freelancer, srv.URL+"/api/v1/tasks?project="+sharedID)
	wantStatus(t, resp, http.StatusOK)
	if list, _ := decode(t, resp)["tasks"].([]any); len(list) != 1 {
		t.Fatalf("project tasks = %v, want 1", list)
	}
	resp = get(t, freelancer, srv.URL+"/api/v1/projects/"+sharedID)
	wantStatus(t, resp, http.StatusOK)
	if p := field(decode(t, resp), "project"); p["open_tasks"] != float64(1) || p["total_tasks"] != float64(1) {
		t.Fatalf("project counts = %v", p)
	}

	// Deleting the project keeps its tasks.
	resp = do(t, freelancer, http.MethodDelete, srv.URL+"/api/v1/projects/"+sharedID, nil)
	wantStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
	resp = get(t, client, srv.URL+"/api/v1/tasks")
	wantStatus(t, resp, http.StatusOK)
	list, _ := decode(t, resp)["tasks"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["project"] != nil {
		t.Fatalf("tasks after deleting the project = %v", list)
	}
}

func TestProjects_EachFreelancerSeesTheirTasks(t *testing.T) {
	srv, _ := newAPI(t)
	first := signUpFreelancer(t, srv, "one@example.com")
	client := acceptNew(t, srv, invite(t, srv, first, "client@example.com"))
	second := signUpFreelancer(t, srv, "two@example.com")
	resp := acceptInvite(t, srv, newClient(), invite(t, srv, second, "client@example.com"), map[string]string{"password": "clientsecret1"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	firstID, secondID := myID(t, srv, first), myID(t, srv, second)

	project := createProject(t, srv, client, map[string]any{"freelancer_id": firstID, "name": "Site"})
	projectID := project["id"].(string)
	var onIt bool
	for _, p := range project["participants"].([]any) {
		person := p.(map[string]any)
		if person["id"] == firstID && person["role"] == "freelancer" {
			onIt = true
		}
	}
	if !onIt {
		t.Fatalf("chosen freelancer is not a participant: %v", project["participants"])
	}

	// A freelancer who isn't on the project is not attached to a task.
	resp = post(t, client, srv.URL+"/api/v1/tasks", map[string]any{"project_id": projectID, "freelancer_id": secondID, "title": "Not theirs"})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()

	resp = post(t, client, srv.URL+"/api/v1/projects/"+projectID+"/participants", map[string]string{"user_id": secondID})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// With two freelancers the task has to name one.
	resp = post(t, client, srv.URL+"/api/v1/tasks", map[string]any{"project_id": projectID, "title": "Whose?"})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()

	newTask(t, srv, client, map[string]any{"project_id": projectID, "freelancer_id": firstID, "title": "One"})
	newTask(t, srv, client, map[string]any{"project_id": projectID, "freelancer_id": secondID, "title": "Two"})

	if got := taskTitles(t, first, srv.URL); len(got) != 1 || got[0] != "One" {
		t.Fatalf("first freelancer sees %v", got)
	}
	if got := taskTitles(t, second, srv.URL); len(got) != 1 || got[0] != "Two" {
		t.Fatalf("second freelancer sees %v", got)
	}
	if got := taskTitles(t, client, srv.URL); len(got) != 2 {
		t.Fatalf("client sees %v, want both", got)
	}
}

func TestProjects_ClientStartsWithSeveralFreelancers(t *testing.T) {
	srv, _ := newAPI(t)
	first := signUpFreelancer(t, srv, "one@example.com")
	client := acceptNew(t, srv, invite(t, srv, first, "client@example.com"))
	second := signUpFreelancer(t, srv, "two@example.com")
	resp := acceptInvite(t, srv, newClient(), invite(t, srv, second, "client@example.com"), map[string]string{"password": "clientsecret1"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	firstID, secondID := myID(t, srv, first), myID(t, srv, second)

	project := createProject(t, srv, client, map[string]any{"freelancer_ids": []string{firstID, secondID}, "name": "Site"})
	got := map[string]bool{}
	for _, p := range project["participants"].([]any) {
		person := p.(map[string]any)
		if person["role"] == "freelancer" {
			got[person["id"].(string)] = true
		}
	}
	if !got[firstID] || !got[secondID] {
		t.Fatalf("participants = %v", project["participants"])
	}
	wantStatus(t, get(t, first, srv.URL+"/api/v1/projects/"+project["id"].(string)), http.StatusOK)
	wantStatus(t, get(t, second, srv.URL+"/api/v1/projects/"+project["id"].(string)), http.StatusOK)
}

func TestProjects_ClientAddsSeveralFreelancers(t *testing.T) {
	srv, _ := newAPI(t)
	first := signUpFreelancer(t, srv, "one@example.com")
	client := acceptNew(t, srv, invite(t, srv, first, "client@example.com"))
	second := signUpFreelancer(t, srv, "two@example.com")
	resp := acceptInvite(t, srv, newClient(), invite(t, srv, second, "client@example.com"), map[string]string{"password": "clientsecret1"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	third := signUpFreelancer(t, srv, "three@example.com")
	resp = acceptInvite(t, srv, newClient(), invite(t, srv, third, "client@example.com"), map[string]string{"password": "clientsecret1"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	firstID, secondID, thirdID := myID(t, srv, first), myID(t, srv, second), myID(t, srv, third)

	project := createProject(t, srv, client, map[string]any{"freelancer_id": firstID, "name": "Site"})
	projectID := project["id"].(string)
	resp = post(t, client, srv.URL+"/api/v1/projects/"+projectID+"/participants", map[string]any{"user_ids": []string{secondID, thirdID}})
	wantStatus(t, resp, http.StatusOK)
	body := decode(t, resp)
	got := map[string]bool{}
	for _, p := range body["participants"].([]any) {
		person := p.(map[string]any)
		if person["role"] == "freelancer" {
			got[person["id"].(string)] = true
		}
	}
	if !got[firstID] || !got[secondID] || !got[thirdID] {
		t.Fatalf("participants = %v", body["participants"])
	}
	wantStatus(t, get(t, second, srv.URL+"/api/v1/projects/"+projectID), http.StatusOK)
	wantStatus(t, get(t, third, srv.URL+"/api/v1/projects/"+projectID), http.StatusOK)
}

func TestProjects_ClientChangesTaskFreelancer(t *testing.T) {
	srv, _ := newAPI(t)
	first := signUpFreelancer(t, srv, "one@example.com")
	client := acceptNew(t, srv, invite(t, srv, first, "client@example.com"))
	second := signUpFreelancer(t, srv, "two@example.com")
	resp := acceptInvite(t, srv, newClient(), invite(t, srv, second, "client@example.com"), map[string]string{"password": "clientsecret1"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	firstID, secondID := myID(t, srv, first), myID(t, srv, second)
	project := createProject(t, srv, client, map[string]any{"freelancer_ids": []string{firstID, secondID}, "name": "Site"})
	task := newTask(t, srv, client, map[string]any{"project_id": project["id"], "freelancer_id": firstID, "title": "Logo"})
	taskURL := srv.URL + "/api/v1/tasks/" + task["id"].(string)

	resp = do(t, client, http.MethodPut, taskURL+"/freelancer", map[string]string{"freelancer_id": secondID})
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "task")["freelancer"].(map[string]any)["id"]; got != secondID {
		t.Fatalf("freelancer = %v", got)
	}
	if got := taskTitles(t, first, srv.URL); len(got) != 0 {
		t.Fatalf("first freelancer still sees %v", got)
	}
	if got := taskTitles(t, second, srv.URL); len(got) != 1 || got[0] != "Logo" {
		t.Fatalf("second freelancer sees %v", got)
	}

	resp = do(t, client, http.MethodPut, taskURL+"/freelancer", map[string]string{"freelancer_id": ""})
	wantStatus(t, resp, http.StatusOK)
	if field(decode(t, resp), "task")["freelancer"] != nil {
		t.Fatal("freelancer still assigned")
	}
	if got := taskTitles(t, client, srv.URL); len(got) != 1 {
		t.Fatalf("client sees %v", got)
	}
	resp = do(t, client, http.MethodDelete, taskURL+"/participants/"+myID(t, srv, client), nil)
	wantStatus(t, resp, http.StatusConflict)
	resp.Body.Close()
}
