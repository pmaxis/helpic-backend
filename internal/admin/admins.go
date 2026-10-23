package admin

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/pmaxis/helpic/internal/auth"
	"github.com/pmaxis/helpic/internal/user"
)

// Who can open the panel, and when they signed in: the admins with their
// devices, and every sign-in to an admin account, the failed ones too.

// Admin is an admin account.
type Admin struct {
	ID        string
	Email     string
	Name      string
	TwoFactor bool
	CreatedAt time.Time
	// LastSignIn is the newest sign-in that went through; nil before one.
	LastSignIn *time.Time
	Sessions   []Session
}

// Admins returns every admin, the oldest first, with their devices.
func (s *Service) Admins(ctx context.Context) ([]Admin, error) {
	return s.repo.Admins(ctx)
}

// Admins lists the admin accounts and their live sessions.
func (r *Repository) Admins(ctx context.Context) ([]Admin, error) {
	rows, err := r.db.Query(ctx, `
		SELECT u.id, u.email, u.name, u.two_factor_secret IS NOT NULL, u.created_at,
			(SELECT max(at) FROM admin_sign_ins WHERE admin_id = u.id AND outcome = 'ok')
		FROM users u WHERE u.role = 'admin'
		ORDER BY u.created_at, u.email`)
	if err != nil {
		return nil, fmt.Errorf("list admins: %w", err)
	}
	admins, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Admin, error) {
		a := Admin{Sessions: []Session{}}
		err := row.Scan(&a.ID, &a.Email, &a.Name, &a.TwoFactor, &a.CreatedAt, &a.LastSignIn)
		return a, err
	})
	if err != nil {
		return nil, fmt.Errorf("scan admins: %w", err)
	}

	rows, err = r.db.Query(ctx, `
		SELECT s.user_id, s.id, s.user_agent, s.ip, s.created_at, s.last_used_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE u.role = 'admin' AND s.expires_at > now()
		ORDER BY s.last_used_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list admin sessions: %w", err)
	}
	defer rows.Close()
	byID := map[string]*Admin{}
	for i := range admins {
		byID[admins[i].ID] = &admins[i]
	}
	for rows.Next() {
		var owner string
		var s Session
		if err := rows.Scan(&owner, &s.ID, &s.UserAgent, &s.IP, &s.CreatedAt, &s.LastUsedAt); err != nil {
			return nil, fmt.Errorf("scan admin session: %w", err)
		}
		if a := byID[owner]; a != nil {
			a.Sessions = append(a.Sessions, s)
		}
	}
	return admins, rows.Err()
}

// SignInRecord is one sign-in to an admin account.
type SignInRecord struct {
	ID int64
	At time.Time
	// AdminID is nil once the account is gone; the email stays.
	AdminID    *string
	AdminEmail string
	// Outcome is an auth.SignIn outcome.
	Outcome   string
	IP        string
	UserAgent string
}

// SignInFilter picks a page of sign-ins, the newest first.
type SignInFilter struct {
	// Failed keeps only the attempts that didn't go through.
	Failed bool
	Page   int
	Limit  int
}

// RecordSignIn notes a sign-in when it was to an admin account. For
// auth.Service.OnSignIn; a failure to note it is logged, not returned, so
// signing in goes on.
func (s *Service) RecordSignIn(ctx context.Context, in auth.SignIn) {
	if in.User.Role != user.RoleAdmin {
		return
	}
	if err := s.repo.RecordSignIn(ctx, in); err != nil {
		slog.Error("failed to record an admin sign-in", "error", err)
	}
}

// SignIns returns a page of sign-ins to admin accounts and how many match.
func (s *Service) SignIns(ctx context.Context, f SignInFilter) ([]SignInRecord, int, error) {
	return s.repo.SignIns(ctx, f)
}

// RecordSignIn stores the sign-in.
func (r *Repository) RecordSignIn(ctx context.Context, in auth.SignIn) error {
	if _, err := r.db.Exec(ctx, `
		INSERT INTO admin_sign_ins (admin_id, admin_email, outcome, ip, user_agent) VALUES ($1, $2, $3, $4, $5)`,
		in.User.ID, in.User.Email, in.Outcome, in.Client.IP, in.Client.UserAgent); err != nil {
		return fmt.Errorf("record admin sign-in: %w", err)
	}
	return nil
}

// SignIns lists a page of sign-ins, the newest first.
func (r *Repository) SignIns(ctx context.Context, f SignInFilter) ([]SignInRecord, int, error) {
	const where = `WHERE (NOT $1 OR outcome <> 'ok')`
	var total int
	if err := r.db.QueryRow(ctx, `SELECT count(*) FROM admin_sign_ins `+where, f.Failed).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count admin sign-ins: %w", err)
	}
	rows, err := r.db.Query(ctx, `SELECT id, at, admin_id, admin_email, outcome, ip, user_agent
		FROM admin_sign_ins `+where+`
		ORDER BY id DESC
		LIMIT $2 OFFSET $3`, f.Failed, f.Limit, (f.Page-1)*f.Limit)
	if err != nil {
		return nil, 0, fmt.Errorf("list admin sign-ins: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (SignInRecord, error) {
		var s SignInRecord
		err := row.Scan(&s.ID, &s.At, &s.AdminID, &s.AdminEmail, &s.Outcome, &s.IP, &s.UserAgent)
		return s, err
	})
	if err != nil {
		return nil, 0, fmt.Errorf("scan admin sign-ins: %w", err)
	}
	return out, total, nil
}
