package integration

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// deleteAccount asks to delete the signed-in account.
func deleteAccount(t *testing.T, srv *httptest.Server, c *http.Client, body map[string]string) *http.Response {
	t.Helper()
	return do(t, c, http.MethodDelete, srv.URL+"/api/v1/me", body)
}

func TestAccountDeletion_SharedWorkStays(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	freelancerID := myID(t, srv, freelancer)
	shared := newTask(t, srv, client, map[string]any{"freelancer_id": freelancerID, "title": "Logo"})
	resp := post(t, freelancer, srv.URL+"/api/v1/comments?task="+shared["id"].(string), map[string]string{"body": "Two options by Friday"})
	wantStatus(t, resp, http.StatusCreated)
	resp.Body.Close()
	personal := newTask(t, srv, freelancer, map[string]any{"title": "Taxes"})

	// The password has to be right.
	resp = deleteAccount(t, srv, freelancer, map[string]string{"current_password": "wrong-pass"})
	wantStatus(t, resp, http.StatusBadRequest)
	if fields := decode(t, resp)["error"].(map[string]any)["fields"].(map[string]any); fields["current_password"] == nil {
		t.Fatalf("fields = %v", fields)
	}

	resp = deleteAccount(t, srv, freelancer, map[string]string{"current_password": "supersecret1"})
	wantStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()

	// Signed out, and can't sign in again.
	resp = get(t, freelancer, srv.URL+"/api/v1/me")
	wantStatus(t, resp, http.StatusUnauthorized)
	resp.Body.Close()
	resp = post(t, newClient(), srv.URL+"/api/v1/auth/login", map[string]string{"email": "free@example.com", "password": "supersecret1"})
	wantStatus(t, resp, http.StatusUnauthorized)
	resp.Body.Close()

	// The old address is told.
	if e := lastEmailTo(t, "free@example.com"); !strings.Contains(e.Subject, "deleted") {
		t.Fatalf("last email = %q", e.Subject)
	}

	// The client keeps the task and the comment, under a name that says nothing.
	task := getTask(t, srv, client, shared["id"].(string))
	f := task["freelancer"].(map[string]any)
	if f["id"] != freelancerID || f["name"] != "Deleted user" || f["email"] != "" {
		t.Fatalf("freelancer = %v", f)
	}
	resp = get(t, client, srv.URL+"/api/v1/comments?task="+shared["id"].(string))
	wantStatus(t, resp, http.StatusOK)
	if list, _ := decode(t, resp)["comments"].([]any); len(list) != 1 || list[0].(map[string]any)["author"].(map[string]any)["name"] != "Deleted user" {
		t.Fatalf("comments = %v", list)
	}

	// The personal task is gone; the freelancer isn't on the client's list, nor can get new work.
	var n int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM tasks WHERE id = $1`, personal["id"]).Scan(&n); err != nil || n != 0 {
		t.Fatalf("personal task left: %d, %v", n, err)
	}
	resp = get(t, client, srv.URL+"/api/v1/freelancers")
	wantStatus(t, resp, http.StatusOK)
	if list, _ := decode(t, resp)["freelancers"].([]any); len(list) != 0 {
		t.Fatalf("freelancers = %v", list)
	}
	resp = post(t, client, srv.URL+"/api/v1/tasks", map[string]any{"freelancer_id": freelancerID, "title": "Banner"})
	if resp.StatusCode < 400 {
		t.Fatalf("task for a deleted freelancer: %d", resp.StatusCode)
	}
	resp.Body.Close()

	// The email is free for a new account.
	signUpFreelancer(t, srv, "free@example.com")
}

func TestAccountDeletion_TeamAndTwoFactor(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	owner := acceptNew(t, srv, invite(t, srv, freelancer, "owner@acme.example"))
	member := joinTeam(t, srv, owner, "max@acme.example")
	memberID := myID(t, srv, member)

	// An owner with people in the company is refused until they're gone.
	resp := deleteAccount(t, srv, owner, map[string]string{"current_password": "clientsecret1"})
	wantStatus(t, resp, http.StatusConflict)
	resp.Body.Close()

	resp = deleteAccount(t, srv, member, map[string]string{"current_password": "membersecret1"})
	wantStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()

	// The member left the company, so the owner may go now.
	resp = get(t, owner, srv.URL+"/api/v1/team")
	wantStatus(t, resp, http.StatusOK)
	if body := decode(t, resp); strings.Contains(fmt.Sprint(body), memberID) {
		t.Fatalf("team still has the member: %v", body)
	}
	resp = deleteAccount(t, srv, owner, map[string]string{"current_password": "clientsecret1"})
	wantStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()

	var n int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM users WHERE deleted_at IS NOT NULL AND email = ''`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("deleted accounts = %d, %v", n, err)
	}

	// With two-factor on, a code is needed too.
	_, codes := turnOnTwoFactor(t, srv, freelancer)
	resp = deleteAccount(t, srv, freelancer, map[string]string{"current_password": "supersecret1"})
	wantStatus(t, resp, http.StatusForbidden)
	if code := errorCode(decode(t, resp)); code != "TWO_FACTOR_REQUIRED" {
		t.Fatalf("code = %s", code)
	}
	resp = deleteAccount(t, srv, freelancer, map[string]string{"current_password": "supersecret1", "code": "000000"})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()
	resp = deleteAccount(t, srv, freelancer, map[string]string{"current_password": "supersecret1", "code": codes[0]})
	wantStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
}

func TestAccountDeletion_ByAdmin(t *testing.T) {
	srv, _ := newAPI(t)
	boss, codes := stepUpAdmin(t, srv, "boss@example.com")
	dev := signUpFreelancer(t, srv, "dev@example.com")
	id := myID(t, srv, dev)

	resp := do(t, boss, http.MethodDelete, srv.URL+"/api/v1/admin/users/"+id, map[string]string{"reason": "Asked by email", "code": codes.next(t)})
	wantStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()

	// Gone from the panel, recorded under the email it had, and signed out.
	resp = get(t, boss, srv.URL+"/api/v1/admin/users/"+id)
	wantStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()
	last := adminActions(t, srv, boss, "")["actions"].([]any)[0].(map[string]any)
	if last["action"] != "delete" || last["target_email"] != "dev@example.com" || last["reason"] != "Asked by email" {
		t.Fatalf("last action = %v", last)
	}
	resp = get(t, dev, srv.URL+"/api/v1/me")
	wantStatus(t, resp, http.StatusUnauthorized)
	resp.Body.Close()

	// Twice is no such account; an admin's own account isn't deleted from the settings.
	resp = do(t, boss, http.MethodDelete, srv.URL+"/api/v1/admin/users/"+id, map[string]string{"code": codes.next(t)})
	wantStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()
	resp = deleteAccount(t, srv, boss, map[string]string{"current_password": "supersecret1"})
	wantStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()
}
