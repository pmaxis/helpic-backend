package auth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/skip2/go-qrcode"
	"golang.org/x/crypto/bcrypt"

	"github.com/pmaxis/helpic/internal/i18n"
	"github.com/pmaxis/helpic/internal/mail"
	"github.com/pmaxis/helpic/internal/ratelimit"
	"github.com/pmaxis/helpic/internal/totp"
	"github.com/pmaxis/helpic/internal/user"
)

// Two-factor sign-in: after the password, a code from an authenticator app,
// or one of the recovery codes made when it was turned on. Setting it up
// takes the password, then a code from the app, so a stolen session alone
// can't turn it on (and lock the owner out) or off.

const (
	// twoFactorIssuer is the name the authenticator app shows the account under.
	twoFactorIssuer = "Helpic"
	// recoveryCodeCount is how many recovery codes a user gets at a time.
	recoveryCodeCount = 10
	// recoveryCodeBytes makes a code of ten base32 letters: 50 bits.
	recoveryCodeBytes = 5 * 10 / 8
)

var (
	// ErrTwoFactorRequired is returned by Login when the password is right
	// and the account also needs a code.
	ErrTwoFactorRequired = errors.New("two-factor code required")
	// ErrTwoFactorCode is returned for a code that is wrong or already used.
	ErrTwoFactorCode = errors.New("two-factor code is wrong")
	// ErrTwoFactorOn is returned when setting up two-factor that is on already.
	ErrTwoFactorOn = errors.New("two-factor sign-in is already on")
	// ErrTwoFactorOff is returned when changing two-factor that is off.
	ErrTwoFactorOff = errors.New("two-factor sign-in is off")
	// ErrTwoFactorNotStarted is returned when confirming a setup that wasn't
	// started, or was started again elsewhere meanwhile.
	ErrTwoFactorNotStarted = errors.New("two-factor setup not started")
	// ErrWrongPassword is returned when the current password doesn't match.
	ErrWrongPassword = errors.New("current password is wrong")
)

// CodeLimitError is returned when the account had too many wrong codes;
// Retry is when the next one may be tried.
type CodeLimitError struct {
	Retry time.Duration
}

func (e *CodeLimitError) Error() string {
	return fmt.Sprintf("too many wrong two-factor codes, retry in %s", e.Retry)
}

// TwoFactorStatus is where an account's two-factor sign-in stands.
type TwoFactorStatus struct {
	Enabled           bool
	RecoveryCodesLeft int
}

// TwoFactorSetup is what the user adds to their authenticator app: the
// otpauth:// link as a QR code, and the secret to type in by hand.
type TwoFactorSetup struct {
	Secret string
	URI    string
	// QR is an SVG image of URI, as a data: URL.
	QR string
}

// --- sealing secrets -------------------------------------------------------------

// Sealer encrypts the authenticator secrets kept in the database, so a copy
// of the database alone doesn't give away anyone's codes.
type Sealer struct {
	aead cipher.AEAD
}

// NewSealer makes a Sealer from the server's key. The AES key is derived
// from it, so a key shared with something else (JWT_SECRET in development)
// still seals with a key of its own.
func NewSealer(key string) (*Sealer, error) {
	if key == "" {
		return nil, errors.New("two-factor key is empty")
	}
	k, err := hkdf.Key(sha256.New, []byte(key), nil, "helpic two-factor secrets", 32)
	if err != nil {
		return nil, fmt.Errorf("derive two-factor key: %w", err)
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, fmt.Errorf("two-factor cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("two-factor cipher: %w", err)
	}
	return &Sealer{aead: aead}, nil
}

// seal encrypts a secret: a random nonce, then the ciphertext.
func (s *Sealer) seal(secret string) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	return s.aead.Seal(nonce, nonce, []byte(secret), nil), nil
}

// open decrypts what seal made.
func (s *Sealer) open(sealed []byte) (string, error) {
	n := s.aead.NonceSize()
	if len(sealed) < n {
		return "", errors.New("open two-factor secret: too short")
	}
	plain, err := s.aead.Open(nil, sealed[:n], sealed[n:], nil)
	if err != nil {
		return "", fmt.Errorf("open two-factor secret: %w", err)
	}
	return string(plain), nil
}

