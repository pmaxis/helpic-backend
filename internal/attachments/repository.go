package attachments

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository handles PostgreSQL access for attachments.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository creates an attachments Repository backed by the given pool.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

const selectAttachment = `SELECT a.id, a.task_id, a.file_name, a.content_type, a.size_bytes, a.created_at,
		a.scan_status, a.threat, u.id, u.name, u.email,
		a.series_id, a.version, NOT EXISTS (SELECT 1 FROM attachments n WHERE n.series_id = a.series_id AND n.version > a.version),
		coalesce(a.review, ''), rv.id, rv.name, rv.email, a.reviewed_at
	FROM attachments a JOIN users u ON u.id = a.uploaded_by
	LEFT JOIN users rv ON rv.id = a.reviewed_by`

func scan(row pgx.Row) (Attachment, error) {
	var (
		a                     Attachment
		rvID, rvName, rvEmail *string
	)
	err := row.Scan(&a.ID, &a.TaskID, &a.FileName, &a.ContentType, &a.SizeBytes, &a.CreatedAt,
		&a.Scan, &a.Threat, &a.Uploader.ID, &a.Uploader.Name, &a.Uploader.Email,
		&a.SeriesID, &a.Version, &a.Latest, &a.Review, &rvID, &rvName, &rvEmail, &a.ReviewedAt)
	if rvID != nil {
		a.Reviewer = &Uploader{ID: *rvID, Name: *rvName, Email: *rvEmail}
	}
	return a, err
}

// List returns a task's files, oldest first: each series where its first
// version came, its versions in order.
func (r *Repository) List(ctx context.Context, taskID string) ([]Attachment, error) {
	rows, err := r.db.Query(ctx, selectAttachment+` WHERE a.task_id = $1
		ORDER BY (SELECT min(f.created_at) FROM attachments f WHERE f.series_id = a.series_id), a.series_id, a.version`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list attachments: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Attachment, error) { return scan(row) })
	if err != nil {
		return nil, fmt.Errorf("scan attachments: %w", err)
	}
	return out, nil
}

// Get loads one file, or ErrNotFound.
func (r *Repository) Get(ctx context.Context, id string) (Attachment, error) {
	a, err := scan(r.db.QueryRow(ctx, selectAttachment+` WHERE a.id = as_uuid($1)`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Attachment{}, ErrNotFound
	}
	if err != nil {
		return Attachment{}, fmt.Errorf("get attachment: %w", err)
	}
	return a, nil
}

// NewID reserves an ID for a file about to be written.
func (r *Repository) NewID(ctx context.Context) (string, error) {
	var id string
	if err := r.db.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&id); err != nil {
		return "", fmt.Errorf("new attachment id: %w", err)
	}
	return id, nil
}

