package rates_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pmaxis/helpic/internal/rates"
)

func TestRate_Convert(t *testing.T) {
	cases := []struct {
		rate  string
		cents int64
		want  int64
	}{
		{"45.0256", 10000, 450256},                      // $100.00 → ₴4 502.56
		{"45.0256", 1, 45},                              // 45.0256 kopiyky rounds down
		{"0.5", 1, 1},                                   // a half rounds up
		{"59.47", 123456, 7341928},                      // 73 419.2832 rounds down
		{"45.0256", 100_000_000_000, 4_502_560_000_000}, // the largest price
	}
	for _, c := range cases {
		got, err := rates.Rate{UAH: c.rate}.Convert(c.cents)
		if err != nil || got != c.want {
			t.Errorf("%d at %s = %d, %v; want %d", c.cents, c.rate, got, err, c.want)
		}
	}
	if _, err := (rates.Rate{UAH: "abc"}).Convert(1); err == nil {
		t.Error("a rate that isn't a number converted")
	}
}

func TestClient_Rates(t *testing.T) {
	var asked string
	bank := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.Query().Get("date")
		w.Write([]byte(`[
			{"r030":12,"txt":"Алжирський динар","rate":0.33343,"cc":"DZD","exchangedate":"18.10.2026","special":null},
			{"r030":840,"txt":"Долар США","rate":44.8871,"cc":"USD","exchangedate":"18.10.2026","special":"N"},
			{"r030":978,"txt":"Євро","rate":50.3081,"cc":"EUR","exchangedate":"18.10.2026","special":null}
		]`))
	}))
	defer bank.Close()

	list, err := rates.NewClient(bank.URL).Rates(context.Background(), time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if asked != "20261018" {
		t.Fatalf("asked for %q", asked)
	}
	// Only the currencies Helpic bills in, with the bank's digits.
	if len(list) != 2 || list[0].Currency != "USD" || list[0].UAH != "44.8871" || list[1].UAH != "50.3081" ||
		!list[0].On.Equal(time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("rates = %+v", list)
	}
}

func TestClient_RatesFailure(t *testing.T) {
	bank := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer bank.Close()
	if _, err := rates.NewClient(bank.URL).Rates(context.Background(), time.Now()); err == nil {
		t.Fatal("no error from a bank that is down")
	}
}