// --- service -------------------------------------------------------------------

// UseTwoFactor lets accounts turn on two-factor sign-in, with secrets sealed
// by sealer. guesses counts wrong codes per account; nil doesn't limit them.
func (s *Service) UseTwoFactor(sealer *Sealer, guesses ratelimit.Gate) {
	s.sealer, s.codeGuesses = sealer, guesses
}

// TwoFactor returns where the user's two-factor sign-in stands.
func (s *Service) TwoFactor(ctx context.Context, userID string) (TwoFactorStatus, error) {
	u, err := s.repo.GetByID(ctx, userID)
	if err != nil {
		return TwoFactorStatus{}, err
	}
	if !u.TwoFactor {
		return TwoFactorStatus{}, nil
	}
	left, err := s.repo.RecoveryCodesLeft(ctx, userID)
	if err != nil {
		return TwoFactorStatus{}, err
	}
	return TwoFactorStatus{Enabled: true, RecoveryCodesLeft: left}, nil
}

// BeginTwoFactor checks the password and makes a new secret for the user to
// add to their app. It isn't used for signing in until ConfirmTwoFactor.
func (s *Service) BeginTwoFactor(ctx context.Context, userID, password string) (TwoFactorSetup, error) {
	u, err := s.checkPassword(ctx, userID, password)
	if err != nil {
		return TwoFactorSetup{}, err
	}
	if u.TwoFactor {
		return TwoFactorSetup{}, ErrTwoFactorOn
	}
	secret, err := totp.NewSecret()
	if err != nil {
		return TwoFactorSetup{}, err
	}
	sealed, err := s.sealer.seal(secret)
	if err != nil {
		return TwoFactorSetup{}, err
	}
	if err := s.repo.SetPendingSecret(ctx, userID, sealed); err != nil {
		return TwoFactorSetup{}, err
	}
	uri := totp.URI(twoFactorIssuer, u.Email, secret)
	qr, err := qrSVG(uri)
	if err != nil {
		return TwoFactorSetup{}, err
	}
	return TwoFactorSetup{Secret: secret, URI: uri, QR: qr}, nil
}

// ConfirmTwoFactor turns two-factor on with a code from the app the new
// secret went into. It signs out every device but this one (sessionID), and
// returns the recovery codes, shown this once.
func (s *Service) ConfirmTwoFactor(ctx context.Context, userID, sessionID, code string) (user.User, []string, error) {
	if err := s.checkGuesses(ctx, userID); err != nil {
		return user.User{}, nil, err
	}
	sealed, err := s.repo.PendingSecret(ctx, userID)
	if err != nil {
		return user.User{}, nil, err
	}
	secret, err := s.sealer.open(sealed)
	if err != nil {
		return user.User{}, nil, err
	}
	step, ok, err := totp.Verify(secret, normalizeCode(code), time.Now())
	if err != nil {
		return user.User{}, nil, err
	}
	if !ok {
		return user.User{}, nil, s.wrongCode(ctx, userID)
	}
	codes, hashes, err := newRecoveryCodes()
	if err != nil {
		return user.User{}, nil, err
	}
	if err := s.repo.ConfirmTwoFactor(ctx, userID, sealed, step, hashes); err != nil {
		return user.User{}, nil, err
	}
	if err := s.repo.DeleteOtherSessions(ctx, userID, sessionID); err != nil {
		return user.User{}, nil, err
	}
	u, err := s.repo.GetByID(ctx, userID)
	if err != nil {
		return user.User{}, nil, err
	}
	s.notice(ctx, u, "Two-factor sign-in is on", "Two-factor sign-in was turned on for your Helpic account, %s. Signing in now also takes a code from your authenticator app, and your other devices were signed out.")
	return u, codes, nil
}

