package integration

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// upload posts one file to a task's attachments.
func upload(t *testing.T, srv *httptest.Server, c *http.Client, taskID, name string, content []byte) *http.Response {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	part.Write(content)
	mw.Close()
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/attachments?task="+taskID, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

var pngBytes = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 64)...)

func TestAttachments(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	outsider := signUpFreelancer(t, srv, "other@example.com")
	task := newTask(t, srv, freelancer, map[string]any{"client_id": myID(t, srv, client), "title": "Logo"})
	taskID := task["id"].(string)

	// An image, named with a path that is dropped.
	resp := upload(t, srv, freelancer, taskID, "../../mockup.png", pngBytes)
	wantStatus(t, resp, http.StatusCreated)
	img := field(decode(t, resp), "attachment")
	if img["file_name"] != "mockup.png" || img["content_type"] != "image/png" || img["is_image"] != true || img["can_delete"] != true {
		t.Fatalf("image = %v", img)
	}
	imgURL := srv.URL + "/api/v1/attachments/" + img["id"].(string)

	// A page posing as an image is still a page: never previewed, never run.
	resp = upload(t, srv, client, taskID, `C:\docs\page.png`, []byte("<!doctype html><script>alert(1)</script>"))
	wantStatus(t, resp, http.StatusCreated)
	page := field(decode(t, resp), "attachment")
	if page["file_name"] != "page.png" || page["is_image"] != false || !strings.HasPrefix(page["content_type"].(string), "text/html") {
		t.Fatalf("page = %v", page)
	}
	resp = get(t, freelancer, srv.URL+"/api/v1/attachments/"+page["id"].(string)+"?inline=1")
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	if d := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(d, "attachment;") || resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("page served as %q", d)
	}

	// The client sees both and downloads the image; only an image shows inline.
	resp = get(t, client, srv.URL+"/api/v1/attachments?task="+taskID)
	wantStatus(t, resp, http.StatusOK)
	list, _ := decode(t, resp)["attachments"].([]any)
	if len(list) != 2 || list[0].(map[string]any)["can_delete"] != false {
		t.Fatalf("client list = %v", list)
	}
	resp = get(t, client, imgURL+"?inline=1")
	wantStatus(t, resp, http.StatusOK)
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !bytes.Equal(got, pngBytes) || !strings.HasPrefix(resp.Header.Get("Content-Disposition"), "inline;") {
		t.Fatalf("download = %d bytes, %q", len(got), resp.Header.Get("Content-Disposition"))
	}

	// The client is told about the freelancer's file.
	notes, _ := notifications(t, srv, client)
	found := false
	for _, n := range notes {
		if n.Kind == "attachment_added" {
			found = true
		}
	}
	if !found {
		t.Fatalf("client notifications = %+v", notes)
	}

	// Someone outside the task sees nothing.
	wantStatus(t, get(t, outsider, srv.URL+"/api/v1/attachments?task="+taskID), http.StatusNotFound)
	wantStatus(t, get(t, outsider, imgURL), http.StatusNotFound)
	resp = upload(t, srv, outsider, taskID, "x.txt", []byte("hi"))
	wantStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()

	// Limits: too large, empty.
	resp = upload(t, srv, freelancer, taskID, "big.bin", bytes.Repeat([]byte("x"), 2<<20))
	wantStatus(t, resp, http.StatusRequestEntityTooLarge)
	resp.Body.Close()
	resp = upload(t, srv, freelancer, taskID, "empty.txt", nil)
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()

	// Only the uploader deletes a file.
	resp = do(t, client, http.MethodDelete, imgURL, nil)
	wantStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()
	resp = do(t, freelancer, http.MethodDelete, imgURL, nil)
	wantStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
	wantStatus(t, get(t, freelancer, imgURL), http.StatusNotFound)

	// Deleting the task takes its files along.
	pageURL := srv.URL + "/api/v1/attachments/" + page["id"].(string)
	resp = do(t, freelancer, http.MethodDelete, srv.URL+"/api/v1/tasks/"+taskID, nil)
	wantStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
	wantStatus(t, get(t, client, pageURL), http.StatusNotFound)
}
