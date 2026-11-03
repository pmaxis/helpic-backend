package integration

import (
	"net/http"
	"slices"
	"testing"
	"time"
)

// estimateOf reads the task as the viewer sees it.
func estimateOf(t *testing.T, c *http.Client, taskURL string) map[string]any {
	t.Helper()
	resp := get(t, c, taskURL)
	wantStatus(t, resp, http.StatusOK)
	return field(decode(t, resp), "task")
}

func TestEstimates_FixedPrice(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	member := joinTeam(t, srv, client, "member@example.com")
	clientID := myID(t, srv, client)

	task := newTask(t, srv, freelancer, map[string]any{"client_id": clientID, "title": "Logo"})
	taskURL := srv.URL + "/api/v1/tasks/" + task["id"].(string)
	if task["estimate"] != nil || task["can_estimate"] != true || task["can_send_estimate"] != false {
		t.Fatalf("new task = %v", task)
	}

	// Nothing to send before there's a price.
	resp := post(t, freelancer, taskURL+"/estimate", nil)
	wantStatus(t, resp, http.StatusConflict)
	resp.Body.Close()

	// The freelancer prices it, says how long it takes, and sends it.
	resp = do(t, freelancer, http.MethodPut, taskURL+"/price", map[string]any{"price": "500"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	resp = do(t, freelancer, http.MethodPut, taskURL+"/estimate-time", map[string]any{"seconds": 30})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()
	resp = do(t, freelancer, http.MethodPut, taskURL+"/estimate-time", map[string]any{"seconds": 8 * 3600})
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "task"); got["estimate_seconds"] != float64(8*3600) || got["can_send_estimate"] != true {
		t.Fatalf("estimated = %v", got)
	}
	resp = post(t, freelancer, taskURL+"/estimate", nil)
	wantStatus(t, resp, http.StatusOK)
	sent := field(decode(t, resp), "task")
	e, _ := sent["estimate"].(map[string]any)
	if e == nil || e["status"] != "pending" || e["cents"] != float64(50000) || e["seconds"] != float64(8*3600) ||
		e["billing"] != "fixed" || e["outdated"] != false || sent["can_send_estimate"] != false || sent["can_withdraw_estimate"] != true {
		t.Fatalf("sent = %v", sent)
	}
	resp = post(t, freelancer, taskURL+"/estimate", nil)
	wantStatus(t, resp, http.StatusConflict)
	resp.Body.Close()

	// Only the client is asked: the team doesn't see the price.
	notes, _ := notifications(t, srv, client)
	if len(notes) == 0 || notes[0].Kind != "estimate_sent" || notes[0].Subject != "Logo" {
		t.Fatalf("client notifications = %+v", notes)
	}
	for _, n := range func() []note { n, _ := notifications(t, srv, member); return n }() {
		if n.Kind == "estimate_sent" {
			t.Fatalf("a member was asked to approve: %+v", n)
		}
	}
	if got := estimateOf(t, member, taskURL); got["estimate"] != nil || got["can_review_estimate"] != false {
		t.Fatalf("member view = %v", got)
	}

	// The freelancer doesn't review their own estimate; a member doesn't either.
	resp = do(t, freelancer, http.MethodPut, taskURL+"/estimate/review", map[string]any{"review": "approved"})
	wantStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()
	resp = do(t, member, http.MethodPut, taskURL+"/estimate/review", map[string]any{"review": "approved"})
	wantStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()

	// Asking for changes needs a note, which the freelancer is told.
	if got := estimateOf(t, client, taskURL); got["can_review_estimate"] != true {
		t.Fatalf("client view = %v", got)
	}
	resp = do(t, client, http.MethodPut, taskURL+"/estimate/review", map[string]any{"review": "changes_requested"})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()
	resp = do(t, client, http.MethodPut, taskURL+"/estimate/review", map[string]any{"review": "changes_requested", "note": "  Too much for a logo  "})
	wantStatus(t, resp, http.StatusOK)
	e = field(decode(t, resp), "task")["estimate"].(map[string]any)
	if e["status"] != "changes_requested" || e["note"] != "Too much for a logo" || e["reviewer"].(map[string]any)["email"] != "client@example.com" {
		t.Fatalf("changes asked = %v", e)
	}
	notes, _ = notifications(t, srv, freelancer)
	if notes[0].Kind != "estimate_changes_requested" || notes[0].Detail != "Too much for a logo" {
		t.Fatalf("freelancer notifications = %+v", notes)
	}
	// Answered, it can't be taken back, but it can be sent again.
	resp = do(t, freelancer, http.MethodDelete, taskURL+"/estimate", nil)
	wantStatus(t, resp, http.StatusConflict)
	resp.Body.Close()

	// A new price puts the estimate out of date: the client can't answer it
	// until it's sent again, which starts the review over.
	resp = do(t, freelancer, http.MethodPut, taskURL+"/price", map[string]any{"price": "400"})
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "task"); got["estimate"].(map[string]any)["outdated"] != true || got["can_send_estimate"] != true {
		t.Fatalf("after a new price = %v", got)
	}
	if got := estimateOf(t, client, taskURL); got["can_review_estimate"] != false {
		t.Fatalf("client view of an outdated estimate = %v", got)
	}
	resp = do(t, client, http.MethodPut, taskURL+"/estimate/review", map[string]any{"review": "approved"})
	wantStatus(t, resp, http.StatusConflict)
	resp.Body.Close()
	resp = post(t, freelancer, taskURL+"/estimate", nil)
	wantStatus(t, resp, http.StatusOK)
	e = field(decode(t, resp), "task")["estimate"].(map[string]any)
	if e["status"] != "pending" || e["cents"] != float64(40000) || e["note"] != "" || e["reviewer"] != nil {
		t.Fatalf("sent again = %v", e)
	}

	// The client approves it; the freelancer is told.
	resp = do(t, client, http.MethodPut, taskURL+"/estimate/review", map[string]any{"review": "approved", "note": "ignored"})
	wantStatus(t, resp, http.StatusOK)
	got := field(decode(t, resp), "task")
	if e := got["estimate"].(map[string]any); e["status"] != "approved" || e["note"] != "" || e["reviewed_at"] == nil {
		t.Fatalf("approved = %v", e)
	}
	notes, _ = notifications(t, srv, freelancer)
	if notes[0].Kind != "estimate_approved" {
		t.Fatalf("freelancer notifications = %+v", notes)
	}
	if got := estimateOf(t, freelancer, taskURL); got["can_send_estimate"] != false || got["can_withdraw_estimate"] != false {
		t.Fatalf("freelancer view of an approved estimate = %v", got)
	}

	// The history keeps every step; the team doesn't see the money's.
	kinds := func(c *http.Client) []string {
		resp := get(t, c, taskURL+"/history")
		wantStatus(t, resp, http.StatusOK)
		var out []string
		for _, x := range decode(t, resp)["events"].([]any) {
			if k := x.(map[string]any)["kind"].(string); k != "created" && k != "price" {
				out = append(out, k)
			}
		}
		return out
	}
	want := []string{"estimate", "estimate_sent", "estimate_reviewed", "estimate_sent", "estimate_reviewed"}
	if got := kinds(client); !slices.Equal(got, want) {
		t.Fatalf("history = %v, want %v", got, want)
	}
	if got := kinds(member); !slices.Equal(got, []string{"estimate"}) {
		t.Fatalf("member history = %v", got)
	}
}

