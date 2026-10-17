// Package token issues and verifies the JWTs used for authentication. It is
// shared infrastructure: the auth service issues tokens, the auth middleware
// verifies them.
package token

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ErrInvalid is returned for any token that fails signature or claim validation
// (including expired tokens).
var ErrInvalid = errors.New("invalid token")

// claims are the registered claims plus the account role, which never changes
// for an account and so is safe to carry in the token, and the session the
// token was issued for.
type claims struct {
	Role      string `json:"role"`
	SessionID string `json:"sid"`
	jwt.RegisteredClaims
}

// Claims is what a verified token says about its bearer.
type Claims struct {
	UserID    string
	Role      string
	SessionID string
}

// Manager signs and parses HS256 JWTs.
type Manager struct {
	secret     []byte
	expiration time.Duration
}

// NewManager creates a token Manager. The secret must be non-empty.
func NewManager(secret string, expiration time.Duration) *Manager {
	return &Manager{secret: []byte(secret), expiration: expiration}
}

// Generate returns a signed JWT whose subject is the given user ID and which
// carries the account role and the session, together with its expiry time
// (useful for setting the cookie's Max-Age).
func (m *Manager) Generate(userID, role, sessionID string) (string, time.Time, error) {
	now := time.Now()
	expiresAt := now.Add(m.expiration)
	c := claims{
		Role:      role,
		SessionID: sessionID,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(m.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign token: %w", err)
	}
	return signed, expiresAt, nil
}

// Verify validates the token and returns the user ID from its subject claim,
// the account role and the session. A token without a session (issued before
// sessions existed) is invalid.
func (m *Manager) Verify(raw string) (Claims, error) {
	c := &claims{}
	parsed, err := jwt.ParseWithClaims(raw, c, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return m.secret, nil
	})
	if err != nil || !parsed.Valid {
		return Claims{}, ErrInvalid
	}
	if c.Subject == "" || c.Role == "" || c.SessionID == "" {
		return Claims{}, ErrInvalid
	}
	return Claims{UserID: c.Subject, Role: c.Role, SessionID: c.SessionID}, nil
}
