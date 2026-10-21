package timelog

import (
	"context"
	"time"

	"github.com/pmaxis/helpic/internal/tasks"
)

// taskAccess finds a task the user takes part in. Implemented by *tasks.Service.
type taskAccess interface {
	Get(ctx context.Context, userID, id string) (tasks.Task, error)
}

// Service holds the time rules: the task's freelancer writes time on it,
// whoever sees its price reads it, and only an entry's author changes it.
type Service struct {
	repo  *Repository
	tasks taskAccess
	// loc is the app's time zone: a timer's entry is on today's date there.
	loc *time.Location
	now func() time.Time
}

// NewService creates a timelog Service.
func NewService(repo *Repository, tasks taskAccess, loc *time.Location) *Service {
	if loc == nil {
		loc = time.UTC
	}
	return &Service{repo: repo, tasks: tasks, loc: loc, now: time.Now}
}

// List returns the time on a task, for whoever sees its price.
func (s *Service) List(ctx context.Context, userID, taskID string) ([]Entry, error) {
	t, err := s.tasks.Get(ctx, userID, taskID)
	if err != nil {
		return nil, err
	}
	if !tasks.CanSeePrice(t, userID) {
		return nil, ErrNotAllowed
	}
	return s.repo.List(ctx, t.ID)
}

// writable finds a task the user may write time on.
func (s *Service) writable(ctx context.Context, userID, taskID string) (tasks.Task, error) {
	t, err := s.tasks.Get(ctx, userID, taskID)
	if err != nil {
		return tasks.Task{}, err
	}
	if t.Archived() {
		return tasks.Task{}, tasks.ErrArchived
	}
	if t.Billed {
		return tasks.Task{}, ErrBilled
	}
	if !tasks.CanTrackTime(t, userID) {
		return tasks.Task{}, ErrNotAllowed
	}
	return t, nil
}

// Create writes time spent on the task by hand.
func (s *Service) Create(ctx context.Context, userID, taskID string, p Params) (Entry, error) {
	t, err := s.writable(ctx, userID, taskID)
	if err != nil {
		return Entry{}, err
	}
	id, err := s.repo.Create(ctx, t.ID, userID, p)
	if err != nil {
		return Entry{}, err
	}
	return s.repo.Get(ctx, id)
}

// own finds the user's entry on a task they may still write time on.
func (s *Service) own(ctx context.Context, userID, id string) (Entry, error) {
	e, err := s.repo.Get(ctx, id)
	if err != nil {
		return Entry{}, err
	}
	if e.User.ID != userID {
		// Someone else's entry: not found unless they see it.
		if _, err := s.List(ctx, userID, e.TaskID); err != nil {
			return Entry{}, ErrNotFound
		}
		return Entry{}, ErrNotAuthor
	}
	if _, err := s.writable(ctx, userID, e.TaskID); err != nil {
		return Entry{}, err
	}
	return e, nil
}

// Update rewrites the user's entry. A running one is stopped first.
func (s *Service) Update(ctx context.Context, userID, id string, p Params) (Entry, error) {
	e, err := s.own(ctx, userID, id)
	if err != nil {
		return Entry{}, err
	}
	if e.Running() {
		return Entry{}, ErrRunning
	}
	if err := s.repo.Update(ctx, e.ID, p); err != nil {
		return Entry{}, err
	}
	return s.repo.Get(ctx, e.ID)
}

// Delete removes the user's entry, running or not.
func (s *Service) Delete(ctx context.Context, userID, id string) error {
	e, err := s.own(ctx, userID, id)
	if err != nil {
		return err
	}
	return s.repo.Delete(ctx, e.ID)
}

// Running returns the user's running timer, or ErrNoTimer.
func (s *Service) Running(ctx context.Context, userID string) (Entry, error) {
	return s.repo.Running(ctx, userID)
}

// Start starts a timer on the task, as a new entry for today; one already
// running elsewhere stops and keeps its time.
func (s *Service) Start(ctx context.Context, userID, taskID string) (Entry, error) {
	t, err := s.writable(ctx, userID, taskID)
	if err != nil {
		return Entry{}, err
	}
	now := s.now().In(s.loc)
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	id, err := s.repo.Start(ctx, t.ID, userID, day, now)
	if err != nil {
		return Entry{}, err
	}
	return s.repo.Get(ctx, id)
}

// Stop ends the user's running timer and returns its entry.
func (s *Service) Stop(ctx context.Context, userID string) (Entry, error) {
	id, err := s.repo.Stop(ctx, userID, s.now())
	if err != nil {
		return Entry{}, err
	}
	return s.repo.Get(ctx, id)
}
