// Package applog keeps the server's warnings and errors in the database for
// the admin panel. A slog handler copies every record of WARN and above to a
// sink that writes them in batches; the log on stdout is unchanged. Entries
// are kept for Retention.
package applog

import "time"

// Retention is how long entries are kept.
const Retention = 30 * 24 * time.Hour

// Levels as they are stored.
const (
	LevelWarn  = "WARN"
	LevelError = "ERROR"
)

// Entry is one warning or error.
type Entry struct {
	ID      int64
	At      time.Time
	Level   string
	Message string
	// Attrs are the record's attributes; groups are nested objects.
	Attrs map[string]any
}

// Filter picks a page of entries, the newest first.
type Filter struct {
	// Level is LevelWarn, LevelError or empty for both.
	Level string
	// Query matches the message or any attribute, any part of them.
	Query string
	// Page is from 1.
	Page  int
	Limit int
}

// Summary counts the entries of the last day.
type Summary struct {
	Warnings int
	Errors   int
}