// DisableTwoFactor turns two-factor off; it takes the password and a code.
func (s *Service) DisableTwoFactor(ctx context.Context, userID, password, code string) (user.User, error) {
	if _, err := s.checkBoth(ctx, userID, password, code); err != nil {
		return user.User{}, err
	}
	if err := s.repo.DisableTwoFactor(ctx, userID); err != nil {
		return user.User{}, err
	}
	u, err := s.repo.GetByID(ctx, userID)
	if err != nil {
		return user.User{}, err
	}
	s.notice(ctx, u, "Two-factor sign-in is off", "Two-factor sign-in was turned off for your Helpic account, %s. Signing in takes only the password now.")
	return u, nil
}

// RenewRecoveryCodes replaces the user's recovery codes with new ones; it
// takes the password and a code. The old ones stop working.
func (s *Service) RenewRecoveryCodes(ctx context.Context, userID, password, code string) ([]string, error) {
	u, err := s.checkBoth(ctx, userID, password, code)
	if err != nil {
		return nil, err
	}
	codes, hashes, err := newRecoveryCodes()
	if err != nil {
		return nil, err
	}
	if err := s.repo.ReplaceRecoveryCodes(ctx, userID, hashes); err != nil {
		return nil, err
	}
	s.notice(ctx, u, "New recovery codes", "New recovery codes were made for your Helpic account, %s. The old ones no longer work.")
	return codes, nil
}

// checkBoth checks the password and then a code of a user with two-factor on.
func (s *Service) checkBoth(ctx context.Context, userID, password, code string) (user.User, error) {
	u, err := s.checkPassword(ctx, userID, password)
	if err != nil {
		return user.User{}, err
	}
	if !u.TwoFactor {
		return user.User{}, ErrTwoFactorOff
	}
	if err := s.secondFactor(ctx, userID, code); err != nil {
		return user.User{}, err
	}
	return u, nil
}

// checkPassword returns the user when password is theirs; ErrWrongPassword
// when it isn't.
func (s *Service) checkPassword(ctx context.Context, userID, password string) (user.User, error) {
	u, err := s.repo.GetByID(ctx, userID)
	if err != nil {
		return user.User{}, err
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		return user.User{}, ErrWrongPassword
	}
	return u, nil
}

// secondFactor checks a code of a user with two-factor on: one from their
// app, not used before, or an unused recovery code, which it uses up.
// ErrTwoFactorRequired when there is none.
func (s *Service) secondFactor(ctx context.Context, userID, code string) error {
	code = normalizeCode(code)
	if code == "" {
		return ErrTwoFactorRequired
	}
	if err := s.checkGuesses(ctx, userID); err != nil {
		return err
	}
	if isAppCode(code) {
		sealed, step, err := s.repo.Secret(ctx, userID)
		if err != nil {
			return err
		}
		secret, err := s.sealer.open(sealed)
		if err != nil {
			return err
		}
		step, ok, err := totp.Verify(secret, code, time.Now())
		if err != nil {
			return err
		}
		// A code seen once, here or by whoever looked over a shoulder, is spent.
		if ok {
			ok, err = s.repo.UseStep(ctx, userID, step)
			if err != nil {
				return err
			}
		}
		if !ok {
			return s.wrongCode(ctx, userID)
		}
		return nil
	}
	ok, err := s.repo.UseRecoveryCode(ctx, userID, HashToken(code))
	if err != nil {
		return err
	}
	if !ok {
		return s.wrongCode(ctx, userID)
	}
	return nil
}

// checkGuesses refuses with a CodeLimitError once the account has had too
// many wrong codes.
func (s *Service) checkGuesses(ctx context.Context, userID string) error {
	if s.codeGuesses == nil {
		return nil
	}
	ok, retry, err := s.codeGuesses.Check(ctx, userID)
	if err != nil {
		return err
	}
	if !ok {
		return &CodeLimitError{Retry: retry}
	}
	return nil
}

// wrongCode counts a wrong code against the account and returns ErrTwoFactorCode.
func (s *Service) wrongCode(ctx context.Context, userID string) error {
	if s.codeGuesses != nil {
		if err := s.codeGuesses.Take(ctx, userID); err != nil {
			return err
		}
	}
	return ErrTwoFactorCode
}

