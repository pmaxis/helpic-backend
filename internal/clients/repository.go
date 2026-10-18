package clients

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/pmaxis/helpic/internal/user"
)

const uniqueViolationCode = "23505"

// Repository handles PostgreSQL access for invitations and freelancer–client
// links. Freelancer-side queries are scoped by the freelancer's ID.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository creates a clients Repository backed by the given pool.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

const invitationColumns = `id, freelancer_id, email, name, expires_at, accepted_at, created_at`

func scanInvitation(row pgx.Row) (Invitation, error) {
	var i Invitation
	err := row.Scan(&i.ID, &i.FreelancerID, &i.Email, &i.Name, &i.ExpiresAt, &i.AcceptedAt, &i.CreatedAt)
	return i, err
}

// CreateInvitation stores a new pending invitation by its link's hash. An
// expired pending invitation for the same email is replaced; a live one
// yields ErrAlreadyInvited.
func (r *Repository) CreateInvitation(ctx context.Context, freelancerID, email, name string, tokenHash []byte, expiresAt time.Time) (Invitation, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return Invitation{}, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err := tx.Exec(ctx, `DELETE FROM invitations
		WHERE freelancer_id = $1 AND email = $2 AND accepted_at IS NULL AND expires_at <= now()`,
		freelancerID, email); err != nil {
		return Invitation{}, fmt.Errorf("drop expired invitation: %w", err)
	}

	inv, err := scanInvitation(tx.QueryRow(ctx, `INSERT INTO invitations (freelancer_id, email, name, token_hash, expires_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+invitationColumns, freelancerID, email, name, tokenHash, expiresAt))
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolationCode {
		return Invitation{}, ErrAlreadyInvited
	}
	if err != nil {
		return Invitation{}, fmt.Errorf("create invitation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Invitation{}, fmt.Errorf("commit: %w", err)
	}
	return inv, nil
}

// ListInvitations returns the freelancer's invitations that were not accepted
// yet, newest first. Expired ones are included so they can be re-sent.
func (r *Repository) ListInvitations(ctx context.Context, freelancerID string) ([]Invitation, error) {
	rows, err := r.db.Query(ctx, `SELECT `+invitationColumns+` FROM invitations
		WHERE freelancer_id = $1 AND accepted_at IS NULL
		ORDER BY created_at DESC`, freelancerID)
	if err != nil {
		return nil, fmt.Errorf("list invitations: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Invitation, error) { return scanInvitation(row) })
	if err != nil {
		return nil, fmt.Errorf("scan invitations: %w", err)
	}
	return out, nil
}

// DeleteInvitation revokes one of the freelancer's pending invitations.
func (r *Repository) DeleteInvitation(ctx context.Context, freelancerID, id string) error {
	tag, err := r.db.Exec(ctx, `DELETE FROM invitations
		WHERE id = $1 AND freelancer_id = $2 AND accepted_at IS NULL`, id, freelancerID)
	if err != nil {
		return fmt.Errorf("delete invitation: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// InvitationByToken loads the invitation whose link has this hash, together
// with the freelancer who sent it. Returns ErrNotFound for an unknown token.
func (r *Repository) InvitationByToken(ctx context.Context, tokenHash []byte) (Invitation, Party, error) {
	var (
		i Invitation
		f Party
	)
	err := r.db.QueryRow(ctx, `SELECT i.id, i.freelancer_id, i.email, i.name, i.expires_at, i.accepted_at, i.created_at,
			u.id, u.name, u.email
		FROM invitations i
		JOIN users u ON u.id = i.freelancer_id
		WHERE i.token_hash = $1`, tokenHash).
		Scan(&i.ID, &i.FreelancerID, &i.Email, &i.Name, &i.ExpiresAt, &i.AcceptedAt, &i.CreatedAt,
			&f.ID, &f.Name, &f.Email)
	if errors.Is(err, pgx.ErrNoRows) {
		return Invitation{}, Party{}, ErrNotFound
	}
	if err != nil {
		return Invitation{}, Party{}, fmt.Errorf("get invitation: %w", err)
	}
	return i, f, nil
}

// UserByEmail loads an account by normalized email, or user.ErrNotFound.
func (r *Repository) UserByEmail(ctx context.Context, email string) (user.User, error) {
	u, err := user.Scan(r.db.QueryRow(ctx, `SELECT `+user.Columns+` FROM users WHERE email = $1`, email))
	if errors.Is(err, pgx.ErrNoRows) {
		return user.User{}, user.ErrNotFound
	}
	if err != nil {
		return user.User{}, fmt.Errorf("get user by email: %w", err)
	}
	return u, nil
}

// IsTeamMember reports whether the account is a member of a client's company.
func (r *Repository) IsTeamMember(ctx context.Context, userID string) (bool, error) {
	var ok bool
	if err := r.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM company_members WHERE user_id = $1)`, userID).Scan(&ok); err != nil {
		return false, fmt.Errorf("check team member: %w", err)
	}
	return ok, nil
}

// IsLinked reports whether the freelancer and the client are already linked.
func (r *Repository) IsLinked(ctx context.Context, freelancerID, clientID string) (bool, error) {
	var ok bool
	err := r.db.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM freelancer_clients WHERE freelancer_id = $1 AND client_id = $2)`,
		freelancerID, clientID).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("check link: %w", err)
	}
	return ok, nil
}

// Accept marks the invitation accepted and links its freelancer with the
// client, atomically. An invitation that was accepted or expired in the
// meantime yields ErrInvitationInvalid.
func (r *Repository) Accept(ctx context.Context, invitationID, clientID string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var freelancerID string
	err = tx.QueryRow(ctx, `UPDATE invitations SET accepted_at = now()
		WHERE id = $1 AND accepted_at IS NULL AND expires_at > now()
		RETURNING freelancer_id`, invitationID).Scan(&freelancerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrInvitationInvalid
	}
	if err != nil {
		return fmt.Errorf("accept invitation: %w", err)
	}

	if _, err := tx.Exec(ctx, `INSERT INTO freelancer_clients (freelancer_id, client_id)
		VALUES ($1, $2) ON CONFLICT DO NOTHING`, freelancerID, clientID); err != nil {
		return fmt.Errorf("link client: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// ListClients returns the freelancer's clients with their open task counts,
// most recently linked first.
func (r *Repository) ListClients(ctx context.Context, freelancerID string) ([]Link, error) {
	return r.listLinks(ctx, `SELECT u.id, u.name, u.email, fc.created_at,
			(SELECT count(*) FROM tasks t
			 WHERE t.freelancer_id = fc.freelancer_id AND t.client_id = fc.client_id
			   AND t.status IN ('new', 'in_progress'))
		FROM freelancer_clients fc
		JOIN users u ON u.id = fc.client_id
		WHERE fc.freelancer_id = $1
		ORDER BY fc.created_at DESC`, freelancerID)
}

// ListFreelancers returns the freelancers the client works with — for a
// company member, the company owner's — counting the open tasks they see.
func (r *Repository) ListFreelancers(ctx context.Context, clientID string) ([]Link, error) {
	return r.listLinks(ctx, `SELECT u.id, u.name, u.email, fc.created_at,
			(SELECT count(*) FROM tasks t
			 WHERE t.freelancer_id = fc.freelancer_id AND t.client_id = fc.client_id
			   AND t.status IN ('new', 'in_progress')
			   AND sees_task($1, t.id, t.freelancer_id, t.client_id))
		FROM freelancer_clients fc
		JOIN users u ON u.id = fc.freelancer_id
		WHERE fc.client_id = acting_client($1)
		ORDER BY fc.created_at DESC`, clientID)
}

func (r *Repository) listLinks(ctx context.Context, q, userID string) ([]Link, error) {
	rows, err := r.db.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("list links: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Link, error) {
		var l Link
		err := row.Scan(&l.ID, &l.Name, &l.Email, &l.LinkedAt, &l.OpenTasks)
		return l, err
	})
	if err != nil {
		return nil, fmt.Errorf("scan links: %w", err)
	}
	return out, nil
}
