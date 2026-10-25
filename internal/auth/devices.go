package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/pmaxis/helpic/internal/i18n"
	"github.com/pmaxis/helpic/internal/mail"
	"github.com/pmaxis/helpic/internal/user"
)

// A known device is a browser the account was signed in from, by a random
// token in a cookie of its own. Signing in from any other browser is emailed
// to the user: if it wasn't them, someone else has the password.
const (
	// DeviceCookie holds the browser's token. It signs no one in.
	DeviceCookie = "known_device"
	// deviceTTL is how long a browser stays known without a sign-in.
	deviceTTL = 365 * 24 * time.Hour
	// deviceTokenLength is the length of a token NewToken makes.
	deviceTokenLength = 43
)

// How SeeDevice found a browser.
type deviceSeen int

const (
	deviceKnown deviceSeen = iota
	// deviceNew is a browser the account hasn't used.
	deviceNew
	// deviceFirst is the account's first browser: a new account, or one not
	// signed in since devices were first kept. It goes unannounced.
	deviceFirst
)

// UseLocation sets the time zone sign-in times are told in; UTC without it.
func (s *Service) UseLocation(loc *time.Location) {
	s.loc = loc
}

// recognize learns the browser signing in to u and returns the token for its
// device cookie, emailing u when the account didn't know the browser. A
// browser keeps its token when another account signs in on it; a token is
// made only for a browser that has none. A failure here signs in anyway,
// with no token and no email.
func (s *Service) recognize(ctx context.Context, u user.User, c Client) string {
	raw := c.Device
	if len(raw) != deviceTokenLength {
		var err error
		if raw, _, err = NewToken(); err != nil {
			slog.Error("failed to make a device token", "error", err)
			return ""
		}
	}
	seen, err := s.repo.SeeDevice(ctx, u.ID, HashToken(raw))
	if err != nil {
		slog.Error("failed to record a device", "user_id", u.ID, "error", err)
		return ""
	}
	if seen == deviceNew {
		s.newDeviceNotice(ctx, u, c)
	}
	return raw
}

// newDeviceNotice tells u by email about a sign-in from a browser the
// account doesn't know.
func (s *Service) newDeviceNotice(ctx context.Context, u user.User, c Client) {
	t := func(text string, args ...any) string { return i18n.T(u.Locale, text, args...) }
	loc := s.loc
	if loc == nil {
		loc = time.UTC
	}
	now := time.Now().In(loc)
	ip := c.IP
	if ip == "" {
		ip = t("unknown")
	}
	err := s.mail.Send(ctx, mail.Email{
		To:      u.Email,
		Locale:  u.Locale,
		Subject: t("New sign-in to your Helpic account"),
		Heading: t("New sign-in"),
		Paragraphs: []string{
			t("Someone signed in to your Helpic account, %s, from a device it hasn't been used on before.", u.Email),
			t("Device: %s", DeviceName(u.Locale, c.UserAgent)),
			t("IP address: %s", ip),
			t("Time: %s, %s (%s)", i18n.Date(u.Locale, now), now.Format("15:04"), loc.String()),
			t("If that was you, there's nothing to do. If it wasn't, change your password right away and sign that device out under Settings → Security."),
		},
		Button: &mail.Button{Text: t("Review your devices"), URL: s.appURL + "/settings?tab=security"},
	})
	if err != nil {
		slog.Error("failed to queue new sign-in email", "error", err)
	}
}

