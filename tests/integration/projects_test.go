package integration

import (
	"net/http"
	"net/http/httptest"
	"strings"
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

// sharedProject is a client's project with two freelancers, each linked with
// the client.
type sharedProject struct {
	client, first, second           *http.Client
	clientID, firstID, secondID, id string
}

func newSharedProject(t *testing.T, srv *httptest.Server) sharedProject {
	t.Helper()
	first := signUpFreelancer(t, srv, "one@example.com")
	client := acceptNew(t, srv, invite(t, srv, first, "client@example.com"))
	second := signUpFreelancer(t, srv, "two@example.com")
	resp := acceptInvite(t, srv, newClient(), invite(t, srv, second, "client@example.com"), map[string]string{"password": "clientsecret1"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	p := sharedProject{client: client, first: first, second: second,
		clientID: myID(t, srv, client), firstID: myID(t, srv, first), secondID: myID(t, srv, second)}
	p.id = createProject(t, srv, client, map[string]any{"freelancer_ids": []string{p.firstID, p.secondID}, "name": "Site"})["id"].(string)
	return p
}

func TestProjects_TaskWaitingForFreelancer(t *testing.T) {
	srv, _ := newAPI(t)
	p := newSharedProject(t, srv)
	task := newTask(t, srv, p.client, map[string]any{"project_id": p.id, "freelancer_id": p.firstID, "title": "Logo"})
	taskURL := srv.URL + "/api/v1/tasks/" + task["id"].(string)
	resp := do(t, p.client, http.MethodPut, taskURL+"/freelancer", map[string]string{"freelancer_id": ""})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// It is still edited, moved on the project's board and labelled with the
	// project board's labels.
	resp = do(t, p.client, http.MethodPut, taskURL, map[string]any{"title": "Logo v2", "description": "Bolder"})
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "task")["title"]; got != "Logo v2" {
		t.Fatalf("title = %v", got)
	}
	inProgress := column(getBoard(t, srv, p.client, "?project="+p.id), "In progress")
	resp = post(t, p.client, srv.URL+"/api/v1/board/moves", map[string]string{"task_id": task["id"].(string), "column_id": inProgress.ID})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	resp = post(t, p.client, srv.URL+"/api/v1/board/labels", map[string]string{"project_id": p.id, "name": "Brand", "color": "teal"})
	wantStatus(t, resp, http.StatusCreated)
	label := field(decode(t, resp), "label")
	resp = do(t, p.client, http.MethodPut, taskURL+"/labels", map[string]any{"label_ids": []any{label["id"]}})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// Off the project it would have no board: not taken out, and the project
	// isn't deleted from under it.
	resp = do(t, p.client, http.MethodPut, taskURL, map[string]any{"title": "Logo v2", "project_id": ""})
	wantStatus(t, resp, http.StatusConflict)
	resp.Body.Close()
	resp = do(t, p.client, http.MethodDelete, srv.URL+"/api/v1/projects/"+p.id, nil)
	wantStatus(t, resp, http.StatusConflict)
	resp.Body.Close()

	// Once someone does it, the project goes and the task stays.
	resp = do(t, p.client, http.MethodPut, taskURL+"/freelancer", map[string]string{"freelancer_id": p.secondID})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	resp = do(t, p.client, http.MethodDelete, srv.URL+"/api/v1/projects/"+p.id, nil)
	wantStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
	if got := taskTitles(t, p.second, srv.URL); len(got) != 1 || got[0] != "Logo v2" {
		t.Fatalf("second freelancer sees %v", got)
	}
}

func TestProjects_TaskOutsideProjectKeepsFreelancer(t *testing.T) {
	srv, _ := newAPI(t)
	p := newSharedProject(t, srv)
	task := newTask(t, srv, p.client, map[string]any{"freelancer_id": p.firstID, "title": "Logo"})
	resp := do(t, p.client, http.MethodPut, srv.URL+"/api/v1/tasks/"+task["id"].(string)+"/freelancer", map[string]string{"freelancer_id": ""})
	wantStatus(t, resp, http.StatusConflict)
	resp.Body.Close()
	// Swapping for another linked freelancer is fine.
	resp = do(t, p.client, http.MethodPut, srv.URL+"/api/v1/tasks/"+task["id"].(string)+"/freelancer", map[string]string{"freelancer_id": p.secondID})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
}

func TestProjects_OnlyClientSideAssigns(t *testing.T) {
	srv, _ := newAPI(t)
	p := newSharedProject(t, srv)
	task := newTask(t, srv, p.first, map[string]any{"project_id": p.id, "title": "Logo", "price": "500"})
	taskURL := srv.URL + "/api/v1/tasks/" + task["id"].(string)

	// The freelancer doesn't hand their task to someone else.
	resp := do(t, p.first, http.MethodPut, taskURL+"/freelancer", map[string]string{"freelancer_id": p.secondID})
	wantStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()

	// The client does, and the first freelancer's price doesn't go with it.
	resp = do(t, p.client, http.MethodPut, taskURL+"/freelancer", map[string]string{"freelancer_id": p.secondID})
	wantStatus(t, resp, http.StatusOK)
	got := field(decode(t, resp), "task")
	if got["freelancer"].(map[string]any)["id"] != p.secondID || got["price_cents"] != nil {
		t.Fatalf("reassigned task = %v", got)
	}
}

func TestProjects_LabelsOnSharedProject(t *testing.T) {
	srv, _ := newAPI(t)
	p := newSharedProject(t, srv)
	task := newTask(t, srv, p.client, map[string]any{"project_id": p.id, "freelancer_id": p.secondID, "title": "Logo"})
	labelsURL := srv.URL + "/api/v1/board/labels"

	// The project's board shows the project pair's labels; they go on any
	// task of the project, the second freelancer's too.
	resp := post(t, p.client, labelsURL, map[string]string{"project_id": p.id, "name": "Brand", "color": "teal"})
	wantStatus(t, resp, http.StatusCreated)
	label := field(decode(t, resp), "label")
	resp = do(t, p.second, http.MethodPut, srv.URL+"/api/v1/tasks/"+task["id"].(string)+"/labels", map[string]any{"label_ids": []any{label["id"]}})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// But the labels are the first freelancer's pair's: the second one doesn't
	// change them.
	resp = post(t, p.second, labelsURL, map[string]string{"project_id": p.id, "name": "Mine", "color": "red"})
	wantStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()
}

func TestProjects_MoveKeepsOtherFreelancersOrder(t *testing.T) {
	srv, _ := newAPI(t)
	p := newSharedProject(t, srv)
	a1 := newTask(t, srv, p.client, map[string]any{"project_id": p.id, "freelancer_id": p.firstID, "title": "A1"})
	b1 := newTask(t, srv, p.client, map[string]any{"project_id": p.id, "freelancer_id": p.secondID, "title": "B1"})
	a2 := newTask(t, srv, p.client, map[string]any{"project_id": p.id, "freelancer_id": p.firstID, "title": "A2"})
	newCol := column(getBoard(t, srv, p.client, "?project="+p.id), "New")
	move := func(c *http.Client, task map[string]any, after string) {
		t.Helper()
		resp := post(t, c, srv.URL+"/api/v1/board/moves", map[string]string{"task_id": task["id"].(string), "column_id": newCol.ID, "after_task_id": after})
		wantStatus(t, resp, http.StatusOK)
		resp.Body.Close()
	}
	move(p.client, a1, "")
	move(p.client, b1, a1["id"].(string))
	move(p.client, a2, b1["id"].(string))

	// The first freelancer sees only A1 and A2, and puts A2 right after A1:
	// B1, which they don't see, keeps its place after it.
	move(p.first, a2, a1["id"].(string))
	if got := column(getBoard(t, srv, p.client, "?project="+p.id), "New").Tasks; strings.Join(got, ",") != "A1,A2,B1" {
		t.Fatalf("column = %v", got)
	}
}

// freelancerIDs are the freelancers among the people in list.
func freelancerIDs(list any) map[string]bool {
	got := map[string]bool{}
	for _, p := range list.([]any) {
		person := p.(map[string]any)
		if person["role"] == "freelancer" {
			got[person["id"].(string)] = true
		}
	}
	return got
}

func TestProjects_FreelancersDontSeeEachOther(t *testing.T) {
	srv, _ := newAPI(t)
	p := newSharedProject(t, srv)
	projectURL := srv.URL + "/api/v1/projects/" + p.id
	task := newTask(t, srv, p.client, map[string]any{"project_id": p.id, "freelancer_id": p.firstID, "title": "Logo"})
	taskURL := srv.URL + "/api/v1/tasks/" + task["id"].(string)

	// On the project, in its list and its people, a freelancer sees only themself.
	for _, who := range []struct {
		client *http.Client
		id     string
	}{{p.first, p.firstID}, {p.second, p.secondID}} {
		project := field(decode(t, get(t, who.client, projectURL)), "project")
		if got := freelancerIDs(project["participants"]); len(got) != 1 || !got[who.id] {
			t.Fatalf("project participants = %v", project["participants"])
		}
		people := decode(t, get(t, who.client, projectURL+"/people"))
		if got := freelancerIDs(people["participants"]); len(got) != 1 || !got[who.id] {
			t.Fatalf("project people = %v", people["participants"])
		}
		list := decode(t, get(t, who.client, srv.URL+"/api/v1/projects"))["projects"].([]any)
		if got := freelancerIDs(list[0].(map[string]any)["participants"]); len(got) != 1 || !got[who.id] {
			t.Fatalf("listed participants = %v", got)
		}
	}

	// On a task, its freelancer isn't offered the others who could take it.
	if got := freelancerIDs(decode(t, get(t, p.first, taskURL+"/people"))["people"]); len(got) != 1 || !got[p.firstID] {
		t.Fatalf("task people for the freelancer = %v", got)
	}

	// The client, who picks the freelancers, sees them all.
	project := field(decode(t, get(t, p.client, projectURL)), "project")
	if got := freelancerIDs(project["participants"]); !got[p.firstID] || !got[p.secondID] {
		t.Fatalf("client's project participants = %v", project["participants"])
	}
	if got := freelancerIDs(decode(t, get(t, p.client, taskURL+"/people"))["people"]); !got[p.firstID] || !got[p.secondID] {
		t.Fatalf("client's task people = %v", got)
	}
}

func TestProjects_FreelancerSeesTheAuthorFreelancer(t *testing.T) {
	srv, _ := newAPI(t)
	first := signUpFreelancer(t, srv, "one@example.com")
	client := acceptNew(t, srv, invite(t, srv, first, "client@example.com"))
	second := signUpFreelancer(t, srv, "two@example.com")
	resp := acceptInvite(t, srv, newClient(), invite(t, srv, second, "client@example.com"), map[string]string{"password": "clientsecret1"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	firstID, secondID := myID(t, srv, first), myID(t, srv, second)

	// The first freelancer starts the project and brings the second onto it.
	project := createProject(t, srv, first, map[string]any{"client_id": myID(t, srv, client), "name": "Site"})
	projectURL := srv.URL + "/api/v1/projects/" + project["id"].(string)
	resp = post(t, first, projectURL+"/participants", map[string]any{"user_ids": []string{secondID}})
	wantStatus(t, resp, http.StatusOK)
	if got := freelancerIDs(decode(t, resp)["participants"]); !got[firstID] || !got[secondID] {
		t.Fatalf("author's participants = %v", got)
	}

	// The second sees who started it, and themself.
	people := decode(t, get(t, second, projectURL+"/people"))
	if got := freelancerIDs(people["participants"]); len(got) != 2 || !got[firstID] || !got[secondID] {
		t.Fatalf("participants = %v", people["participants"])
	}
}
