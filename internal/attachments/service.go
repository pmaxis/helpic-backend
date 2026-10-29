package attachments

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/pmaxis/helpic/internal/notifications"
	"github.com/pmaxis/helpic/internal/tasks"
)

const maxFileName = 200

// taskAccess finds a task the user takes part in and tells its other side
// what happened. Implemented by *tasks.Service.
type taskAccess interface {
	Get(ctx context.Context, userID, id string) (tasks.Task, error)
	Tell(ctx context.Context, actorID string, t tasks.Task, kind, detail string, withTask bool, skip ...string)
	Record(ctx context.Context, taskID, actorID, kind string, data map[string]any)
}

// Service holds the attachment rules: whoever takes part in a task sees,
// downloads and adds its files; only the uploader deletes one.
type Service struct {
	repo    *Repository
	store   *Store
	tasks   taskAccess
	maxSize int64
	quota   int64
	// scanner checks new files for malware; nil stores them unscanned.
	scanner Scanner
}

// Scanner checks a file for malware, naming the threat it found or "" for
// a clean file. Implemented by *clamav.Client.
type Scanner interface {
	Scan(ctx context.Context, r io.Reader) (threat string, err error)
}

// UseScanner has new files checked for malware before they download, and
// files stored before checked as well.
func (s *Service) UseScanner(sc Scanner) {
	s.scanner = sc
}

// NewService creates an attachments Service; maxSize caps one file in bytes,
// and quota (when above zero) all the files one user has uploaded.
func NewService(repo *Repository, store *Store, tasks taskAccess, maxSize, quota int64) *Service {
	return &Service{repo: repo, store: store, tasks: tasks, maxSize: maxSize, quota: quota}
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
	if t.Archived() {
		return Attachment{}, tasks.ErrArchived
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
		Scan:        ScanUnscanned,
	}
	if s.scanner != nil {
		a.Scan = ScanPending
	}
	if err := s.repo.Create(ctx, a, s.quota); err != nil {
		_ = s.store.Remove(id)
		return Attachment{}, err
	}
	if s.scanner != nil {
		go s.scanSoon(id)
	}
	s.tasks.Record(ctx, t.ID, userID, tasks.EventAttachmentAdded, map[string]any{"name": a.FileName})
	s.tasks.Tell(ctx, userID, t, notifications.KindAttachmentAdded, a.FileName, true)
	return s.repo.Get(ctx, id)
}

// Open returns a file of a task the user takes part in, for reading:
// ErrScanPending while it waits for its check, ErrInfected after malware
// was found in it.
func (s *Service) Open(ctx context.Context, userID, id string) (Attachment, *os.File, error) {
	a, err := s.visible(ctx, userID, id)
	if err != nil {
		return Attachment{}, nil, err
	}
	switch a.Scan {
	case ScanPending:
		return Attachment{}, nil, ErrScanPending
	case ScanInfected:
		return Attachment{}, nil, ErrInfected
	}
	f, err := s.store.Open(a.ID)
	return a, f, err
}

// scanBatch is how many files one run of Run checks at most.
const scanBatch = 20

// scanSoon checks a new upload at once rather than at the next run of Run,
// unless another server already took it. Its context isn't the request's,
// which ends with the upload; should the server stop first, Run takes the
// file up later.
func (s *Service) scanSoon(id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	ok, err := s.repo.ClaimOne(ctx, id)
	if err != nil || !ok {
		if err != nil {
			slog.Error("failed to take an upload for its check", "attachment_id", id, "error", err)
		}
		return
	}
	a, err := s.repo.Get(ctx, id)
	if err != nil {
		slog.Error("failed to load an upload for its check", "attachment_id", id, "error", err)
		return
	}
	if err := s.check(ctx, a); err != nil {
		slog.Error("failed to check an upload for malware", "attachment_id", id, "error", err)
	}
}

// Run checks the files waiting for it, for the background jobs. A file that
// couldn't be checked waits for the next run, and the run fails.
func (s *Service) Run(ctx context.Context) error {
	if s.scanner == nil {
		return nil
	}
	list, err := s.repo.ClaimToScan(ctx, scanBatch)
	if err != nil {
		return err
	}
	var errs []error
	for _, a := range list {
		if err := s.check(ctx, a); err != nil {
			errs = append(errs, fmt.Errorf("attachment %s: %w", a.ID, err))
		}
	}
	return errors.Join(errs...)
}

// check scans a file taken for its check and records the outcome. Malware
// takes the bytes away for good; the row stays to say what happened.
func (s *Service) check(ctx context.Context, a Attachment) error {
	f, err := s.store.Open(a.ID)
	if errors.Is(err, ErrNotFound) {
		// Deleted meanwhile.
		return nil
	}
	if err != nil {
		_ = s.repo.ReleaseScan(ctx, a.ID)
		return err
	}
	threat, err := s.scanner.Scan(ctx, f)
	f.Close()
	if err != nil {
		if rerr := s.repo.ReleaseScan(ctx, a.ID); rerr != nil {
			slog.Error("failed to hand back a file for its next check", "attachment_id", a.ID, "error", rerr)
		}
		return err
	}
	if threat == "" {
		return s.repo.SetScan(ctx, a.ID, ScanClean, "")
	}
	if err := s.repo.SetScan(ctx, a.ID, ScanInfected, threat); err != nil {
		return err
	}
	slog.Warn("malware found in an attachment; its file was removed",
		"attachment_id", a.ID, "task_id", a.TaskID, "uploader_id", a.Uploader.ID, "threat", threat)
	return s.store.Remove(a.ID)
}

// Delete removes a file its uploader added, while its task isn't archived.
func (s *Service) Delete(ctx context.Context, userID, id string) error {
	a, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	t, err := s.tasks.Get(ctx, userID, a.TaskID)
	if err != nil {
		return ErrNotFound
	}
	if a.Uploader.ID != userID {
		return ErrNotAllowed
	}
	if t.Archived() {
		return tasks.ErrArchived
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
