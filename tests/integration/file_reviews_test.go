package integration

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

// uploadVersion uploads a file as a new version of another on the task.
func uploadVersion(t *testing.T, srv *httptest.Server, c *http.Client, taskID, replaces, name string, content []byte) *http.Response {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	part.Write(content)
	mw.Close()
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/attachments?task="+taskID+"&replaces="+replaces, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestFileReviews(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	clientID := myID(t, srv, client)
	taskID := newTask(t, srv, freelancer, map[string]any{"client_id": clientID, "title": "Logo"})["id"].(string)
	otherTask := newTask(t, srv, freelancer, map[string]any{"client_id": clientID, "title": "Landing"})["id"].(string)

	v1 := uploaded(t, upload(t, srv, freelancer, taskID, "logo.png", pngBytes))
	if v1["series_id"] != v1["id"] || v1["version"] != float64(1) || v1["latest"] != true || v1["review"] != nil {
		t.Fatalf("first version = %v", v1)
	}
	list := func(c *http.Client) []map[string]any {
		t.Helper()
		resp := get(t, c, srv.URL+"/api/v1/attachments?task="+taskID)
		wantStatus(t, resp, http.StatusOK)
		var out []map[string]any
		for _, a := range decode(t, resp)["attachments"].([]any) {
			out = append(out, a.(map[string]any))
		}
		return out
	}
	review := func(c *http.Client, id, decision string, want int) map[string]any {
		t.Helper()
		resp := do(t, c, http.MethodPut, srv.URL+"/api/v1/attachments/"+id+"/review", map[string]string{"review": decision})
		wantStatus(t, resp, want)
		if want != http.StatusOK {
			resp.Body.Close()
			return nil
		}
		return field(decode(t, resp), "attachment")
	}

	// The client's side reviews; the freelancer doesn't.
	if got := list(freelancer); got[0]["can_review"] != false {
		t.Fatalf("freelancer's view = %v", got[0])
	}
	if got := list(client); got[0]["can_review"] != true {
		t.Fatalf("client's view = %v", got[0])
	}
	review(freelancer, v1["id"].(string), "approved", http.StatusForbidden)
	review(client, v1["id"].(string), "love it", http.StatusBadRequest)
	got := review(client, v1["id"].(string), "changes_requested", http.StatusOK)
	if got["review"] != "changes_requested" || field(got, "reviewed_by")["email"] != "client@example.com" || got["reviewed_at"] == nil {
		t.Fatalf("reviewed file = %v", got)
	}
	notes, _ := notifications(t, srv, freelancer)
	if countKind(notes, "file_changes_requested") != 1 {
		t.Fatalf("freelancer notifications = %+v", notes)
	}

	// A new version, on this task only, starts without a review; the old one
	// can't be reviewed any more.
	resp := uploadVersion(t, srv, freelancer, otherTask, v1["id"].(string), "logo-v2.png", pngBytes)
	wantStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()
	v2 := uploaded(t, uploadVersion(t, srv, freelancer, taskID, v1["id"].(string), "logo-v2.png", pngBytes))
	if v2["series_id"] != v1["id"] || v2["version"] != float64(2) || v2["review"] != nil {
		t.Fatalf("second version = %v", v2)
	}
	files := list(client)
	if len(files) != 2 || files[0]["latest"] != false || files[0]["can_review"] != false || files[1]["latest"] != true || files[1]["can_review"] != true {
		t.Fatalf("versions = %v", files)
	}
	review(client, v1["id"].(string), "approved", http.StatusConflict)
	review(client, v2["id"].(string), "approved", http.StatusOK)
	notes, _ = notifications(t, srv, freelancer)
	if countKind(notes, "file_approved") != 1 {
		t.Fatalf("freelancer notifications after approval = %+v", notes)
	}
	// "" takes it back.
	if got := review(client, v2["id"].(string), "", http.StatusOK); got["review"] != nil || got["reviewed_by"] != nil {
		t.Fatalf("review taken back = %v", got)
	}

	// The task's history keeps the reviews.
	resp = get(t, freelancer, srv.URL+"/api/v1/tasks/"+taskID+"/history")
	wantStatus(t, resp, http.StatusOK)
	reviews := 0
	for _, e := range decode(t, resp)["events"].([]any) {
		if e.(map[string]any)["kind"] == "file_reviewed" {
			reviews++
		}
	}
	if reviews != 3 {
		t.Fatalf("file reviews in the history = %d", reviews)
	}

	// A personal task has nobody to review its files.
	personal := newTask(t, srv, freelancer, map[string]any{"title": "Notes"})["id"].(string)
	own := uploaded(t, upload(t, srv, freelancer, personal, "sketch.png", pngBytes))
	review(freelancer, own["id"].(string), "approved", http.StatusForbidden)
}

