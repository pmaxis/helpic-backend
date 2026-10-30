package reports

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/pmaxis/helpic/internal/comments"
	"github.com/pmaxis/helpic/internal/notifications"
)

var (
	// ErrClosed is returned for a comment or a question on a report whose
	// discussion isn't open: never sent, or paid.
	ErrClosed = errors.New("the report's discussion is closed")
	// ErrNotRecallable is returned for taking back a report that isn't sent
	// and unpaid.
	ErrNotRecallable = errors.New("only a sent, unpaid report can be taken back")
)

// Comment is one message in a report's discussion.
type Comment struct {
	ID string
	// ItemID is the line it's about; "" for the report as a whole, or once
	// the line was dropped. ItemTitle is that line's task as it was named,
	// "" for the report as a whole.
	ItemID    string
	ItemTitle string
	Author    Party
	Body      string
	CreatedAt time.Time
}

// Discussing reports whether the report's two sides talk about it now: once
// it's sent, while a draft its freelancer took back to correct, until paid.
func Discussing(r Report) bool {
	return r.Client != nil && (r.Status == StatusSent || (r.Status == StatusDraft && r.RecalledAt != nil))
}

// CanDiscuss reports whether the viewer may comment on the report and
// resolve its questions: either side of it, while it's discussed.
func CanDiscuss(r Report, viewerID string) bool {
	return onBothSides(r, viewerID) && Discussing(r)
}

// CanRecall reports whether the viewer may take the report back to correct
// it: its freelancer, while it's sent and unpaid.
func CanRecall(r Report, viewerID string) bool {
	return r.Client != nil && r.Freelancer.ID == viewerID && r.Status == StatusSent
}

// CanDelete reports whether the viewer may delete the report: its
// freelancer, while it's a draft the client never had.
func CanDelete(r Report, viewerID string) bool {
	return r.Freelancer.ID == viewerID && r.Status == StatusDraft && r.RecalledAt == nil
}

// Comments returns a report's discussion, oldest first.
func (r *Repository) Comments(ctx context.Context, reportID string) ([]Comment, error) {
	rows, err := r.db.Query(ctx, `SELECT c.id, coalesce(c.item_id::text, ''), c.item_title, c.body, c.created_at, u.id, u.name, u.email
		FROM report_comments c JOIN users u ON u.id = c.author_id
		WHERE c.report_id = $1 ORDER BY c.created_at, c.id`, reportID)
	if err != nil {
		return nil, fmt.Errorf("list report comments: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Comment, error) {
		var c Comment
		err := row.Scan(&c.ID, &c.ItemID, &c.ItemTitle, &c.Body, &c.CreatedAt, &c.Author.ID, &c.Author.Name, &c.Author.Email)
		return c, err
	})
	if err != nil {
		return nil, fmt.Errorf("scan report comments: %w", err)
	}
	return out, nil
}

// AddComment stores a comment on the report, or on one of its lines; one by
// the client on a line opens a question on it.
func (r *Repository) AddComment(ctx context.Context, reportID string, item *Item, authorID, body string, question bool) (string, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var itemID *string
	title := ""
	if item != nil {
		itemID, title = &item.ID, item.Title
	}
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO report_comments (report_id, item_id, item_title, author_id, body) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		reportID, itemID, title, authorID, body).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("add report comment: %w", err)
	}
	if question && item != nil {
		if _, err := tx.Exec(ctx, `UPDATE report_items SET question_open = true WHERE id = $1`, item.ID); err != nil {
			return "", fmt.Errorf("open question: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit: %w", err)
	}
	return id, nil
}

// Resolve closes the question on one of the report's lines.
func (r *Repository) Resolve(ctx context.Context, reportID, itemID string) error {
	if _, err := r.db.Exec(ctx, `UPDATE report_items SET question_open = false WHERE id = as_uuid($2) AND report_id = $1`, reportID, itemID); err != nil {
		return fmt.Errorf("resolve question: %w", err)
	}
	return nil
}

