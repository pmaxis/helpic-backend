package integration

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// endWork stops working with the other side: a freelancer with a client
// (DELETE /clients/{id}), a client with a freelancer (DELETE /freelancers/{id}).
func endWork(t *testing.T, srv *httptest.Server, c *http.Client, path, id string) *http.Response {
	t.Helper()
	return do(t, c, http.MethodDelete, srv.URL+"/api/v1/"+path+"/"+id, nil)
}

func wantEndWork(t *testing.T, srv *httptest.Server, c *http.Client, path, id string, status int) {
	t.Helper()
	resp := endWork(t, srv, c, path, id)
	wantStatus(t, resp, status)
	resp.Body.Close()
}

func linkCount(t *testing.T, srv *httptest.Server, c *http.Client, path string) int {
	t.Helper()
	resp := get(t, c, srv.URL+"/api/v1/"+path)
	wantStatus(t, resp, http.StatusOK)
	list, _ := decode(t, resp)[path].([]any)
	return len(list)
}

// canEnd is whether the list at path offers to end the work with id.
func canEnd(t *testing.T, srv *httptest.Server, c *http.Client, path, id string) bool {
	t.Helper()
	resp := get(t, c, srv.URL+"/api/v1/"+path)
	wantStatus(t, resp, http.StatusOK)
	list, _ := decode(t, resp)[path].([]any)
	for _, x := range list {
		if l := x.(map[string]any); l["id"] == id {
			return l["can_end"] == true
		}
	}
	t.Fatalf("%s has no %s", path, id)
	return false
}

