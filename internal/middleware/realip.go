package middleware

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// RealIP puts the client's address in r.RemoteAddr when the request came
// through a trusted proxy: the rightmost X-Forwarded-For entry that isn't one
// of the proxies. From anyone else the header is ignored, since a client can
// write whatever it likes there.
func RealIP(trusted []netip.Prefix) func(http.Handler) http.Handler {
	isTrusted := func(ip netip.Addr) bool {
		for _, p := range trusted {
			if p.Contains(ip.Unmap()) {
				return true
			}
		}
		return false
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(trusted) == 0 {
				next.ServeHTTP(w, r)
				return
			}
			peer, err := netip.ParseAddr(ClientIP(r))
			if err != nil || !isTrusted(peer) {
				next.ServeHTTP(w, r)
				return
			}
			hops := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
			for i := len(hops) - 1; i >= 0; i-- {
				ip, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
				if err != nil {
					break
				}
				if !isTrusted(ip) || i == 0 {
					r.RemoteAddr = ip.Unmap().String()
					break
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ClientIP is the client's address: r.RemoteAddr without the port (RealIP
// has already put the real one there).
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
