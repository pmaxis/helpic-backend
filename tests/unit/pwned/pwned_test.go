package pwned_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pmaxis/helpic/internal/pwned"
)

// SHA-1 of "password123": CBFDAC6008F9CAB4083784CBD1874F76618D2A97.
const prefix, suffix = "CBFDA", "C6008F9CAB4083784CBD1874F76618D2A97"

func TestBreached(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"listed", "0018A45C4D1DEF81644B54AB7F969B88D65:1\r\n" + suffix + ":3861493\r\n", true},
		{"listed in lower case", "c6008f9cab4083784cbd1874f76618d2a97:2\n", true},
		{"padding only", suffix + ":0\r\n", false},
		{"not listed", "0018A45C4D1DEF81644B54AB7F969B88D65:1\r\n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/range/"+prefix {
					t.Errorf("path = %q, want /range/%s", r.URL.Path, prefix)
				}
				w.Write([]byte(c.body)) //nolint:errcheck
			}))
			defer srv.Close()
			got, err := pwned.NewClient(srv.URL+"/").Breached(context.Background(), "password123")
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Fatalf("breached = %v, want %v", got, c.want)
			}
		})
	}
}

func TestBreached_ServiceError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	if _, err := pwned.NewClient(srv.URL).Breached(context.Background(), "password123"); err == nil {
		t.Fatal("want an error when the service fails")
	}
}
