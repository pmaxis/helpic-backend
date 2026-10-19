package reports

import (
	"context"
	"time"

	"github.com/pmaxis/helpic/internal/i18n"
	"github.com/pmaxis/helpic/internal/notifications"
)

// notifier tells the client a report was sent. Implemented by
// *notifications.Service.
type notifier interface {
	Notify(ctx context.Context, n notifications.Notification)
}

// Service holds the report rules: the freelancer makes, edits and sends a
// report; the client reads it once sent.
type Service struct {
	repo   *Repository
	notify notifier
	tasks  taskRecorder
	// zone is the IANA name completed days are counted in.
	zone string
}

// NewService creates a reports Service; tasks records a paid report's moves
// in its tasks' history. zone is the app time zone, where a completed day is
// a day; "" is UTC.
func NewService(repo *Repository, notify notifier, tasks taskRecorder, zone string) *Service {
	if zone == "" {
		zone = "UTC"
	}
	return &Service{repo: repo, notify: notify, tasks: tasks, zone: zone}
}

// List returns the reports the user may see.
func (s *Service) List(ctx context.Context, userID string) ([]Report, error) {
	return s.repo.List(ctx, userID)
}

// Get returns a report the user may see.
func (s *Service) Get(ctx context.Context, userID, id string) (Report, error) {
	return s.repo.Get(ctx, userID, id)
}

// Create makes a draft report for one of the freelancer's clients — or, with
// no client, on their personal work — filled with the tasks completed in the
// period.
func (s *Service) Create(ctx context.Context, freelancerID string, p CreateParams) (Report, error) {
	var err error
	if p.ClientID != "" {
		ok, err := s.repo.Linked(ctx, freelancerID, p.ClientID)
		if err != nil {
			return Report{}, err
		}
		if !ok {
			return Report{}, ErrNotLinked
		}
	}
	projects := make([]ProjectRef, 0, len(p.ProjectIDs))
	for _, projectID := range p.ProjectIDs {
		name, err := s.repo.ProjectName(ctx, freelancerID, p.ClientID, projectID)
		if err != nil {
			return Report{}, err
		}
		projects = append(projects, ProjectRef{ID: projectID, Name: name})
	}
	if p.Title == "" {
		// One project names the report; several make it a general work report.
		head := ""
		if len(projects) == 1 {
			head = projects[0].Name
		}
		p.Title = DefaultTitle(head, p.From, p.To, i18n.FromContext(ctx))
	}
	id, err := s.repo.Create(ctx, freelancerID, p, projects, s.zone)
	if err != nil {
		return Report{}, err
	}
	s.record(ctx, id, freelancerID, EventCreated, nil)
	return s.repo.Get(ctx, freelancerID, id)
}

