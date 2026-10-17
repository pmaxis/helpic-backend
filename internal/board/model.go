// Package board is the Jira-style board shared by a freelancer and a client.
// Every freelancer–client pair — and a freelancer's personal work — has its
// own workflow: an ordered list of statuses, which are the board's columns.
// Each status belongs to a category (new, in progress, done, cancelled), which
// keeps the role rules, open counts and list filters working whatever the
// statuses are called. The freelancer manages the columns; both sides move
// cards, and moving a card sets the task's status.
package board

import (
	"errors"
	"slices"

	"github.com/pmaxis/helpic/internal/tasks"
)

var (
	ErrNotFound = errors.New("column not found")
	// ErrNoBoard is returned for a board the user isn't part of.
	ErrNoBoard = errors.New("board not found")
	// ErrNotManager is returned when a client tries to change the columns;
	// only the freelancer manages the workflow.
	ErrNotManager = errors.New("only the freelancer manages the board")
	// ErrHasTasks is returned when removing a column whose tasks have no
	// other column of the same category to move to.
	ErrHasTasks = errors.New("column has tasks and no other column of its category")
	// ErrLastColumn is returned when removing a board's only column.
	ErrLastColumn = errors.New("a board needs at least one column")
)

// Pair identifies a workflow: a freelancer and a client, or a freelancer's
// personal work (ClientID "").
type Pair struct {
	FreelancerID string
	ClientID     string
}

// Column is a workflow status — one column of the board.
type Column struct {
	ID       string
	Pair     Pair
	Name     string
	Category string
	Position int
}

// Lane is a column with its tasks, in order.
type Lane struct {
	Column Column
	Tasks  []tasks.Task
}

// Arrange puts each task into its status's column. Within a column, tasks
// somebody ordered come first by position; the rest keep the order they came
// in.
func Arrange(columns []Column, list []tasks.Task) []Lane {
	lanes := make([]Lane, len(columns))
	byID := make(map[string]int, len(columns))
	for i, c := range columns {
		lanes[i] = Lane{Column: c, Tasks: []tasks.Task{}}
		byID[c.ID] = i
	}
	for _, t := range list {
		if i, ok := byID[t.StatusID]; ok {
			lanes[i].Tasks = append(lanes[i].Tasks, t)
		}
	}
	for i := range lanes {
		slices.SortStableFunc(lanes[i].Tasks, func(a, b tasks.Task) int {
			switch {
			case a.Position != nil && b.Position != nil:
				return *a.Position - *b.Position
			case a.Position != nil:
				return -1
			case b.Position != nil:
				return 1
			}
			return 0
		})
	}
	return lanes
}