func TestEndWork_FreelancerWaitsForOpenTasksAndUnpaidReports(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	clientID := myID(t, srv, client)
	today := time.Now().Format("2006-01-02")

	freelancerID := myID(t, srv, freelancer)
	if !canEnd(t, srv, freelancer, "clients", clientID) || !canEnd(t, srv, client, "freelancers", freelancerID) {
		t.Fatal("nothing holds them, yet ending isn't offered")
	}
	task := newTask(t, srv, freelancer, map[string]any{"client_id": clientID, "title": "Logo"})
	taskURL := srv.URL + "/api/v1/tasks/" + task["id"].(string)
	wantEndWork(t, srv, freelancer, "clients", clientID, http.StatusConflict)
	if canEnd(t, srv, freelancer, "clients", clientID) || canEnd(t, srv, client, "freelancers", freelancerID) {
		t.Fatal("ending offered with an open task")
	}

	resp := post(t, freelancer, taskURL+"/status", map[string]string{"status": "done"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	resp = post(t, freelancer, srv.URL+"/api/v1/reports", map[string]string{"client_id": clientID, "from": today, "to": today})
	wantStatus(t, resp, http.StatusCreated)
	reportURL := srv.URL + "/api/v1/reports/" + field(decode(t, resp), "report")["id"].(string)
	// A draft is the freelancer's own: it doesn't hold them.
	resp = post(t, freelancer, reportURL+"/send", nil)
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	wantEndWork(t, srv, freelancer, "clients", clientID, http.StatusConflict)
	if canEnd(t, srv, freelancer, "clients", clientID) {
		t.Fatal("ending offered with an unpaid report")
	}

	resp = post(t, client, reportURL+"/paid", nil)
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	wantEndWork(t, srv, freelancer, "clients", clientID, http.StatusNoContent)

	if n := linkCount(t, srv, freelancer, "clients"); n != 0 {
		t.Fatalf("clients = %d, want 0", n)
	}
	if n := linkCount(t, srv, client, "freelancers"); n != 0 {
		t.Fatalf("freelancers = %d, want 0", n)
	}
	list, _ := notifications(t, srv, client)
	if len(list) == 0 || list[0].Kind != "freelancer_left" || list[0].Actor != "free@example.com" {
		t.Fatalf("client's notifications = %+v", list)
	}

	// What they did together stays; nothing new starts.
	wantStatus(t, get(t, freelancer, taskURL), http.StatusOK)
	wantStatus(t, get(t, client, taskURL), http.StatusOK)
	wantStatus(t, get(t, client, reportURL), http.StatusOK)
	resp = post(t, freelancer, srv.URL+"/api/v1/tasks", map[string]any{"client_id": clientID, "title": "Banner"})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()
	wantEndWork(t, srv, freelancer, "clients", clientID, http.StatusNotFound)

	// Either can invite the other again.
	resp = acceptInvite(t, srv, newClient(), invite(t, srv, freelancer, "client@example.com"), map[string]string{"password": "clientsecret1"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	if n := linkCount(t, srv, freelancer, "clients"); n != 1 {
		t.Fatalf("clients after a new invitation = %d, want 1", n)
	}
}

func TestEndWork_ClientsOwnerEndsAndProjectsAreLeft(t *testing.T) {
	srv, _ := newAPI(t)
	first := signUpFreelancer(t, srv, "one@example.com")
	client := acceptNew(t, srv, invite(t, srv, first, "client@example.com"))
	second := signUpFreelancer(t, srv, "two@example.com")
	resp := acceptInvite(t, srv, newClient(), invite(t, srv, second, "client@example.com"), map[string]string{"password": "clientsecret1"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	firstID, secondID, clientID := myID(t, srv, first), myID(t, srv, second), myID(t, srv, client)
	resp = do(t, client, http.MethodPut, srv.URL+"/api/v1/team", map[string]string{"name": "Acme Inc"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	member := joinTeam(t, srv, client, "max@acme.test")

	byClient := createProject(t, srv, client, map[string]any{"freelancer_ids": []string{firstID, secondID}, "name": "Site"})
	byFreelancer := createProject(t, srv, first, map[string]any{"client_id": clientID, "name": "Logo kit"})
	byClientURL := srv.URL + "/api/v1/projects/" + byClient["id"].(string)
	byFreelancerURL := srv.URL + "/api/v1/projects/" + byFreelancer["id"].(string)

	task := newTask(t, srv, client, map[string]any{"freelancer_id": firstID, "title": "Banner"})
	taskURL := srv.URL + "/api/v1/tasks/" + task["id"].(string)

	// Only the company's owner decides who it works with.
	if canEnd(t, srv, member, "freelancers", secondID) || !canEnd(t, srv, client, "freelancers", secondID) {
		t.Fatal("ending is offered to the member, or not to the owner")
	}
	wantEndWork(t, srv, member, "freelancers", firstID, http.StatusForbidden)
	wantEndWork(t, srv, client, "freelancers", firstID, http.StatusConflict)

	resp = post(t, first, taskURL+"/status", map[string]string{"status": "done"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	wantEndWork(t, srv, client, "freelancers", firstID, http.StatusNoContent)

	if n := linkCount(t, srv, client, "freelancers"); n != 1 {
		t.Fatalf("freelancers = %d, want 1", n)
	}
	list, _ := notifications(t, srv, first)
	if len(list) == 0 || list[0].Kind != "client_left" {
		t.Fatalf("freelancer's notifications = %+v", list)
	}

	// The first freelancer is off the client's projects; the one they started
	// passes to the client. The second freelancer stays.
	wantStatus(t, get(t, first, byClientURL), http.StatusNotFound)
	wantStatus(t, get(t, first, byFreelancerURL), http.StatusNotFound)
	wantStatus(t, get(t, second, byClientURL), http.StatusOK)
	resp = do(t, client, http.MethodPut, byFreelancerURL, map[string]any{"name": "Logo kit v2"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// Ending again, or with someone they never worked with, finds nobody.
	wantEndWork(t, srv, client, "freelancers", firstID, http.StatusNotFound)
	wantEndWork(t, srv, client, "freelancers", "not-an-id", http.StatusNotFound)
}