// recoveryCodeNotice tells the user by email that one of their recovery
// codes was used, and how many are left: a code is for a lost phone, so
// someone else using one means they have the codes and the password.
func (s *Service) recoveryCodeNotice(ctx context.Context, userID string) {
	u, err := s.repo.GetByID(ctx, userID)
	if err != nil {
		slog.Error("failed to load the user for a recovery code email", "error", err)
		return
	}
	left, err := s.repo.RecoveryCodesLeft(ctx, userID)
	if err != nil {
		slog.Error("failed to count recovery codes", "error", err)
		return
	}
	t := func(text string, args ...any) string { return i18n.T(u.Locale, text, args...) }
	err = s.mail.Send(ctx, mail.Email{
		To:      u.Email,
		Locale:  u.Locale,
		Subject: t("A recovery code was used on your Helpic account"),
		Heading: t("A recovery code was used"),
		Paragraphs: []string{
			t("A recovery code was just used instead of a code from the authenticator app on your Helpic account, %s. It no longer works.", u.Email),
			t("Recovery codes left: %d.", left),
			t("If that was you, make new codes under Settings → Security once you have the app again. If it wasn't, someone has your password and your codes: change the password and make new codes right away."),
		},
		Button: &mail.Button{Text: t("Open security settings"), URL: s.appURL + "/settings?tab=security"},
	})
	if err != nil {
		slog.Error("failed to queue recovery code email", "error", err)
	}
}

// DeviceName says what a user agent is, as the sessions list in the app
// does: "Chrome on macOS", "Chrome на macOS"; "Unknown device" when it says
// nothing useful.
func DeviceName(locale, userAgent string) string {
	browser := firstMatch(userAgent, [][2]string{
		{"Edg/", "Edge"}, {"OPR/", "Opera"}, {"Firefox/", "Firefox"}, {"Chrome/", "Chrome"}, {"Safari/", "Safari"},
	})
	os := firstMatch(userAgent, [][2]string{
		{"iPhone", "iOS"}, {"iPad", "iPadOS"}, {"Android", "Android"}, {"CrOS", "ChromeOS"},
		{"Mac OS X", "macOS"}, {"Windows", "Windows"}, {"Linux", "Linux"},
	})
	switch {
	case browser != "" && os != "":
		return i18n.T(locale, "%s on %s", browser, os)
	case browser != "":
		return browser
	case os != "":
		return os
	}
	return i18n.T(locale, "Unknown device")
}

// firstMatch is the label of the first token the user agent contains.
func firstMatch(userAgent string, table [][2]string) string {
	for _, row := range table {
		if strings.Contains(userAgent, row[0]) {
			return row[1]
		}
	}
	return ""
}

// --- repository ----------------------------------------------------------------

// SeeDevice records a sign-in from the browser with the token hash to the
// user's account, and says whether the account knew it.
func (r *Repository) SeeDevice(ctx context.Context, userID string, hash []byte) (deviceSeen, error) {
	tag, err := r.db.Exec(ctx, `
		UPDATE known_devices SET last_seen_at = now()
		WHERE user_id = $1 AND token_hash = $2`, userID, hash)
	if err != nil {
		return 0, fmt.Errorf("see device: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return deviceKnown, nil
	}
	var first bool
	err = r.db.QueryRow(ctx, `
		WITH prior AS (SELECT NOT EXISTS (SELECT 1 FROM known_devices WHERE user_id = $1) AS first)
		INSERT INTO known_devices (user_id, token_hash) VALUES ($1, $2)
		ON CONFLICT DO NOTHING
		RETURNING (SELECT first FROM prior)`, userID, hash).Scan(&first)
	if errors.Is(err, pgx.ErrNoRows) {
		// Another sign-in from the same browser added it a moment ago.
		return deviceKnown, nil
	}
	if err != nil {
		return 0, fmt.Errorf("add device: %w", err)
	}
	if first {
		return deviceFirst, nil
	}
	return deviceNew, nil
}

// DeleteStaleDevices forgets browsers not signed in from for deviceTTL.
func (r *Repository) DeleteStaleDevices(ctx context.Context) error {
	if _, err := r.db.Exec(ctx, `DELETE FROM known_devices WHERE last_seen_at < $1`, time.Now().Add(-deviceTTL)); err != nil {
		return fmt.Errorf("delete stale devices: %w", err)
	}
	return nil
}
