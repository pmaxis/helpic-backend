package integration

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

type boardColumn struct {
	ID, Name, Category string
	Tasks              []string // titles
}

type boardView struct {
	CanManage bool
	Columns   []boardColumn
}

func getBoard(t *testing.T, srv *httptest.Server, c *http.Client, query string) boardView {
	t.Helper()
	resp := get(t, c, srv.URL+"/api/v1/board"+query)
	wantStatus(t, resp, http.StatusOK)
	body := decode(t, resp)
	out := boardView{CanManage: body["can_manage"] == true}
	for _, x := range body["columns"].([]any) {
		m := x.(map[string]any)
		col := boardColumn{ID: m["id"].(string), Name: m["name"].(string), Category: m["category"].(string)}
		for _, tk := range m["tasks"].([]any) {
			col.Tasks = append(col.Tasks, tk.(map[string]any)["title"].(string))
		}
		out.Columns = append(out.Columns, col)
	}
	return out
}

// backlogTitles lists the titles of the tasks waiting in the user's backlogs, in order.
func backlogTitles(t *testing.T, srv *httptest.Server, c *http.Client, query string) []string {
	t.Helper()
	resp := get(t, c, srv.URL+"/api/v1/tasks?backlog=1"+query)
	wantStatus(t, resp, http.StatusOK)
	out := []string{}
	for _, tk := range decode(t, resp)["tasks"].([]any) {
		out = append(out, tk.(map[string]any)["title"].(string))
	}
	return out
}

func column(b boardView, name string) boardColumn {
	for _, c := range b.Columns {
		if c.Name == name {
			return c
		}
	}
	return boardColumn{}
}

func newTask(t *testing.T, srv *httptest.Server, c *http.Client, body map[string]any) map[string]any {
	t.Helper()
	resp := post(t, c, srv.URL+"/api/v1/tasks", body)
	wantStatus(t, resp, http.StatusCreated)
	return field(decode(t, resp), "task")
}

func TestBoard_SharedWorkflow(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	freelancerID, clientID := myID(t, srv, freelancer), myID(t, srv, client)

	// Both sides open the same board; it starts with the three default statuses.
	fb := getBoard(t, srv, freelancer, "?with="+clientID)
	cb := getBoard(t, srv, client, "?with="+freelancerID)
	if len(fb.Columns) != 3 || fb.Columns[0].ID != cb.Columns[0].ID || !fb.CanManage || cb.CanManage {
		t.Fatalf("freelancer board = %+v, client board = %+v", fb, cb)
	}

	// The freelancer adds "Review"; the client can't touch the columns.
	resp := post(t, freelancer, srv.URL+"/api/v1/board/columns", map[string]string{"with": clientID, "name": "Review"})
	wantStatus(t, resp, http.StatusCreated)
	resp.Body.Close()
	resp = post(t, client, srv.URL+"/api/v1/board/columns", map[string]string{"with": freelancerID, "name": "Mine"})
	wantStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()
	review := column(getBoard(t, srv, client, "?with="+freelancerID), "Review")
	if review.ID == "" {
		t.Fatal("client doesn't see the Review column")
	}
	resp = do(t, client, http.MethodPut, srv.URL+"/api/v1/board/columns/"+review.ID, map[string]string{"name": "X"})
	wantStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()

	// A task moved to Review shows that status to both sides.
	task := newTask(t, srv, client, map[string]any{"freelancer_id": freelancerID, "title": "Logo"})
	if task["status_name"] != "New" {
		t.Fatalf("new task status = %v", task["status_name"])
	}
	resp = post(t, freelancer, srv.URL+"/api/v1/board/moves", map[string]string{"task_id": task["id"].(string), "column_id": review.ID})
	wantStatus(t, resp, http.StatusOK)
	moved := field(decode(t, resp), "task")
	if moved["status_name"] != "Review" || moved["status"] != "in_progress" {
		t.Fatalf("moved = %v", moved)
	}
	resp = get(t, client, srv.URL+"/api/v1/tasks/"+task["id"].(string))
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "task")["status_name"]; got != "Review" {
		t.Fatalf("client sees status %v", got)
	}

	// The client can't put it in Done.
	done := column(getBoard(t, srv, client, "?with="+freelancerID), "Done")
	resp = post(t, client, srv.URL+"/api/v1/board/moves", map[string]string{"task_id": task["id"].(string), "column_id": done.ID})
	wantStatus(t, resp, http.StatusConflict)
	resp.Body.Close()

	// Renaming a column renames the task's status and leaves its bucket.
	resp = do(t, freelancer, http.MethodPut, srv.URL+"/api/v1/board/columns/"+review.ID, map[string]string{"name": "QA"})
	wantStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
	resp = get(t, client, srv.URL+"/api/v1/tasks/"+task["id"].(string))
	wantStatus(t, resp, http.StatusOK)
	got := field(decode(t, resp), "task")
	if got["status_name"] != "QA" || got["status"] != "in_progress" {
		t.Fatalf("after renaming = %v", got)
	}

	// Deleting the column moves its tasks to the other column of the same bucket.
	resp = do(t, freelancer, http.MethodDelete, srv.URL+"/api/v1/board/columns/"+review.ID, nil)
	wantStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
	if got := column(getBoard(t, srv, freelancer, "?with="+clientID), "In progress").Tasks; len(got) != 1 || got[0] != "Logo" {
		t.Fatalf("In progress after delete = %v", got)
	}
	resp = do(t, freelancer, http.MethodDelete, srv.URL+"/api/v1/board/columns/"+done.ID, nil)
	wantStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
	resp = do(t, freelancer, http.MethodDelete, srv.URL+"/api/v1/board/columns/"+column(getBoard(t, srv, freelancer, "?with="+clientID), "In progress").ID, nil)
	wantStatus(t, resp, http.StatusConflict)
	resp.Body.Close()
}

