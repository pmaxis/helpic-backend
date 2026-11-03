package linkpreview_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/pmaxis/helpic/internal/linkpreview"
)

// loopback lets the fetcher reach the test's own server, and nothing else.
func loopback() *linkpreview.Fetcher {
	return linkpreview.New(linkpreview.Options{Allow: func(ap netip.AddrPort) bool { return ap.Addr().IsLoopback() }})
}

func page(contentType, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write([]byte(body))
	}
}

func TestTitle(t *testing.T) {
	cases := []struct {
		name, contentType, body, want string
	}{
		{"title", "text/html; charset=utf-8", "<html><head><title>  Docs &amp;\n  more </title></head><body>x</body></html>", "Docs & more"},
		{"og:title first", "text/html", `<head><title>Site</title><meta property="og:title" content="The page"></head>`, "The page"},
		{"twitter:title", "text/html", `<head><meta name="twitter:title" content="Tweet title"><title>Site</title></head>`, "Tweet title"},
		{"another charset", "text/html; charset=windows-1251", "<head><title>\xd2\xc7</title></head>", "ТЗ"},
		{"meta charset", "text/html", "<head><meta charset=\"windows-1251\"><title>\xd2\xc7</title></head>", "ТЗ"},
		{"a long title cut", "text/html", "<title>" + strings.Repeat("a", 400) + "</title>", strings.Repeat("a", 299) + "…"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(page(c.contentType, c.body))
			defer srv.Close()
			got, err := loopback().Title(context.Background(), srv.URL+"/a#frag", "uk")
			if err != nil || got != c.want {
				t.Fatalf("Title = %q, %v; want %q", got, err, c.want)
			}
		})
	}
}

func TestNoTitle(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"no title":    page("text/html", "<head></head><body><title>late</title></body>"),
		"not html":    page("application/pdf", "%PDF-1.7 <title>x</title>"),
		"not found":   func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "<title>404</title>", http.StatusNotFound) },
		"empty title": page("text/html", "<title>   </title>"),
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(h)
			defer srv.Close()
			if _, err := loopback().Title(context.Background(), srv.URL, ""); !errors.Is(err, linkpreview.ErrNoTitle) {
				t.Fatalf("err = %v, want ErrNoTitle", err)
			}
		})
	}
}

func TestInvalidURL(t *testing.T) {
	for _, raw := range []string{"", "ftp://example.com", "javascript:alert(1)", "https://", "https://user:pass@example.com", "example.com", "https://x.io/" + strings.Repeat("a", 2000)} {
		if _, err := loopback().Title(context.Background(), raw, ""); !errors.Is(err, linkpreview.ErrInvalidURL) {
			t.Errorf("Title(%q) err = %v, want ErrInvalidURL", raw, err)
		}
	}
}

// With the default rule, the server's own network is out of reach: the
// test's server, and a public-looking page redirecting to it.
func TestBlocksPrivateAddresses(t *testing.T) {
	var hits atomic.Int32
	inside := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		page("text/html", "<title>secret</title>")(w, nil)
	}))
	defer inside.Close()

	f := linkpreview.New(linkpreview.Options{})
	for _, raw := range []string{inside.URL, strings.Replace(inside.URL, "127.0.0.1", "localhost", 1), "http://[::1]/", "http://169.254.169.254/latest/meta-data/"} {
		if _, err := f.Title(context.Background(), raw, ""); err == nil {
			t.Errorf("Title(%q) went through", raw)
		}
	}

	// The first hop is allowed (loopback on its port), its redirect isn't
	// (the inside server stands for another address).
	insidePort := netip.MustParseAddrPort(strings.TrimPrefix(inside.URL, "http://")).Port()
	outside := httptest.NewServer(http.RedirectHandler(inside.URL, http.StatusFound))
	defer outside.Close()
	f = linkpreview.New(linkpreview.Options{Allow: func(ap netip.AddrPort) bool { return ap.Addr().IsLoopback() && ap.Port() != insidePort }})
	if _, err := f.Title(context.Background(), outside.URL, ""); err == nil {
		t.Error("a redirect to a blocked address went through")
	}
	if hits.Load() != 0 {
		t.Errorf("the inside server was reached %d times", hits.Load())
	}
}

func TestAllowed(t *testing.T) {
	cases := map[string]bool{
		"93.184.215.14:443":           true,
		"93.184.215.14:80":            true,
		"[2606:2800:21f:cb07::1]:443": true,
		"93.184.215.14:8080":          false,
		"93.184.215.14:22":            false,
		"127.0.0.1:443":               false,
		"10.1.2.3:80":                 false,
		"172.16.0.1:80":               false,
		"192.168.1.1:80":              false,
		"169.254.169.254:80":          false,
		"100.64.0.1:80":               false,
		"0.0.0.0:80":                  false,
		"255.255.255.255:80":          false,
		"224.0.0.1:80":                false,
		"[::1]:443":                   false,
		"[fd00::1]:443":               false,
		"[fe80::1]:443":               false,
		"[::ffff:127.0.0.1]:443":      false,
		"[::ffff:10.0.0.1]:443":       false,
		"[64:ff9b::a00:1]:443":        false,
		"[2002:a00:1::]:443":          false,
	}
	for addr, want := range cases {
		if got := linkpreview.Allowed(netip.MustParseAddrPort(addr)); got != want {
			t.Errorf("Allowed(%s) = %v, want %v", addr, got, want)
		}
	}
}

func TestRemembersTitles(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		page("text/html", "<title>Once</title>")(w, nil)
	}))
	defer srv.Close()
	f := loopback()
	for range 3 {
		if got, err := f.Title(context.Background(), srv.URL, "en"); err != nil || got != "Once" {
			t.Fatalf("Title = %q, %v", got, err)
		}
	}
	if hits.Load() != 1 {
		t.Errorf("fetched %d times, want 1", hits.Load())
	}
}
