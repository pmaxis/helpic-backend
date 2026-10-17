package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/pmaxis/helpic/internal/middleware"
	"github.com/pmaxis/helpic/internal/user"
)

// A session is one signed-in device. The browser holds a short-lived access
// token (a JWT naming the session) and a long-lived refresh token, which is
// swapped for a new one on every refresh. Only the refresh token's hash is
// stored.

// refreshGrace is how long the refresh token a rotation replaced still gets a
// new access token: two tabs refreshing at once both send the same token, and
// the slower one must not look like a thief. Used later than that, it is one.
const refreshGrace = 30 * time.Second

// maxUserAgent caps the user agent a session remembers.
const maxUserAgent = 512

var (
	// ErrSessionInvalid is returned for a refresh token that is unknown,
	// expired or was replaced long ago.
	ErrSessionInvalid = errors.New("session invalid")

	// ErrSessionNotFound is returned when the user has no such session.
	ErrSessionNotFound = errors.New("session not found")
)

// Client is the device signing in, as a session remembers it.
type Client struct {
	UserAgent string
	IP        string
}

// ClientOf reads the device from a request. The IP is the connection's peer,
// or the client behind a trusted proxy (see middleware.RealIP).
func ClientOf(r *http.Request) Client {
	ua := r.UserAgent()
	if len(ua) > maxUserAgent {
		ua = ua[:maxUserAgent]
	}
	return Client{UserAgent: ua, IP: middleware.ClientIP(r)}
}

// Session is a signed-in device.
type Session struct {
	ID         string
	UserID     string
	UserAgent  string
	IP         string
	CreatedAt  time.Time
	LastUsedAt time.Time
	ExpiresAt  time.Time
}

// Tokens are what signing in or refreshing hands the browser. Refresh is empty
// when the browser keeps the refresh token it has.
type Tokens struct {
	Access         string
	AccessExpires  time.Time
	Refresh        string
	RefreshExpires time.Time
}

// newRefreshToken returns a random refresh token and the hash to store.
func newRefreshToken() (string, []byte, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, fmt.Errorf("generate refresh token: %w", err)
	}
	raw := base64.RawURLEncoding.EncodeToString(b)
	return raw, hashToken(raw), nil
}

func hashToken(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}

// --- service -------------------------------------------------------------------

// StartSession signs in an account that is already verified: by password, or
// one just created from an invitation.
func (s *Service) StartSession(ctx context.Context, u user.User, c Client) (Tokens, error) {
	raw, hash, err := newRefreshToken()
	if err != nil {
		return Tokens{}, err
	}
	expires := time.Now().Add(s.refreshTTL)
	sess, err := s.repo.CreateSession(ctx, u.ID, hash, c, expires)
	if err != nil {
		return Tokens{}, err
	}
	access, accessExpires, err := s.tokens.Generate(u.ID, u.Role, sess.ID)
	if err != nil {
		return Tokens{}, err
	}
	return Tokens{Access: access, AccessExpires: accessExpires, Refresh: raw, RefreshExpires: expires}, nil
}

// Refresh swaps a refresh token for a new one and a new access token. A token
// that was replaced a moment ago (another tab refreshed first) gets only an
// access token; one replaced earlier than that is a stolen copy, and the
// session ends for everyone holding it.
func (s *Service) Refresh(ctx context.Context, raw string) (Tokens, error) {
	if raw == "" {
		return Tokens{}, ErrSessionInvalid
	}
	old := hashToken(raw)
	next, nextHash, err := newRefreshToken()
	if err != nil {
		return Tokens{}, err
	}
	expires := time.Now().Add(s.refreshTTL)

	sess, role, err := s.repo.Rotate(ctx, old, nextHash, expires)
	if err == nil {
		access, accessExpires, err := s.tokens.Generate(sess.UserID, role, sess.ID)
		if err != nil {
			return Tokens{}, err
		}
		return Tokens{Access: access, AccessExpires: accessExpires, Refresh: next, RefreshExpires: expires}, nil
	}
	if !errors.Is(err, ErrSessionNotFound) {
		return Tokens{}, err
	}

	sess, rotatedAt, role, err := s.repo.Replaced(ctx, old)
	if errors.Is(err, ErrSessionNotFound) {
		return Tokens{}, ErrSessionInvalid
	}
	if err != nil {
		return Tokens{}, err
	}
	if time.Since(rotatedAt) > refreshGrace {
		if err := s.repo.DeleteSession(ctx, sess.ID); err != nil {
			return Tokens{}, err
		}
		return Tokens{}, ErrSessionInvalid
	}
	access, accessExpires, err := s.tokens.Generate(sess.UserID, role, sess.ID)
	if err != nil {
		return Tokens{}, err
	}
	return Tokens{Access: access, AccessExpires: accessExpires}, nil
}

// Logout ends the session the refresh token belongs to. An unknown token is
// not an error: the browser is signed out either way.
func (s *Service) Logout(ctx context.Context, raw string) error {
	if raw == "" {
		return nil
	}
	return s.repo.DeleteByRefresh(ctx, hashToken(raw))
}

// Sessions lists the user's signed-in devices, the most recently used first.
func (s *Service) Sessions(ctx context.Context, userID string) ([]Session, error) {
	return s.repo.ListSessions(ctx, userID)
}