func TestBoard_PersonalAndOrdering(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")

	b := getBoard(t, srv, freelancer, "?with=personal")
	todo := b.Columns[0]
	first := newTask(t, srv, freelancer, map[string]any{"title": "First"})
	second := newTask(t, srv, freelancer, map[string]any{"title": "Second", "status_id": todo.ID})

	// Put First below Second.
	resp := post(t, freelancer, srv.URL+"/api/v1/board/moves", map[string]string{
		"task_id": first["id"].(string), "column_id": todo.ID, "after_task_id": second["id"].(string),
	})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	if got := getBoard(t, srv, freelancer, "?with=personal").Columns[0].Tasks; len(got) != 2 || got[0] != "Second" || got[1] != "First" {
		t.Fatalf("order = %v", got)
	}

	// Creating straight into a column puts the task in that status.
	done := column(b, "Done")
	task := newTask(t, srv, freelancer, map[string]any{"title": "Already done", "status_id": done.ID})
	if task["status"] != "done" || task["status_id"] != done.ID {
		t.Fatalf("created in Done = %v", task)
	}

	// Reorder columns: Done first.
	ids := []string{done.ID}
	for _, c := range b.Columns {
		if c.ID != done.ID {
			ids = append(ids, c.ID)
		}
	}
	resp = do(t, freelancer, http.MethodPut, srv.URL+"/api/v1/board/columns", map[string]any{"ids": ids})
	wantStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
	if got := getBoard(t, srv, freelancer, "?with=personal").Columns[0].Name; got != "Done" {
		t.Fatalf("first column = %v", got)
	}

	// Someone else's board is off limits.
	other := signUpFreelancer(t, srv, "other@example.com")
	resp = do(t, other, http.MethodDelete, srv.URL+"/api/v1/board/columns/"+done.ID, nil)
	wantStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()
	resp = post(t, other, srv.URL+"/api/v1/tasks", map[string]any{"title": "Sneaky", "status_id": done.ID})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()
}

