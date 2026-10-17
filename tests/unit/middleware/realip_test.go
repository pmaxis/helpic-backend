package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/pmaxis/helpic/internal/middleware"
)

func TestRealIP(t *testing.T) {
	proxies := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("192.0.2.7/32")}
	cases := []struct {
		name    string
		trusted []netip.Prefix
		peer    string
		xff     string
		want    string
	}{
		{"no proxies configured: the header is ignored", nil, "203.0.113.9:4000", "1.2.3.4", "203.0.113.9"},
		{"untrusted peer: the header is ignored", proxies, "203.0.113.9:4000", "1.2.3.4", "203.0.113.9"},
		{"trusted peer: the client it names", proxies, "10.1.2.3:4000", "198.51.100.5", "198.51.100.5"},
		{"a chain: the rightmost that isn't a proxy", proxies, "10.1.2.3:4000", "6.6.6.6, 198.51.100.5, 192.0.2.7", "198.51.100.5"},
		{"all proxies: the leftmost", proxies, "10.1.2.3:4000", "10.9.9.9, 192.0.2.7", "10.9.9.9"},
		{"trusted peer without the header", proxies, "10.1.2.3:4000", "", "10.1.2.3"},
		{"garbage in the header", proxies, "10.1.2.3:4000", "not-an-ip", "10.1.2.3"},
		{"IPv6", []netip.Prefix{netip.MustParsePrefix("::1/128")}, "[::1]:4000", "2001:db8::5", "2001:db8::5"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got string
			h := middleware.RealIP(c.trusted)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				got = middleware.ClientIP(r)
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = c.peer
			if c.xff != "" {
				req.Header.Set("X-Forwarded-For", c.xff)
			}
			h.ServeHTTP(httptest.NewRecorder(), req)
			if got != c.want {
				t.Fatalf("client IP = %q, want %q", got, c.want)
			}
		})
	}
}
