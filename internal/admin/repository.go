package admin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository reads across every account; it writes only activity and new
// admins.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository creates an admin Repository backed by the given pool.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// dateLayout is how days go to and from PostgreSQL's date.
const dateLayout = "2006-01-02"

func date(t time.Time) string { return t.Format(dateLayout) }

func parseDate(s string) (time.Time, error) { return time.Parse(dateLayout, s) }

// TwoFactor tells whether the user signs in with two-factor; false when
// there is no such user.
func (r *Repository) TwoFactor(ctx context.Context, userID string) (bool, error) {
	var on bool
	err := r.db.QueryRow(ctx, `SELECT two_factor_secret IS NOT NULL FROM users WHERE id = $1`, userID).Scan(&on)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("admin two-factor: %w", err)
	}
	return on, nil
}

// CreateAdmin inserts an admin account; ErrEmailExists when the email is
// taken.
func (r *Repository) CreateAdmin(ctx context.Context, email, name, passwordHash, locale string) error {
	_, err := r.db.Exec(ctx, `INSERT INTO users (email, first_name, role, password_hash, locale) VALUES ($1, $2, 'admin', $3, $4)`,
		email, name, passwordHash, locale)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrEmailExists
	}
	if err != nil {
		return fmt.Errorf("create admin: %w", err)
	}
	return nil
}

// MarkActive records that the user used Helpic on the day.
func (r *Repository) MarkActive(ctx context.Context, userID string, day time.Time) error {
	_, err := r.db.Exec(ctx, `INSERT INTO user_activity (user_id, day) VALUES ($1, $2::date) ON CONFLICT DO NOTHING`, userID, date(day))
	if err != nil {
		return fmt.Errorf("mark active: %w", err)
	}
	return nil
}

// Admins are left out of the statistics: they are not users of Helpic. Their
// activity is never recorded (Track skips them), so only the counts of
// accounts need to.

