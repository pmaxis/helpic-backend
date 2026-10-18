// Package board is the Jira-style board shared by a freelancer and a client.
// Every project has its own workflow, and so does a freelancer–client pair's
// work outside any project (and a freelancer's personal work): an ordered
// list of statuses, which are the board's columns. A status keeps a bucket
// (new, in progress, done) so open counts, list filters and who
// may move a card still work whatever the status is called. The freelancer
// manages the columns, and so does the author of a project on that board.
// Both sides move cards, and moving a card sets the task's status.
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
	// ErrNotManager is returned when someone who neither freelances the board
	// nor authored a project on it tries to change the columns.
	ErrNotManager = errors.New("only the freelancer or the project's author manages the board")
	// ErrHasTasks is returned when removing a column whose tasks have no
	// other column of the same bucket to move to.
	ErrHasTasks = errors.New("column has tasks and no other column of its bucket")
	// ErrLastColumn is returned when removing a board's only column.
	ErrLastColumn = errors.New("a board needs at least one column")
)

// Pair identifies a workflow: a freelancer and a client, or a freelancer's
// personal work (ClientID ""). ProjectID is set for a project's own workflow
// and empty for the pair's workflow (tasks outside any project). AuthorID is
// that project's creator when the board was opened for one.
type Pair struct {
	FreelancerID string
	ClientID     string
	ProjectID    string
	AuthorID     string
}

// OpenToParticipants reports whether someone who doesn't manage the board may
// move a card into a status of this bucket. Done is the manager's.
func OpenToParticipants(bucket string) bool {
	return bucket != tasks.StatusDone
}

// A status's category is fixed, the way Jira's is: to do, in progress, or done.
const (
	StatusCategoryTodo       = "todo"
	StatusCategoryInProgress = "in_progress"
	StatusCategoryDone       = "done"
)

// StatusCategoryOf is the category behind a stored bucket.
func StatusCategoryOf(bucket string) string {
	switch bucket {
	case tasks.StatusNew:
		return StatusCategoryTodo
	case tasks.StatusInProgress:
		return StatusCategoryInProgress
	default:
		return StatusCategoryDone
	}
}

// BucketOfStatusCategory maps a category to the bucket tasks store.
func BucketOfStatusCategory(category string) (string, bool) {
	switch category {
	case StatusCategoryTodo:
		return tasks.StatusNew, true
	case StatusCategoryInProgress:
		return tasks.StatusInProgress, true
	case StatusCategoryDone:
		return tasks.StatusDone, true
	default:
		return "", false
	}
}

// Column is a workflow status — one column of the board. Category is the
// bucket the rest of the product stores: new, in progress, or done.
type Column struct {
	ID                  string
	Pair                Pair
	Name                string
	Category            string
	ParticipantsCanMove bool
	Position            int
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
