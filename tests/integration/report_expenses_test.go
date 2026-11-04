package integration

import (
	"net/http"
	"testing"
	"time"
)

func TestReportExpenses(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	clientID := myID(t, srv, client)
	today := time.Now().Format("2006-01-02")

	task := newTask(t, srv, freelancer, map[string]any{"client_id": clientID, "title": "Logo", "price": "100"})
	resp := post(t, freelancer, srv.URL+"/api/v1/tasks/"+task["id"].(string)+"/status", map[string]string{"status": "done"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	resp = post(t, freelancer, srv.URL+"/api/v1/reports", map[string]string{"client_id": clientID, "from": today, "to": today})
	wantStatus(t, resp, http.StatusCreated)
	rep := field(decode(t, resp), "report")
	reportURL := srv.URL + "/api/v1/reports/" + rep["id"].(string)
	taskLine := rep["items"].([]any)[0].(map[string]any)["id"].(string)

	// What an expense needs, and who adds one.
	for _, bad := range []map[string]string{
		{"title": " ", "spent_on": today, "amount": "20"},
		{"title": "Hosting", "spent_on": "soon", "amount": "20"},
		{"title": "Hosting", "spent_on": today, "amount": "0"},
		{"title": "Hosting", "spent_on": today, "amount": "-5"},
	} {
		resp = post(t, freelancer, reportURL+"/expenses", bad)
		wantStatus(t, resp, http.StatusBadRequest)
		resp.Body.Close()
	}
	expense := map[string]string{"title": "Hosting", "spent_on": today, "amount": "20.50"}
	resp = post(t, client, reportURL+"/expenses", expense)
	wantStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()

	// It joins the total and the expenses' part of it, not the tasks' count.
	resp = post(t, freelancer, reportURL+"/expenses", expense)
	wantStatus(t, resp, http.StatusCreated)
	rep = field(decode(t, resp), "report")
	line := rep["items"].([]any)[1].(map[string]any)
	if line["expense"] != true || line["title"] != "Hosting" || line["price_cents"] != float64(2050) || line["task_id"] != nil || line["completed_on"] != today {
		t.Fatalf("expense line = %v", line)
	}
	if rep["total_cents"] != float64(12050) || rep["expenses_cents"] != float64(2050) || rep["item_count"] != float64(1) {
		t.Fatalf("report with an expense = %v", rep)
	}
	expenseURL := reportURL + "/expenses/" + line["id"].(string)

	// It's edited on its own endpoint, and is no task to write notes on.
	resp = do(t, freelancer, http.MethodPut, expenseURL, map[string]string{"title": "Hosting, a year", "spent_on": today, "amount": "30"})
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "report"); got["total_cents"] != float64(13000) {
		t.Fatalf("after the edit = %v", got)
	}
	resp = do(t, freelancer, http.MethodPut, reportURL+"/expenses/"+taskLine, expense)
	wantStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()
	resp = do(t, freelancer, http.MethodPut, reportURL+"/items/"+line["id"].(string), map[string]string{"notes": "x"})
	wantStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()

	// A second one, dropped the way a task is.
	resp = post(t, freelancer, reportURL+"/expenses", expense)
	wantStatus(t, resp, http.StatusCreated)
	items := field(decode(t, resp), "report")["items"].([]any)
	resp = do(t, freelancer, http.MethodDelete, reportURL+"/items/"+items[2].(map[string]any)["id"].(string), nil)
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "report"); got["expenses_cents"] != float64(3000) {
		t.Fatalf("after dropping one = %v", got)
	}

	// The freelancer can't relabel it by changing the currency.
	resp = do(t, freelancer, http.MethodPut, srv.URL+"/api/v1/me/currency", map[string]string{"currency": "EUR"})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()

	// A sent report keeps it, and it can't change; the client can ask about it.
	resp = post(t, freelancer, reportURL+"/send", nil)
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	resp = post(t, freelancer, reportURL+"/expenses", expense)
	wantStatus(t, resp, http.StatusConflict)
	resp.Body.Close()
	resp = do(t, freelancer, http.MethodPut, expenseURL, expense)
	wantStatus(t, resp, http.StatusConflict)
	resp.Body.Close()
	resp = get(t, client, reportURL)
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "report"); got["total_cents"] != float64(13000) || got["expenses_cents"] != float64(3000) {
		t.Fatalf("the client's copy = %v", got)
	}
	resp = post(t, client, reportURL+"/comments", map[string]string{"item_id": line["id"].(string), "body": "Which plan?"})
	wantStatus(t, resp, http.StatusCreated)
	resp.Body.Close()

	// Taken back it's a draft's line again, amount kept, and stays through payment.
	resp = post(t, freelancer, reportURL+"/recall", nil)
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "report"); got["total_cents"] != float64(13000) || got["open_questions"] != float64(1) {
		t.Fatalf("taken back = %v", got)
	}
	resp = post(t, freelancer, reportURL+"/send", nil)
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	resp = post(t, client, reportURL+"/paid", nil)
	wantStatus(t, resp, http.StatusOK)
	got := field(decode(t, resp), "report")
	if got["status"] != "paid" || got["total_cents"] != float64(13000) {
		t.Fatalf("paid = %v", got)
	}

	// Drafting them stays the freelancer's: the client's history doesn't show it.
	resp = get(t, client, reportURL+"/history")
	wantStatus(t, resp, http.StatusOK)
	for _, e := range decode(t, resp)["events"].([]any) {
		if k := e.(map[string]any)["kind"]; k == "expense_added" || k == "expense_changed" || k == "expense_removed" {
			t.Fatalf("the client sees %v", e)
		}
	}
	resp = get(t, freelancer, reportURL+"/history")
	wantStatus(t, resp, http.StatusOK)
	kinds := map[any]bool{}
	for _, e := range decode(t, resp)["events"].([]any) {
		kinds[e.(map[string]any)["kind"]] = true
	}
	if !kinds["expense_added"] || !kinds["expense_changed"] || !kinds["expense_removed"] {
		t.Fatalf("the freelancer's history = %v", kinds)
	}
}

// A report can bill expenses alone: no finished task, yet something to send.
func TestReportExpenses_WithoutTasks(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	clientID := myID(t, srv, client)
	today := time.Now().Format("2006-01-02")

	resp := post(t, freelancer, srv.URL+"/api/v1/reports", map[string]string{"client_id": clientID, "from": today, "to": today})
	wantStatus(t, resp, http.StatusCreated)
	reportURL := srv.URL + "/api/v1/reports/" + field(decode(t, resp), "report")["id"].(string)

	resp = post(t, freelancer, reportURL+"/expenses", map[string]string{"title": "Licence", "spent_on": today, "amount": "49"})
	wantStatus(t, resp, http.StatusCreated)
	resp.Body.Close()
	resp = post(t, freelancer, reportURL+"/send", nil)
	wantStatus(t, resp, http.StatusOK)
	got := field(decode(t, resp), "report")
	if got["status"] != "sent" || got["total_cents"] != float64(4900) || got["item_count"] != float64(0) {
		t.Fatalf("sent = %v", got)
	}
}