func TestEstimates_HourlyAndPermissions(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	freelancerID := myID(t, srv, freelancer)

	resp := do(t, freelancer, http.MethodPut, srv.URL+"/api/v1/me/rate", map[string]any{"rate": "40"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	task := newTask(t, srv, client, map[string]any{"freelancer_id": freelancerID, "title": "Landing"})
	taskURL := srv.URL + "/api/v1/tasks/" + task["id"].(string)
	resp = do(t, freelancer, http.MethodPut, taskURL+"/price", map[string]any{"billing": "hourly"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// The client doesn't estimate, nor send.
	resp = do(t, client, http.MethodPut, taskURL+"/estimate-time", map[string]any{"seconds": 3600})
	wantStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()
	resp = post(t, client, taskURL+"/estimate", nil)
	wantStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()

	// By the hour the time is what's sent, at the rate: 1 h 30 min at 40.
	resp = post(t, freelancer, taskURL+"/estimate", nil)
	wantStatus(t, resp, http.StatusConflict)
	resp.Body.Close()
	resp = do(t, freelancer, http.MethodPut, taskURL+"/estimate-time", map[string]any{"seconds": 90 * 60})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	resp = post(t, freelancer, taskURL+"/estimate", nil)
	wantStatus(t, resp, http.StatusOK)
	e := field(decode(t, resp), "task")["estimate"].(map[string]any)
	if e["billing"] != "hourly" || e["seconds"] != float64(5400) || e["cents"] != float64(6000) {
		t.Fatalf("hourly estimate = %v", e)
	}

	// Time tracked on it doesn't put it out of date; a new rate doesn't either.
	resp = post(t, freelancer, srv.URL+"/api/v1/time-entries?task="+task["id"].(string),
		map[string]string{"spent_on": time.Now().Format("2006-01-02"), "duration": "45m"})
	wantStatus(t, resp, http.StatusCreated)
	resp.Body.Close()
	resp = do(t, freelancer, http.MethodPut, srv.URL+"/api/v1/me/rate", map[string]any{"rate": "50"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	if got := estimateOf(t, client, taskURL); got["estimate"].(map[string]any)["outdated"] != false || got["can_review_estimate"] != true {
		t.Fatalf("after time and a new rate = %v", got)
	}

	// Taken back before the client answers, it's gone.
	resp = do(t, freelancer, http.MethodDelete, taskURL+"/estimate", nil)
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "task"); got["estimate"] != nil || got["can_send_estimate"] != true {
		t.Fatalf("taken back = %v", got)
	}
	resp = do(t, client, http.MethodPut, taskURL+"/estimate/review", map[string]any{"review": "approved"})
	wantStatus(t, resp, http.StatusConflict)
	resp.Body.Close()

	// A personal task has nobody to approve it.
	own := newTask(t, srv, freelancer, map[string]any{"title": "Taxes", "price": "10"})
	resp = post(t, freelancer, srv.URL+"/api/v1/tasks/"+own["id"].(string)+"/estimate", nil)
	wantStatus(t, resp, http.StatusConflict)
	resp.Body.Close()
}
