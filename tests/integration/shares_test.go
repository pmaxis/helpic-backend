package integration

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestShares(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	task := newTask(t, srv, freelancer, map[string]any{"client_id": myID(t, srv, client), "title": "Logo", "description": "Two variants", "due_on": "2031-05-01"})
	taskID := task["id"].(string)
	shareURL := srv.URL + "/api/v1/tasks/" + taskID + "/share"
	newTask(t, srv, freelancer, map[string]any{"parent_id": taskID, "title": "Sketches"})
	resp := upload(t, srv, freelancer, taskID, "mock.png", pngBytes)
	wantStatus(t, resp, http.StatusCreated)
	fileID := field(decode(t, resp), "attachment")["id"].(string)
	other := newTask(t, srv, freelancer, map[string]any{"client_id": myID(t, srv, client), "title": "Other"})
	resp = upload(t, srv, freelancer, other["id"].(string), "other.txt", []byte("not shared"))
	wantStatus(t, resp, http.StatusCreated)
	otherFile := field(decode(t, resp), "attachment")["id"].(string)

	// The freelancer may share it; the client didn't create it, so may not.
	if task["can_share"] != true {
		t.Fatalf("can_share = %v for the freelancer", task["can_share"])
	}
	resp = get(t, client, srv.URL+"/api/v1/tasks/"+taskID)
	wantStatus(t, resp, http.StatusOK)
	if field(decode(t, resp), "task")["can_share"] != false {
		t.Fatal("the client may share a task they didn't create")
	}
	resp = post(t, client, shareURL, nil)
	wantStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()

	// No link yet; then one, which stays the same when asked for again.
	resp = get(t, freelancer, shareURL)
	wantStatus(t, resp, http.StatusOK)
	if share := decode(t, resp)["share"]; share != nil {
		t.Fatalf("share = %v, want null", share)
	}
	resp = post(t, freelancer, shareURL, nil)
	wantStatus(t, resp, http.StatusCreated)
	link := field(decode(t, resp), "share")["url"].(string)
	i := strings.Index(link, "/share/")
	if i < 0 {
		t.Fatalf("url = %q", link)
	}
	token := link[i+len("/share/"):]
	resp = post(t, freelancer, shareURL, nil)
	wantStatus(t, resp, http.StatusOK)
	if again := field(decode(t, resp), "share")["url"]; again != link {
		t.Fatalf("url = %v, then %v", link, again)
	}

	// Anyone opens it, without an account: the task, its subtasks and files,
	// and nothing about the people or the money.
	anyone := &http.Client{}
	resp = get(t, anyone, srv.URL+"/api/v1/shared/"+token)
	wantStatus(t, resp, http.StatusOK)
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	for _, leak := range []string{"example.com", "price", "freelancer", "client"} {
		if strings.Contains(string(raw), leak) {
			t.Fatalf("shared task gives away %q: %s", leak, raw)
		}
	}
	resp = get(t, anyone, srv.URL+"/api/v1/shared/"+token)
	shared := field(decode(t, resp), "task")
	if shared["title"] != "Logo" || shared["description"] != "Two variants" || shared["due_on"] != "2031-05-01" || shared["status"] != "new" {
		t.Fatalf("shared task = %v", shared)
	}
	subtasks, _ := shared["subtasks"].([]any)
	files, _ := shared["files"].([]any)
	if len(subtasks) != 1 || subtasks[0].(map[string]any)["title"] != "Sketches" {
		t.Fatalf("subtasks = %v", subtasks)
	}
	if len(files) != 1 || files[0].(map[string]any)["id"] != fileID {
		t.Fatalf("files = %v", files)
	}

	// Its files download; another task's don't, nor does a made-up link.
	resp = get(t, anyone, srv.URL+"/api/v1/shared/"+token+"/files/"+fileID+"?inline=1")
	wantStatus(t, resp, http.StatusOK)
	if got := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(got, "inline") {
		t.Fatalf("disposition = %q", got)
	}
	resp.Body.Close()
	for _, url := range []string{
		"/api/v1/shared/" + token + "/files/" + otherFile,
		"/api/v1/shared/" + token + "/files/not-an-id",
		"/api/v1/shared/made-up",
		"/api/v1/shared/made-up/files/" + fileID,
	} {
		resp = get(t, anyone, srv.URL+url)
		wantStatus(t, resp, http.StatusNotFound)
		resp.Body.Close()
	}

	// Turned off, the link opens nothing; sharing again is a new link.
	resp = do(t, client, http.MethodDelete, shareURL, nil)
	wantStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()
	resp = do(t, freelancer, http.MethodDelete, shareURL, nil)
	wantStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
	resp = get(t, anyone, srv.URL+"/api/v1/shared/"+token)
	wantStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()
	resp = post(t, freelancer, shareURL, nil)
	wantStatus(t, resp, http.StatusCreated)
	if again := field(decode(t, resp), "share")["url"]; again == link {
		t.Fatal("sharing again gave the old link back")
	}

	// The history says who turned it on and off.
	resp = get(t, client, srv.URL+"/api/v1/tasks/"+taskID+"/history")
	wantStatus(t, resp, http.StatusOK)
	var kinds []string
	for _, e := range decode(t, resp)["events"].([]any) {
		kinds = append(kinds, e.(map[string]any)["kind"].(string))
	}
	if got := strings.Join(kinds, ","); !strings.HasSuffix(got, "shared,unshared,shared") {
		t.Fatalf("history = %s", got)
	}
}

func TestShares_TaskCreatorShares(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	task := newTask(t, srv, client, map[string]any{"freelancer_id": myID(t, srv, freelancer), "title": "Logo"})
	shareURL := srv.URL + "/api/v1/tasks/" + task["id"].(string) + "/share"

	// The client set the task, so they share it, and so does its freelancer.
	for _, c := range []*http.Client{client, freelancer} {
		resp := post(t, c, shareURL, nil)
		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		resp.Body.Close()
	}

	// Someone who doesn't see the task doesn't learn it exists.
	outsider := signUpFreelancer(t, srv, "other@example.com")
	resp := get(t, outsider, shareURL)
	wantStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()
}

func TestShares_GoneWithTheAccount(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	task := newTask(t, srv, client, map[string]any{"freelancer_id": myID(t, srv, freelancer), "title": "Logo"})
	resp := post(t, freelancer, srv.URL+"/api/v1/tasks/"+task["id"].(string)+"/share", nil)
	wantStatus(t, resp, http.StatusCreated)
	link := field(decode(t, resp), "share")["url"].(string)
	token := link[strings.Index(link, "/share/")+len("/share/"):]

	// The task stays with the client, but the link its freelancer gave out goes.
	resp = deleteAccount(t, srv, freelancer, map[string]string{"current_password": "supersecret1"})
	wantStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
	resp = get(t, &http.Client{}, srv.URL+"/api/v1/shared/"+token)
	wantStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()
}
