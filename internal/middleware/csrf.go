package middleware

import (
	"fmt"
	"net/http"

	"github.com/pmaxis/helpic/internal/response"
)

// CrossOrigin returns middleware that turns away state-changing requests a
// browser sends from another origin, the frontend's (trustedOrigin) aside.
// SameSite cookies alone don't stop them: a page on a sibling subdomain is the
// same site, and its forms and fetches carry the session cookie.
//
// The browser says where a request came from in Sec-Fetch-Site, or else in
// Origin; GET, HEAD and OPTIONS always pass, and so do requests with neither
// header, which come from outside a browser and carry no one's cookies.
func CrossOrigin(trustedOrigin string) (func(http.Handler) http.Handler, error) {
	p := http.NewCrossOriginProtection()
	if err := p.AddTrustedOrigin(trustedOrigin); err != nil {
		return nil, fmt.Errorf("cors origin: %w", err)
	}
	p.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response.Error(w, http.StatusForbidden, response.CodeForbidden, "Requests from other sites are not allowed")
	}))
	return p.Handler, nil
}
