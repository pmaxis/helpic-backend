package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/pmaxis/helpic/internal/user"
)

// Repository handles PostgreSQL access for connected chats and connect codes.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository creates a telegram Repository backed by the given pool.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// linkColumns reads a telegram_links row with its user's language and role.
const linkColumns = `user_id, chat_id, username, kinds, linked_at,
	coalesce((SELECT u.locale FROM users u WHERE u.id = telegram_links.user_id), 'en'),
	coalesce((SELECT u.role FROM users u WHERE u.id = telegram_links.user_id), '')`

func scanLink(row pgx.Row) (Link, error) {
	var l Link
	err := row.Scan(&l.UserID, &l.ChatID, &l.Username, &l.Kinds, &l.LinkedAt, &l.Locale, &l.Role)
	return l, err
}

// Get returns the user's connected chat. ErrNotConnected when there is none.
func (r *Repository) Get(ctx context.Context, userID string) (Link, error) {
	l, err := scanLink(r.db.QueryRow(ctx, `SELECT `+linkColumns+` FROM telegram_links WHERE user_id = $1`, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Link{}, ErrNotConnected
	}
	if err != nil {
		return Link{}, fmt.Errorf("get telegram link: %w", err)
	}
	return l, nil
}

// NewCode stores a connect code for the user, replacing any earlier one.
func (r *Repository) NewCode(ctx context.Context, userID string, hash []byte, expiresAt time.Time) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `DELETE FROM telegram_link_codes WHERE user_id = $1 OR expires_at <= now()`, userID); err != nil {
		return fmt.Errorf("clear telegram codes: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO telegram_link_codes (code_hash, user_id, expires_at) VALUES ($1, $2, $3)`,
		hash, userID, expiresAt); err != nil {
		return fmt.Errorf("create telegram code: %w", err)
	}
	return tx.Commit(ctx)
}

// TakeCode uses up a connect code and returns the user it was for, their
// name and language. ok is false for an unknown or expired code.
func (r *Repository) TakeCode(ctx context.Context, hash []byte) (userID, name, locale string, ok bool, err error) {
	var live bool
	err = r.db.QueryRow(ctx, `
		DELETE FROM telegram_link_codes c USING users u
		WHERE c.code_hash = $1 AND u.id = c.user_id
		RETURNING c.user_id, u.name, u.locale, c.expires_at > now()`, hash).Scan(&userID, &name, &locale, &live)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", false, nil
	}
	if err != nil {
		return "", "", "", false, fmt.Errorf("take telegram code: %w", err)
	}
	return userID, name, locale, live, nil
}

