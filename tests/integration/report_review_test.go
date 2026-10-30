package integration

import (
	"net/http"
	"testing"
	"time"
)

func TestReports_ClientReview(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	clientID := myID(t, srv, client)
	today := time.Now().Format("2006-01-02")

	task := newTask(t, srv, freelancer, map[string]any{"client_id": clientID, "title": "Logo"})
	resp := post(t, freelancer, srv.URL+"/api/v1/tasks/"+task["id"].(string)+"/status", map[string]string{"status": "done"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	resp = post(t, freelancer, srv.URL+"/api/v1/reports", map[string]string{"client_id": clientID, "from": today, "to": today})
	wantStatus(t, resp, http.StatusCreated)
	reportURL := srv.URL + "/api/v1/reports/" + field(decode(t, resp), "report")["id"].(string)

	review := func(c *http.Client, body map[string]string, want int) map[string]any {
		t.Helper()
		resp := do(t, c, http.MethodPut, reportURL+"/review", body)
		wantStatus(t, resp, want)
		if want != http.StatusOK {
			resp.Body.Close()
			return nil
		}
		return field(decode(t, resp), "report")
	}
	read := func(c *http.Client) map[string]any {
		t.Helper()
		resp := get(t, c, reportURL)
		wantStatus(t, resp, http.StatusOK)
		return field(decode(t, resp), "report")
	}
	send := func() {
		t.Helper()
		resp := post(t, freelancer, reportURL+"/send", nil)
		wantStatus(t, resp, http.StatusOK)
		resp.Body.Close()
	}

	// A draft isn't the client's to review: they don't see it.
	review(client, map[string]string{"review": "approved"}, http.StatusNotFound)
	send()

	if got := read(freelancer); got["can_review"] != false || got["review"] != nil {
		t.Fatalf("freelancer's view = %v", got)
	}
	if got := read(client); got["can_review"] != true {
		t.Fatalf("client's view = %v", got)
	}
	review(freelancer, map[string]string{"review": "approved"}, http.StatusForbidden)
	review(client, map[string]string{"review": "maybe"}, http.StatusBadRequest)
	// Asking for changes says what.
	review(client, map[string]string{"review": "changes_requested", "comment": "  "}, http.StatusBadRequest)

	got := review(client, map[string]string{"review": "changes_requested", "comment": "The logo was 400, not 500"}, http.StatusOK)
	if got["review"] != "changes_requested" || got["reviewed_at"] == nil || got["comment_count"] != float64(1) {
		t.Fatalf("changes asked for = %v", got)
	}
	resp = get(t, freelancer, reportURL+"/comments")
	wantStatus(t, resp, http.StatusOK)
	if list := decode(t, resp)["comments"].([]any); len(list) != 1 || list[0].(map[string]any)["body"] != "The logo was 400, not 500" {
		t.Fatalf("discussion = %v", list)
	}
	notes, _ := notifications(t, srv, freelancer)
	if countKind(notes, "report_changes_requested") != 1 || countKind(notes, "report_comment") != 0 {
		t.Fatalf("freelancer notifications = %+v", notes)
	}
	if got := read(freelancer); got["review_note"] != "The logo was 400, not 500" || got["can_answer"] != true {
		t.Fatalf("freelancer's view of the changes asked for = %v", got)
	}

	// The freelancer may keep it as it is, saying why: the client reviews it again.
	keep := func(c *http.Client, comment string, want int) map[string]any {
		t.Helper()
		resp := post(t, c, reportURL+"/keep", map[string]string{"comment": comment})
		wantStatus(t, resp, want)
		if want != http.StatusOK {
			resp.Body.Close()
			return nil
		}
		return field(decode(t, resp), "report")
	}
	keep(freelancer, " ", http.StatusBadRequest)
	keep(client, "Nope", http.StatusForbidden)
	got = keep(freelancer, "We agreed on 500 in the brief", http.StatusOK)
	if got["review"] != nil || got["review_note"] != "" || got["review_answer"] != "We agreed on 500 in the brief" || got["can_answer"] != false || got["comment_count"] != float64(2) {
		t.Fatalf("kept as it is = %v", got)
	}
	keep(freelancer, "Again", http.StatusConflict)
	clientNotes, _ := notifications(t, srv, client)
	if countKind(clientNotes, "report_changes_declined") != 1 {
		t.Fatalf("client notifications = %+v", clientNotes)
	}
	if got := read(client); got["can_review"] != true || got["review_answer"] != "We agreed on 500 in the brief" {
		t.Fatalf("client's view after the answer = %v", got)
	}
	// Asked again, the answer is gone.
	if got := review(client, map[string]string{"review": "changes_requested", "comment": "Still 400"}, http.StatusOK); got["review_answer"] != "" || got["review_note"] != "Still 400" {
		t.Fatalf("asked again = %v", got)
	}

	// Taken back and sent again, it starts over.
	resp = post(t, freelancer, reportURL+"/recall", nil)
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "report"); got["review"] != nil {
		t.Fatalf("recalled report's review = %v", got["review"])
	}
	review(client, map[string]string{"review": "approved"}, http.StatusNotFound)
	send()
	if got := read(client); got["review"] != nil || got["can_review"] != true {
		t.Fatalf("report sent again = %v", got)
	}
	if got := review(client, map[string]string{"review": "approved"}, http.StatusOK); got["review"] != "approved" {
		t.Fatalf("approved = %v", got)
	}
	notes, _ = notifications(t, srv, freelancer)
	if countKind(notes, "report_approved") != 1 {
		t.Fatalf("freelancer notifications after approval = %+v", notes)
	}

	// The history keeps both, for both sides.
	resp = get(t, client, reportURL+"/history")
	wantStatus(t, resp, http.StatusOK)
	kinds := map[string]int{}
	for _, e := range decode(t, resp)["events"].([]any) {
		kinds[e.(map[string]any)["kind"].(string)]++
	}
	if kinds["changes_requested"] != 2 || kinds["changes_declined"] != 1 || kinds["approved"] != 1 {
		t.Fatalf("client's history = %v", kinds)
	}

	// Paid, it's settled.
	resp = post(t, client, reportURL+"/paid", nil)
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	review(client, map[string]string{"review": "changes_requested", "comment": "Late"}, http.StatusConflict)
	if got := read(client); got["review"] != "approved" || got["can_review"] != false {
		t.Fatalf("paid report = %v", got)
	}
}
