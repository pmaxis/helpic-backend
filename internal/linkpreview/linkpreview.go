// Package linkpreview reads the title of a web page, so a link put in a
// task's description can be named after the page it leads to.
//
// The server fetches what a user names, so the fetch is fenced in: only
// http and https on ports 80 and 443, only public addresses (checked on the
// address each connection is made to, so a name that resolves to a public
// address and then to a private one gets nowhere), a few redirects, a short
// time, and only the head of an HTML page is read.
package linkpreview

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

const (
	// timeout bounds a fetch, redirects and all; someone waits on it.
	timeout = 5 * time.Second
	// maxRedirects a fetch follows.
	maxRedirects = 3
	// maxBytes of a page read: a title is in its head.
	maxBytes = 512 << 10
	// MaxURLLength is the longest address taken.
	MaxURLLength = 2000
	// maxTitle is the most of a title kept, in characters.
	maxTitle = 300
	// cacheFor is how long a title is remembered; cacheSize how many are.
	cacheFor  = time.Hour
	cacheSize = 1000
)

var (
	// ErrInvalidURL is an address that isn't an http(s) URL of a host.
	ErrInvalidURL = errors.New("linkpreview: invalid url")
	// ErrNoTitle is a page that couldn't be read or has no title.
	ErrNoTitle = errors.New("linkpreview: no title")
	// errBlocked is an address the fetch may not go to.
	errBlocked = errors.New("linkpreview: address not allowed")
)

// Options adjust a Fetcher; the zero value is what the server runs with.
type Options struct {
	// Allow tells whether a connection may be made to an address and port;
	// nil allows public addresses on ports 80 and 443 (see Allowed).
	Allow func(addr netip.AddrPort) bool
}

// Fetcher reads pages' titles.
type Fetcher struct {
	http *http.Client

	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	title string
	until time.Time
}

// New creates a Fetcher.
func New(opts Options) *Fetcher {
	allow := opts.Allow
	if allow == nil {
		allow = Allowed
	}
	dialer := &net.Dialer{
		Timeout: timeout,
		// Control runs on the very address being dialed, after the name
		// was resolved: no lookup can slip a private address past it.
		Control: func(_, address string, _ syscall.RawConn) error {
			ap, err := netip.ParseAddrPort(address)
			if err != nil || !allow(ap) {
				return errBlocked
			}
			return nil
		},
	}
	transport := &http.Transport{
		// Never through a proxy: the check above must see the page's own address.
		Proxy:                  nil,
		DialContext:            dialer.DialContext,
		TLSHandshakeTimeout:    timeout,
		ResponseHeaderTimeout:  timeout,
		MaxResponseHeaderBytes: 64 << 10,
		DisableKeepAlives:      true,
	}
	client := &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > maxRedirects {
				return errors.New("linkpreview: too many redirects")
			}
			if _, err := Parse(req.URL.String()); err != nil {
				return err
			}
			return nil
		},
	}
	return &Fetcher{http: client, cache: make(map[string]cached)}
}

// Allowed is the default rule: a public unicast address, on port 80 or 443.
func Allowed(ap netip.AddrPort) bool {
	if p := ap.Port(); p != 80 && p != 443 {
		return false
	}
	return Public(ap.Addr())
}

// nonPublic are ranges that reach no one on the internet, or someone's own
// network, beyond what netip's own checks know.
var nonPublic = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),       // "this network"
	netip.MustParsePrefix("100.64.0.0/10"),   // carrier-grade NAT
	netip.MustParsePrefix("192.0.0.0/24"),    // IETF protocol assignments
	netip.MustParsePrefix("192.0.2.0/24"),    // documentation
	netip.MustParsePrefix("198.18.0.0/15"),   // benchmarking
	netip.MustParsePrefix("198.51.100.0/24"), // documentation
	netip.MustParsePrefix("203.0.113.0/24"),  // documentation
	netip.MustParsePrefix("240.0.0.0/4"),     // reserved, and broadcast
	netip.MustParsePrefix("64:ff9b::/96"),    // NAT64: an IPv4 address inside
	netip.MustParsePrefix("64:ff9b:1::/48"),  // local NAT64
	netip.MustParsePrefix("2001:db8::/32"),   // documentation
	netip.MustParsePrefix("2002::/16"),       // 6to4: an IPv4 address inside
	netip.MustParsePrefix("2001::/32"),       // Teredo: an IPv4 address inside
}