// notice tells the user by email that their two-factor sign-in changed, in
// case it wasn't them. text names their email with %s.
func (s *Service) notice(ctx context.Context, u user.User, heading, text string) {
	err := s.mail.Send(ctx, mail.Email{
		To:      u.Email,
		Locale:  u.Locale,
		Subject: i18n.T(u.Locale, "Helpic: %s", i18n.T(u.Locale, heading)),
		Heading: i18n.T(u.Locale, heading),
		Paragraphs: []string{
			i18n.T(u.Locale, text, u.Email),
			i18n.T(u.Locale, "If that was you, there's nothing to do. If it wasn't, change your password right away, and check which devices are signed in under Settings → Security."),
		},
	})
	if err != nil {
		slog.Error("failed to queue two-factor email", "error", err)
	}
}

// normalizeCode drops the spaces and dashes people type or paste with a
// code, and lowercases a recovery code.
func normalizeCode(code string) string {
	return strings.ToLower(strings.NewReplacer(" ", "", "-", "", "\t", "").Replace(strings.TrimSpace(code)))
}

// isAppCode tells an authenticator app's code (six digits) from a recovery code.
func isAppCode(code string) bool {
	if len(code) != totp.Digits {
		return false
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

var recoveryEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// newRecoveryCodes makes a set of recovery codes, as shown ("abcde-fghij"),
// and the hashes they're kept by.
func newRecoveryCodes() ([]string, [][]byte, error) {
	codes := make([]string, recoveryCodeCount)
	hashes := make([][]byte, recoveryCodeCount)
	for i := range codes {
		b := make([]byte, recoveryCodeBytes)
		if _, err := rand.Read(b); err != nil {
			return nil, nil, fmt.Errorf("generate recovery code: %w", err)
		}
		raw := strings.ToLower(recoveryEncoding.EncodeToString(b))
		codes[i] = raw[:5] + "-" + raw[5:]
		hashes[i] = HashToken(raw)
	}
	return codes, hashes, nil
}

// qrSVG draws content as a QR code: an SVG with a quiet zone, black on
// white whatever the page's theme, as a data: URL.
func qrSVG(content string) (string, error) {
	q, err := qrcode.New(content, qrcode.Medium)
	if err != nil {
		return "", fmt.Errorf("draw QR code: %w", err)
	}
	bits := q.Bitmap() // includes the quiet zone
	var path strings.Builder
	for y, row := range bits {
		for x, dark := range row {
			if dark {
				fmt.Fprintf(&path, "M%d %dh1v1h-1z", x, y)
			}
		}
	}
	n := len(bits)
	svg := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges"><rect width="%d" height="%d" fill="#fff"/><path d="%s" fill="#000"/></svg>`,
		n, n, n, n, path.String())
	return "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(svg)), nil
}

// --- repository ----------------------------------------------------------------

// GetByID loads an account. Returns user.ErrNotFound when there is none.
func (r *Repository) GetByID(ctx context.Context, id string) (user.User, error) {
	u, err := user.Scan(r.db.QueryRow(ctx, `SELECT `+user.Columns+` FROM users WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return user.User{}, user.ErrNotFound
	}
	if err != nil {
		return user.User{}, fmt.Errorf("get user: %w", err)
	}
	return u, nil
}

// SetPendingSecret keeps a secret awaiting its first code, replacing one
// set up before.
func (r *Repository) SetPendingSecret(ctx context.Context, userID string, sealed []byte) error {
	if _, err := r.db.Exec(ctx, `UPDATE users SET two_factor_pending = $2 WHERE id = $1`, userID, sealed); err != nil {
		return fmt.Errorf("set pending two-factor secret: %w", err)
	}
	return nil
}

// PendingSecret returns the secret awaiting its first code.
// ErrTwoFactorNotStarted when there is none, or two-factor is on already.
func (r *Repository) PendingSecret(ctx context.Context, userID string) ([]byte, error) {
	var sealed []byte
	err := r.db.QueryRow(ctx, `
		SELECT two_factor_pending FROM users
		WHERE id = $1 AND two_factor_pending IS NOT NULL AND two_factor_secret IS NULL`, userID).Scan(&sealed)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrTwoFactorNotStarted
	}
	if err != nil {
		return nil, fmt.Errorf("get pending two-factor secret: %w", err)
	}
	return sealed, nil
}

// ConfirmTwoFactor makes the pending secret, if it is still pending, the one
// signing in takes codes from, with step as its last used, and gives the user
// these recovery codes. ErrTwoFactorNotStarted when the secret was replaced
// or turned on meanwhile.
func (r *Repository) ConfirmTwoFactor(ctx context.Context, userID string, pending []byte, step int64, hashes [][]byte) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	tag, err := tx.Exec(ctx, `
		UPDATE users SET two_factor_secret = two_factor_pending, two_factor_pending = NULL, two_factor_step = $3
		WHERE id = $1 AND two_factor_pending = $2 AND two_factor_secret IS NULL`, userID, pending, step)
	if err != nil {
		return fmt.Errorf("turn two-factor on: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrTwoFactorNotStarted
	}
	if err := replaceRecoveryCodes(ctx, tx, userID, hashes); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Secret returns the secret signing in takes codes from, and the last step
// a code was used for. ErrTwoFactorOff when two-factor is off.
func (r *Repository) Secret(ctx context.Context, userID string) ([]byte, int64, error) {
	var (
		sealed []byte
		step   int64
	)
	err := r.db.QueryRow(ctx, `
		SELECT two_factor_secret, two_factor_step FROM users
		WHERE id = $1 AND two_factor_secret IS NOT NULL`, userID).Scan(&sealed, &step)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, ErrTwoFactorOff
	}
	if err != nil {
		return nil, 0, fmt.Errorf("get two-factor secret: %w", err)
	}
	return sealed, step, nil
}

// UseStep records that a code for step was used, and reports false when one
// for it, or a later one, was used already.
func (r *Repository) UseStep(ctx context.Context, userID string, step int64) (bool, error) {
	tag, err := r.db.Exec(ctx, `UPDATE users SET two_factor_step = $2 WHERE id = $1 AND two_factor_step < $2`, userID, step)
	if err != nil {
		return false, fmt.Errorf("use two-factor step: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// UseRecoveryCode uses up the user's unused recovery code with this hash, and
// reports false when there is none.
func (r *Repository) UseRecoveryCode(ctx context.Context, userID string, hash []byte) (bool, error) {
	tag, err := r.db.Exec(ctx, `
		UPDATE two_factor_recovery_codes SET used_at = now()
		WHERE user_id = $1 AND code_hash = $2 AND used_at IS NULL`, userID, hash)
	if err != nil {
		return false, fmt.Errorf("use recovery code: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// RecoveryCodesLeft counts the user's unused recovery codes.
func (r *Repository) RecoveryCodesLeft(ctx context.Context, userID string) (int, error) {
	var n int
	err := r.db.QueryRow(ctx, `SELECT count(*) FROM two_factor_recovery_codes WHERE user_id = $1 AND used_at IS NULL`, userID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count recovery codes: %w", err)
	}
	return n, nil
}

// ReplaceRecoveryCodes gives the user these recovery codes instead of theirs.
func (r *Repository) ReplaceRecoveryCodes(ctx context.Context, userID string, hashes [][]byte) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := replaceRecoveryCodes(ctx, tx, userID, hashes); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func replaceRecoveryCodes(ctx context.Context, tx pgx.Tx, userID string, hashes [][]byte) error {
	if _, err := tx.Exec(ctx, `DELETE FROM two_factor_recovery_codes WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("clear recovery codes: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO two_factor_recovery_codes (user_id, code_hash)
		SELECT $1, h FROM unnest($2::bytea[]) AS h`, userID, hashes); err != nil {
		return fmt.Errorf("create recovery codes: %w", err)
	}
	return nil
}

// DisableTwoFactor forgets the user's secrets and recovery codes.
func (r *Repository) DisableTwoFactor(ctx context.Context, userID string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `
		UPDATE users SET two_factor_secret = NULL, two_factor_pending = NULL, two_factor_step = 0
		WHERE id = $1`, userID); err != nil {
		return fmt.Errorf("turn two-factor off: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM two_factor_recovery_codes WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("clear recovery codes: %w", err)
	}
	return tx.Commit(ctx)
}
