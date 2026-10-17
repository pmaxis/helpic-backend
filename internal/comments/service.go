package comments

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/pmaxis/helpic/internal/notifications"
	"github.com/pmaxis/helpic/internal/tasks"
)

// excerptLength caps the comment text quoted in a notification.
const excerptLength = 120

// taskAccess finds a task the user takes part in and tells its other side
// what happened. Implemented by *tasks.Service.
type taskAccess interface {
	Get(ctx context.Context, userID, id string) (tasks.Task, error)
	Tell(ctx context.Context, actorID string, t tasks.Task, kind, detail string, withTask bool)
}

// Service holds the comment rules: both sides of a task read and write its
// comments; only the author edits or deletes one.
type Service struct {
	repo  *Repository
	tasks taskAccess
}

// NewService creates a comments Service.
func NewService(repo *Repository, tasks taskAccess) *Service {
	return &Service{repo: repo, tasks: tasks}
}

// List returns the comments on a task the user takes part in.
func (s *Service) List(ctx context.Context, userID, taskID string) ([]Comment, error) {
	if _, err := s.tasks.Get(ctx, userID, taskID); err != nil {
		return nil, err
	}
	return s.repo.List(ctx, taskID)
}

// Create adds the user's comment to the task and tells the other side.
func (s *Service) Create(ctx context.Context, userID, taskID, body string) (Comment, error) {
	t, err := s.tasks.Get(ctx, userID, taskID)
	if err != nil {
		return Comment{}, err
	}
	id, err := s.repo.Create(ctx, t.ID, userID, body)
	if err != nil {
		return Comment{}, err
	}
	s.tasks.Tell(ctx, userID, t, notifications.KindCommentAdded, Excerpt(body), true)
	return s.repo.Get(ctx, id)
}

// Update rewrites the user's own comment.
func (s *Service) Update(ctx context.Context, userID, id, body string) (Comment, error) {
	if _, err := s.own(ctx, userID, id); err != nil {
		return Comment{}, err
	}
	if err := s.repo.Update(ctx, id, body); err != nil {
		return Comment{}, err
	}
	return s.repo.Get(ctx, id)
}

// Delete removes the user's own comment.
func (s *Service) Delete(ctx context.Context, userID, id string) error {
	c, err := s.own(ctx, userID, id)
	if err != nil {
		return err
	}
	return s.repo.Delete(ctx, c.ID)
}

// own loads a comment the user wrote: ErrNotFound when they can't see its
// task, ErrNotAuthor when someone else wrote it.
func (s *Service) own(ctx context.Context, userID, id string) (Comment, error) {
	c, err := s.repo.Get(ctx, id)
	if err != nil {
		return Comment{}, err
	}
	if _, err := s.tasks.Get(ctx, userID, c.TaskID); err != nil {
		return Comment{}, ErrNotFound
	}
	if c.Author.ID != userID {
		return Comment{}, ErrNotAuthor
	}
	return c, nil
}

// Excerpt is the start of a comment on one line, for a notification.
func Excerpt(body string) string {
	s := strings.Join(strings.Fields(body), " ")
	if utf8.RuneCountInString(s) <= excerptLength {
		return s
	}
	return strings.TrimSpace(string([]rune(s)[:excerptLength-1])) + "…"
}
