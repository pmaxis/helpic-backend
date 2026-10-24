package admin

import (
	"context"
	"fmt"
)

// The files on tasks: the limits on them, which an account's card weighs
// its uploads against, how much the files uploaded to Helpic take and how
// much room the disk keeping them has left.

// Uploads are the limits on files from the config, zero for none, and where
// the files are kept.
type Uploads struct {
	MaxFileBytes int64
	QuotaBytes   int64
	Dir          string
}

// UseUploads lets the panel weigh an account's files against the quota and
// read the disk keeping them.
func (s *Service) UseUploads(u Uploads) {
	s.uploads = u
}

// Disk is the disk the files are kept on.
type Disk struct {
	TotalBytes uint64
	FreeBytes  uint64
}

// Files counts the files uploaded to Helpic and how much they take, by their
// sizes as uploaded; nothing else on the disk counts.
func (s *Service) Files(ctx context.Context) (count int, bytes int64, err error) {
	return s.repo.Files(ctx)
}

// Files counts every file on a task and sums their sizes.
func (r *Repository) Files(ctx context.Context) (count int, bytes int64, err error) {
	if err = r.db.QueryRow(ctx, `SELECT count(*), coalesce(sum(size_bytes), 0) FROM attachments`).Scan(&count, &bytes); err != nil {
		return 0, 0, fmt.Errorf("count files: %w", err)
	}
	return count, bytes, nil
}

// Disk reads how big the disk keeping the files is and how much of it is
// free; false when it can't.
func (s *Service) Disk() (Disk, bool) {
	if s.uploads.Dir == "" {
		return Disk{}, false
	}
	d, err := diskUsage(s.uploads.Dir)
	return d, err == nil
}
