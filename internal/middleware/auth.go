// Package middleware holds shared HTTP middleware: authentication, CORS and
// panic recovery. It is application infrastructure, not a feature.
package middleware

import (
	"context"
	"net/http"

	"github.com/pmaxis/helpic/internal/response"
	"github.com/pmaxis/helpic/internal/token"
)

type contextKey string

const (
	userIDKey    contextKey = "userID"
	roleKey      contextKey = "role"
	sessionIDKey contextKey = "sessionID"
)

// TokenVerifier is satisfied by *token.Manager.
type TokenVerifier interface {
	Verify(raw string) (token.Claims, error)
}

// Auth returns middleware that reads the JWT from the HttpOnly cookie,
// validates its signature and expiration, and stores the user ID, role and
// session in the request context. Missing, malformed or expired tokens get a 401.
func Auth(verifier TokenVerifier, cookieName string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(cookieName)
			if err != nil || cookie.Value == "" {
				response.Error(w, http.StatusUnauthorized, response.CodeUnauthorized, "Authentication required")
				return
			}
			c, err := verifier.Verify(cookie.Value)
			if err != nil {
				response.Error(w, http.StatusUnauthorized, response.CodeUnauthorized, "Authentication required")
				return
			}
			ctx := context.WithValue(r.Context(), userIDKey, c.UserID)
			ctx = context.WithValue(ctx, roleKey, c.Role)
			ctx = context.WithValue(ctx, sessionIDKey, c.SessionID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// UserID returns the authenticated user's ID from the request context.
func UserID(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(userIDKey).(string)
	return id, ok && id != ""
}

// Role returns the authenticated user's role from the request context.
func Role(ctx context.Context) string {
	role, _ := ctx.Value(roleKey).(string)
	return role
}

// SessionID returns the session the request's token was issued for.
func SessionID(ctx context.Context) string {
	id, _ := ctx.Value(sessionIDKey).(string)
	return id
}

// RequireRole lets through only users with the given role; others get a 403.
// Must run after Auth.
func RequireRole(role string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if Role(r.Context()) != role {
				response.Error(w, http.StatusForbidden, response.CodeForbidden, "You don't have access to this resource")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
