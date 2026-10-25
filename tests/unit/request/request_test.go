package request_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pmaxis/helpic/internal/request"
)

func TestDecode_ContentType(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		want        int
	}{
		{"JSON", "application/json", http.StatusOK},
		{"JSON with a charset", "application/json; charset=utf-8", http.StatusOK},
		{"a form's plain text", "text/plain", http.StatusUnsupportedMediaType},
		{"a form", "application/x-www-form-urlencoded", http.StatusUnsupportedMediaType},
		{"no label", "", http.StatusUnsupportedMediaType},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"title":"x"}`))
			if c.contentType != "" {
				req.Header.Set("Content-Type", c.contentType)
			}
			rec := httptest.NewRecorder()
			var dst struct{ Title string }
			if request.Decode(rec, req, &dst) {
				rec.WriteHeader(http.StatusOK)
			}
			if rec.Code != c.want {
				t.Fatalf("status = %d, want %d", rec.Code, c.want)
			}
		})
	}
}
