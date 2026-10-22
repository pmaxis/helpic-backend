package integration

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pmaxis/helpic/internal/config"
)

// fakeNBU answers like the National Bank of Ukraine: USD at usd on whatever
// day is asked for, or 503 while down. calls counts the requests.
type fakeNBU struct {
	usd   string
	down  atomic.Bool
	calls atomic.Int32
}

func newFakeNBU(t *testing.T, usd string) (*fakeNBU, *httptest.Server) {
	t.Helper()
	bank := &fakeNBU{usd: usd}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bank.calls.Add(1)
		if bank.down.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		day, err := time.Parse("20060102", r.URL.Query().Get("date"))
		if err != nil {
			http.Error(w, "bad date", http.StatusBadRequest)
			return
		}
		fmt.Fprintf(w, `[{"r030":840,"txt":"Долар США","rate":%s,"cc":"USD","exchangedate":"%s","special":"N"}]`, bank.usd, day.Format("02.01.2006"))
	}))
	t.Cleanup(srv.Close)
	if _, err := testPool.Exec(context.Background(), "TRUNCATE exchange_rates"); err != nil {
		t.Fatal(err)
	}
	return bank, srv
}

// pricedReport makes a client's report of one task priced at $100 and
// returns its URL.
func pricedReport(t *testing.T, srv *httptest.Server, freelancer *http.Client, clientID string) string {
	t.Helper()
	task := newTask(t, srv, freelancer, map[string]any{"client_id": clientID, "title": "Logo", "price": "100"})
	resp := post(t, freelancer, srv.URL+"/api/v1/tasks/"+task["id"].(string)+"/status", map[string]string{"status": "done"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	today := time.Now().UTC().Format("2006-01-02")
	resp = post(t, freelancer, srv.URL+"/api/v1/reports", map[string]string{"client_id": clientID, "from": today, "to": today})
	wantStatus(t, resp, http.StatusCreated)
	return srv.URL + "/api/v1/reports/" + field(decode(t, resp), "report")["id"].(string)
}

func reportInUAH(t *testing.T, c *http.Client, url string) map[string]any {
	t.Helper()
	resp := get(t, c, url)
	wantStatus(t, resp, http.StatusOK)
	return field(field(decode(t, resp), "report"), "in_uah")
}

func TestRates_ReportTotalInHryvnias(t *testing.T) {
	bank, nbu := newFakeNBU(t, "45.0256")
	srv, _, _ := newAPIWith(t, func(cfg *config.Config) { cfg.NBUURL = nbu.URL })
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	reportURL := pricedReport(t, srv, freelancer, myID(t, srv, client))

	today := time.Now().UTC().Format("2006-01-02")
	got := reportInUAH(t, freelancer, reportURL)
	if got["rate"] != "45.0256" || got["rate_on"] != today || got["total_cents"] != float64(450256) {
		t.Fatalf("in_uah = %v", got)
	}
	resp := get(t, freelancer, reportURL)
	wantStatus(t, resp, http.StatusOK)
	if item := field(decode(t, resp), "report")["items"].([]any)[0].(map[string]any); item["price_in_uah_cents"] != float64(450256) {
		t.Fatalf("item = %v", item)
	}
	// The day's rate is kept: the bank is asked once.
	calls := bank.calls.Load()
	reportInUAH(t, freelancer, reportURL)
	if bank.calls.Load() != calls {
		t.Fatal("asked the bank again for a rate it had given")
	}

	// The client sees it too, sent and paid.
	resp = post(t, freelancer, reportURL+"/send", nil)
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	if got := reportInUAH(t, client, reportURL); got["total_cents"] != float64(450256) {
		t.Fatalf("client's in_uah = %v", got)
	}
	resp = post(t, client, reportURL+"/paid", nil)
	wantStatus(t, resp, http.StatusOK)
	if got := field(field(decode(t, resp), "report"), "in_uah"); got["rate_on"] != today {
		t.Fatalf("paid in_uah = %v", got)
	}
}

func TestRates_BankDownUsesTheLatestRateKept(t *testing.T) {
	bank, nbu := newFakeNBU(t, "45.0256")
	bank.down.Store(true)
	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	if _, err := testPool.Exec(context.Background(),
		"INSERT INTO exchange_rates (currency, rate_on, uah) VALUES ('USD', $1, 44.5)", yesterday); err != nil {
		t.Fatal(err)
	}
	srv, _, _ := newAPIWith(t, func(cfg *config.Config) { cfg.NBUURL = nbu.URL })
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	reportURL := pricedReport(t, srv, freelancer, myID(t, srv, client))

	got := reportInUAH(t, freelancer, reportURL)
	if got["rate"] != "44.5" || got["rate_on"] != yesterday || got["total_cents"] != float64(445000) {
		t.Fatalf("in_uah = %v", got)
	}
	// A bank that is down is left alone for a while.
	calls := bank.calls.Load()
	reportInUAH(t, freelancer, reportURL)
	if bank.calls.Load() != calls {
		t.Fatal("asked a bank that is down again at once")
	}
}

func TestRates_NotForHryvniasOrWithoutARate(t *testing.T) {
	bank, nbu := newFakeNBU(t, "45.0256")
	bank.down.Store(true)
	srv, _, _ := newAPIWith(t, func(cfg *config.Config) { cfg.NBUURL = nbu.URL })
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	clientID := myID(t, srv, client)

	// No rate at all: the report shows without hryvnias.
	if got := reportInUAH(t, freelancer, pricedReport(t, srv, freelancer, clientID)); got != nil {
		t.Fatalf("in_uah without a rate = %v", got)
	}

	// A freelancer who bills in hryvnias needs none.
	bank.down.Store(false)
	uah := signUpFreelancer(t, srv, "uah@example.com")
	resp := do(t, uah, http.MethodPut, srv.URL+"/api/v1/me/currency", map[string]string{"currency": "UAH"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	uahClient := acceptNew(t, srv, invite(t, srv, uah, "uah-client@example.com"))
	if got := reportInUAH(t, uah, pricedReport(t, srv, uah, myID(t, srv, uahClient))); got != nil {
		t.Fatalf("in_uah for a report in hryvnias = %v", got)
	}
}