func TestBoard_DeletingColumns(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	b := getBoard(t, srv, freelancer, "?with=personal")

	del := func(name string, want int) {
		t.Helper()
		resp := do(t, freelancer, http.MethodDelete, srv.URL+"/api/v1/board/columns/"+column(b, name).ID, nil)
		wantStatus(t, resp, want)
		resp.Body.Close()
	}

	// A column with tasks and no other column of its bucket stays.
	task := newTask(t, srv, freelancer, map[string]any{"title": "Ship it", "status_id": column(b, "Done").ID})
	del("Done", http.StatusConflict)

	// With no "new" column left, new tasks go to the first column.
	del("New", http.StatusNoContent)
	if got := newTask(t, srv, freelancer, map[string]any{"title": "Next"}); got["status_name"] != "In progress" {
		t.Fatalf("new task went to %v", got["status_name"])
	}

	// Moving a task into a bucket the board has no column for is refused cleanly.
	resp := post(t, freelancer, srv.URL+"/api/v1/tasks/"+task["id"].(string)+"/status", map[string]string{"status": "new"})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()

	// The last column can't go.
	resp = post(t, freelancer, srv.URL+"/api/v1/board/moves", map[string]string{"task_id": task["id"].(string), "column_id": column(b, "In progress").ID})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	b = getBoard(t, srv, freelancer, "?with=personal")
	for _, c := range b.Columns {
		if c.Name != "In progress" {
			del(c.Name, http.StatusNoContent)
		}
	}
	resp = post(t, freelancer, srv.URL+"/api/v1/board/moves", map[string]string{"task_id": task["id"].(string), "column_id": column(b, "In progress").ID})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	del("In progress", http.StatusConflict)
}

func TestBoard_ProjectAuthor(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	freelancerID, clientID := myID(t, srv, freelancer), myID(t, srv, client)

	// The client starts the project, so they manage its columns and may file a task in any of them.
	owned := createProject(t, srv, client, map[string]any{"freelancer_id": freelancerID, "name": "Mobile app"})
	ownedID := owned["id"].(string)
	if !getBoard(t, srv, client, "?project="+ownedID).CanManage {
		t.Fatal("the project's author can't manage its columns")
	}
	resp := post(t, client, srv.URL+"/api/v1/board/columns", map[string]string{"project_id": ownedID, "name": "Review"})
	wantStatus(t, resp, http.StatusCreated)
	resp.Body.Close()
	board := getBoard(t, srv, client, "?project="+ownedID)
	review := column(board, "Review")
	if review.ID == "" {
		t.Fatal("Review column missing")
	}
	// The freelancer shares the workflow and sees the column on the pair's board.
	if column(getBoard(t, srv, freelancer, "?with="+clientID), "Review").ID == "" {
		t.Fatal("freelancer doesn't see the author's column")
	}
	// The freelancer works on it but didn't start it, so its columns aren't theirs to change.
	if getBoard(t, srv, freelancer, "?project="+ownedID).CanManage {
		t.Fatal("the freelancer manages a project the client started")
	}
	resp = post(t, freelancer, srv.URL+"/api/v1/board/columns", map[string]string{"project_id": ownedID, "name": "Mine"})
	wantStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()
	resp = do(t, freelancer, http.MethodPut, srv.URL+"/api/v1/board/columns/"+review.ID, map[string]string{"name": "Mine"})
	wantStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()
	resp = do(t, freelancer, http.MethodDelete, srv.URL+"/api/v1/board/columns/"+review.ID, nil)
	wantStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()

	resp = do(t, client, http.MethodPut, srv.URL+"/api/v1/board/columns/"+review.ID, map[string]string{"name": "QA"})
	wantStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()

	done := column(board, "Done")
	ids := []string{done.ID}
	for _, c := range board.Columns {
		if c.ID != done.ID {
			ids = append(ids, c.ID)
		}
	}
	resp = do(t, client, http.MethodPut, srv.URL+"/api/v1/board/columns", map[string]any{"ids": ids})
	wantStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
	if got := getBoard(t, srv, client, "?project="+ownedID).Columns[0].Name; got != "Done" {
		t.Fatalf("first column = %v", got)
	}

	task := newTask(t, srv, client, map[string]any{"project_id": ownedID, "title": "Ship it", "status_id": done.ID})
	if task["status"] != "done" || task["status_name"] != "Done" {
		t.Fatalf("created in Done = %v", task)
	}
	// A column of this project is not a status on the pair's board.
	resp = post(t, client, srv.URL+"/api/v1/tasks", map[string]any{"freelancer_id": freelancerID, "title": "Not mine", "status_id": done.ID})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()

	// Invited onto someone else's project, the client only moves cards.
	shared := createProject(t, srv, freelancer, map[string]any{"client_id": clientID, "name": "Website"})
	sharedID := shared["id"].(string)
	resp = post(t, freelancer, srv.URL+"/api/v1/projects/"+sharedID+"/participants", map[string]string{"user_id": clientID})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	if getBoard(t, srv, client, "?project="+sharedID).CanManage {
		t.Fatal("an invited client manages someone else's project")
	}
	resp = post(t, client, srv.URL+"/api/v1/board/columns", map[string]string{"project_id": sharedID, "name": "Mine"})
	wantStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()
	sharedDone := column(getBoard(t, srv, client, "?project="+sharedID), "Done")
	resp = post(t, client, srv.URL+"/api/v1/tasks", map[string]any{"project_id": sharedID, "title": "Nope", "status_id": sharedDone.ID})
	wantStatus(t, resp, http.StatusConflict)
	resp.Body.Close()
}