// Public reports whether addr is a public unicast address.
func Public(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() || !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() ||
		addr.IsLinkLocalUnicast() || addr.IsUnspecified() || addr.IsMulticast() {
		return false
	}
	for _, p := range nonPublic {
		if p.Contains(addr) {
			return false
		}
	}
	return true
}

// Parse checks an address and returns it as a URL: http or https, with a
// host and no user and password in it. Where it may lead is the dialer's to
// tell, on the address it connects to.
func Parse(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > MaxURLLength {
		return nil, ErrInvalidURL
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return nil, ErrInvalidURL
	}
	u.Fragment = ""
	return u, nil
}

// Title fetches the page at raw and returns its title: what it names itself
// for sharing (og:title, twitter:title), else its <title>.
func (f *Fetcher) Title(ctx context.Context, raw, language string) (string, error) {
	u, err := Parse(raw)
	if err != nil {
		return "", err
	}
	// A page may name itself in the reader's language.
	key := language + " " + u.String()
	if t, ok := f.cached(key); ok {
		if t == "" {
			return "", ErrNoTitle
		}
		return t, nil
	}
	title, err := f.fetch(ctx, u, language)
	if err != nil && !errors.Is(err, ErrNoTitle) {
		// A page that couldn't be reached may be back in a moment: not remembered.
		return "", err
	}
	f.remember(key, title)
	if title == "" {
		return "", ErrNoTitle
	}
	return title, nil
}

func (f *Fetcher) fetch(ctx context.Context, u *url.URL, language string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", ErrInvalidURL
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; HelpicLinkPreview/1.0)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9,*/*;q=0.1")
	if language != "" {
		req.Header.Set("Accept-Language", language+",en;q=0.8")
	}
	resp, err := f.http.Do(req) // #nosec G107 G704 -- the address is checked by Parse, and every connection by the dialer's Control
	if err != nil {
		return "", fmt.Errorf("linkpreview: fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", ErrNoTitle
	}
	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if mediaType != "text/html" && mediaType != "application/xhtml+xml" {
		return "", ErrNoTitle
	}
	body, err := charset.NewReader(io.LimitReader(resp.Body, maxBytes), resp.Header.Get("Content-Type"))
	if err != nil {
		return "", ErrNoTitle
	}
	return clean(findTitle(body)), nil
}

// findTitle reads the page's head for its title.
func findTitle(r io.Reader) string {
	z := html.NewTokenizer(r)
	var title, og, twitter string
	inTitle := false
	for {
		switch z.Next() {
		case html.ErrorToken:
			return first(og, twitter, title)
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			switch string(name) {
			case "title":
				inTitle = title == ""
			case "meta":
				var prop, content string
				for hasAttr {
					var k, v []byte
					k, v, hasAttr = z.TagAttr()
					switch string(k) {
					case "property", "name":
						prop = strings.ToLower(string(v))
					case "content":
						content = string(v)
					}
				}
				switch prop {
				case "og:title":
					og = content
				case "twitter:title":
					twitter = content
				}
			case "body":
				// The head is over; a title doesn't come later.
				return first(og, twitter, title)
			}
		case html.TextToken:
			if inTitle {
				title += string(z.Text())
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			switch string(name) {
			case "title":
				inTitle = false
			case "head":
				return first(og, twitter, title)
			}
		}
	}
}

func first(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// clean makes a title one line of at most maxTitle characters.
func clean(s string) string {
	s = strings.Join(strings.Fields(strings.ToValidUTF8(s, "")), " ")
	if utf8.RuneCountInString(s) > maxTitle {
		s = string([]rune(s)[:maxTitle-1]) + "…"
	}
	return s
}

func (f *Fetcher) cached(key string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.cache[key]
	if !ok || time.Now().After(c.until) {
		return "", false
	}
	return c.title, true
}

func (f *Fetcher) remember(key, title string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	if len(f.cache) >= cacheSize {
		for k, c := range f.cache {
			if now.After(c.until) {
				delete(f.cache, k)
			}
		}
		// Still full: forget any one, the map's order is as good as random.
		for k := range f.cache {
			if len(f.cache) < cacheSize {
				break
			}
			delete(f.cache, k)
		}
	}
	f.cache[key] = cached{title: title, until: now.Add(cacheFor)}
}
