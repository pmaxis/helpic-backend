package shares

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/pmaxis/helpic/internal/attachments"
	"github.com/pmaxis/helpic/internal/tasks"
)

// taskAccess finds a task the user takes part in and adds to its history.
// Implemented by *tasks.Service.
type taskAccess interface {
	Get(ctx context.Context, userID, id string) (tasks.Task, error)
	Record(ctx context.Context, taskID, actorID, kind string, data map[string]any)
}

// files reads a shared task's files. Implemented by *attachments.Service.
type files interface {
	OfTask(ctx context.Context, taskID string) ([]attachments.Attachment, error)
	OpenOnTask(ctx context.Context, taskID, id string) (attachments.Attachment, *os.File, error)
}

// Service holds the rules of task links.
type Service struct {
	repo   *Repository
	tasks  taskAccess
	files  files
	appURL string
}

// NewService creates a shares Service; appURL is the frontend links open.
func NewService(repo *Repository, tasks taskAccess, files files, appURL string) *Service {
	return &Service{repo: repo, tasks: tasks, files: files, appURL: strings.TrimRight(appURL, "/")}
}

// URL is the address a link opens.
func (s *Service) URL(sh Share) string {
	return s.appURL + "/share/" + sh.Token
}

// manageable finds a task whose link the user may manage.
func (s *Service) manageable(ctx context.Context, userID, taskID string) (tasks.Task, error) {
	t, err := s.tasks.Get(ctx, userID, taskID)
	if err != nil {
		return tasks.Task{}, err
	}
	if !tasks.CanShare(t, userID) {
		return tasks.Task{}, ErrNotAllowed
	}
	return t, nil
}

// Get returns the task's link, nil when it has none.
func (s *Service) Get(ctx context.Context, userID, taskID string) (*Share, error) {
	t, err := s.manageable(ctx, userID, taskID)
	if err != nil {
		return nil, err
	}
	sh, err := s.repo.Get(ctx, t.ID)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &sh, nil
}

// Create turns the task's link on and notes it in the history; a task that
// has one keeps it, and created is false.
func (s *Service) Create(ctx context.Context, userID, taskID string) (sh Share, created bool, err error) {
	t, err := s.manageable(ctx, userID, taskID)
	if err != nil {
		return Share{}, false, err
	}
	token, err := newToken()
	if err != nil {
		return Share{}, false, err
	}
	sh, created, err = s.repo.Create(ctx, t.ID, token, userID)
	if err != nil {
		return Share{}, false, err
	}
	if created {
		s.tasks.Record(ctx, t.ID, userID, tasks.EventShared, nil)
	}
	return sh, created, nil
}

// Delete turns the task's link off: it opens nothing any more.
func (s *Service) Delete(ctx context.Context, userID, taskID string) error {
	t, err := s.manageable(ctx, userID, taskID)
	if err != nil {
		return err
	}
	removed, err := s.repo.Delete(ctx, t.ID)
	if err != nil {
		return err
	}
	if removed {
		s.tasks.Record(ctx, t.ID, userID, tasks.EventUnshared, nil)
	}
	return nil
}

// Shared is what a link shows: the task, its subtasks and its files.
func (s *Service) Shared(ctx context.Context, token string) (Task, []Subtask, []attachments.Attachment, error) {
	t, err := s.repo.Task(ctx, token)
	if err != nil {
		return Task{}, nil, nil, err
	}
	subtasks, err := s.repo.Subtasks(ctx, t.ID)
	if err != nil {
		return Task{}, nil, nil, err
	}
	list, err := s.files.OfTask(ctx, t.ID)
	if err != nil {
		return Task{}, nil, nil, err
	}
	return t, subtasks, list, nil
}

// OpenFile opens one of the files a link shows.
func (s *Service) OpenFile(ctx context.Context, token, id string) (attachments.Attachment, *os.File, error) {
	t, err := s.repo.Task(ctx, token)
	if err != nil {
		return attachments.Attachment{}, nil, err
	}
	return s.files.OpenOnTask(ctx, t.ID, id)
}

// newToken is a link's secret: 32 random bytes, URL-safe.
func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate share token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