// DefaultTitle names a report after its project (if any) and period, in the
// locale: "Website — Oct 2026", or "Work report — 1 Oct – 15 Oct 2026"
// ("Звіт про роботу — 1 жовтня – 15 жовтня 2026").
func DefaultTitle(project string, from, to time.Time, locale string) string {
	head := i18n.T(locale, "Work report")
	if project != "" {
		head = project
	}
	lastDay := time.Date(from.Year(), from.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
	if from.Day() == 1 && to.Year() == from.Year() && to.Month() == from.Month() && to.Day() == lastDay {
		if locale == i18n.UK {
			return head + " — " + i18n.Month(locale, from)
		}
		return head + " — " + from.Format("Jan 2006")
	}
	return head + " — " + i18n.DayMonth(locale, from) + " – " + i18n.Date(locale, to)
}

// owned loads a draft the freelancer owns: ErrNotFound for anyone else,
// ErrSent once it's sent.
func (s *Service) owned(ctx context.Context, freelancerID, id string) (Report, error) {
	r, err := s.repo.Get(ctx, freelancerID, id)
	if err != nil {
		return Report{}, err
	}
	if r.Freelancer.ID != freelancerID {
		return Report{}, ErrNotFound
	}
	if r.Status != StatusDraft {
		return Report{}, ErrSent
	}
	return r, nil
}

// Update edits a draft's title, summary and kind.
func (s *Service) Update(ctx context.Context, freelancerID, id string, p UpdateParams) (Report, error) {
	before, err := s.owned(ctx, freelancerID, id)
	if err != nil {
		return Report{}, err
	}
	if err := s.repo.Update(ctx, id, p); err != nil {
		return Report{}, err
	}
	after, err := s.repo.Get(ctx, freelancerID, id)
	if err != nil {
		return Report{}, err
	}
	if before.Title != after.Title {
		s.record(ctx, id, freelancerID, EventTitle, map[string]any{"from": before.Title, "to": after.Title})
	}
	if before.Summary != after.Summary {
		s.record(ctx, id, freelancerID, EventSummary, nil)
	}
	if before.Kind != after.Kind {
		s.record(ctx, id, freelancerID, EventKind, map[string]any{"from": before.Kind, "to": after.Kind})
	}
	return after, nil
}

// SetItemNotes rewrites what a draft says was done in one of its tasks.
func (s *Service) SetItemNotes(ctx context.Context, freelancerID, id, itemID, notes string) (Report, error) {
	r, err := s.owned(ctx, freelancerID, id)
	if err != nil {
		return Report{}, err
	}
	item, ok := findItem(r, itemID)
	if !ok {
		return Report{}, ErrNotFound
	}
	if err := s.repo.SetItemNotes(ctx, id, itemID, notes); err != nil {
		return Report{}, err
	}
	if item.Notes != notes {
		s.record(ctx, id, freelancerID, EventItemNotes, map[string]any{"task": item.Title})
	}
	return s.repo.Get(ctx, freelancerID, id)
}

// RemoveItem drops a task from a draft.
func (s *Service) RemoveItem(ctx context.Context, freelancerID, id, itemID string) (Report, error) {
	r, err := s.owned(ctx, freelancerID, id)
	if err != nil {
		return Report{}, err
	}
	item, ok := findItem(r, itemID)
	if !ok {
		return Report{}, ErrNotFound
	}
	if err := s.repo.RemoveItem(ctx, id, itemID); err != nil {
		return Report{}, err
	}
	s.record(ctx, id, freelancerID, EventItemRemoved, map[string]any{"task": item.Title})
	return s.repo.Get(ctx, freelancerID, id)
}

// Send sends a draft to its client, who is notified. A personal report has no
// one to send it to.
func (s *Service) Send(ctx context.Context, freelancerID, id string) (Report, error) {
	r, err := s.owned(ctx, freelancerID, id)
	if err != nil {
		return Report{}, err
	}
	if r.Client == nil {
		return Report{}, ErrNoClient
	}
	if err := s.repo.Send(ctx, id, time.Now()); err != nil {
		return Report{}, err
	}
	s.record(ctx, id, freelancerID, EventSent, nil)
	s.notify.Notify(ctx, notifications.Notification{
		UserID:    r.Client.ID,
		ActorID:   freelancerID,
		Kind:      notifications.KindReportSent,
		ReportID:  r.ID,
		ProjectID: soleProject(r),
		Subject:   r.Title,
	})
	return s.repo.Get(ctx, freelancerID, id)
}

func findItem(r Report, itemID string) (Item, bool) {
	for _, it := range r.Items {
		if it.ID == itemID {
			return it, true
		}
	}
	return Item{}, false
}

// soleProject is the report's project when it covers exactly one, for the
// notification to point at; "" otherwise.
func soleProject(r Report) string {
	if len(r.Projects) == 1 {
		return r.Projects[0].ID
	}
	return ""
}

// Delete removes one of the freelancer's drafts. A sent or paid report stays,
// so the client keeps the copy they were sent.
func (s *Service) Delete(ctx context.Context, freelancerID, id string) error {
	r, err := s.repo.Get(ctx, freelancerID, id)
	if err != nil {
		return err
	}
	if r.Freelancer.ID != freelancerID {
		return ErrNotFound
	}
	if r.Status != StatusDraft {
		return ErrKept
	}
	return s.repo.Delete(ctx, id)
}
