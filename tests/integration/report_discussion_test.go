package integration

import (
	"net/http"
	"testing"
	"time"
)

// countKind counts the user's notifications of a kind.
func countKind(notes []note, kind string) int {
	n := 0
	for _, x := range notes {
		if x.Kind == kind {
			n++
		}
	}
	return n
}

func TestReports_Discussion(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	outsider := signUpFreelancer(t, srv, "other@example.com")
	clientID := myID(t, srv, client)
	today := time.Now().Format("2006-01-02")

	finish := func(title, price string) string {
		task := newTask(t, srv, freelancer, map[string]any{"client_id": clientID, "title": title})
		id := task["id"].(string)
		resp := do(t, freelancer, http.MethodPut, srv.URL+"/api/v1/tasks/"+id+"/price", map[string]any{"price": price})
		wantStatus(t, resp, http.StatusOK)
		resp.Body.Close()
		resp = post(t, freelancer, srv.URL+"/api/v1/tasks/"+id+"/status", map[string]string{"status": "done"})
		wantStatus(t, resp, http.StatusOK)
		resp.Body.Close()
		return id
	}
	logoTask := finish("Logo", "500")
	finish("Landing", "300")

	resp := post(t, freelancer, srv.URL+"/api/v1/reports", map[string]string{"client_id": clientID, "from": today, "to": today, "title": "October"})
	wantStatus(t, resp, http.StatusCreated)
	rep := field(decode(t, resp), "report")
	reportURL := srv.URL + "/api/v1/reports/" + rep["id"].(string)
	itemOf := func(r map[string]any, title string) map[string]any {
		for _, it := range r["items"].([]any) {
			if m := it.(map[string]any); m["title"] == title {
				return m
			}
		}
		t.Fatalf("no %q in %v", title, r["items"])
		return nil
	}
	logoItem, landingItem := itemOf(rep, "Logo")["id"].(string), itemOf(rep, "Landing")["id"].(string)
	read := func(c *http.Client) map[string]any {
		t.Helper()
		resp := get(t, c, reportURL)
		wantStatus(t, resp, http.StatusOK)
		return field(decode(t, resp), "report")
	}
	comment := func(c *http.Client, itemID, body string, want int) map[string]any {
		t.Helper()
		resp := post(t, c, reportURL+"/comments", map[string]string{"item_id": itemID, "body": body})
		wantStatus(t, resp, want)
		if want != http.StatusCreated {
			resp.Body.Close()
			return nil
		}
		return field(decode(t, resp), "comment")
	}

	// A draft never sent has no discussion: the client can't see it, and
	// its freelancer has nobody to talk to.
	if rep["can_comment"] != false || rep["can_recall"] != false {
		t.Fatalf("draft's permissions = %v", rep)
	}
	comment(client, logoItem, "Hm?", http.StatusNotFound)
	comment(freelancer, logoItem, "Note to self", http.StatusConflict)

	resp = post(t, freelancer, reportURL+"/send", nil)
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// The client asks about a line: a question opens on it.
	comment(client, logoItem, "  ", http.StatusBadRequest)
	comment(client, "not-an-id", "Which one?", http.StatusNotFound)
	comment(outsider, logoItem, "Hi", http.StatusNotFound)
	asked := comment(client, logoItem, "Why 500 for the logo? We agreed 400.", http.StatusCreated)
	if asked["item_id"] != logoItem || asked["item_title"] != "Logo" || field(asked, "author")["email"] != "client@example.com" {
		t.Fatalf("question = %v", asked)
	}
	got := read(freelancer)
	if got["open_questions"] != float64(1) || got["comment_count"] != float64(1) || got["can_comment"] != true || got["can_recall"] != true {
		t.Fatalf("report with a question = %v", got)
	}
	if it := itemOf(got, "Logo"); it["question_open"] != true || it["comment_count"] != float64(1) {
		t.Fatalf("questioned line = %v", it)
	}
	notes, _ := notifications(t, srv, freelancer)
	if countKind(notes, "report_comment") != 1 {
		t.Fatalf("freelancer notifications = %+v", notes)
	}

	// The freelancer answers; a comment on the report as a whole opens nothing.
	comment(freelancer, logoItem, "You're right, I'll fix it.", http.StatusCreated)
	comment(client, "", "Otherwise all good.", http.StatusCreated)
	if got := read(client); got["open_questions"] != float64(1) || got["comment_count"] != float64(3) || got["can_recall"] != false {
		t.Fatalf("client's view = %v", got)
	}
	notes, _ = notifications(t, srv, client)
	if countKind(notes, "report_comment") != 1 {
		t.Fatalf("client notifications = %+v", notes)
	}

	// The freelancer takes it back: a draft again, out of the client's sight
	// and still not to be deleted, its prices live again.
	resp = post(t, client, reportURL+"/recall", nil)
	wantStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()
	resp = post(t, freelancer, reportURL+"/recall", nil)
	wantStatus(t, resp, http.StatusOK)
	got = field(decode(t, resp), "report")
	if got["status"] != "draft" || got["sent_at"] != nil || got["recalled_at"] == nil || got["can_edit"] != true || got["can_delete"] != false || got["can_comment"] != true {
		t.Fatalf("recalled report = %v", got)
	}
	resp = post(t, freelancer, reportURL+"/recall", nil)
	wantStatus(t, resp, http.StatusConflict)
	resp.Body.Close()
	resp = get(t, client, reportURL)
	wantStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()
	resp = do(t, freelancer, http.MethodDelete, reportURL, nil)
	wantStatus(t, resp, http.StatusConflict)
	resp.Body.Close()
	notes, _ = notifications(t, srv, client)
	if countKind(notes, "report_recalled") != 1 {
		t.Fatalf("client notifications after the recall = %+v", notes)
	}

	resp = do(t, freelancer, http.MethodPut, srv.URL+"/api/v1/tasks/"+logoTask+"/price", map[string]any{"price": "400"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	if got := read(freelancer); got["total_cents"] != float64(70000) {
		t.Fatalf("recalled draft's total = %v", got["total_cents"])
	}
	// Said while it's a draft: the client hears nothing yet.
	comment(freelancer, logoItem, "Fixed to 400.", http.StatusCreated)
	notes, _ = notifications(t, srv, client)
	if countKind(notes, "report_comment") != 1 {
		t.Fatalf("client heard of a comment on a draft: %+v", notes)
	}

	// Sent again, the client sees all that was said, and resolves it.
	resp = post(t, freelancer, reportURL+"/send", nil)
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	resp = get(t, client, reportURL+"/comments")
	wantStatus(t, resp, http.StatusOK)
	if list := decode(t, resp)["comments"].([]any); len(list) != 4 || list[3].(map[string]any)["body"] != "Fixed to 400." {
		t.Fatalf("discussion = %v", list)
	}
	resp = post(t, client, reportURL+"/items/"+landingItem+"/resolve", nil)
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	resp = post(t, client, reportURL+"/items/"+logoItem+"/resolve", nil)
	wantStatus(t, resp, http.StatusOK)
	got = field(decode(t, resp), "report")
	if got["open_questions"] != float64(0) || got["total_cents"] != float64(70000) || itemOf(got, "Logo")["question_open"] != false {
		t.Fatalf("resolved report = %v", got)
	}

	// The client sees the questions in the history; only the freelancer the drafting.
	kinds := func(c *http.Client) []string {
		resp := get(t, c, reportURL+"/history")
		wantStatus(t, resp, http.StatusOK)
		out := []string{}
		for _, e := range decode(t, resp)["events"].([]any) {
			out = append(out, e.(map[string]any)["kind"].(string))
		}
		return out
	}
	if got := kinds(client); len(got) != 5 || got[0] != "sent" || got[1] != "question" || got[2] != "recalled" || got[3] != "sent" || got[4] != "resolved" {
		t.Fatalf("client's history = %v", got)
	}

	// Paid, the discussion closes but stays to read.
	resp = post(t, client, reportURL+"/paid", nil)
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "report"); got["can_comment"] != false || got["can_recall"] != false {
		t.Fatalf("paid report = %v", got)
	}
	comment(client, "", "Thanks!", http.StatusConflict)
	resp = post(t, freelancer, reportURL+"/recall", nil)
	wantStatus(t, resp, http.StatusConflict)
	resp.Body.Close()
	resp = get(t, freelancer, reportURL+"/comments")
	wantStatus(t, resp, http.StatusOK)
	if list := decode(t, resp)["comments"].([]any); len(list) != 4 {
		t.Fatalf("discussion after payment = %v", list)
	}
}

func TestReports_DroppedLineKeepsItsDiscussion(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	clientID := myID(t, srv, client)
	today := time.Now().Format("2006-01-02")

	for _, title := range []string{"Logo", "Landing"} {
		task := newTask(t, srv, freelancer, map[string]any{"client_id": clientID, "title": title})
		resp := post(t, freelancer, srv.URL+"/api/v1/tasks/"+task["id"].(string)+"/status", map[string]string{"status": "done"})
		wantStatus(t, resp, http.StatusOK)
		resp.Body.Close()
	}
	resp := post(t, freelancer, srv.URL+"/api/v1/reports", map[string]string{"client_id": clientID, "from": today, "to": today})
	wantStatus(t, resp, http.StatusCreated)
	rep := field(decode(t, resp), "report")
	reportURL := srv.URL + "/api/v1/reports/" + rep["id"].(string)
	var logoItem string
	for _, it := range rep["items"].([]any) {
		if m := it.(map[string]any); m["title"] == "Logo" {
			logoItem = m["id"].(string)
		}
	}
	resp = post(t, freelancer, reportURL+"/send", nil)
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	resp = post(t, client, reportURL+"/comments", map[string]string{"item_id": logoItem, "body": "We didn't order a logo"})
	wantStatus(t, resp, http.StatusCreated)
	resp.Body.Close()

	// Taken back, the line goes; what was said about it stays, naming it.
	resp = post(t, freelancer, reportURL+"/recall", nil)
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	resp = do(t, freelancer, http.MethodDelete, reportURL+"/items/"+logoItem, nil)
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "report"); got["open_questions"] != float64(0) || got["item_count"] != float64(1) {
		t.Fatalf("report without the line = %v", got)
	}
	resp = get(t, freelancer, reportURL+"/comments")
	wantStatus(t, resp, http.StatusOK)
	c := decode(t, resp)["comments"].([]any)[0].(map[string]any)
	if c["item_id"] != nil || c["item_title"] != "Logo" {
		t.Fatalf("comment on the dropped line = %v", c)
	}
}
