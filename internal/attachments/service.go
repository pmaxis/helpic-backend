package attachments

import (
	"bufio"
	"context"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"io"

	"github.com/pmaxis/helpic/internal/notifications"
	"github.com/pmaxis/helpic/internal/tasks"
)

const maxFileName = 200

// taskAccess finds a task the user takes part in and tells its other side
// what happened. Implemented by *tasks.Service.
type taskAccess interface {
	Get(ctx context.Context, userID, id string) (tasks.Task, error)
	Tell(ctx context.Context, actorID string, t tasks.Task, kind, detail string, withTask bool)
	Record(ctx context.Context, taskID, actorID, kind string, data map[string]any)
}

// Service holds the attachment rules: whoever takes part in a task sees,
// downloads and adds its files; only the uploader deletes one.
type Service struct {
	repo    *Repository
	store   *Store
	tasks   taskAccess
	maxSize int64
}

// NewService creates an attachments Service; maxSize caps one file in bytes.
func NewService(repo *Repository, store *Store, tasks taskAccess, maxSize int64) *Service {
	return &Service{repo: repo, store: store, tasks: tasks, maxSize: maxSize}
}

// MaxSize is the largest file accepted, in bytes.
func (s *Service) MaxSize() int64 { return s.maxSize }

// List returns the files on a task the user takes part in.
func (s *Service) List(ctx context.Context, userID, taskID string) ([]Attachment, error) {
	if _, err := s.tasks.Get(ctx, userID, taskID); err != nil {
		return nil, err
	}
	return s.repo.List(ctx, taskID)
}

// Upload stores a file on the task. Its type is read from the bytes, never
// taken from the browser.
func (s *Service) Upload(ctx context.Context, userID, taskID, fileName string, r io.Reader) (Attachment, error) {
	t, err := s.tasks.Get(ctx, userID, taskID)
	if err != nil {
		return Attachment{}, err
	}
	id, err := s.repo.NewID(ctx)
	if err != nil {
		return Attachment{}, err
	}

	br := bufio.NewReaderSize(r, 512)
	head, _ := br.Peek(512)
	if len(head) == 0 {
		return Attachment{}, ErrEmpty
	}
	size, err := s.store.Save(id, br, s.maxSize)
	if err != nil {
		return Attachment{}, err
	}

	a := Attachment{
		ID:          id,
		TaskID:      t.ID,
		Uploader:    Uploader{ID: userID},
		FileName:    cleanName(fileName),
		ContentType: sniff(head),
		SizeBytes:   size,
	}
	if err := s.repo.Create(ctx, a); err != nil {
		_ = s.store.Remove(id)
		return Attachment{}, err
	}
	s.tasks.Record(ctx, t.ID, userID, tasks.EventAttachmentAdded, map[string]any{"name": a.FileName})
	s.tasks.Tell(ctx, userID, t, notifications.KindAttachmentAdded, a.FileName, true)
	return s.repo.Get(ctx, id)
}

// Open returns a file of a task the user takes part in, for reading.
func (s *Service) Open(ctx context.Context, userID, id string) (Attachment, *os.File, error) {
	a, err := s.visible(ctx, userID, id)
	if err != nil {
		return Attachment{}, nil, err
	}
	f, err := s.store.Open(a.ID)
	return a, f, err
}

// Delete removes a file its uploader added.
func (s *Service) Delete(ctx context.Context, userID, id string) error {
	a, err := s.visible(ctx, userID, id)
	if err != nil {
		return err
	}
	if a.Uploader.ID != userID {
		return ErrNotAllowed
	}
	if err := s.repo.Delete(ctx, a.ID); err != nil {
		return err
	}
	s.tasks.Record(ctx, a.TaskID, userID, tasks.EventAttachmentRemoved, map[string]any{"name": a.FileName})
	return s.store.Remove(a.ID)
}

// visible loads a file whose task the user takes part in; ErrNotFound otherwise.
func (s *Service) visible(ctx context.Context, userID, id string) (Attachment, error) {
	a, err := s.repo.Get(ctx, id)
	if err != nil {
		return Attachment{}, err
	}
	if _, err := s.tasks.Get(ctx, userID, a.TaskID); err != nil {
		return Attachment{}, ErrNotFound
	}
	return a, nil
}

// TaskDeleted is the tasks package's delete hook: it notes the task's files
// before the task goes, and the returned func removes them once it's gone.
func (s *Service) TaskDeleted(ctx context.Context, taskID string) func() {
	ids, err := s.repo.IDsOfTask(ctx, taskID)
	if err != nil {
		slog.Error("list files of a deleted task", "task_id", taskID, "error", err)
		return func() {}
	}
	return func() {
		for _, id := range ids {
			if err := s.store.Remove(id); err != nil {
				slog.Error("remove file of a deleted task", "attachment_id", id, "error", err)
			}
		}
	}
}

// sniff names the file's type from its first bytes, without parameters.
func sniff(head []byte) string {
	t, _, err := mime.ParseMediaType(http.DetectContentType(head))
	if err != nil {
		return "application/octet-stream"
	}
	return t
}

// cleanName keeps the file's base name, without control characters or path
// parts, and caps its length.
func cleanName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) > maxFileName {
		ext := ""
		if i := strings.LastIndex(name, "."); i > 0 && utf8.RuneCountInString(name[i:]) <= 10 {
			ext = name[i:]
		}
		runes := []rune(strings.TrimSuffix(name, ext))
		name = string(runes[:maxFileName-utf8.RuneCountInString(ext)]) + ext
	}
	if name == "" || name == "." || name == ".." {
		return "file"
	}
	return name
}