func TestFileReviews_PinnedComments(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	clientID := myID(t, srv, client)
	taskID := newTask(t, srv, freelancer, map[string]any{"client_id": clientID, "title": "Logo"})["id"].(string)
	otherTask := newTask(t, srv, freelancer, map[string]any{"client_id": clientID, "title": "Landing"})["id"].(string)
	image := uploaded(t, upload(t, srv, freelancer, taskID, "mockup.png", pngBytes))["id"].(string)
	brief := uploaded(t, upload(t, srv, freelancer, taskID, "brief.txt", []byte("plain text")))["id"].(string)
	elsewhere := uploaded(t, upload(t, srv, freelancer, otherTask, "other.png", pngBytes))["id"].(string)
	commentsURL := srv.URL + "/api/v1/comments?task=" + taskID

	for _, pin := range []map[string]any{
		{"attachment_id": image, "x": 1.5, "y": 0.5},
		{"attachment_id": "", "x": 0.5, "y": 0.5},
		{"attachment_id": brief, "x": 0.5, "y": 0.5},
		{"attachment_id": elsewhere, "x": 0.5, "y": 0.5},
	} {
		resp := post(t, client, commentsURL, map[string]any{"body": "Here", "pin": pin})
		wantStatus(t, resp, http.StatusBadRequest)
		resp.Body.Close()
	}

	resp := post(t, client, commentsURL, map[string]any{"body": "Make the **logo** bigger", "pin": map[string]any{"attachment_id": image, "x": 0.25, "y": 0.75}})
	wantStatus(t, resp, http.StatusCreated)
	c := field(decode(t, resp), "comment")
	if pin := field(c, "pin"); pin["attachment_id"] != image || pin["x"] != 0.25 || pin["y"] != 0.75 {
		t.Fatalf("pinned comment = %v", c)
	}
	// It's a comment on the task like any other: the freelancer hears of it.
	notes, _ := notifications(t, srv, freelancer)
	if countKind(notes, "comment_added") != 1 {
		t.Fatalf("freelancer notifications = %+v", notes)
	}
	// An edit keeps it where it was.
	resp = do(t, client, http.MethodPut, srv.URL+"/api/v1/comments/"+c["id"].(string), map[string]any{"body": "Make it much bigger"})
	wantStatus(t, resp, http.StatusOK)
	if pin := field(field(decode(t, resp), "comment"), "pin"); pin["attachment_id"] != image {
		t.Fatalf("edited comment's pin = %v", pin)
	}

	// The image goes; the comment stays, pointing at nothing.
	resp = do(t, freelancer, http.MethodDelete, srv.URL+"/api/v1/attachments/"+image, nil)
	wantStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
	resp = get(t, client, commentsURL)
	wantStatus(t, resp, http.StatusOK)
	list := decode(t, resp)["comments"].([]any)
	if pin := field(list[0].(map[string]any), "pin"); len(list) != 1 || pin["attachment_id"] != nil || pin["x"] != 0.25 {
		t.Fatalf("comments after the image went = %v", list)
	}
}
