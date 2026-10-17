package board

import (
	"context"
	"slices"

	"github.com/pmaxis/helpic/internal/tasks"
	"github.com/pmaxis/helpic/internal/user"
)

// taskSource is the part of the tasks service the board needs. Implemented by
// *tasks.Service.
type taskSource interface {
	List(ctx context.Context, userID, role string, f tasks.Filter) ([]tasks.Task, error)
	Get(ctx context.Context, userID, id string) (tasks.Task, error)
	MoveTo(ctx context.Context, userID, role, id, statusID string) (tasks.Task, error)
}

// Service holds the board rules.
type Service struct {
	repo  *Repository
	tasks taskSource
}

// NewService creates a board Service.
func NewService(repo *Repository, tasks taskSource) *Service {
	return &Service{repo: repo, tasks: tasks}
}

// Scope says which board to open: a project's (its pair's board, showing the
// project's tasks), or the one with a counterpart — a client's ID or
// "personal" for a freelancer, a freelancer's ID for a client.
type Scope struct {
	With      string
	ProjectID string
	// Projects keeps only these projects' tasks on the board (NoProject: the
	// tasks outside any); empty keeps them all.
	Projects []string
}

// Pair resolves the board the user asked for, or ErrNoBoard.
func (s *Service) Pair(ctx context.Context, userID, role string, sc Scope) (Pair, error) {
	if sc.ProjectID != "" {
		return s.repo.ProjectPair(ctx, userID, sc.ProjectID)
	}
	p := Pair{FreelancerID: userID, ClientID: sc.With}
	if role == user.RoleClient {
		// A company member works on the company's boards: its owner is the client.
		acting, err := s.repo.ActingClient(ctx, userID)
		if err != nil {
			return Pair{}, err
		}
		p = Pair{FreelancerID: sc.With, ClientID: acting}
	} else if sc.With == tasks.PersonalFilter {
		p.ClientID = ""
	}
	if p.FreelancerID == "" {
		return Pair{}, ErrNoBoard
	}
	if p.ClientID != "" {
		ok, err := s.repo.Linked(ctx, p.FreelancerID, p.ClientID)
		if err != nil {
			return Pair{}, err
		}
		if !ok {
			return Pair{}, ErrNoBoard
		}
	}
	return p, nil
}

// counterpart is the tasks filter value for a pair's tasks, as the user sees them.
func counterpart(p Pair, role string) string {
	if role == user.RoleClient {
		return p.FreelancerID
	}
	if p.ClientID == "" {
		return tasks.PersonalFilter
	}
	return p.ClientID
}

// CanManage reports whether the user manages the pair's columns.
func CanManage(p Pair, userID string) bool {
	return p.FreelancerID == userID
}

// Columns returns a board's columns without tasks.
func (s *Service) Columns(ctx context.Context, userID, role string, sc Scope) (Pair, []Column, error) {
	p, err := s.Pair(ctx, userID, role, sc)
	if err != nil {
		return Pair{}, nil, err
	}
	cols, err := s.repo.Columns(ctx, p)
	return p, cols, err
}

// Board returns a board's columns with their tasks; a project scope shows only
// that project's tasks.
func (s *Service) Board(ctx context.Context, userID, role string, sc Scope) (Pair, []Lane, error) {
	p, cols, err := s.Columns(ctx, userID, role, sc)
	if err != nil {
		return Pair{}, nil, err
	}
	list, err := s.tasks.List(ctx, userID, role, tasks.Filter{CounterpartID: counterpart(p, role), ProjectID: sc.ProjectID})
	if err != nil {
		return Pair{}, nil, err
	}
	list = filterProjects(list, sc.Projects)
	return p, Arrange(cols, list), nil
}