// EndSession signs one of the user's devices out. Its access token works
// until it expires; it can't be refreshed.
func (s *Service) EndSession(ctx context.Context, userID, id string) error {
	return s.repo.DeleteUserSession(ctx, userID, id)
}

// EndOtherSessions signs out every device of the user but the one keepID
// names.
func (s *Service) EndOtherSessions(ctx context.Context, userID, keepID string) error {
	return s.repo.DeleteOtherSessions(ctx, userID, keepID)
}

// --- repository ----------------------------------------------------------------

const sessionColumns = `s.id, s.user_id, s.user_agent, s.ip, s.created_at, s.last_used_at, s.expires_at`

func scanSession(row pgx.Row, extra ...any) (Session, error) {
	var sess Session
	dest := append([]any{&sess.ID, &sess.UserID, &sess.UserAgent, &sess.IP, &sess.CreatedAt, &sess.LastUsedAt, &sess.ExpiresAt}, extra...)
	return sess, row.Scan(dest...)
}

// CreateSession stores a new session, clearing the user's expired ones.
func (r *Repository) CreateSession(ctx context.Context, userID string, refreshHash []byte, c Client, expiresAt time.Time) (Session, error) {
	if _, err := r.db.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1 AND expires_at <= now()`, userID); err != nil {
		return Session{}, fmt.Errorf("clear expired sessions: %w", err)
	}
	sess, err := scanSession(r.db.QueryRow(ctx, `
		INSERT INTO sessions AS s (user_id, refresh_hash, user_agent, ip, expires_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+sessionColumns, userID, refreshHash, c.UserAgent, c.IP, expiresAt))
	if err != nil {
		return Session{}, fmt.Errorf("create session: %w", err)
	}
	return sess, nil
}

// Rotate replaces a live session's current refresh token with next and
// returns the session with its user's role. ErrSessionNotFound when old is
// not a live session's current token.
func (r *Repository) Rotate(ctx context.Context, old, next []byte, expiresAt time.Time) (Session, string, error) {
	var role string
	sess, err := scanSession(r.db.QueryRow(ctx, `
		UPDATE sessions s
		SET prev_refresh_hash = s.refresh_hash, refresh_hash = $2, rotated_at = now(),
		    last_used_at = now(), expires_at = $3
		FROM users u
		WHERE u.id = s.user_id AND s.refresh_hash = $1 AND s.expires_at > now()
		RETURNING `+sessionColumns+`, u.role`, old, next, expiresAt), &role)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, "", ErrSessionNotFound
	}
	if err != nil {
		return Session{}, "", fmt.Errorf("rotate session: %w", err)
	}
	return sess, role, nil
}

// Replaced finds the live session whose previous refresh token this is, with
// when it was replaced and the user's role. ErrSessionNotFound when none.
func (r *Repository) Replaced(ctx context.Context, hash []byte) (Session, time.Time, string, error) {
	var (
		rotatedAt time.Time
		role      string
	)
	sess, err := scanSession(r.db.QueryRow(ctx, `
		SELECT `+sessionColumns+`, s.rotated_at, u.role
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.prev_refresh_hash = $1 AND s.expires_at > now()`, hash), &rotatedAt, &role)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, time.Time{}, "", ErrSessionNotFound
	}
	if err != nil {
		return Session{}, time.Time{}, "", fmt.Errorf("find replaced session: %w", err)
	}
	return sess, rotatedAt, role, nil
}

// DeleteSession removes a session.
func (r *Repository) DeleteSession(ctx context.Context, id string) error {
	if _, err := r.db.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, id); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// DeleteByRefresh removes the session holding this refresh token, current or
// just replaced.
func (r *Repository) DeleteByRefresh(ctx context.Context, hash []byte) error {
	if _, err := r.db.Exec(ctx, `DELETE FROM sessions WHERE refresh_hash = $1 OR prev_refresh_hash = $1`, hash); err != nil {
		return fmt.Errorf("delete session by token: %w", err)
	}
	return nil
}

// ListSessions returns the user's live sessions, the most recently used first.
func (r *Repository) ListSessions(ctx context.Context, userID string) ([]Session, error) {
	rows, err := r.db.Query(ctx, `
		SELECT `+sessionColumns+` FROM sessions s
		WHERE s.user_id = $1 AND s.expires_at > now()
		ORDER BY s.last_used_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		sess, err := scanSession(rows)
		if err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		out = append(out, sess)
	}
	return out, rows.Err()
}

// DeleteUserSession removes one of the user's sessions. ErrSessionNotFound
// when the user has no such session.
func (r *Repository) DeleteUserSession(ctx context.Context, userID, id string) error {
	tag, err := r.db.Exec(ctx, `DELETE FROM sessions WHERE id::text = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrSessionNotFound
	}
	return nil
}

// DeleteOtherSessions removes all of the user's sessions but keepID.
func (r *Repository) DeleteOtherSessions(ctx context.Context, userID, keepID string) error {
	if _, err := r.db.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1 AND id::text <> $2`, userID, keepID); err != nil {
		return fmt.Errorf("delete other sessions: %w", err)
	}
	return nil
}