// Recall makes a sent, unpaid report a draft again, at the given time: its
// prices, currency and look follow the tasks and the freelancer again, as
// in any draft. A logo nothing else shows any more is deleted.
func (r *Repository) Recall(ctx context.Context, id string, at time.Time) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var logo *string
	err = tx.QueryRow(ctx, `UPDATE reports r SET status = 'draft', sent_at = NULL, recalled_at = $2,
			currency = NULL, brand_color = NULL, business_details = NULL, brand_logo_id = NULL
		FROM reports old WHERE old.id = r.id AND r.id = $1 AND r.status = 'sent'
		RETURNING old.brand_logo_id::text`, id, at).Scan(&logo)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotRecallable
	}
	if err != nil {
		return fmt.Errorf("recall report: %w", err)
	}
	if logo != nil {
		if _, err := tx.Exec(ctx, `DELETE FROM brand_logos l WHERE l.id = $1
			AND NOT EXISTS (SELECT 1 FROM users u WHERE u.brand_logo_id = l.id)
			AND NOT EXISTS (SELECT 1 FROM reports o WHERE o.brand_logo_id = l.id)`, *logo); err != nil {
			return fmt.Errorf("drop unused logo: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// Comments returns the discussion on a report the user may see.
func (s *Service) Comments(ctx context.Context, userID, id string) ([]Comment, error) {
	r, err := s.repo.Get(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	return s.repo.Comments(ctx, r.ID)
}

// Comment adds the user's comment to the report — on one of its lines when
// itemID isn't "" — and tells the other side. The client commenting on a
// line asks a question about it, open until either side resolves it.
func (s *Service) Comment(ctx context.Context, userID, id, itemID, body string) (Comment, error) {
	r, err := s.discussed(ctx, userID, id)
	if err != nil {
		return Comment{}, err
	}
	var item *Item
	if itemID != "" {
		it, ok := findItem(r, itemID)
		if !ok {
			return Comment{}, ErrNotFound
		}
		item = &it
	}
	byClient := r.Client.ID == userID
	question := byClient && item != nil && !item.QuestionOpen
	commentID, err := s.repo.AddComment(ctx, r.ID, item, userID, body, byClient)
	if err != nil {
		return Comment{}, err
	}
	if question {
		s.record(ctx, r.ID, userID, EventQuestion, map[string]any{"task": item.Title})
	}
	other := r.Client.ID
	if byClient {
		other = r.Freelancer.ID
	}
	// The client doesn't see a draft: they hear once it's sent again.
	if r.Status == StatusSent || other == r.Freelancer.ID {
		s.notify.Notify(ctx, notifications.Notification{
			UserID:    other,
			ActorID:   userID,
			Kind:      notifications.KindReportComment,
			ReportID:  r.ID,
			ProjectID: soleProject(r),
			Subject:   r.Title,
			Detail:    comments.Excerpt(body),
		})
	}
	list, err := s.repo.Comments(ctx, r.ID)
	if err != nil {
		return Comment{}, err
	}
	for _, c := range list {
		if c.ID == commentID {
			return c, nil
		}
	}
	return Comment{}, ErrNotFound
}

// Resolve closes the question on one of the report's lines: either side
// may, once it's answered or the line corrected.
func (s *Service) Resolve(ctx context.Context, userID, id, itemID string) (Report, error) {
	r, err := s.discussed(ctx, userID, id)
	if err != nil {
		return Report{}, err
	}
	item, ok := findItem(r, itemID)
	if !ok {
		return Report{}, ErrNotFound
	}
	if item.QuestionOpen {
		if err := s.repo.Resolve(ctx, r.ID, item.ID); err != nil {
			return Report{}, err
		}
		s.record(ctx, r.ID, userID, EventResolved, map[string]any{"task": item.Title})
	}
	return s.read(ctx, userID, id)
}

// discussed loads a report the user may comment on: ErrNotFound when they
// can't see it, ErrNotParty for anyone but its two sides, ErrClosed when its
// discussion isn't open.
func (s *Service) discussed(ctx context.Context, userID, id string) (Report, error) {
	r, err := s.repo.Get(ctx, userID, id)
	if err != nil {
		return Report{}, err
	}
	if !onBothSides(r, userID) {
		return Report{}, ErrNotParty
	}
	if !Discussing(r) {
		return Report{}, ErrClosed
	}
	return r, nil
}

// Recall takes a sent, unpaid report back to its freelancer to correct: a
// draft again, which the client no longer sees until it's sent again. The
// client is told; its discussion and open questions stay.
func (s *Service) Recall(ctx context.Context, freelancerID, id string) (Report, error) {
	r, err := s.repo.Get(ctx, freelancerID, id)
	if err != nil {
		return Report{}, err
	}
	if r.Freelancer.ID != freelancerID {
		return Report{}, ErrNotFound
	}
	if !CanRecall(r, freelancerID) {
		return Report{}, ErrNotRecallable
	}
	if err := s.repo.Recall(ctx, r.ID, time.Now()); err != nil {
		return Report{}, err
	}
	s.record(ctx, r.ID, freelancerID, EventRecalled, nil)
	s.notify.Notify(ctx, notifications.Notification{
		UserID:    r.Client.ID,
		ActorID:   freelancerID,
		Kind:      notifications.KindReportRecalled,
		ReportID:  r.ID,
		ProjectID: soleProject(r),
		Subject:   r.Title,
	})
	return s.read(ctx, freelancerID, id)
}