// CreateColumn adds a column at the end of a board; only its freelancer may.
func (s *Service) CreateColumn(ctx context.Context, userID, role string, sc Scope, cp ColumnParams) error {
	p, err := s.Pair(ctx, userID, role, sc)
	if err != nil {
		return err
	}
	if !CanManage(p, userID) {
		return ErrNotManager
	}
	if _, err := s.repo.Columns(ctx, p); err != nil { // make sure the defaults exist first
		return err
	}
	return s.repo.Create(ctx, p, cp.Name, cp.Category)
}

// UpdateColumn renames a column or changes its category; its tasks take the
// new category.
func (s *Service) UpdateColumn(ctx context.Context, userID, id string, cp ColumnParams) error {
	if _, err := s.managed(ctx, userID, id); err != nil {
		return err
	}
	return s.repo.Update(ctx, id, cp.Name, cp.Category)
}

// DeleteColumn removes a column, unless it is the board's only one. An empty
// column just goes; a column with tasks moves them to the first other column
// of its category, and can't be removed (ErrHasTasks) when there is none.
func (s *Service) DeleteColumn(ctx context.Context, userID, id string) error {
	c, err := s.managed(ctx, userID, id)
	if err != nil {
		return err
	}
	cols, err := s.repo.Columns(ctx, c.Pair)
	if err != nil {
		return err
	}
	if len(cols) <= 1 {
		return ErrLastColumn
	}
	n, err := s.repo.TaskCount(ctx, id)
	if err != nil {
		return err
	}
	if n == 0 {
		return s.repo.Delete(ctx, id, "")
	}
	for _, x := range cols {
		if x.ID != c.ID && x.Category == c.Category {
			return s.repo.Delete(ctx, id, x.ID)
		}
	}
	return ErrHasTasks
}

// Reorder sets the order of a board's columns; only the freelancer may.
func (s *Service) Reorder(ctx context.Context, userID, role string, ids []string) error {
	if role != user.RoleFreelancer {
		return ErrNotManager
	}
	return s.repo.Reorder(ctx, userID, ids)
}

// managed loads a column the user manages. The client of the pair gets
// ErrNotManager, anyone else ErrNotFound.
func (s *Service) managed(ctx context.Context, userID, id string) (Column, error) {
	c, err := s.repo.Column(ctx, id)
	if err != nil {
		return Column{}, err
	}
	switch userID {
	case c.Pair.FreelancerID:
		return c, nil
	case c.Pair.ClientID:
		return Column{}, ErrNotManager
	}
	return Column{}, ErrNotFound
}

// Move puts a task into a column right after afterTaskID ("" = at the top).
// A column of another category changes the task's status category, which
// fails with tasks.ErrTransition when the user's role may not make it.
func (s *Service) Move(ctx context.Context, userID, role, taskID, columnID, afterTaskID string) (tasks.Task, error) {
	t, err := s.tasks.MoveTo(ctx, userID, role, taskID, columnID)
	if err != nil {
		return tasks.Task{}, err
	}

	// Rewrite the whole column's order across the pair's tasks, so cards
	// nobody moved keep their places around this one.
	p := Pair{FreelancerID: t.Freelancer.ID}
	if t.Client != nil {
		p.ClientID = t.Client.ID
	}
	list, err := s.tasks.List(ctx, userID, role, tasks.Filter{CounterpartID: counterpart(p, role)})
	if err != nil {
		return tasks.Task{}, err
	}
	cols, err := s.repo.Columns(ctx, p)
	if err != nil {
		return tasks.Task{}, err
	}
	var order []string
	for _, l := range Arrange(cols, list) {
		if l.Column.ID != columnID {
			continue
		}
		for _, x := range l.Tasks {
			if x.ID != taskID {
				order = append(order, x.ID)
			}
		}
	}
	at := 0
	if i := slices.Index(order, afterTaskID); i >= 0 {
		at = i + 1
	}
	order = slices.Insert(order, at, taskID)
	if err := s.repo.PlaceAll(ctx, columnID, order); err != nil {
		return tasks.Task{}, err
	}
	return t, nil
}
