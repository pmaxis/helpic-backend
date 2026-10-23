package applog

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository stores and reads the entries.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository creates an applog Repository backed by the given pool.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// Insert writes the entries in one round trip.
func (r *Repository) Insert(ctx context.Context, entries []Entry) error {
	rows := make([][]any, 0, len(entries))
	for _, e := range entries {
		attrs, err := json.Marshal(e.Attrs)
		if err != nil {
			attrs = []byte(`{}`)
		}
		rows = append(rows, []any{e.At, e.Level, e.Message, string(attrs)})
	}
	_, err := r.db.CopyFrom(ctx, pgx.Identifier{"app_logs"}, []string{"at", "level", "message", "attrs"}, pgx.CopyFromRows(rows))
	if err != nil {
		return fmt.Errorf("insert app logs: %w", err)
	}
	return nil
}

// escapeLike makes s match itself in a LIKE pattern.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// List returns a page of entries, the newest first, and how many match in
// all.
func (r *Repository) List(ctx context.Context, f Filter) ([]Entry, int, error) {
	const where = `WHERE ($1 = '' OR level = $1)
		  AND ($2 = '' OR message ILIKE '%' || $2 || '%' OR attrs::text ILIKE '%' || $2 || '%')`
	args := []any{f.Level, escapeLike(strings.TrimSpace(f.Query))}

	var total int
	if err := r.db.QueryRow(ctx, `SELECT count(*) FROM app_logs `+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count app logs: %w", err)
	}

	rows, err := r.db.Query(ctx, `SELECT id, at, level, message, attrs FROM app_logs `+where+`
		ORDER BY id DESC
		LIMIT $3 OFFSET $4`, append(args, f.Limit, (f.Page-1)*f.Limit)...)
	if err != nil {
		return nil, 0, fmt.Errorf("list app logs: %w", err)
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.At, &e.Level, &e.Message, &e.Attrs); err != nil {
			return nil, 0, fmt.Errorf("scan app log: %w", err)
		}
		out = append(out, e)
	}
	return out, total, rows.Err()
}

// Summary counts the entries since `since`.
func (r *Repository) Summary(ctx context.Context, since time.Time) (Summary, error) {
	var s Summary
	err := r.db.QueryRow(ctx, `SELECT
			count(*) FILTER (WHERE level = 'WARN'),
			count(*) FILTER (WHERE level = 'ERROR')
		FROM app_logs WHERE at >= $1`, since).Scan(&s.Warnings, &s.Errors)
	if err != nil {
		return Summary{}, fmt.Errorf("app log summary: %w", err)
	}
	return s, nil
}

// Cleanup deletes the entries older than Retention.
func (r *Repository) Cleanup(ctx context.Context) error {
	if _, err := r.db.Exec(ctx, `DELETE FROM app_logs WHERE at < $1`, time.Now().Add(-Retention)); err != nil {
		return fmt.Errorf("clean up app logs: %w", err)
	}
	return nil
}