// Totals counts the app as it stands; today is the current day.
func (r *Repository) Totals(ctx context.Context, today time.Time) (Totals, error) {
	var t Totals
	err := r.db.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM users WHERE role <> 'admin'),
			(SELECT count(*) FROM users WHERE role = 'freelancer'),
			(SELECT count(*) FROM users WHERE role = 'client'),
			(SELECT count(*) FROM pending_signups WHERE expires_at > now()),
			(SELECT count(*) FROM users WHERE two_factor_secret IS NOT NULL AND role <> 'admin'),
			(SELECT count(*) FROM user_activity WHERE day = $1::date),
			(SELECT count(DISTINCT user_id) FROM user_activity WHERE day > $1::date - 7),
			(SELECT count(DISTINCT user_id) FROM user_activity WHERE day > $1::date - 30),
			(SELECT count(*) FROM tasks),
			(SELECT count(*) FROM projects)`,
		date(today),
	).Scan(&t.Users, &t.Freelancers, &t.Clients, &t.PendingSignups, &t.TwoFactor,
		&t.ActiveDay, &t.ActiveWeek, &t.ActiveMonth, &t.Tasks, &t.Projects)
	if err != nil {
		return Totals{}, fmt.Errorf("admin totals: %w", err)
	}
	return t, nil
}

// Period counts the days from `from` up to, not including, `until`; start
// and end are the moments those days begin in the app's time zone.
func (r *Repository) Period(ctx context.Context, from, until, start, end time.Time) (Period, error) {
	var p Period
	err := r.db.QueryRow(ctx, `SELECT
			(SELECT count(DISTINCT user_id) FROM user_activity WHERE day >= $1::date AND day < $2::date),
			(SELECT count(*) FROM users WHERE created_at >= $3 AND created_at < $4 AND role <> 'admin'),
			(SELECT count(*) FROM tasks WHERE created_at >= $3 AND created_at < $4),
			(SELECT count(*) FROM tasks WHERE completed_at >= $3 AND completed_at < $4)`,
		date(from), date(until), start, end,
	).Scan(&p.Active, &p.NewUsers, &p.TasksCreated, &p.TasksCompleted)
	if err != nil {
		return Period{}, fmt.Errorf("admin period: %w", err)
	}
	return p, nil
}

// Days counts each day from `from` to `to`, in the time zone zone; start is
// the moment `from` began there.
func (r *Repository) Days(ctx context.Context, from, to, start time.Time, zone string) ([]Day, error) {
	rows, err := r.db.Query(ctx, `WITH days AS (
			SELECT generate_series($1::date, $2::date, interval '1 day')::date AS day
		), signups AS (
			SELECT (created_at AT TIME ZONE $3)::date AS day, count(*) AS n FROM users WHERE created_at >= $4 AND role <> 'admin' GROUP BY 1
		), active AS (
			SELECT day, count(*) AS n FROM user_activity WHERE day BETWEEN $1::date AND $2::date GROUP BY 1
		), created AS (
			SELECT (created_at AT TIME ZONE $3)::date AS day, count(*) AS n FROM tasks WHERE created_at >= $4 GROUP BY 1
		), completed AS (
			SELECT (completed_at AT TIME ZONE $3)::date AS day, count(*) AS n FROM tasks WHERE completed_at >= $4 GROUP BY 1
		)
		SELECT d.day::text, coalesce(s.n, 0), coalesce(a.n, 0), coalesce(c.n, 0), coalesce(x.n, 0)
		FROM days d
		LEFT JOIN signups s USING (day)
		LEFT JOIN active a USING (day)
		LEFT JOIN created c USING (day)
		LEFT JOIN completed x USING (day)
		ORDER BY d.day`,
		date(from), date(to), zone, start)
	if err != nil {
		return nil, fmt.Errorf("admin days: %w", err)
	}
	defer rows.Close()
	var out []Day
	for rows.Next() {
		var d Day
		var day string
		if err := rows.Scan(&day, &d.Signups, &d.Active, &d.TasksCreated, &d.TasksCompleted); err != nil {
			return nil, fmt.Errorf("scan admin day: %w", err)
		}
		if d.Day, err = parseDate(day); err != nil {
			return nil, fmt.Errorf("parse admin day: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// escapeLike makes s match itself in a LIKE pattern.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// accountColumns are an account's list columns, in Account's order, for a
// query over users u; $1 is today.
const accountColumns = `u.id, u.email, u.name, u.role, u.two_factor_secret IS NOT NULL AS two_factor,
	(SELECT count(*) FROM tasks t WHERE t.freelancer_id = u.id OR t.client_id = u.id) AS tasks,
	(SELECT count(*) FROM projects p WHERE p.freelancer_id = u.id OR p.client_id = u.id) AS projects,
	(SELECT count(*) FROM user_activity a WHERE a.user_id = u.id AND a.day > $1::date - 30) AS active_days,
	(SELECT max(a.day) FROM user_activity a WHERE a.user_id = u.id) AS last_active,
	u.created_at`

func scanAccount(row pgx.Row, extra ...any) (Account, error) {
	var a Account
	var last *time.Time
	dest := append([]any{&a.ID, &a.Email, &a.Name, &a.Role, &a.TwoFactor, &a.Tasks, &a.Projects, &a.ActiveDays, &last, &a.CreatedAt}, extra...)
	if err := row.Scan(dest...); err != nil {
		return Account{}, err
	}
	if last != nil {
		day := time.Date(last.Year(), last.Month(), last.Day(), 0, 0, 0, 0, time.UTC)
		a.LastActive = &day
	}
	return a, nil
}

// orders are the accounts list's orders; ties go to the newest.
var orders = map[string]string{
	SortCreated:    `created_at DESC, id`,
	SortLastActive: `last_active DESC NULLS LAST, created_at DESC, id`,
	SortTasks:      `tasks DESC, created_at DESC, id`,
}

// Accounts returns a page of accounts and how many match in all; today is
// the current day.
func (r *Repository) Accounts(ctx context.Context, f AccountFilter, today time.Time) ([]Account, int, error) {
	order, ok := orders[f.Sort]
	if !ok {
		order = orders[SortCreated]
	}
	const list = `WITH list AS (
			SELECT ` + accountColumns + `
			FROM users u
			WHERE u.role <> 'admin'
			  AND ($2 = '' OR u.email ILIKE '%' || $2 || '%' OR u.name ILIKE '%' || $2 || '%')
			  AND ($3 = '' OR u.role = $3)
		)
		SELECT * FROM list
		WHERE CASE $4
			WHEN 'active' THEN last_active > $1::date - 7
			WHEN 'idle' THEN last_active <= $1::date - 30
			WHEN 'never' THEN last_active IS NULL
			ELSE true END`
	args := []any{date(today), escapeLike(strings.TrimSpace(f.Query)), f.Role, f.Activity}

	var total int
	if err := r.db.QueryRow(ctx, `SELECT count(*) FROM (`+list+`) matched`, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count accounts: %w", err)
	}

	rows, err := r.db.Query(ctx, list+` ORDER BY `+order+` LIMIT $5 OFFSET $6`, append(args, f.Limit, (f.Page-1)*f.Limit)...)
	if err != nil {
		return nil, 0, fmt.Errorf("list accounts: %w", err)
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan account: %w", err)
		}
		out = append(out, a)
	}
	return out, total, rows.Err()
}

// AccountDetail loads one account in full, with the days it was active from
// since on. ErrNotFound when there is none.
func (r *Repository) AccountDetail(ctx context.Context, id string, today, since time.Time) (AccountDetail, error) {
	var d AccountDetail
	var teamName, teamRole *string
	var teamMembers *int
	var days []time.Time
	a, err := scanAccount(r.db.QueryRow(ctx, `SELECT `+accountColumns+`,
			u.locale, u.currency,
			coalesce(own.name, mc.name),
			CASE WHEN own.id IS NOT NULL THEN 'owner' ELSE m.role END,
			(SELECT count(*) FROM company_members cm WHERE cm.company_id = coalesce(own.id, mc.id)),
			(SELECT count(*) FROM freelancer_clients fc WHERE fc.freelancer_id = u.id OR fc.client_id = u.id),
			(SELECT count(*) FROM reports r WHERE (r.freelancer_id = u.id OR r.client_id = u.id) AND r.sent_at IS NOT NULL),
			(SELECT count(*) FROM reports r WHERE (r.freelancer_id = u.id OR r.client_id = u.id) AND r.paid_at IS NOT NULL),
			(SELECT coalesce(sum(seconds), 0) FROM time_entries te WHERE te.user_id = u.id),
			(SELECT count(*) FROM comments c WHERE c.author_id = u.id),
			EXISTS (SELECT 1 FROM telegram_links tl WHERE tl.user_id = u.id),
			(SELECT count(*) FROM sessions s WHERE s.user_id = u.id AND s.expires_at > now()),
			(SELECT max(s.created_at) FROM sessions s WHERE s.user_id = u.id),
			coalesce((SELECT array_agg(a.day ORDER BY a.day) FROM user_activity a WHERE a.user_id = u.id AND a.day >= $3::date), '{}')
		FROM users u
		LEFT JOIN companies own ON own.owner_id = u.id
		LEFT JOIN company_members m ON m.user_id = u.id
		LEFT JOIN companies mc ON mc.id = m.company_id
		WHERE u.id = $2 AND u.role <> 'admin'`, date(today), id, date(since)),
		&d.Locale, &d.Currency, &teamName, &teamRole, &teamMembers, &d.Partners, &d.ReportsSent, &d.ReportsPaid,
		&d.SecondsLogged, &d.Comments, &d.Telegram, &d.Sessions, &d.LastSignIn, &days)
	if errors.Is(err, pgx.ErrNoRows) {
		return AccountDetail{}, ErrNotFound
	}
	if err != nil {
		var pgErr interface{ SQLState() string }
		// Not a UUID: no such account either.
		if errors.As(err, &pgErr) && pgErr.SQLState() == "22P02" {
			return AccountDetail{}, ErrNotFound
		}
		return AccountDetail{}, fmt.Errorf("admin account detail: %w", err)
	}
	d.Account = a
	if teamName != nil && teamRole != nil {
		d.Team = &Team{Name: *teamName, Role: *teamRole}
		if teamMembers != nil {
			d.Team.Members = *teamMembers
		}
	}
	d.ActiveSince = since
	for _, day := range days {
		d.ActiveOn = append(d.ActiveOn, time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC))
	}
	return d, nil
}
