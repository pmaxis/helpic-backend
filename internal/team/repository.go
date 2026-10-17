package team

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository handles PostgreSQL access for companies, members and invitations.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository creates a team Repository backed by the given pool.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// Membership is the company someone joined and their role in it.
type Membership struct {
	Owner   Person
	Company string
	Role    string
}

// MembershipOf returns the company the user is a member of, or ok=false when
// they aren't anyone's member.
func (r *Repository) MembershipOf(ctx context.Context, userID string) (m Membership, ok bool, err error) {
	err = r.db.QueryRow(ctx, `SELECT u.id, u.name, u.email, c.name, m.role FROM company_members m
		JOIN companies c ON c.id = m.company_id JOIN users u ON u.id = c.owner_id
		WHERE m.user_id = $1`, userID).Scan(&m.Owner.ID, &m.Owner.Name, &m.Owner.Email, &m.Company, &m.Role)
	if errors.Is(err, pgx.ErrNoRows) {
		return Membership{}, false, nil
	}
	if err != nil {
		return Membership{}, false, fmt.Errorf("get membership: %w", err)
	}
	return m, true, nil
}

// Person loads an account's public side.
func (r *Repository) Person(ctx context.Context, userID string) (Person, error) {
	var p Person
	err := r.db.QueryRow(ctx, `SELECT id, name, email FROM users WHERE id = $1`, userID).Scan(&p.ID, &p.Name, &p.Email)
	if err != nil {
		return Person{}, fmt.Errorf("get person: %w", err)
	}
	return p, nil
}

// Company returns the owner's company ID and name; ok=false when they have
// none yet.
func (r *Repository) Company(ctx context.Context, ownerID string) (id, name string, ok bool, err error) {
	err = r.db.QueryRow(ctx, `SELECT id, name FROM companies WHERE owner_id = $1`, ownerID).Scan(&id, &name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, fmt.Errorf("get company: %w", err)
	}
	return id, name, true, nil
}

// EnsureCompany returns the owner's company, making it the first time.
func (r *Repository) EnsureCompany(ctx context.Context, ownerID string) (string, error) {
	var id string
	err := r.db.QueryRow(ctx, `INSERT INTO companies (owner_id) VALUES ($1)
		ON CONFLICT (owner_id) DO UPDATE SET owner_id = EXCLUDED.owner_id RETURNING id`, ownerID).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("ensure company: %w", err)
	}
	return id, nil
}

// Rename names the owner's company.
func (r *Repository) Rename(ctx context.Context, ownerID, name string) error {
	if _, err := r.db.Exec(ctx, `INSERT INTO companies (owner_id, name) VALUES ($1, $2)
		ON CONFLICT (owner_id) DO UPDATE SET name = EXCLUDED.name`, ownerID, name); err != nil {
		return fmt.Errorf("rename company: %w", err)
	}
	return nil
}

// Members lists a company's members, earliest first.
func (r *Repository) Members(ctx context.Context, companyID string) ([]Member, error) {
	rows, err := r.db.Query(ctx, `SELECT u.id, u.name, u.email, m.role, m.created_at FROM company_members m
		JOIN users u ON u.id = m.user_id WHERE m.company_id = $1 ORDER BY m.created_at`, companyID)
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Member, error) {
		var m Member
		return m, row.Scan(&m.ID, &m.Name, &m.Email, &m.Role, &m.JoinedAt)
	})
	if err != nil {
		return nil, fmt.Errorf("scan members: %w", err)
	}
	return out, nil
}

const inviteColumns = `id, company_id, email, name, role, token, expires_at, created_at`

func scanInvite(row pgx.Row) (Invite, error) {
	var i Invite
	err := row.Scan(&i.ID, &i.CompanyID, &i.Email, &i.Name, &i.Role, &i.Token, &i.ExpiresAt, &i.CreatedAt)
	return i, err
}

