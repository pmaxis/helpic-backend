package integration

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestLinkPreview(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	url := srv.URL + "/api/v1/link-preview"

	// Signed in only.
	resp := do(t, http.DefaultClient, http.MethodPost, url, map[string]any{"url": "https://example.com"})
	wantStatus(t, resp, http.StatusUnauthorized)
	resp.Body.Close()

	for _, bad := range []string{"", "example.com", "ftp://example.com/a", "javascript:alert(1)"} {
		resp = do(t, freelancer, http.MethodPost, url, map[string]any{"url": bad})
		wantStatus(t, resp, http.StatusBadRequest)
		resp.Body.Close()
	}

	// The server's own network is out of reach, here the test's own page.
	var hits atomic.Int32
	inside := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<title>secret</title>"))
	}))
	defer inside.Close()
	for _, raw := range []string{inside.URL, "http://localhost/", "http://169.254.169.254/latest/meta-data/"} {
		resp = do(t, freelancer, http.MethodPost, url, map[string]any{"url": raw})
		wantStatus(t, resp, http.StatusUnprocessableEntity)
		resp.Body.Close()
	}
	if hits.Load() != 0 {
		t.Fatalf("the inside page was fetched %d times", hits.Load())
	}
}
