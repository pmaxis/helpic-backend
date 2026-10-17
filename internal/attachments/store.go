package attachments

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Store keeps attachment bytes in a directory, one file per attachment ID.
type Store struct {
	dir string
}

// NewStore uses dir, creating it if needed.
func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("uploads dir: %w", err)
	}
	return &Store{dir: dir}, nil
}

// path maps an ID to its file; IDs are UUIDs from the database, so they never
// hold a path separator — Base is a second guard.
func (s *Store) path(id string) string {
	return filepath.Join(s.dir, filepath.Base(id))
}

// Save writes r to the attachment's file, reading at most max bytes;
// a longer stream yields ErrTooLarge and leaves no file behind.
func (s *Store) Save(id string, r io.Reader, max int64) (int64, error) {
	tmp, err := os.CreateTemp(s.dir, ".upload-*")
	if err != nil {
		return 0, fmt.Errorf("create upload: %w", err)
	}
	defer os.Remove(tmp.Name()) // a no-op once renamed

	n, err := io.Copy(tmp, io.LimitReader(r, max+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return 0, fmt.Errorf("write upload: %w", err)
	}
	if n > max {
		return 0, ErrTooLarge
	}
	if err := os.Rename(tmp.Name(), s.path(id)); err != nil {
		return 0, fmt.Errorf("store upload: %w", err)
	}
	return n, nil
}

// Open returns the attachment's file for reading.
func (s *Store) Open(id string) (*os.File, error) {
	f, err := os.Open(s.path(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	return f, err
}

// Remove deletes the attachment's file; a missing one is fine.
func (s *Store) Remove(id string) error {
	if err := os.Remove(s.path(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove upload: %w", err)
	}
	return nil
}
