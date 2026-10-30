package reports

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
)

// Kinds of report history event.
const (
	EventCreated     = "created"
	EventTitle       = "title"        // from, to
	EventSummary     = "summary"      // the summary changed
	EventKind        = "kind"         // from, to: basic / detailed
	EventItemNotes   = "item_notes"   // task: the task's notes changed
	EventItemRemoved = "item_removed" // task: dropped from the report
	EventSent        = "sent"
	EventPaid        = "paid"     // tasks: how many it archived
	EventQuestion    = "question" // task: the client asked about the line
	EventResolved    = "resolved" // task: the question on the line was resolved
	EventRecalled    = "recalled" // the freelancer took it back to correct it
	// EventApproved: the client approved it; EventChangesRequested: they
	// asked for changes to it.
	EventApproved         = "approved"
	EventChangesRequested = "changes_requested"
	// EventChangesDeclined: the freelancer kept it as it was, saying why.
	EventChangesDeclined = "changes_declined"
)

// clientEvents are what a client sees of a report's history: from the moment
// it reached them, and what was said about it. Drafting stays the
// freelancer's.
var clientEvents = map[string]bool{EventSent: true, EventPaid: true, EventQuestion: true, EventResolved: true, EventRecalled: true,
	EventApproved: true, EventChangesRequested: true, EventChangesDeclined: true}

// Event is one change in a report's history.
type Event struct {
	ID        string
	Actor     Party
	Kind      string
	Data      map[string]any
	CreatedAt time.Time
}

// AddEvent stores one history event.
func (r *Repository) AddEvent(ctx context.Context, reportID, actorID, kind string, data map[string]any) error {
	if data == nil {
		data = map[string]any{}
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("encode event: %w", err)
	}
	if _, err := r.db.Exec(ctx, `INSERT INTO report_events (report_id, actor_id, kind, data) VALUES ($1, $2, $3, $4)`,
		reportID, actorID, kind, raw); err != nil {
		return fmt.Errorf("add report event: %w", err)
	}
	return nil
}

// Events returns a report's history, oldest first.
func (r *Repository) Events(ctx context.Context, reportID string) ([]Event, error) {
	rows, err := r.db.Query(ctx, `SELECT e.id, e.kind, e.data, e.created_at, u.id, u.name, u.email
		FROM report_events e JOIN users u ON u.id = e.actor_id
		WHERE e.report_id = $1 ORDER BY e.created_at, e.id`, reportID)
	if err != nil {
		return nil, fmt.Errorf("list report events: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Event, error) {
		var (
			e   Event
			raw []byte
		)
		if err := row.Scan(&e.ID, &e.Kind, &raw, &e.CreatedAt, &e.Actor.ID, &e.Actor.Name, &e.Actor.Email); err != nil {
			return e, err
		}
		return e, json.Unmarshal(raw, &e.Data)
	})
	if err != nil {
		return nil, fmt.Errorf("scan report events: %w", err)
	}
	return out, nil
}

// record adds an event to the report's history; like a notification, it
// never fails the change it records.
func (s *Service) record(ctx context.Context, reportID, actorID, kind string, data map[string]any) {
	if err := s.repo.AddEvent(ctx, reportID, actorID, kind, data); err != nil {
		slog.Error("record report event", "report_id", reportID, "kind", kind, "error", err)
	}
}

// History returns what happened to a report the user may see. Its client sees
// only what happened once it was sent to them.
func (s *Service) History(ctx context.Context, userID, id string) ([]Event, error) {
	r, err := s.repo.Get(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	events, err := s.repo.Events(ctx, r.ID)
	if err != nil || r.Freelancer.ID == userID {
		return events, err
	}
	out := events[:0]
	for _, e := range events {
		if clientEvents[e.Kind] {
			out = append(out, e)
		}
	}
	return out, nil
}

// EventDTO is one change in a report's history.
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
