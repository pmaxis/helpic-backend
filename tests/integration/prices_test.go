package integration

import (
	"net/http"
	"testing"
	"time"
)

func TestPrices_TasksAndReports(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	clientID, freelancerID := myID(t, srv, client), myID(t, srv, freelancer)

	// The freelancer bills in euros.
	resp := do(t, freelancer, http.MethodPut, srv.URL+"/api/v1/me/currency", map[string]string{"currency": "eur"})
	wantStatus(t, resp, http.StatusOK)
	if u := field(decode(t, resp), "user"); u["currency"] != "EUR" {
		t.Fatalf("user = %v", u)
	}
	resp = do(t, freelancer, http.MethodPut, srv.URL+"/api/v1/me/currency", map[string]string{"currency": "XYZ"})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()

	// A freelancer prices a task when creating it; a client can't.
	logo := newTask(t, srv, freelancer, map[string]any{"client_id": clientID, "title": "Logo", "price": "1 200,50"})
	if logo["price_cents"] != float64(120050) || logo["currency"] != "EUR" || logo["can_set_price"] != true {
		t.Fatalf("priced task = %v", logo)
	}
	resp = post(t, client, srv.URL+"/api/v1/tasks", map[string]any{"freelancer_id": freelancerID, "title": "Cheap", "price": "1"})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()

	// The freelancer prices a client's task, even once it's done; the client
	// sees the price but can't change it.
	landing := newTask(t, srv, client, map[string]any{"freelancer_id": freelancerID, "title": "Landing"})
	landingURL := srv.URL + "/api/v1/tasks/" + landing["id"].(string)
	newTask(t, srv, client, map[string]any{"freelancer_id": freelancerID, "title": "Unpriced"})
	for _, id := range []string{logo["id"].(string), landing["id"].(string)} {
		resp = post(t, freelancer, srv.URL+"/api/v1/tasks/"+id+"/status", map[string]string{"status": "done"})
		wantStatus(t, resp, http.StatusOK)
		resp.Body.Close()
	}
	resp = do(t, freelancer, http.MethodPut, landingURL+"/price", map[string]any{"price": "300"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	resp = do(t, freelancer, http.MethodPut, landingURL+"/price", map[string]any{"price": "3.999"})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()
	resp = do(t, client, http.MethodPut, landingURL+"/price", map[string]any{"price": "1"})
	wantStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()
	resp = get(t, client, landingURL)
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "task"); got["price_cents"] != float64(30000) || got["can_set_price"] != false {
		t.Fatalf("client view = %v", got)
	}

	// The report sums the priced tasks.
	today := time.Now().Format("2006-01-02")
	resp = post(t, freelancer, srv.URL+"/api/v1/reports", map[string]string{"client_id": clientID, "from": today, "to": today})
	wantStatus(t, resp, http.StatusCreated)
	rep := field(decode(t, resp), "report")
	reportURL := srv.URL + "/api/v1/reports/" + rep["id"].(string)
	if rep["total_cents"] != float64(150050) || rep["currency"] != "EUR" {
		t.Fatalf("report = %v", rep)
	}

	// The list carries the total too.
	resp = get(t, freelancer, srv.URL+"/api/v1/reports")
	wantStatus(t, resp, http.StatusOK)
	if list := decode(t, resp)["reports"].([]any); len(list) != 1 || list[0].(map[string]any)["total_cents"] != float64(150050) {
		t.Fatalf("report list = %v", list)
	}

	// A draft follows price changes; sending freezes them.
	resp = do(t, freelancer, http.MethodPut, landingURL+"/price", map[string]any{"price": "500"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	resp = get(t, freelancer, reportURL)
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "report"); got["total_cents"] != float64(170050) {
		t.Fatalf("draft after a price change = %v", got)
	}
	resp = post(t, freelancer, reportURL+"/send", nil)
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	resp = do(t, freelancer, http.MethodPut, landingURL+"/price", map[string]any{"price": nil})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	resp = do(t, freelancer, http.MethodPut, srv.URL+"/api/v1/me/currency", map[string]string{"currency": "USD"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	resp = get(t, client, reportURL)
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "report"); got["total_cents"] != float64(170050) || got["currency"] != "EUR" {
		t.Fatalf("sent report after changes = %v", got)
	}
}
