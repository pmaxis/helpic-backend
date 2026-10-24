package admin

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// The files on tasks across Helpic: how much the accounts have uploaded
// against their quota, and how full the disk keeping the files is. Sizes and
// counts only; never a file's name or contents.

// Uploads are the limits on files from the config, zero for none, and where
// the files are kept.
type Uploads struct {
	MaxFileBytes int64
	QuotaBytes   int64
	Dir          string
}

// UseUploads lets the panel weigh the files against their limits and their
// disk.
func (s *Service) UseUploads(u Uploads) {
	s.uploads = u
}

// NearQuotaShare is how much of their quota an account has used to count as
// near it.
const NearQuotaShare = 0.9

// topUploaders is how many of the accounts that uploaded the most are shown.
const topUploaders = 5

// Uploader is an account by what it has uploaded.
type Uploader struct {
	ID    string
	Email string
	Name  string
	Files int
	Bytes int64
}

// Disk is the disk the files are kept on.
type Disk struct {
	TotalBytes uint64
	FreeBytes  uint64
}

// Storage is the files as they stand.
type Storage struct {
	Files int
	Bytes int64
	// Accounts have uploaded a file at least.
	Accounts int
	// NearQuota have used NearQuotaShare of the quota or more; zero
	// without a quota.
	NearQuota    int
	QuotaBytes   int64
	MaxFileBytes int64
	// Top are the accounts that uploaded the most, the most first.
	Top []Uploader
	// Disk is nil when it can't be read.
	Disk *Disk
}

// Storage weighs the files against the limits and reads their disk.
func (s *Service) Storage(ctx context.Context) (Storage, error) {
	near := int64(0)
	if s.uploads.QuotaBytes > 0 {
		near = int64(float64(s.uploads.QuotaBytes) * NearQuotaShare)
	}
	st, err := s.repo.Storage(ctx, near)
	if err != nil {
		return Storage{}, err
	}
	st.QuotaBytes, st.MaxFileBytes = s.uploads.QuotaBytes, s.uploads.MaxFileBytes
	if s.uploads.Dir != "" {
		if d, err := diskUsage(s.uploads.Dir); err == nil {
			st.Disk = &d
		}
	}
	return st, nil
}

// Storage counts the files and who uploaded them; near is how much an
// account has to have uploaded to count as near its quota, zero for no quota.
func (r *Repository) Storage(ctx context.Context, near int64) (Storage, error) {
	var st Storage
	err := r.db.QueryRow(ctx, `SELECT count(*), coalesce(sum(size_bytes), 0), count(DISTINCT uploaded_by),
			CASE WHEN $1::bigint > 0 THEN (SELECT count(*) FROM (
				SELECT 1 FROM attachments GROUP BY uploaded_by HAVING sum(size_bytes) >= $1::bigint) n)
			ELSE 0 END
		FROM attachments`, near).Scan(&st.Files, &st.Bytes, &st.Accounts, &st.NearQuota)
	if err != nil {
		return Storage{}, fmt.Errorf("count files: %w", err)
	}
	rows, err := r.db.Query(ctx, `SELECT u.id::text, u.email, u.name, count(*), sum(a.size_bytes)
		FROM attachments a JOIN users u ON u.id = a.uploaded_by
		GROUP BY u.id
		ORDER BY sum(a.size_bytes) DESC, u.email
		LIMIT $1`, topUploaders)
	if err != nil {
		return Storage{}, fmt.Errorf("list top uploaders: %w", err)
	}
	st.Top, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (Uploader, error) {
		var u Uploader
		return u, row.Scan(&u.ID, &u.Email, &u.Name, &u.Files, &u.Bytes)
	})
	if err != nil {
		return Storage{}, fmt.Errorf("scan top uploaders: %w", err)
	}
	return st, nil
}