// Create records a stored file, or returns ErrQuota when it would take its
// uploader past quota bytes (0 = no limit). The uploader's row is locked
// while the sum is read, so uploads at the same time count one another.
func (r *Repository) Create(ctx context.Context, a Attachment, quota int64) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if quota > 0 {
		var used int64
		if err := tx.QueryRow(ctx, `SELECT (SELECT coalesce(sum(size_bytes), 0) FROM attachments
				WHERE uploaded_by = u.id AND scan_status <> 'infected')
			FROM users u WHERE u.id = $1 FOR UPDATE`, a.Uploader.ID).Scan(&used); err != nil {
			return fmt.Errorf("sum uploads: %w", err)
		}
		if used+a.SizeBytes > quota {
			return ErrQuota
		}
	}
	// A new version comes after the series' latest; the series' rows are
	// locked, so two new versions at once don't take the same number.
	version := 1
	if a.SeriesID != a.ID {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM attachments WHERE series_id = $1 FOR UPDATE`, a.SeriesID); err != nil {
			return fmt.Errorf("lock series: %w", err)
		}
		if err := tx.QueryRow(ctx, `SELECT coalesce(max(version), 0) + 1 FROM attachments WHERE series_id = $1`, a.SeriesID).Scan(&version); err != nil {
			return fmt.Errorf("next version: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO attachments (id, task_id, uploaded_by, file_name, content_type, size_bytes, scan_status, series_id, version)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, a.ID, a.TaskID, a.Uploader.ID, a.FileName, a.ContentType, a.SizeBytes, a.Scan, a.SeriesID, version); err != nil {
		return fmt.Errorf("create attachment: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// SetReview records what the reviewer thinks of a version; "" takes it back.
func (r *Repository) SetReview(ctx context.Context, id, review, reviewerID string, at time.Time) error {
	_, err := r.db.Exec(ctx, `UPDATE attachments SET review = NULLIF($2, ''),
			reviewed_by = CASE WHEN $2 = '' THEN NULL ELSE $3::uuid END,
			reviewed_at = CASE WHEN $2 = '' THEN NULL ELSE $4::timestamptz END
		WHERE id = $1`, id, review, reviewerID, at)
	if err != nil {
		return fmt.Errorf("review attachment: %w", err)
	}
	return nil
}

// Delete removes a file's record.
func (r *Repository) Delete(ctx context.Context, id string) error {
	if _, err := r.db.Exec(ctx, `DELETE FROM attachments WHERE id = $1`, id); err != nil {
		return fmt.Errorf("delete attachment: %w", err)
	}
	return nil
}

// IDsOfTask lists a task's file IDs, to remove the files with the task.
func (r *Repository) IDsOfTask(ctx context.Context, taskID string) ([]string, error) {
	rows, err := r.db.Query(ctx, `SELECT id::text FROM attachments WHERE task_id = as_uuid($1)`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list attachment ids: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("scan attachment ids: %w", err)
	}
	return ids, nil
}

// claimAfter is how long a file taken for a check stays with that server;
// after it, another takes it, in case the first stopped halfway.
const claimAfter = 5 * time.Minute

// ClaimToScan takes up to limit files waiting for a check, new uploads
// before files stored without a scanner, oldest first.
func (r *Repository) ClaimToScan(ctx context.Context, limit int) ([]Attachment, error) {
	rows, err := r.db.Query(ctx, `
		WITH taken AS (
			UPDATE attachments SET scan_claimed_at = now()
			WHERE id IN (
				SELECT id FROM attachments
				WHERE scan_status IN ('pending', 'unscanned') AND (scan_claimed_at IS NULL OR scan_claimed_at < $2)
				ORDER BY scan_status = 'unscanned', created_at
				LIMIT $1
				FOR UPDATE SKIP LOCKED)
			RETURNING *)
		`+strings.Replace(selectAttachment, "FROM attachments a", "FROM taken a", 1), limit, time.Now().Add(-claimAfter))
	if err != nil {
		return nil, fmt.Errorf("claim attachments to scan: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Attachment, error) { return scan(row) })
	if err != nil {
		return nil, fmt.Errorf("scan claimed attachments: %w", err)
	}
	return out, nil
}

// ClaimOne takes the new upload for its check, unless another server has.
func (r *Repository) ClaimOne(ctx context.Context, id string) (bool, error) {
	tag, err := r.db.Exec(ctx, `UPDATE attachments SET scan_claimed_at = now()
		WHERE id = $1 AND scan_status = 'pending' AND scan_claimed_at IS NULL`, id)
	if err != nil {
		return false, fmt.Errorf("claim attachment to scan: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// SetScan records how the check went: status is ScanClean or ScanInfected.
func (r *Repository) SetScan(ctx context.Context, id, status, threat string) error {
	if _, err := r.db.Exec(ctx, `UPDATE attachments
		SET scan_status = $2, threat = $3, scanned_at = now(), scan_claimed_at = NULL
		WHERE id = $1`, id, status, threat); err != nil {
		return fmt.Errorf("set attachment scan: %w", err)
	}
	return nil
}

// ReleaseScan hands back a file whose check failed, for the next try.
func (r *Repository) ReleaseScan(ctx context.Context, id string) error {
	if _, err := r.db.Exec(ctx, `UPDATE attachments SET scan_claimed_at = NULL WHERE id = $1`, id); err != nil {
		return fmt.Errorf("release attachment scan: %w", err)
	}
	return nil
}
