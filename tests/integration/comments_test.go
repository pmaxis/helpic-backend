package integration

import (
	"net/http"
	"testing"
)

func TestComments(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	outsider := signUpFreelancer(t, srv, "other@example.com")
	task := newTask(t, srv, client, map[string]any{"freelancer_id": myID(t, srv, freelancer), "title": "Logo"})
	listURL := srv.URL + "/api/comments?task=" + task["id"].(string)

	// Both sides talk.
	resp := post(t, client, listURL, map[string]string{"body": "  Can we try a darker blue?  "})
	wantStatus(t, resp, http.StatusCreated)
	first := field(decode(t, resp), "comment")
	if first["body"] != "Can we try a darker blue?" || first["can_edit"] != true || first["edited"] != false {
		t.Fatalf("comment = %v", first)
	}
	firstURL := srv.URL + "/api/comments/" + first["id"].(string)
	resp = post(t, freelancer, listURL, map[string]string{"body": "Sure, two options by Friday"})
	wantStatus(t, resp, http.StatusCreated)
	resp.Body.Close()
	resp = post(t, freelancer, listURL, map[string]string{"body": "   "})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()

	resp = get(t, freelancer, listURL)
	wantStatus(t, resp, http.StatusOK)
	list, _ := decode(t, resp)["comments"].([]any)
	if len(list) != 2 || list[0].(map[string]any)["can_edit"] != false || list[1].(map[string]any)["body"] != "Sure, two options by Friday" {
		t.Fatalf("freelancer sees = %v", list)
	}

	// The freelancer hears about the client's comment, with its text.
	notes, _ := notifications(t, srv, freelancer)
	found := false
	for _, n := range notes {
		if n.Kind == "comment_added" && n.Subject == "Logo" {
			found = true
		}
	}
	if !found {
		t.Fatalf("freelancer notifications = %+v", notes)
	}

	// Only the author edits or deletes.
	resp = do(t, freelancer, http.MethodPut, firstURL, map[string]string{"body": "Hijacked"})
	wantStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()
	resp = do(t, client, http.MethodPut, firstURL, map[string]string{"body": "Can we try a darker navy?"})
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "comment"); got["body"] != "Can we try a darker navy?" || got["edited"] != true {
		t.Fatalf("edited = %v", got)
	}
	resp = do(t, freelancer, http.MethodDelete, firstURL, nil)
	wantStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()
	resp = do(t, client, http.MethodDelete, firstURL, nil)
	wantStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()

	// Someone outside the task sees nothing and can't write.
	wantStatus(t, get(t, outsider, listURL), http.StatusNotFound)
	resp = post(t, outsider, listURL, map[string]string{"body": "Hi"})
	wantStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()
}
