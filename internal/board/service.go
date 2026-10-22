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

// Scope says which board to open: a project's own workflow, or the one with a
// counterpart — a client's ID or "personal" for a freelancer, a freelancer's
// ID for a client. The counterpart board is the pair's workflow, and, once a
// project of that pair has its own columns, a merge of those workflows.
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
	if sc.With == tasks.PersonalFilter {
		// Anyone's own board: its owner stands where the freelancer would.
		return Pair{FreelancerID: userID}, nil
	}
	if role == user.RoleClient {
		// A company member works on the company's boards: its owner is the client.
		acting, err := s.repo.ActingClient(ctx, userID)
		if err != nil {
			return Pair{}, err
		}
		p = Pair{FreelancerID: sc.With, ClientID: acting}
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
	if p.ClientID == "" {
		return tasks.PersonalFilter
	}
	if role == user.RoleClient {
		return p.FreelancerID
	}
	return p.ClientID
}

// CanManage reports whether the user manages the board's columns: a
// project's are its author's alone, a pair's are its freelancer's.
func CanManage(p Pair, userID string) bool {
	if p.ProjectID != "" {
		return p.AuthorID == userID
	}
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

// Board returns a board's columns with their tasks. A project scope is that
// project's workflow. A pair scope is the pair's own workflow, unless one of
// its projects has columns of its own: then the columns are merged by name,
// the way the all board merges pairs, and merged is true.
func (s *Service) Board(ctx context.Context, userID, role string, sc Scope) (Pair, []Lane, bool, error) {
	p, cols, err := s.Columns(ctx, userID, role, sc)
	if err != nil {
		return Pair{}, nil, false, err
	}
	// On a project the client sees every freelancer's tasks. A freelancer's list
	// is already limited to their own.
	filter := tasks.Filter{ProjectID: sc.ProjectID, CounterpartID: counterpart(p, role)}
	if sc.ProjectID != "" && role == user.RoleClient {
		filter.CounterpartID = ""
	}
	list, err := s.tasks.List(ctx, userID, role, filter)
	if err != nil {
		return Pair{}, nil, false, err
	}
	list = filterProjects(list, sc.Projects)
	if p.ProjectID == "" {
		extra, err := s.repo.ProjectColumns(ctx, p)
		if err != nil {
			return Pair{}, nil, false, err
		}
		if len(extra) > 0 {
			return p, placeOnMerged(append(cols, extra...), list), true, nil
		}
	}
	return p, Arrange(cols, list), false, nil
}

// CreateColumn adds a column at the end of a board: a project's author on its
// board, the freelancer on the pair's.
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
	bucket := tasks.StatusInProgress
	if cp.StatusCategory != "" {
		bucket, _ = BucketOfStatusCategory(cp.StatusCategory)
	}
	return s.repo.Create(ctx, p, cp.Name, bucket)
}

// UpdateColumn renames a column. A new category moves its tasks into that bucket.
func (s *Service) UpdateColumn(ctx context.Context, userID, id string, cp ColumnParams) error {
	c, err := s.managed(ctx, userID, id)
	if err != nil {
		return err
	}
	if cp.StatusCategory == "" || StatusCategoryOf(c.Category) == cp.StatusCategory {
		return s.repo.Rename(ctx, id, cp.Name)
	}
	bucket, _ := BucketOfStatusCategory(cp.StatusCategory)
	return s.repo.Update(ctx, id, cp.Name, bucket)
}

// DeleteColumn removes a column, unless it is the board's only one. An empty
// column just goes; a column with tasks moves them to the first other column
// of the same category, and can't be removed (ErrHasTasks) when there is none.
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
	same := ""
	for _, x := range cols {
		if x.ID != c.ID && StatusCategoryOf(x.Category) == StatusCategoryOf(c.Category) {
			same = x.ID
			break
		}
	}
	if n > 0 && same == "" {
		return ErrHasTasks
	}
	if n == 0 && same == "" {
		// Only archived tasks may be left: they need some other column to
		// point at, though they're on no board.
		archived, err := s.repo.HasArchived(ctx, id)
		if err != nil {
			return err
		}
		if !archived {
			return s.repo.Delete(ctx, id, "")
		}
		for _, x := range cols {
			if x.ID != c.ID {
				same = x.ID
				break
			}
		}
	}
	return s.repo.Delete(ctx, id, same)
}

// Reorder sets the order of a board's columns: a project's author on its
// board, the freelancer on the pair's.
func (s *Service) Reorder(ctx context.Context, userID string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	c, err := s.managed(ctx, userID, ids[0])
	if err != nil {
		return err
	}
	return s.repo.Reorder(ctx, c.Pair, ids)
}

// managed loads a column the user manages. Someone on the board who doesn't
// manage it gets ErrNotManager; anyone else gets ErrNotFound.
func (s *Service) managed(ctx context.Context, userID, id string) (Column, error) {
	c, err := s.repo.Column(ctx, id)
	if err != nil {
		return Column{}, err
	}
	if err := s.managesPair(ctx, userID, c.Pair); err != nil {
		return Column{}, err
	}
	return c, nil
}

// managesPair reports whether the user may change this workflow: a project's
// is its author's alone, a pair's is its freelancer's. The pair's other side
// gets ErrNotManager; anyone else gets ErrNotFound.
func (s *Service) managesPair(ctx context.Context, userID string, p Pair) error {
	if p.ProjectID != "" {
		author, err := s.repo.ProjectAuthor(ctx, p.ProjectID)
		if err != nil {
			return err
		}
		if author == userID {
			return nil
		}
	} else if userID == p.FreelancerID {
		return nil
	}
	if userID == p.FreelancerID || userID == p.ClientID {
		return ErrNotManager
	}
	return ErrNotFound
}

// Move puts a task into a column right after afterTaskID ("" = at the top).
// A column of another bucket changes the task's status, which fails with
// tasks.ErrTransition when the user's role may not make it.
func (s *Service) Move(ctx context.Context, userID, role, taskID, columnID, afterTaskID string) (tasks.Task, error) {
	t, err := s.tasks.MoveTo(ctx, userID, role, taskID, columnID)
	if err != nil {
		return tasks.Task{}, err
	}

	// Rewrite the whole column's order across every task in it, including
	// ones this user doesn't see (another freelancer's on a project, ones a
	// contractor isn't on), so cards nobody moved keep their places around
	// this one.
	ids, err := s.repo.ColumnTaskIDs(ctx, columnID)
	if err != nil {
		return tasks.Task{}, err
	}
	order := slices.DeleteFunc(ids, func(id string) bool { return id == taskID })
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
