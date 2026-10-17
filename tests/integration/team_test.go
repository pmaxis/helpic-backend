package integration

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// joinTeam invites the email to the owner's company and joins with a new
// account; it returns the signed-in member.
func joinTeam(t *testing.T, srv *httptest.Server, owner *http.Client, email string) *http.Client {
	t.Helper()
	resp := post(t, owner, srv.URL+"/api/team/invites", map[string]string{"email": email, "name": "Max"})
	wantStatus(t, resp, http.StatusCreated)
	token := field(decode(t, resp), "invite")["token"].(string)

	member := newClient()
	resp = post(t, member, srv.URL+"/api/team-invites/"+token+"/accept", map[string]string{
		"name": "Max Member", "password": "membersecret", "password_confirmation": "membersecret",
	})
	wantStatus(t, resp, http.StatusOK)
	if u := field(decode(t, resp), "user"); u["role"] != "client" || u["member_of"] == nil {
		t.Fatalf("new member = %v", u)
	}
	return member
}

func TestTeam(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	owner := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	ownerID, freelancerID := myID(t, srv, owner), myID(t, srv, freelancer)

	// The owner names the company and invites someone.
	resp := do(t, owner, http.MethodPut, srv.URL+"/api/team", map[string]string{"name": "  Acme   Inc  "})
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "team"); got["name"] != "Acme Inc" || got["role"] != "owner" {
		t.Fatalf("team = %v", got)
	}
	resp = post(t, owner, srv.URL+"/api/team/invites", map[string]string{"email": "free@example.com"})
	wantStatus(t, resp, http.StatusBadRequest) // already has an account
	resp.Body.Close()

	resp = post(t, owner, srv.URL+"/api/team/invites", map[string]string{"email": "max@acme.example"})
	wantStatus(t, resp, http.StatusCreated)
	token := field(decode(t, resp), "invite")["token"].(string)
	resp = get(t, newClient(), srv.URL+"/api/team-invites/"+token)
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "invite"); got["company"] != "Acme Inc" || got["email"] != "max@acme.example" {
		t.Fatalf("invite lookup = %v", got)
	}
	resp = do(t, owner, http.MethodDelete, srv.URL+"/api/team/invites/"+field(decode(t, get(t, owner, srv.URL+"/api/team")), "team")["invites"].([]any)[0].(map[string]any)["id"].(string), nil)
	wantStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
	wantStatus(t, get(t, newClient(), srv.URL+"/api/team-invites/"+token), http.StatusNotFound)

	member := joinTeam(t, srv, owner, "max@acme.example")
	memberID := myID(t, srv, member)

	// The member works with the company's freelancer…
	resp = get(t, member, srv.URL+"/api/freelancers")
	wantStatus(t, resp, http.StatusOK)
	if list, _ := decode(t, resp)["freelancers"].([]any); len(list) != 1 {
		t.Fatalf("member's freelancers = %v", list)
	}
	ownerTask := newTask(t, srv, owner, map[string]any{"freelancer_id": freelancerID, "title": "Owner's task"})
	task := newTask(t, srv, member, map[string]any{"freelancer_id": freelancerID, "title": "Member's task"})
	taskURL := srv.URL + "/api/tasks/" + task["id"].(string)
	if task["client"].(map[string]any)["id"] != ownerID || task["created_by"] != memberID || task["can_edit"] != true {
		t.Fatalf("member's task = %v", task)
	}

	// …sees what the owner sees, and the owner sees the member's work.
	for _, c := range []*http.Client{owner, member} {
		resp = get(t, c, srv.URL+"/api/tasks")
		wantStatus(t, resp, http.StatusOK)
		if list, _ := decode(t, resp)["tasks"].([]any); len(list) != 2 {
			t.Fatalf("company tasks = %v", list)
		}
	}
	wantStatus(t, get(t, member, srv.URL+"/api/tasks/"+ownerTask["id"].(string)), http.StatusOK)

	// The price is the freelancer's and the owner's: members don't see it,
	// nor its changes.
	resp = do(t, freelancer, http.MethodPut, taskURL+"/price", map[string]any{"price": "450"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	resp = get(t, owner, taskURL)
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "task"); got["price_cents"] != float64(45000) || got["can_see_price"] != true {
		t.Fatalf("owner's view of the price = %v", got)
	}
	resp = get(t, member, taskURL)
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "task"); got["price_cents"] != nil || got["currency"] != "" || got["can_see_price"] != false {
		t.Fatalf("member's view of the price = %v", got)
	}
	resp = get(t, member, srv.URL+"/api/tasks")
	wantStatus(t, resp, http.StatusOK)
	for _, x := range decode(t, resp)["tasks"].([]any) {
		if x.(map[string]any)["price_cents"] != nil {
			t.Fatalf("member's list shows a price: %v", x)
		}
	}
	resp = get(t, member, taskURL+"/history")
	wantStatus(t, resp, http.StatusOK)
	for _, e := range decode(t, resp)["events"].([]any) {
		if e.(map[string]any)["kind"] == "price" {
			t.Fatalf("member sees a price change: %v", e)
		}
	}

		// Its participants: the freelancer first, then the member who set it;
	// the owner did nothing on it, so isn't one.
	resp = get(t, owner, taskURL)
	wantStatus(t, resp, http.StatusOK)
	parts := field(decode(t, resp), "task")["participants"].([]any)
	if len(parts) != 2 || parts[0].(map[string]any)["id"] != freelancerID || parts[1].(map[string]any)["id"] != memberID {
		t.Fatalf("participants = %v", parts)
	}

	// The freelancer sees who on the client's team set it.
	resp = get(t, freelancer, taskURL)
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "task"); got["creator"].(map[string]any)["name"] != "Max Member" || got["client"].(map[string]any)["id"] != ownerID {
		t.Fatalf("freelancer's view of the member's task = %v", got)
	}
	if b := getBoard(t, srv, member, "?with="+freelancerID); len(b.Columns) == 0 {
		t.Fatal("the member sees no board")
	}

	// The freelancer's reply reaches both the owner and the member who set the task.
	resp = post(t, freelancer, srv.URL+"/api/comments?task="+task["id"].(string), map[string]string{"body": "On it"})
	wantStatus(t, resp, http.StatusCreated)
	resp.Body.Close()
	for _, c := range []*http.Client{owner, member} {
		notes, _ := notifications(t, srv, c)
		found := false
		for _, n := range notes {
			if n.Kind == "comment_added" {
				found = true
			}
		}
		if !found {
			t.Fatalf("not told about the comment: %+v", notes)
		}
	}
	notes, _ := notifications(t, srv, owner)
	joined := false
	for _, n := range notes {
		if n.Kind == "member_joined" {
			joined = true
		}
	}
	if !joined {
		t.Fatalf("owner wasn't told someone joined: %+v", notes)
	}

	// Reports are the owner's alone.
	resp = post(t, freelancer, taskURL+"/status", map[string]string{"status": "done"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	today := time.Now().Format("2006-01-02")
	resp = post(t, freelancer, srv.URL+"/api/reports", map[string]string{"client_id": ownerID, "from": today, "to": today})
	wantStatus(t, resp, http.StatusCreated)
	reportURL := srv.URL + "/api/reports/" + field(decode(t, resp), "report")["id"].(string)
	resp = post(t, freelancer, reportURL+"/send", nil)
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	wantStatus(t, get(t, owner, reportURL), http.StatusOK)
	wantStatus(t, get(t, member, reportURL), http.StatusNotFound)
	resp = get(t, member, srv.URL+"/api/reports")
	wantStatus(t, resp, http.StatusOK)
	if list, _ := decode(t, resp)["reports"].([]any); len(list) != 0 {
		t.Fatalf("member sees reports: %v", list)
	}

	// A member doesn't run the team, and can't become a client of their own.
	resp = get(t, member, srv.URL+"/api/team")
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "team"); got["role"] != "member" || got["name"] != "Acme Inc" || got["members"] != nil {
		t.Fatalf("member's view of the team = %v", got)
	}
	resp = post(t, member, srv.URL+"/api/team/invites", map[string]string{"email": "other@acme.example"})
	wantStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()
	resp = post(t, freelancer, srv.URL+"/api/invitations", map[string]string{"email": "max@acme.example", "name": "Max"})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()

	// Removed from the team, the member sees none of the company's work.
	resp = do(t, owner, http.MethodDelete, srv.URL+"/api/team/members/"+memberID, nil)
	wantStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
	resp = get(t, member, srv.URL+"/api/tasks")
	wantStatus(t, resp, http.StatusOK)
	if list, _ := decode(t, resp)["tasks"].([]any); len(list) != 0 {
		t.Fatalf("removed member's tasks = %v", list)
	}
	wantStatus(t, get(t, member, taskURL), http.StatusNotFound)
}