// Invites lists a company's pending invitations, newest first; expired ones
// stay, so they can be sent again.
func (r *Repository) Invites(ctx context.Context, companyID string) ([]Invite, error) {
	rows, err := r.db.Query(ctx, `SELECT `+inviteColumns+` FROM company_invites
		WHERE company_id = $1 AND accepted_at IS NULL ORDER BY created_at DESC`, companyID)
	if err != nil {
		return nil, fmt.Errorf("list invites: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Invite, error) { return scanInvite(row) })
	if err != nil {
		return nil, fmt.Errorf("scan invites: %w", err)
	}
	return out, nil
}

// EmailTaken reports whether an account uses the email.
func (r *Repository) EmailTaken(ctx context.Context, email string) (bool, error) {
	var ok bool
	if err := r.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE email = $1)`, email).Scan(&ok); err != nil {
		return false, fmt.Errorf("check email: %w", err)
	}
	return ok, nil
}

// CreateInvite stores a pending invitation; an expired one for the same email
// is replaced, a live one yields ErrAlreadyInvited.
func (r *Repository) CreateInvite(ctx context.Context, companyID, email, name, role, token string, expiresAt time.Time) (Invite, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return Invite{}, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err := tx.Exec(ctx, `DELETE FROM company_invites
		WHERE company_id = $1 AND email = $2 AND accepted_at IS NULL AND expires_at <= now()`, companyID, email); err != nil {
		return Invite{}, fmt.Errorf("drop expired invite: %w", err)
	}
	inv, err := scanInvite(tx.QueryRow(ctx, `INSERT INTO company_invites (company_id, email, name, role, token, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING `+inviteColumns, companyID, email, name, role, token, expiresAt))
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return Invite{}, ErrAlreadyInvited
	}
	if err != nil {
		return Invite{}, fmt.Errorf("create invite: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Invite{}, fmt.Errorf("commit: %w", err)
	}
	return inv, nil
}

// DeleteInvite revokes one of the company's pending invitations.
func (r *Repository) DeleteInvite(ctx context.Context, companyID, id string) error {
	tag, err := r.db.Exec(ctx, `DELETE FROM company_invites
		WHERE id::text = $2 AND company_id = $1 AND accepted_at IS NULL`, companyID, id)
	if err != nil {
		return fmt.Errorf("delete invite: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RemoveMember takes a member out of the company. Their account stays; it
// just no longer sees the company's work.
func (r *Repository) RemoveMember(ctx context.Context, companyID, userID string) error {
	tag, err := r.db.Exec(ctx, `DELETE FROM company_members WHERE company_id = $1 AND user_id::text = $2`, companyID, userID)
	if err != nil {
		return fmt.Errorf("remove member: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetRole makes a member of the company a member or a contractor.
func (r *Repository) SetRole(ctx context.Context, companyID, userID, role string) error {
	tag, err := r.db.Exec(ctx, `UPDATE company_members SET role = $3 WHERE company_id = $1 AND user_id::text = $2`, companyID, userID, role)
	if err != nil {
		return fmt.Errorf("set member role: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// InviteByToken loads a pending invitation with its company's name and owner;
// unknown, used and expired ones yield ErrInviteInvalid.
func (r *Repository) InviteByToken(ctx context.Context, token string) (Invite, string, Person, error) {
	var (
		i       Invite
		company string
		owner   Person
		used    *time.Time
	)
	err := r.db.QueryRow(ctx, `SELECT i.id, i.company_id, i.email, i.name, i.role, i.token, i.expires_at, i.created_at, i.accepted_at,
			c.name, u.id, u.name, u.email
		FROM company_invites i JOIN companies c ON c.id = i.company_id JOIN users u ON u.id = c.owner_id
		WHERE i.token = $1`, token).
		Scan(&i.ID, &i.CompanyID, &i.Email, &i.Name, &i.Role, &i.Token, &i.ExpiresAt, &i.CreatedAt, &used, &company, &owner.ID, &owner.Name, &owner.Email)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && (used != nil || i.Expired())) {
		return Invite{}, "", Person{}, ErrInviteInvalid
	}
	if err != nil {
		return Invite{}, "", Person{}, fmt.Errorf("get invite: %w", err)
	}
	return i, company, owner, nil
}

// Join makes the user a member, in the invitation's role, and uses it up.
func (r *Repository) Join(ctx context.Context, inviteID, companyID, userID, role string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	tag, err := tx.Exec(ctx, `UPDATE company_invites SET accepted_at = now() WHERE id = $1 AND accepted_at IS NULL`, inviteID)
	if err != nil {
		return fmt.Errorf("use invite: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrInviteInvalid
	}
	if _, err := tx.Exec(ctx, `INSERT INTO company_members (user_id, company_id, role) VALUES ($1, $2, $3)`, userID, companyID, role); err != nil {
		return fmt.Errorf("add member: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}
