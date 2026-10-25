// Package policies keeps the site's policies: privacy, terms of use and
// cookies, in English and Ukrainian. Helpic comes with a text of each
// (defaults/); an admin may write their own, and deleting it brings the one
// that came back.
//
// A text is plain lines: "## " starts a heading, "- " a list item, and an
// empty line ends a paragraph. The frontend lays it out from that, with no
// HTML, so nothing an admin writes can run in a reader's browser.
package policies

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/pmaxis/helpic/internal/i18n"
)

// The policies.
const (
	Privacy = "privacy"
	Terms   = "terms"
	Cookies = "cookies"
)

// Kinds are all the policies, in the order the panel lists them.
var Kinds = []string{Privacy, Terms, Cookies}

// MaxBody is how long a policy may be, in bytes.
const MaxBody = 100_000

// ErrUnknown is a policy or language Helpic doesn't have.
var ErrUnknown = errors.New("no such policy")

//go:embed defaults/*.txt
var defaults embed.FS

// Policy is one policy in one language.
type Policy struct {
	Kind   string
	Locale string
	Body   string
	// Custom is whether an admin wrote it; otherwise it is the default and
	// the rest is empty.
	Custom    bool
	UpdatedAt *time.Time
	// UpdatedBy is the admin's email; empty once their account is gone.
	UpdatedBy string
}

// Service reads and writes the policies.
type Service struct {
	db *pgxpool.Pool
}

// NewService creates a Service.
func NewService(db *pgxpool.Pool) *Service {
	return &Service{db: db}
}

// Valid reports whether kind and locale name a policy Helpic has.
func Valid(kind, locale string) bool {
	return slices.Contains(Kinds, kind) && i18n.Valid(locale)
}

// Default is the text Helpic comes with.
func Default(kind, locale string) (string, error) {
	if !Valid(kind, locale) {
		return "", ErrUnknown
	}
	raw, err := defaults.ReadFile("defaults/" + kind + "." + locale + ".txt")
	if err != nil {
		return "", fmt.Errorf("read default %s.%s: %w", kind, locale, err)
	}
	return string(raw), nil
}

// Get returns the policy as readers see it: the admin's text, or else the
// default. ErrUnknown for no such policy.
func (s *Service) Get(ctx context.Context, kind, locale string) (Policy, error) {
	if !Valid(kind, locale) {
		return Policy{}, ErrUnknown
	}
	p := Policy{Kind: kind, Locale: locale}
	var at time.Time
	var by *string
	err := s.db.QueryRow(ctx, `SELECT p.body, p.updated_at, u.email
		FROM policies p LEFT JOIN users u ON u.id = p.updated_by
		WHERE p.kind = $1 AND p.locale = $2`, kind, locale).Scan(&p.Body, &at, &by)
	if errors.Is(err, pgx.ErrNoRows) {
		p.Body, err = Default(kind, locale)
		return p, err
	}
	if err != nil {
		return Policy{}, fmt.Errorf("get policy: %w", err)
	}
	p.Custom, p.UpdatedAt = true, &at
	if by != nil {
		p.UpdatedBy = *by
	}
	return p, nil
}

// All returns every policy in every language, as Get does.
func (s *Service) All(ctx context.Context) ([]Policy, error) {
	var out []Policy
	for _, kind := range Kinds {
		for _, locale := range i18n.Locales {
			p, err := s.Get(ctx, kind, locale)
			if err != nil {
				return nil, err
			}
			out = append(out, p)
		}
	}
	return out, nil
}

// Save makes body the policy, written by the admin adminID, and returns it.
func (s *Service) Save(ctx context.Context, adminID, kind, locale, body string) (Policy, error) {
	if !Valid(kind, locale) {
		return Policy{}, ErrUnknown
	}
	if _, err := s.db.Exec(ctx, `INSERT INTO policies (kind, locale, body, updated_by) VALUES ($1, $2, $3, $4)
		ON CONFLICT (kind, locale) DO UPDATE SET body = EXCLUDED.body, updated_at = now(), updated_by = EXCLUDED.updated_by`,
		kind, locale, body, adminID); err != nil {
		return Policy{}, fmt.Errorf("save policy: %w", err)
	}
	return s.Get(ctx, kind, locale)
}

// Reset puts the default text back and returns it.
func (s *Service) Reset(ctx context.Context, kind, locale string) (Policy, error) {
	if !Valid(kind, locale) {
		return Policy{}, ErrUnknown
	}
	if _, err := s.db.Exec(ctx, `DELETE FROM policies WHERE kind = $1 AND locale = $2`, kind, locale); err != nil {
		return Policy{}, fmt.Errorf("reset policy: %w", err)
	}
	return s.Get(ctx, kind, locale)
}
