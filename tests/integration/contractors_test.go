package integration

import (
	"net/http"
	"testing"
)

// taskTitles lists the titles of the tasks the client sees.
func taskTitles(t *testing.T, c *http.Client, url string) []string {
	t.Helper()
	resp := get(t, c, url+"/api/v1/tasks")
	wantStatus(t, resp, http.StatusOK)
	var out []string
	for _, x := range decode(t, resp)["tasks"].([]any) {
		out = append(out, x.(map[string]any)["title"].(string))
	}
	return out
}

func TestContractors(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	owner := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	freelancerID := myID(t, srv, freelancer)

	member := joinTeamAs(t, srv, owner, "max@acme.example", "member")
	contractor := joinTeamAs(t, srv, owner, "olga@studio.example", "contractor")
	contractorID := myID(t, srv, contractor)

	resp := get(t, contractor, srv.URL+"/api/v1/me")
	wantStatus(t, resp, http.StatusOK)
	if team := field(decode(t, resp), "user")["member_of"].(map[string]any); team["role"] != "contractor" {
		t.Fatalf("contractor's team = %v", team)
	}

	project := createProject(t, srv, owner, map[string]any{"freelancer_id": freelancerID, "name": "Site"})
	ownerTask := newTask(t, srv, owner, map[string]any{"freelancer_id": freelancerID, "title": "Owner's", "project_id": project["id"]})
	newTask(t, srv, member, map[string]any{"freelancer_id": freelancerID, "title": "Member's", "project_id": project["id"]})
	newTask(t, srv, contractor, map[string]any{"freelancer_id": freelancerID, "title": "Contractor's", "project_id": project["id"]})

	// The contractor sees only the task they set; everyone else sees all three.
	if got := taskTitles(t, contractor, srv.URL); len(got) != 1 || got[0] != "Contractor's" {
		t.Fatalf("contractor sees %v", got)
	}
	for _, c := range []*http.Client{owner, member, freelancer} {
		if got := taskTitles(t, c, srv.URL); len(got) != 3 {
			t.Fatalf("someone sees %v", got)
		}
	}
	ownerTaskURL := srv.URL + "/api/v1/tasks/" + ownerTask["id"].(string)
	wantStatus(t, get(t, contractor, ownerTaskURL), http.StatusNotFound)
	wantStatus(t, get(t, contractor, srv.URL+"/api/v1/comments?task="+ownerTask["id"].(string)), http.StatusNotFound)
	wantStatus(t, get(t, owner, srv.URL+"/api/v1/comments?task="+ownerTask["id"].(string)), http.StatusOK)

	// They see the project, counting only their task, and can't start one.
	resp = get(t, contractor, srv.URL+"/api/v1/projects/"+project["id"].(string))
	wantStatus(t, resp, http.StatusOK)
	if p := field(decode(t, resp), "project"); p["total_tasks"] != float64(1) {
		t.Fatalf("contractor's project = %v", p)
	}
	resp = post(t, contractor, srv.URL+"/api/v1/projects", map[string]any{"freelancer_id": freelancerID, "name": "Mine"})
	wantStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()

	// Added to the owner's task, they're told and see it.
	resp = get(t, owner, ownerTaskURL+"/people")
	wantStatus(t, resp, http.StatusOK)
	if people, _ := decode(t, resp)["people"].([]any); len(people) != 3 || people[0].(map[string]any)["role"] != "owner" {
		t.Fatalf("people = %v", people)
	}
	resp = post(t, owner, ownerTaskURL+"/participants", map[string]string{"user_id": contractorID})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	list, _ := notifications(t, srv, contractor)
	if len(list) == 0 || list[0].Kind != "participant_added" || list[0].Subject != "Owner's" {
		t.Fatalf("contractor's notifications = %+v", list)
	}
	if got := taskTitles(t, contractor, srv.URL); len(got) != 2 {
		t.Fatalf("added contractor sees %v", got)
	}

	// The freelancer's work on it reaches them too.
	resp = post(t, freelancer, ownerTaskURL+"/status", map[string]string{"status": "in_progress"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	if list, _ := notifications(t, srv, contractor); list[0].Kind != "task_status" {
		t.Fatalf("contractor's notifications = %+v", list)
	}

	// A contractor can't change who is on a task they didn't set, nor add
	// someone from outside the company.
	resp = post(t, contractor, ownerTaskURL+"/participants", map[string]string{"user_id": myID(t, srv, member)})
	wantStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()
	resp = post(t, owner, ownerTaskURL+"/participants", map[string]string{"user_id": freelancerID})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()

	// Taken off, they no longer see it; the freelancer stays on.
	resp = do(t, owner, http.MethodDelete, ownerTaskURL+"/participants/"+contractorID, nil)
	wantStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
	wantStatus(t, get(t, contractor, ownerTaskURL), http.StatusNotFound)
	resp = do(t, owner, http.MethodDelete, ownerTaskURL+"/participants/"+freelancerID, nil)
	wantStatus(t, resp, http.StatusConflict)
	resp.Body.Close()

	// Made a member, they see all the company's work.
	resp = do(t, owner, http.MethodPut, srv.URL+"/api/v1/team/members/"+contractorID, map[string]string{"role": "member"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	if got := taskTitles(t, contractor, srv.URL); len(got) != 3 {
		t.Fatalf("contractor made a member sees %v", got)
	}
	resp = do(t, member, http.MethodPut, srv.URL+"/api/v1/team/members/"+contractorID, map[string]string{"role": "contractor"})
	wantStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()
}
