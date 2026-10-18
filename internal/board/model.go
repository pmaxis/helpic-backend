// Package board is the Jira-style board shared by a freelancer and a client.
// Every project has its own workflow, and so does a freelancer–client pair's
// work outside any project (and a freelancer's personal work): an ordered
// list of statuses, which are the board's columns.
// Each status belongs to a category (new, in progress, done, cancelled), which
// keeps the role rules, open counts and list filters working whatever the
// statuses are called. The freelancer manages the columns, and so does the
// author of a project on that board. Both sides move cards, and moving a card
// sets the task's status.
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
	// other column of the same category to move to.
	ErrHasTasks = errors.New("column has tasks and no other column of its category")
	// ErrLastColumn is returned when removing a board's only column.
	ErrLastColumn = errors.New("a board needs at least one column")
	// ErrCategoryNotFound is returned for a category that isn't on this board.
	ErrCategoryNotFound = errors.New("category not found")
	// ErrCategoryExists is returned for a second category with the same name.
	ErrCategoryExists = errors.New("category already exists")
	// ErrCategoryInUse is returned when removing a category that still has columns.
	ErrCategoryInUse = errors.New("category has columns")
	// ErrLastCategory is returned when removing a board's only category.
	ErrLastCategory = errors.New("a board needs at least one category")
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

// Category is a status group the board's manager named. Its settings decide
// whether tasks in it count as open or finished, and who may move a task in.
type Category struct {
	ID                  string
	Pair                Pair
	Name                string
	CountsOpen          bool
	CountsDone          bool
	AllowsDelete        bool
	ParticipantsCanMove bool
	Position            int
	SystemKey           string
}

// Effect is how the owner describes the category: open, done or closed.
func (c Category) Effect() string {
	switch {
	case c.CountsDone:
		return EffectDone
	case c.CountsOpen:
		return EffectOpen
	default:
		return EffectClosed
	}
}

// Bucket is the task status the rest of the product stores: new, in progress,
// done or cancelled.
func (c Category) Bucket() string {
	switch {
	case c.CountsDone:
		return tasks.StatusDone
	case !c.CountsOpen:
		return tasks.StatusCancelled
	case c.AllowsDelete:
		return tasks.StatusNew
	default:
		return tasks.StatusInProgress
	}
}

// Apply sets the stored flags from the effect the owner chose.
func (c *Category) Apply(effect string, participantsCanMove, allowsDelete bool) {
	c.ParticipantsCanMove = participantsCanMove
	switch effect {
	case EffectDone:
		c.CountsDone, c.CountsOpen, c.AllowsDelete = true, false, false
	case EffectClosed:
		c.CountsDone, c.CountsOpen, c.AllowsDelete = false, false, false
	default:
		c.CountsDone, c.CountsOpen, c.AllowsDelete = false, true, allowsDelete
	}
}

const (
	EffectOpen   = "open"
	EffectDone   = "done"
	EffectClosed = "closed"
)

// Column is a workflow status — one column of the board.
type Column struct {
	ID                  string
	Pair                Pair
	Name                string
	Category            string
	CategoryID          string
	CategoryName        string
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
