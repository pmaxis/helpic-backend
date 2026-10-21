package integration

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// archivePage reads one page of the user's archive: its titles and the total.
func archivePage(t *testing.T, srv *httptest.Server, c *http.Client, query string) ([]string, int) {
	t.Helper()
	resp := get(t, c, srv.URL+"/api/v1/archive"+query)
	wantStatus(t, resp, http.StatusOK)
	body := decode(t, resp)
	titles := []string{}
	for _, x := range body["tasks"].([]any) {
		titles = append(titles, x.(map[string]any)["title"].(string))
	}
	return titles, int(body["total"].(float64))
}

// archiveTitles is the first page of the archive, optionally filtered.
func archiveTitles(t *testing.T, srv *httptest.Server, c *http.Client, query string) []string {
	t.Helper()
	titles, _ := archivePage(t, srv, c, query)
	return titles
}

// paidTask is a task of a report the client has paid, with a comment and a
// file on it from before.
func paidTask(t *testing.T, srv *httptest.Server, freelancer, client *http.Client) (taskURL, commentID, fileID string) {
	t.Helper()
	clientID := myID(t, srv, client)
	today := time.Now().Format("2006-01-02")
	task := newTask(t, srv, freelancer, map[string]any{"client_id": clientID, "title": "Logo"})
	taskURL = srv.URL + "/api/v1/tasks/" + task["id"].(string)

	resp := post(t, client, srv.URL+"/api/v1/comments?task="+task["id"].(string), map[string]string{"body": "Looks good"})
	wantStatus(t, resp, http.StatusCreated)
	commentID = field(decode(t, resp), "comment")["id"].(string)
	resp = upload(t, srv, freelancer, task["id"].(string), "logo.png", pngBytes)
	wantStatus(t, resp, http.StatusCreated)
	fileID = field(decode(t, resp), "attachment")["id"].(string)

	resp = post(t, freelancer, taskURL+"/status", map[string]string{"status": "done"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	resp = post(t, freelancer, srv.URL+"/api/v1/reports", map[string]string{"client_id": clientID, "from": today, "to": today})
	wantStatus(t, resp, http.StatusCreated)
	reportURL := srv.URL + "/api/v1/reports/" + field(decode(t, resp), "report")["id"].(string)
	resp = post(t, freelancer, reportURL+"/send", nil)
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	resp = post(t, client, reportURL+"/paid", nil)
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	return taskURL, commentID, fileID
}

func TestArchive_PaidTaskIsClosed(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	taskURL, commentID, fileID := paidTask(t, srv, freelancer, client)
	taskID := taskURL[len(srv.URL+"/api/v1/tasks/"):]

	// Off the task lists, in the archive, and still readable.
	if got := taskTitles(t, freelancer, srv.URL); len(got) != 0 {
		t.Fatalf("task list = %v", got)
	}
	for _, c := range []*http.Client{freelancer, client} {
		if got := archiveTitles(t, srv, c, ""); len(got) != 1 || got[0] != "Logo" {
			t.Fatalf("archive = %v", got)
		}
	}
	resp := get(t, client, taskURL)
	wantStatus(t, resp, http.StatusOK)
	task := field(decode(t, resp), "task")
	if task["can_edit"] != false || task["can_set_price"] != false || task["can_archive"] != false || len(task["next_statuses"].([]any)) != 0 {
		t.Fatalf("archived task = %v", task)
	}
	for _, path := range []string{"/history", "/people"} {
		resp = get(t, client, taskURL+path)
		wantStatus(t, resp, http.StatusOK)
		resp.Body.Close()
	}
	resp = get(t, client, srv.URL+"/api/v1/comments?task="+taskID)
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	resp = get(t, client, srv.URL+"/api/v1/attachments/"+fileID)
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// Nothing on it changes, from either side.
	closed := []struct {
		c            *http.Client
		method, path string
		body         any
	}{
		{freelancer, http.MethodPut, taskURL, map[string]any{"title": "Logo v2"}},
		{freelancer, http.MethodPost, taskURL + "/status", map[string]string{"status": "in_progress"}},
		{client, http.MethodPost, taskURL + "/status", map[string]string{"status": "in_progress"}},
		{freelancer, http.MethodPut, taskURL + "/price", map[string]string{"price": "100"}},
		{client, http.MethodPut, taskURL + "/priority", map[string]string{"priority": "high"}},
		{client, http.MethodPut, taskURL + "/labels", map[string]any{"label_ids": []string{}}},
		{client, http.MethodPost, taskURL + "/participants", map[string]string{"user_id": myID(t, srv, client)}},
		{freelancer, http.MethodDelete, taskURL, nil},
		{client, http.MethodPost, srv.URL + "/api/v1/comments?task=" + taskID, map[string]string{"body": "One more thing"}},
		{client, http.MethodPut, srv.URL + "/api/v1/comments/" + commentID, map[string]string{"body": "Edited"}},
		{client, http.MethodDelete, srv.URL + "/api/v1/comments/" + commentID, nil},
		{freelancer, http.MethodDelete, srv.URL + "/api/v1/attachments/" + fileID, nil},
	}
	for _, x := range closed {
		resp := do(t, x.c, x.method, x.path, x.body)
		if resp.StatusCode != http.StatusConflict {
			t.Errorf("%s %s = %d, want 409", x.method, x.path, resp.StatusCode)
		}
		resp.Body.Close()
	}
	resp = upload(t, srv, freelancer, taskID, "more.png", pngBytes)
	wantStatus(t, resp, http.StatusConflict)
	resp.Body.Close()

	// The board can't take it back either.
	clientID := myID(t, srv, client)
	inProgress := column(getBoard(t, srv, freelancer, "?with="+clientID), "In progress")
	resp = post(t, freelancer, srv.URL+"/api/v1/board/moves", map[string]string{"task_id": taskID, "column_id": inProgress.ID})
	wantStatus(t, resp, http.StatusConflict)
	resp.Body.Close()
}

func TestArchive_PersonalTaskByHand(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	personal := newTask(t, srv, freelancer, map[string]any{"title": "Taxes"})
	if personal["can_archive"] != true {
		t.Fatalf("personal task = %v", personal)
	}
	shared := newTask(t, srv, freelancer, map[string]any{"client_id": myID(t, srv, client), "title": "Logo"})
	if shared["can_archive"] != false {
		t.Fatalf("shared task = %v", shared)
	}

	// Shared work goes to the archive only when its report is paid.
	resp := post(t, freelancer, srv.URL+"/api/v1/tasks/"+shared["id"].(string)+"/archive", nil)
	wantStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()

	// A personal task goes there by hand, in whatever status, once.
	resp = post(t, freelancer, srv.URL+"/api/v1/tasks/"+personal["id"].(string)+"/archive", nil)
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "task"); got["archived_at"] == nil || got["archived_by"] != nil || got["status"] != "new" {
		t.Fatalf("archived task = %v", got)
	}
	resp = post(t, freelancer, srv.URL+"/api/v1/tasks/"+personal["id"].(string)+"/archive", nil)
	wantStatus(t, resp, http.StatusConflict)
	resp.Body.Close()
	if got := taskTitles(t, freelancer, srv.URL); len(got) != 1 || got[0] != "Logo" {
		t.Fatalf("task list = %v", got)
	}
	if got := archiveTitles(t, srv, freelancer, "?with=personal"); len(got) != 1 || got[0] != "Taxes" {
		t.Fatalf("personal archive = %v", got)
	}
	if got := archiveTitles(t, srv, client, ""); len(got) != 0 {
		t.Fatalf("client sees %v in their archive", got)
	}
}

func TestArchive_ColumnWithOnlyArchivedTasksGoes(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	task := newTask(t, srv, freelancer, map[string]any{"title": "Taxes"})
	taskURL := srv.URL + "/api/v1/tasks/" + task["id"].(string)
	resp := post(t, freelancer, taskURL+"/status", map[string]string{"status": "done"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	resp = post(t, freelancer, taskURL+"/archive", nil)
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// Done holds only an archived task: it's empty as far as the board goes.
	done := column(getBoard(t, srv, freelancer, "?with=personal"), "Done")
	resp = do(t, freelancer, http.MethodDelete, srv.URL+"/api/v1/board/columns/"+done.ID, nil)
	wantStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
	resp = get(t, freelancer, taskURL)
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "task"); got["archived_at"] == nil || got["status"] != "done" {
		t.Fatalf("archived task after its column went = %v", got)
	}
}

func TestArchive_Pages(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	for _, title := range []string{"One", "Two", "Three"} {
		task := newTask(t, srv, freelancer, map[string]any{"title": title})
		resp := post(t, freelancer, srv.URL+"/api/v1/tasks/"+task["id"].(string)+"/archive", nil)
		wantStatus(t, resp, http.StatusOK)
		resp.Body.Close()
	}

	// The most recently archived first, a numbered page at a time, with the total.
	first, total := archivePage(t, srv, freelancer, "?limit=2")
	if len(first) != 2 || first[0] != "Three" || first[1] != "Two" || total != 3 {
		t.Fatalf("first page = %v, total %d", first, total)
	}
	second, total := archivePage(t, srv, freelancer, "?limit=2&page=2")
	if len(second) != 1 || second[0] != "One" || total != 3 {
		t.Fatalf("second page = %v, total %d", second, total)
	}
	// A page past the last is empty, and still says how many there are.
	past, total := archivePage(t, srv, freelancer, "?limit=2&page=3")
	if len(past) != 0 || total != 3 {
		t.Fatalf("page past the last = %v, total %d", past, total)
	}
	for _, bad := range []string{"?page=0", "?page=x", "?limit=0", "?limit=500"} {
		resp := get(t, freelancer, srv.URL+"/api/v1/archive"+bad)
		wantStatus(t, resp, http.StatusBadRequest)
		resp.Body.Close()
	}
}

func TestClientPersonalTasks(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	owner := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	member := joinTeamAs(t, srv, owner, "max@acme.example", "member")

	// A client keeps their own to-dos, with nobody else on them.
	resp := post(t, owner, srv.URL+"/api/v1/tasks", map[string]any{"personal": true, "freelancer_id": myID(t, srv, freelancer), "title": "Call the bank"})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()
	task := newTask(t, srv, owner, map[string]any{"personal": true, "title": "Call the bank"})
	taskURL := srv.URL + "/api/v1/tasks/" + task["id"].(string)
	if task["client"] != nil || task["can_edit"] != true || task["can_set_price"] != false || task["can_archive"] != true {
		t.Fatalf("client's personal task = %v", task)
	}

	// It's theirs alone: not their company's, not the freelancer's.
	for _, c := range []*http.Client{member, freelancer} {
		resp = get(t, c, taskURL)
		wantStatus(t, resp, http.StatusNotFound)
		resp.Body.Close()
	}
	if got := taskTitles(t, owner, srv.URL); len(got) != 1 || got[0] != "Call the bank" {
		t.Fatalf("owner's tasks = %v", got)
	}
	resp = get(t, owner, srv.URL+"/api/v1/tasks?with=personal")
	wantStatus(t, resp, http.StatusOK)
	if got := decode(t, resp)["tasks"].([]any); len(got) != 1 {
		t.Fatalf("personal tasks = %v", got)
	}
	resp = get(t, owner, srv.URL+"/api/v1/tasks?with="+myID(t, srv, freelancer))
	wantStatus(t, resp, http.StatusOK)
	if got := decode(t, resp)["tasks"].([]any); len(got) != 0 {
		t.Fatalf("tasks with the freelancer = %v", got)
	}

	// On their personal board, which they run, they move it through.
	b := getBoard(t, srv, owner, "?with=personal")
	if !b.CanManage || len(column(b, "New").Tasks) != 1 {
		t.Fatalf("personal board = %+v", b)
	}
	resp = post(t, owner, srv.URL+"/api/v1/board/moves", map[string]string{"task_id": task["id"].(string), "column_id": column(b, "Done").ID})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	resp = do(t, owner, http.MethodPut, taskURL+"/price", map[string]string{"price": "100"})
	wantStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()

	// And put it away.
	resp = post(t, owner, taskURL+"/archive", nil)
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	if got := archiveTitles(t, srv, owner, "?with=personal"); len(got) != 1 || got[0] != "Call the bank" {
		t.Fatalf("owner's personal archive = %v", got)
	}
	if got := archiveTitles(t, srv, member, ""); len(got) != 0 {
		t.Fatalf("member's archive = %v", got)
	}
}
