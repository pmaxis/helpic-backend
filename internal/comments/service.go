package comments

import (
	"context"
	"slices"
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
	Tell(ctx context.Context, actorID string, t tasks.Task, kind, detail string, withTask bool, skip ...string)
	TellPeople(ctx context.Context, actorID string, t tasks.Task, to []string, kind, detail string, withTask bool)
	Readers(ctx context.Context, t tasks.Task) ([]tasks.Person, error)
}

// Service holds the comment rules: both sides of a task read and write its
// comments; only the author edits or deletes one. A comment mentions people
// who see its task, and tells them.
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

// People lists who a comment on the task can mention: everyone who sees it.
func (s *Service) People(ctx context.Context, userID, taskID string) ([]tasks.Person, error) {
	t, err := s.tasks.Get(ctx, userID, taskID)
	if err != nil {
		return nil, err
	}
	return s.tasks.Readers(ctx, t)
}

// Create adds the user's comment to the task and tells the other side; whoever
// it mentions is told they were, instead.
func (s *Service) Create(ctx context.Context, userID, taskID, body string) (Comment, error) {
	return s.CreatePinned(ctx, userID, taskID, body, nil)
}

// CreatePinned is Create for a comment on a spot of one of the task's
// images; a nil pin makes it a comment on the task as a whole.
func (s *Service) CreatePinned(ctx context.Context, userID, taskID, body string, pin *Pin) (Comment, error) {
	t, err := s.tasks.Get(ctx, userID, taskID)
	if err != nil {
		return Comment{}, err
	}
	if t.Archived() {
		return Comment{}, tasks.ErrArchived
	}
	body, err = s.resolve(ctx, t, body)
	if err != nil {
		return Comment{}, err
	}
	id, err := s.repo.Create(ctx, t.ID, userID, body, pin)
	if err != nil {
		return Comment{}, err
	}
	mentioned := others(Mentions(body), userID)
	s.tasks.Tell(ctx, userID, t, notifications.KindCommentAdded, Excerpt(body), true, mentioned...)
	s.tasks.TellPeople(ctx, userID, t, mentioned, notifications.KindCommentMention, Excerpt(body), true)
	return s.repo.Get(ctx, id)
}

// Update rewrites the user's own comment; anyone it now mentions for the
// first time is told.
func (s *Service) Update(ctx context.Context, userID, id, body string) (Comment, error) {
	c, t, err := s.own(ctx, userID, id)
	if err != nil {
		return Comment{}, err
	}
	body, err = s.resolve(ctx, t, body)
	if err != nil {
		return Comment{}, err
	}
	if err := s.repo.Update(ctx, id, body); err != nil {
		return Comment{}, err
	}
	before := Mentions(c.Body)
	added := slices.DeleteFunc(others(Mentions(body), userID), func(id string) bool { return slices.Contains(before, id) })
	s.tasks.TellPeople(ctx, userID, t, added, notifications.KindCommentMention, Excerpt(body), true)
	return s.repo.Get(ctx, id)
}

// resolve keeps the text's mentions of people who see the task.
func (s *Service) resolve(ctx context.Context, t tasks.Task, body string) (string, error) {
	if len(Mentions(body)) == 0 {
		return body, nil
	}
	readers, err := s.tasks.Readers(ctx, t)
	if err != nil {
		return "", err
	}
	return ResolveMentions(body, readers), nil
}

// others leaves the user out of the IDs: mentioning yourself tells nobody.
func others(ids []string, userID string) []string {
	return slices.DeleteFunc(ids, func(id string) bool { return id == userID })
}

// Delete removes the user's own comment.
func (s *Service) Delete(ctx context.Context, userID, id string) error {
	c, _, err := s.own(ctx, userID, id)
	if err != nil {
		return err
	}
	return s.repo.Delete(ctx, c.ID)
}

// own loads a comment the user wrote, and its task: ErrNotFound when they
// can't see the task, ErrNotAuthor when someone else wrote it,
// tasks.ErrArchived when its task is archived and the discussion closed with
// it.
func (s *Service) own(ctx context.Context, userID, id string) (Comment, tasks.Task, error) {
	c, err := s.repo.Get(ctx, id)
	if err != nil {
		return Comment{}, tasks.Task{}, err
	}
	t, err := s.tasks.Get(ctx, userID, c.TaskID)
	if err != nil {
		return Comment{}, tasks.Task{}, ErrNotFound
	}
	if c.Author.ID != userID {
		return Comment{}, tasks.Task{}, ErrNotAuthor
	}
	if t.Archived() {
		return Comment{}, tasks.Task{}, tasks.ErrArchived
	}
	return c, t, nil
}

// Excerpt is the start of a comment on one line, for a notification, its
// mentions as @Name.
func Excerpt(body string) string {
	s := strings.Join(strings.Fields(Plain(body)), " ")
	if utf8.RuneCountInString(s) <= excerptLength {
		return s
	}
	return strings.TrimSpace(string([]rune(s)[:excerptLength-1])) + "…"
}