func TestBoard_ProjectsHaveTheirOwnWorkflow(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	freelancerID := myID(t, srv, freelancer)

	app := createProject(t, srv, client, map[string]any{"freelancer_id": freelancerID, "name": "Mobile app"})
	site := createProject(t, srv, client, map[string]any{"freelancer_id": freelancerID, "name": "Website"})
	appID, siteID := app["id"].(string), site["id"].(string)

	resp := post(t, client, srv.URL+"/api/v1/board/columns", map[string]string{"project_id": appID, "name": "QA"})
	wantStatus(t, resp, http.StatusCreated)
	resp.Body.Close()

	qa := column(getBoard(t, srv, client, "?project="+appID), "QA")
	if qa.Category != "in_progress" {
		t.Fatalf("a new status counts as %s", qa.Category)
	}
	if column(getBoard(t, srv, client, "?project="+siteID), "QA").ID != "" {
		t.Fatal("the other project picked up the column")
	}
	if column(getBoard(t, srv, client, "?project="+siteID), "New").ID == "" {
		t.Fatal("the other project has no statuses of its own")
	}

	task := newTask(t, srv, client, map[string]any{"project_id": appID, "title": "Check the copy", "status_id": qa.ID})
	resp = do(t, client, http.MethodPut, srv.URL+"/api/v1/board/columns/"+qa.ID, map[string]string{"name": "Shipped"})
	wantStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
	resp = get(t, client, srv.URL+"/api/v1/tasks/"+task["id"].(string))
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "task"); got["status"] != "in_progress" || got["status_name"] != "Shipped" {
		t.Fatalf("after renaming = %v", got)
	}

	resp = post(t, client, srv.URL+"/api/v1/board/columns", map[string]string{
		"project_id": appID, "name": "Won't do", "status_category": "done",
	})
	wantStatus(t, resp, http.StatusCreated)
	resp.Body.Close()
	wont := column(getBoard(t, srv, client, "?project="+appID), "Won't do")
	if wont.Category != "done" {
		t.Fatalf("Won't do counts as %s", wont.Category)
	}
	dropped := newTask(t, srv, client, map[string]any{"project_id": appID, "title": "Skip it", "status_id": wont.ID})
	if dropped["status"] != "done" {
		t.Fatalf("created in a done status = %v", dropped["status"])
	}
	resp = do(t, client, http.MethodPut, srv.URL+"/api/v1/board/columns/"+wont.ID, map[string]string{
		"name": "Later", "status_category": "todo",
	})
	wantStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
	resp = get(t, client, srv.URL+"/api/v1/tasks/"+dropped["id"].(string))
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "task"); got["status"] != "new" || got["status_name"] != "Later" {
		t.Fatalf("after the category became to do = %v", got)
	}

	resp = post(t, client, srv.URL+"/api/v1/board/columns", map[string]string{
		"project_id": appID, "name": "Nope", "status_category": "cancelled",
	})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()
}

