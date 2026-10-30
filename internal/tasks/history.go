package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
)

// Kinds of history event. Data carries "from" and "to" where a value
// changed (names, ISO dates, cents), or "name" for a file.
const (
	EventCreated     = "created"
	EventTitle       = "title"
	EventDescription = "description"
	EventDueOn       = "due_on"
	EventProject     = "project"
	EventStatus      = "status"
	EventPrice       = "price"
	// EventBilling is a switch between a fixed price and billing by the hour.
	EventBilling  = "billing"
	EventPriority = "priority"
	// EventLabels carries "added" and "removed": label names.
	EventLabels            = "labels"
	EventAttachmentAdded   = "attachment_added"
	EventAttachmentRemoved = "attachment_removed"
	// EventFileReviewed: name, version, and review — "approved",
	// "changes_requested", or "" when taken back.
	EventFileReviewed = "file_reviewed"
	// EventArchived carries "report": the paid report's title, or nothing
	// when the owner archived a personal task.
	EventArchived = "archived"
	// EventSubtaskAdded and EventSubtaskRemoved are on the parent and carry
	// "title": the subtask's.
	EventSubtaskAdded   = "subtask_added"
	EventSubtaskRemoved = "subtask_removed"
	// EventCreatedAt is the task's creation date moved; from and to are
	// RFC 3339 times.
	EventCreatedAt = "created_at"
	// EventShared and EventUnshared are the task's public link turned on and
	// off.
	EventShared   = "shared"
	EventUnshared = "unshared"
)

// Event is one change in a task's history.
type Event struct {
	ID        string
	TaskID    string
	Actor     Party
	Kind      string
	Data      map[string]any
	CreatedAt time.Time
}

// change is the data of an event where a value went from one thing to
// another; nil means "none".
func change(from, to any) map[string]any {
	return map[string]any{"from": from, "to": to}
}

// AddEvent stores one history event.
func (r *Repository) AddEvent(ctx context.Context, taskID, actorID, kind string, data map[string]any) error {
	if data == nil {
		data = map[string]any{}
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("encode event: %w", err)
	}
	if _, err := r.db.Exec(ctx, `INSERT INTO task_events (task_id, actor_id, kind, data) VALUES ($1, $2, $3, $4)`,
		taskID, actorID, kind, raw); err != nil {
		return fmt.Errorf("add task event: %w", err)
	}
	return nil
}

// Events returns a task's history, oldest first.
func (r *Repository) Events(ctx context.Context, taskID string) ([]Event, error) {
	rows, err := r.db.Query(ctx, `SELECT e.id, e.task_id, e.kind, e.data, e.created_at, u.id, u.name, u.email
		FROM task_events e JOIN users u ON u.id = e.actor_id
		WHERE e.task_id = $1 ORDER BY e.created_at, e.id`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list task events: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Event, error) {
		var (
			e   Event
			raw []byte
		)
		if err := row.Scan(&e.ID, &e.TaskID, &e.Kind, &raw, &e.CreatedAt, &e.Actor.ID, &e.Actor.Name, &e.Actor.Email); err != nil {
			return e, err
		}
		return e, json.Unmarshal(raw, &e.Data)
	})
	if err != nil {
		return nil, fmt.Errorf("scan task events: %w", err)
	}
	return out, nil
}

// Record adds an event to the task's history. Like a notification, it never
// fails the change it records: an event that can't be stored is logged.
func (s *Service) Record(ctx context.Context, taskID, actorID, kind string, data map[string]any) {
	if err := s.repo.AddEvent(ctx, taskID, actorID, kind, data); err != nil {
		slog.Error("record task event", "task_id", taskID, "kind", kind, "error", err)
	}
}

// History returns the history of a task the user takes part in.
func (s *Service) History(ctx context.Context, userID, taskID string) ([]Event, error) {
	t, err := s.repo.Get(ctx, userID, taskID)
	if err != nil {
		return nil, err
	}
	events, err := s.repo.Events(ctx, t.ID)
	if err != nil || CanSeePrice(t, userID) {
		return events, err
	}
	// A team member doesn't see the price, so not its changes either.
	out := events[:0]
	for _, e := range events {
		if e.Kind != EventPrice {
			out = append(out, e)
		}
	}
	return out, nil
}

// recordEdit records what an edit changed, field by field.
func (s *Service) recordEdit(ctx context.Context, actorID string, before, after Task) {
	if before.Title != after.Title {
		s.Record(ctx, after.ID, actorID, EventTitle, change(before.Title, after.Title))
	}
	if before.Description != after.Description {
		s.Record(ctx, after.ID, actorID, EventDescription, nil)
	}
	if from, to := isoDate(before.DueOn), isoDate(after.DueOn); from != to {
		s.Record(ctx, after.ID, actorID, EventDueOn, change(from, to))
	}
	if from, to := projectName(before.Project), projectName(after.Project); from != to {
		s.Record(ctx, after.ID, actorID, EventProject, change(from, to))
	}
	if from, to := before.CreatedAt.UTC().Format(time.RFC3339), after.CreatedAt.UTC().Format(time.RFC3339); from != to {
		s.Record(ctx, after.ID, actorID, EventCreatedAt, change(from, to))
	}
}

func isoDate(d *time.Time) any {
	if d == nil {
		return nil
	}
	return d.Format(dateLayout)
}

func projectName(p *ProjectRef) any {
	if p == nil {
		return nil
	}
	return p.Name
}

func centsOrNil(c *int64) any {
	if c == nil {
		return nil
	}
	return *c
}

// EventDTO is one change in a task's history.
type EventDTO struct {
	ID        string         `json:"id"`
	Actor     PartyDTO       `json:"actor"`
	Kind      string         `json:"kind"`
	Data      map[string]any `json:"data"`
	CreatedAt time.Time      `json:"created_at"`
}

// ToEventDTO maps a history event.
func ToEventDTO(e Event) EventDTO {
	data := e.Data
	if data == nil {
		data = map[string]any{}
	}
	return EventDTO{ID: e.ID, Actor: PartyDTO(e.Actor), Kind: e.Kind, Data: data, CreatedAt: e.CreatedAt}
}
