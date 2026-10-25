package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pmaxis/helpic/internal/middleware"
)

func TestCrossOrigin(t *testing.T) {
	const frontend = "https://app.helpic.example"
	guard, err := middleware.CrossOrigin(frontend)
	if err != nil {
		t.Fatal(err)
	}
	h := guard(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	cases := []struct {
		name      string
		method    string
		fetchSite string
		origin    string
		want      int
	}{
		{"the frontend, same site", http.MethodPost, "same-site", frontend, http.StatusNoContent},
		{"the frontend, another site", http.MethodDelete, "cross-site", frontend, http.StatusNoContent},
		{"a sibling subdomain", http.MethodPost, "same-site", "https://landing.helpic.example", http.StatusForbidden},
		{"another site", http.MethodPut, "cross-site", "https://evil.example", http.StatusForbidden},
		{"the API's own pages (Swagger)", http.MethodPost, "same-origin", "https://api.helpic.example", http.StatusNoContent},
		{"typed into the address bar", http.MethodPost, "none", "", http.StatusNoContent},
		{"an old browser: Origin alone, another host", http.MethodPatch, "", "https://evil.example", http.StatusForbidden},
		{"an old browser: Origin alone, the frontend", http.MethodPatch, "", frontend, http.StatusNoContent},
		{"outside a browser: no headers", http.MethodPost, "", "", http.StatusNoContent},
		{"a safe method from another site", http.MethodGet, "cross-site", "https://evil.example", http.StatusNoContent},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(c.method, "https://api.helpic.example/api/v1/tasks", nil)
			if c.fetchSite != "" {
				req.Header.Set("Sec-Fetch-Site", c.fetchSite)
			}
			if c.origin != "" {
				req.Header.Set("Origin", c.origin)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != c.want {
				t.Fatalf("status = %d, want %d", rec.Code, c.want)
			}
		})
	}
}

func TestCrossOrigin_BadOrigin(t *testing.T) {
	if _, err := middleware.CrossOrigin("not a url"); err == nil {
		t.Fatal("want an error for a malformed origin")
	}
}