// Connect ties the chat to the user. Reconnecting keeps the kinds they chose.
func (r *Repository) Connect(ctx context.Context, userID string, chatID int64, username string, kinds []string) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO telegram_links (user_id, chat_id, username, kinds) VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id) DO UPDATE SET chat_id = EXCLUDED.chat_id, username = EXCLUDED.username, linked_at = now()`,
		userID, chatID, username, kinds)
	if err != nil {
		return fmt.Errorf("connect telegram: %w", err)
	}
	return nil
}

// SetKinds changes which notification kinds go to the user's chat.
// ErrNotConnected when there is none.
func (r *Repository) SetKinds(ctx context.Context, userID string, kinds []string) (Link, error) {
	l, err := scanLink(r.db.QueryRow(ctx, `UPDATE telegram_links SET kinds = $2 WHERE user_id = $1 RETURNING `+linkColumns, userID, kinds))
	if errors.Is(err, pgx.ErrNoRows) {
		return Link{}, ErrNotConnected
	}
	if err != nil {
		return Link{}, fmt.Errorf("set telegram kinds: %w", err)
	}
	return l, nil
}

// Disconnect removes the user's chat and returns it. ErrNotConnected when
// there was none.
func (r *Repository) Disconnect(ctx context.Context, userID string) (Link, error) {
	l, err := scanLink(r.db.QueryRow(ctx, `DELETE FROM telegram_links WHERE user_id = $1 RETURNING `+linkColumns, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Link{}, ErrNotConnected
	}
	if err != nil {
		return Link{}, fmt.Errorf("disconnect telegram: %w", err)
	}
	return l, nil
}

// DisconnectChat removes every account connected to the chat and returns how
// many there were.
func (r *Repository) DisconnectChat(ctx context.Context, chatID int64) (int64, error) {
	tag, err := r.db.Exec(ctx, `DELETE FROM telegram_links WHERE chat_id = $1`, chatID)
	if err != nil {
		return 0, fmt.Errorf("disconnect telegram chat: %w", err)
	}
	return tag.RowsAffected(), nil
}

// TryLock takes a session advisory lock on one pool connection and holds it
// until release. ok is false when another copy of the API already holds it.
// The lock drops when the connection does, so a crashed process lets the
// next one poll.
func (r *Repository) TryLock(ctx context.Context, key int64) (release func(), ok bool, err error) {
	conn, err := r.db.Acquire(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("acquire telegram lock: %w", err)
	}
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&ok); err != nil {
		conn.Release()
		return nil, false, fmt.Errorf("telegram lock: %w", err)
	}
	if !ok {
		conn.Release()
		return nil, false, nil
	}
	return func() {
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, key)
		conn.Release()
	}, true, nil
}

// DeleteExpiredCodes removes connect codes nobody used in time.
func (r *Repository) DeleteExpiredCodes(ctx context.Context) error {
	if _, err := r.db.Exec(ctx, `DELETE FROM telegram_link_codes WHERE expires_at <= now()`); err != nil {
		return fmt.Errorf("delete expired telegram codes: %w", err)
	}
	return nil
}

// ByChat returns the account connected to the chat: the one a message the
// bot sent there was for (messageID, 0 for none), or else the one connected
// last. ErrNotConnected when the chat has none.
func (r *Repository) ByChat(ctx context.Context, chatID, messageID int64) (Link, error) {
	l, err := scanLink(r.db.QueryRow(ctx, `SELECT `+linkColumns+` FROM telegram_links
		WHERE chat_id = $1
		ORDER BY user_id = (SELECT m.user_id FROM telegram_messages m WHERE m.chat_id = $1 AND m.message_id = $2) DESC NULLS LAST,
			linked_at DESC
		LIMIT 1`, chatID, messageID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Link{}, ErrNotConnected
	}
	if err != nil {
		return Link{}, fmt.Errorf("get telegram link by chat: %w", err)
	}
	return l, nil
}

// Remember keeps who a message the bot sent was for, and the task it was
// about ("" for none).
func (r *Repository) Remember(ctx context.Context, chatID, messageID int64, userID, taskID string) error {
	if _, err := r.db.Exec(ctx, `INSERT INTO telegram_messages (chat_id, message_id, user_id, task_id)
		VALUES ($1, $2, $3, NULLIF($4, '')::uuid)
		ON CONFLICT (chat_id, message_id) DO UPDATE SET user_id = EXCLUDED.user_id, task_id = EXCLUDED.task_id`,
		chatID, messageID, userID, taskID); err != nil {
		return fmt.Errorf("remember telegram message: %w", err)
	}
	return nil
}

// TaskOf returns the task a message the bot sent to the user was about, ""
// when it was about none or is forgotten.
func (r *Repository) TaskOf(ctx context.Context, chatID, messageID int64, userID string) (string, error) {
	var taskID string
	err := r.db.QueryRow(ctx, `SELECT coalesce(task_id::text, '') FROM telegram_messages
		WHERE chat_id = $1 AND message_id = $2 AND user_id = $3`, chatID, messageID, userID).Scan(&taskID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("telegram message task: %w", err)
	}
	return taskID, nil
}

// Counterparts are who the user may set a task with: a freelancer's
// clients, or the freelancers of a client's company, by name.
func (r *Repository) Counterparts(ctx context.Context, userID, role string) ([]Target, error) {
	q := `SELECT u.id::text, coalesce(NULLIF(u.name, ''), u.email) FROM freelancer_clients fc
		JOIN users u ON u.id = fc.client_id AND u.deleted_at IS NULL WHERE fc.freelancer_id = $1 ORDER BY lower(u.name), u.email`
	if role == user.RoleClient {
		q = `SELECT u.id::text, coalesce(NULLIF(u.name, ''), u.email) FROM freelancer_clients fc
			JOIN users u ON u.id = fc.freelancer_id AND u.deleted_at IS NULL WHERE fc.client_id = acting_client($1) ORDER BY lower(u.name), u.email`
	}
	rows, err := r.db.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("list counterparts: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Target, error) {
		var t Target
		return t, row.Scan(&t.ID, &t.Name)
	})
	if err != nil {
		return nil, fmt.Errorf("scan counterparts: %w", err)
	}
	return out, nil
}

// NewDraft keeps a task to be and the choices offered for it, and returns its ID.
func (r *Repository) NewDraft(ctx context.Context, userID string, d Draft) (int64, error) {
	targets, err := json.Marshal(d.Targets)
	if err != nil {
		return 0, fmt.Errorf("encode draft targets: %w", err)
	}
	var id int64
	if err := r.db.QueryRow(ctx, `INSERT INTO telegram_drafts (user_id, title, description, targets)
		VALUES ($1, $2, $3, $4) RETURNING id`, userID, d.Title, d.Description, targets).Scan(&id); err != nil {
		return 0, fmt.Errorf("create telegram draft: %w", err)
	}
	return id, nil
}

// draftColumns reads a telegram_drafts row.
const draftColumns = `title, description, targets, choice, projects`

func scanDraft(row pgx.Row) (Draft, bool, error) {
	var (
		d                 Draft
		targets, projects []byte
	)
	err := row.Scan(&d.Title, &d.Description, &targets, &d.Choice, &projects)
	if errors.Is(err, pgx.ErrNoRows) {
		return Draft{}, false, nil
	}
	if err != nil {
		return Draft{}, false, fmt.Errorf("read telegram draft: %w", err)
	}
	if err := json.Unmarshal(targets, &d.Targets); err != nil {
		return Draft{}, false, fmt.Errorf("decode draft targets: %w", err)
	}
	if err := json.Unmarshal(projects, &d.Projects); err != nil {
		return Draft{}, false, fmt.Errorf("decode draft projects: %w", err)
	}
	return d, true, nil
}

// Draft returns the user's draft; ok is false when it's gone or someone else's.
func (r *Repository) Draft(ctx context.Context, userID string, id int64) (Draft, bool, error) {
	return scanDraft(r.db.QueryRow(ctx, `SELECT `+draftColumns+` FROM telegram_drafts WHERE id = $1 AND user_id = $2`, id, userID))
}

// ChooseFor keeps who the draft's task is with, and the projects offered for it.
func (r *Repository) ChooseFor(ctx context.Context, userID string, id int64, choice string, projects []Target) error {
	raw, err := json.Marshal(projects)
	if err != nil {
		return fmt.Errorf("encode draft projects: %w", err)
	}
	if _, err := r.db.Exec(ctx, `UPDATE telegram_drafts SET choice = $3, projects = $4 WHERE id = $1 AND user_id = $2`,
		id, userID, choice, raw); err != nil {
		return fmt.Errorf("choose for telegram draft: %w", err)
	}
	return nil
}

// TakeDraft uses up the user's draft and returns it; ok is false when it's
// gone or someone else's.
func (r *Repository) TakeDraft(ctx context.Context, userID string, id int64) (Draft, bool, error) {
	return scanDraft(r.db.QueryRow(ctx, `DELETE FROM telegram_drafts WHERE id = $1 AND user_id = $2
		RETURNING `+draftColumns, id, userID))
}

// Projects are the projects a task with this pair may go into, by name: the
// freelancer is on them and the user sees them. clientID is "" for a
// freelancer's own projects.
func (r *Repository) Projects(ctx context.Context, userID, freelancerID, clientID string) ([]Target, error) {
	rows, err := r.db.Query(ctx, `SELECT p.id::text, p.name FROM projects p
		WHERE p.client_id IS NOT DISTINCT FROM NULLIF($3, '')::uuid
		  AND sees_project($1::uuid, p.id)
		  AND EXISTS (SELECT 1 FROM project_participants pp JOIN users u ON u.id = pp.user_id AND u.role = 'freelancer'
			WHERE pp.project_id = p.id AND pp.user_id = $2::uuid)
		ORDER BY lower(p.name), p.id`, userID, freelancerID, clientID)
	if err != nil {
		return nil, fmt.Errorf("list draft projects: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Target, error) {
		var t Target
		return t, row.Scan(&t.ID, &t.Name)
	})
	if err != nil {
		return nil, fmt.Errorf("scan draft projects: %w", err)
	}
	return out, nil
}

// DeleteStale forgets the messages and drafts older than the given age.
func (r *Repository) DeleteStale(ctx context.Context, messages, drafts time.Duration) error {
	if _, err := r.db.Exec(ctx, `DELETE FROM telegram_messages WHERE created_at < now() - make_interval(secs => $1)`, messages.Seconds()); err != nil {
		return fmt.Errorf("delete old telegram messages: %w", err)
	}
	if _, err := r.db.Exec(ctx, `DELETE FROM telegram_drafts WHERE created_at < now() - make_interval(secs => $1)`, drafts.Seconds()); err != nil {
		return fmt.Errorf("delete old telegram drafts: %w", err)
	}
	return nil
}

// ActingClient is the client the user acts as: their company's owner for a
// member or contractor, themselves otherwise.
func (r *Repository) ActingClient(ctx context.Context, userID string) (string, error) {
	var id string
	if err := r.db.QueryRow(ctx, `SELECT acting_client($1::uuid)::text`, userID).Scan(&id); err != nil {
		return "", fmt.Errorf("acting client: %w", err)
	}
	return id, nil
}
