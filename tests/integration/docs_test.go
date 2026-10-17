package integration

import (
	"net/http"
	"strings"
	"testing"
)

func TestAPIDocsAndVersion(t *testing.T) {
	srv, c := newAPI(t)

	// The spec describes the versioned API.
	resp := get(t, c, srv.URL+"/api/docs/doc.json")
	wantStatus(t, resp, http.StatusOK)
	spec := decode(t, resp)
	if spec["basePath"] != "/api/v1" || len(spec["paths"].(map[string]any)) == 0 {
		t.Fatalf("spec = basePath %v, %d paths", spec["basePath"], len(spec["paths"].(map[string]any)))
	}
	resp = get(t, c, srv.URL+"/api/docs/index.html")
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	if !strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("UI content type = %q", resp.Header.Get("Content-Type"))
	}

	// The API lives under /api/v1; the unversioned path is gone.
	wantStatus(t, get(t, c, srv.URL+"/api/v1/me"), http.StatusUnauthorized)
	wantStatus(t, get(t, c, srv.URL+"/api/me"), http.StatusNotFound)
}
