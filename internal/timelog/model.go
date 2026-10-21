// Package timelog is the time a freelancer spends on their tasks: entries
// written in by hand, or counted by a timer. A task billed by the hour is
// priced from it (the database does that, see migration 000042). The time
// on a task is seen by whoever sees its price; only its freelancer writes
// it, and only the author of an entry changes it.
package timelog

import (
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("time entry not found")
	// ErrNotAllowed is returned when someone other than the task's
	// freelancer writes time on it, or someone who doesn't see its price
	// reads it.
	ErrNotAllowed = errors.New("only the task's freelancer writes time on it")
	// ErrNotAuthor is returned when changing someone else's entry.
	ErrNotAuthor = errors.New("only the author changes a time entry")
	// ErrRunning is returned when rewriting the time of an entry whose timer
	// is still running: it's stopped first.
	ErrRunning = errors.New("the timer is still running")
	// ErrBilled is returned for writing time on a task on a sent report: the
	// time there is billed.
	ErrBilled = errors.New("the task's time is billed")
	// ErrNoTimer is returned when stopping a timer while none runs.
	ErrNoTimer = errors.New("no timer is running")
)

// Party is who wrote an entry.
type Party struct {
	ID    string
	Name  string
	Email string
}

// Entry is time spent on a task on one day.
type Entry struct {
	ID        string
	TaskID    string
	TaskTitle string
	User      Party
	SpentOn   time.Time
	// Seconds is the time so far; a running entry adds the time since
	// RunningSince when it stops.
	Seconds      int64
	Note         string
	RunningSince *time.Time
	CreatedAt    time.Time
}

// Running reports whether the entry's timer is counting.
func (e Entry) Running() bool { return e.RunningSince != nil }

// Params is a validated entry written by hand.
type Params struct {
	SpentOn time.Time
	Seconds int64
	Note    string
}