func TestBoard_Backlog(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	freelancerID, clientID := myID(t, srv, freelancer), myID(t, srv, client)
	setBacklog := func(c *http.Client, id string, on bool) *http.Response {
		return do(t, c, http.MethodPut, srv.URL+"/api/v1/tasks/"+id+"/backlog", map[string]bool{"in_backlog": on})
	}

	// A task made straight into the backlog keeps its status but sits off the columns.
	idea := newTask(t, srv, client, map[string]any{"freelancer_id": freelancerID, "title": "Idea", "backlog": true})
	if idea["in_backlog"] != true || idea["status"] != "new" || idea["can_backlog"] != true {
		t.Fatalf("created in the backlog = %v", idea)
	}
	logo := newTask(t, srv, client, map[string]any{"freelancer_id": freelancerID, "title": "Logo"})
	b := getBoard(t, srv, freelancer, "?with="+clientID)
	if got := column(b, "New").Tasks; len(got) != 1 || got[0] != "Logo" {
		t.Fatalf("New = %v", got)
	}
	if got := backlogTitles(t, srv, freelancer, "&with="+clientID); len(got) != 1 || got[0] != "Idea" {
		t.Fatalf("backlog = %v", got)
	}

	// Either side puts a task into the backlog; it shows there for both.
	resp := setBacklog(client, logo["id"].(string), true)
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "task"); got["in_backlog"] != true {
		t.Fatalf("logo = %v", got)
	}
	if got := backlogTitles(t, srv, client, ""); len(got) != 2 {
		t.Fatalf("backlog after putting Logo away = %v", got)
	}
	if got := column(getBoard(t, srv, freelancer, "?with="+clientID), "New").Tasks; len(got) != 0 {
		t.Fatalf("New after putting Logo away = %v", got)
	}

	// The backlog lists the most urgent first; the whole list still has them all.
	resp = do(t, freelancer, http.MethodPut, srv.URL+"/api/v1/tasks/"+idea["id"].(string)+"/priority", map[string]string{"priority": "high"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	if got := backlogTitles(t, srv, freelancer, ""); len(got) != 2 || got[0] != "Idea" || got[1] != "Logo" {
		t.Fatalf("backlog order = %v", got)
	}
	resp = get(t, client, srv.URL+"/api/v1/tasks")
	wantStatus(t, resp, http.StatusOK)
	if n := len(decode(t, resp)["tasks"].([]any)); n != 2 {
		t.Fatalf("all tasks = %d, want 2", n)
	}

	// Moving a card into a column takes it out of the backlog, the same column included.
	newCol := column(b, "New")
	resp = post(t, client, srv.URL+"/api/v1/board/moves", map[string]string{"task_id": logo["id"].(string), "column_id": newCol.ID})
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "task"); got["in_backlog"] != false {
		t.Fatalf("moved back = %v", got)
	}
	inProgress := column(b, "In progress")
	resp = post(t, freelancer, srv.URL+"/api/v1/board/moves", map[string]string{"task_id": idea["id"].(string), "column_id": inProgress.ID})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	got := getBoard(t, srv, client, "?with="+freelancerID)
	if len(backlogTitles(t, srv, client, "")) != 0 || len(column(got, "New").Tasks) != 1 || len(column(got, "In progress").Tasks) != 1 {
		t.Fatalf("after taking both out = %+v", got)
	}

	// Done work closes the backlog: a done task can't go there, and a task in
	// the backlog that's marked done leaves it.
	resp = setBacklog(freelancer, idea["id"].(string), true)
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	resp = post(t, freelancer, srv.URL+"/api/v1/tasks/"+idea["id"].(string)+"/status", map[string]string{"status": "done"})
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "task"); got["in_backlog"] != false || got["can_backlog"] != false {
		t.Fatalf("done = %v", got)
	}
	resp = setBacklog(freelancer, idea["id"].(string), true)
	wantStatus(t, resp, http.StatusConflict)
	resp.Body.Close()

	// Someone who doesn't see the task can't move it.
	other := signUpFreelancer(t, srv, "other@example.com")
	resp = setBacklog(other, logo["id"].(string), true)
	wantStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()

	// The history keeps both moves.
	resp = get(t, client, srv.URL+"/api/v1/tasks/"+logo["id"].(string)+"/history")
	wantStatus(t, resp, http.StatusOK)
	n := 0
	for _, e := range decode(t, resp)["events"].([]any) {
		if e.(map[string]any)["kind"] == "backlog" {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("backlog events = %d, want 2", n)
	}
}
