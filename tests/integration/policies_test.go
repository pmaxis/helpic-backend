package integration

import (
	"net/http"
	"strings"
	"testing"
)

func policy(t *testing.T, c *http.Client, url string) map[string]any {
	t.Helper()
	resp := get(t, c, url)
	wantStatus(t, resp, http.StatusOK)
	return field(decode(t, resp), "policy")
}

func TestPolicies_AnyoneReadsTheDefault(t *testing.T) {
	srv, guest := newAPI(t)

	p := policy(t, guest, srv.URL+"/api/v1/policies/privacy")
	if p["locale"] != "en" || p["custom"] != false || p["updated_at"] != nil || !strings.Contains(p["body"].(string), "## ") {
		t.Fatalf("default privacy = %v", p)
	}
	// The language asked for, or else the request's.
	if p := policy(t, guest, srv.URL+"/api/v1/policies/terms?locale=uk"); p["locale"] != "uk" {
		t.Fatalf("terms = %v", p)
	}
	resp := inUkrainian(t, guest, http.MethodGet, srv.URL+"/api/v1/policies/cookies", nil)
	wantStatus(t, resp, http.StatusOK)
	if p := field(decode(t, resp), "policy"); p["locale"] != "uk" || !strings.Contains(p["body"].(string), "refresh_token") {
		t.Fatalf("cookies = %v", p)
	}

	wantStatus(t, get(t, guest, srv.URL+"/api/v1/policies/refunds"), http.StatusNotFound)
	wantStatus(t, get(t, guest, srv.URL+"/api/v1/policies/privacy?locale=de"), http.StatusNotFound)
}

func TestPolicies_AdminWritesAndResets(t *testing.T) {
	srv, guest := newAPI(t)
	someone := signUpFreelancer(t, srv, "someone@example.com")
	boss := signInAdmin(t, srv, "boss@example.com")
	turnOnTwoFactor(t, srv, boss)
	url := srv.URL + "/api/v1/admin/policies/privacy/uk"

	// Only admins write.
	resp := do(t, someone, http.MethodPut, url, map[string]string{"body": "Mine"})
	wantStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()
	wantStatus(t, get(t, someone, srv.URL+"/api/v1/admin/policies"), http.StatusForbidden)

	resp = do(t, boss, http.MethodPut, url, map[string]string{"body": "  \r\n "})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()
	resp = do(t, boss, http.MethodPut, url, map[string]string{"body": strings.Repeat("a", 100_001)})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()
	resp = do(t, boss, http.MethodPut, srv.URL+"/api/v1/admin/policies/refunds/uk", map[string]string{"body": "Mine"})
	wantStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()

	resp = do(t, boss, http.MethodPut, url, map[string]string{"body": "## Хто ми\r\n\r\nТОВ «Приклад»\n"})
	wantStatus(t, resp, http.StatusOK)
	if p := field(decode(t, resp), "policy"); p["body"] != "## Хто ми\n\nТОВ «Приклад»" || p["custom"] != true || p["updated_by"] != "boss@example.com" {
		t.Fatalf("saved = %v", p)
	}
	// Readers see it at once; the other language keeps its default.
	if p := policy(t, guest, srv.URL+"/api/v1/policies/privacy?locale=uk"); p["body"] != "## Хто ми\n\nТОВ «Приклад»" || p["updated_at"] == nil {
		t.Fatalf("read = %v", p)
	}
	if p := policy(t, guest, srv.URL+"/api/v1/policies/privacy?locale=en"); p["custom"] != false {
		t.Fatalf("English = %v", p)
	}

	resp = get(t, boss, srv.URL+"/api/v1/admin/policies")
	wantStatus(t, resp, http.StatusOK)
	list := decode(t, resp)["policies"].([]any)
	if len(list) != 6 {
		t.Fatalf("policies = %d, want 3 kinds × 2 languages", len(list))
	}
	custom := 0
	for _, raw := range list {
		if raw.(map[string]any)["custom"] == true {
			custom++
		}
	}
	if custom != 1 {
		t.Fatalf("custom = %d", custom)
	}

	resp = do(t, boss, http.MethodDelete, url, nil)
	wantStatus(t, resp, http.StatusOK)
	if p := field(decode(t, resp), "policy"); p["custom"] != false || p["updated_by"] != "" || !strings.Contains(p["body"].(string), "[назва власника]") {
		t.Fatalf("reset = %v", p)
	}
}
