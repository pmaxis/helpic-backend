package integration

import (
	"net/http"
	"strings"
	"testing"
)

// A signed-in browser's cookie goes along from any page of the same site; the
// API still takes changes only from the frontend, and only as JSON.
func TestCrossOrigin_ChangesOnlyFromTheFrontend(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "csrf@example.com")

	send := func(origin, fetchSite, contentType string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/tasks", strings.NewReader(`{"title":"Pay me"}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Origin", origin)
		req.Header.Set("Sec-Fetch-Site", fetchSite)
		req.Header.Set("Content-Type", contentType)
		resp, err := freelancer.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	resp := send("http://landing.localhost:5173", "same-site", "application/json")
	wantStatus(t, resp, http.StatusForbidden)
	if code := errorCode(decode(t, resp)); code != "FORBIDDEN" {
		t.Fatalf("code = %q, want FORBIDDEN", code)
	}

	resp = send(testCfg.CORSOrigin, "same-site", "text/plain")
	wantStatus(t, resp, http.StatusUnsupportedMediaType)
	if code := errorCode(decode(t, resp)); code != "UNSUPPORTED_MEDIA_TYPE" {
		t.Fatalf("code = %q, want UNSUPPORTED_MEDIA_TYPE", code)
	}

	resp = send(testCfg.CORSOrigin, "same-site", "application/json")
	wantStatus(t, resp, http.StatusCreated)
	resp.Body.Close()
}
