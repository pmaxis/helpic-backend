// Package comments is the discussion on a task: its freelancer and client
// write comments there instead of rewriting the description. Either side
// comments; only the author edits or deletes a comment.
package comments

import (
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("comment not found")
	// ErrNotAuthor is returned when changing someone else's comment.
	ErrNotAuthor = errors.New("only the author changes a comment")
)

// Author is who wrote a comment.
type Author struct {
	ID    string
	Name  string
	Email string
}

// Comment is one message on a task.
type Comment struct {
	ID        string
	TaskID    string
	Author    Author
	Body      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Edited reports whether the comment was changed after it was written.
func (c Comment) Edited() bool {
	// Both are now() on insert; the trigger moves updated_at on every edit.
	return c.UpdatedAt.After(c.CreatedAt)
}
