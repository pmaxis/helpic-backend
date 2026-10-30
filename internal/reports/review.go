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

// What the client made of a sent report.
const (
	ReviewApproved         = "approved"
	ReviewChangesRequested = "changes_requested"
)

var (
	// ErrNotReviewer is returned for reviewing a report by anyone but its
	// client.
	ErrNotReviewer = errors.New("only the report's client reviews it")
	// ErrNotReviewable is returned for reviewing a report that isn't sent and
	// unpaid.
	ErrNotReviewable = errors.New("only a sent, unpaid report can be reviewed")
	// ErrNoChangesAsked is returned for keeping a report as it is when its
	// client hasn't asked for changes to it.
	ErrNoChangesAsked = errors.New("the client hasn't asked for changes")
)

// CanAnswer reports whether the viewer may answer the changes the client
// asked for by keeping the report as it is: its freelancer, while it's sent
// and the changes are asked for.
func CanAnswer(r Report, viewerID string) bool {
	return r.Client != nil && r.Freelancer.ID == viewerID && r.Status == StatusSent && r.Review == ReviewChangesRequested
}

// CanReview reports whether the viewer may approve the report or ask for
// changes to it: its client, while it's sent and unpaid.
func CanReview(r Report, viewerID string) bool {
	return r.Client != nil && r.Client.ID == viewerID && r.Status == StatusSent
}

// SetReview stores the client's review of a sent report, with what they
// wrote about it in its discussion when body isn't "".
func (r *Repository) SetReview(ctx context.Context, id, clientID, review, body string, at time.Time) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var got string
	err = tx.QueryRow(ctx, `UPDATE reports SET client_review = $2, client_reviewed_at = $3,
			client_review_note = CASE WHEN $2 = 'changes_requested' THEN $4 ELSE '' END, review_answer = ''
		WHERE id = $1 AND status = 'sent' RETURNING id`, id, review, at, body).Scan(&got)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotReviewable
	}
	if err != nil {
		return fmt.Errorf("review report: %w", err)
	}
	if body != "" {
		if _, err := tx.Exec(ctx, `INSERT INTO report_comments (report_id, author_id, body) VALUES ($1, $2, $3)`, id, clientID, body); err != nil {
			return fmt.Errorf("add review comment: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// Review records what the client made of a sent report: approved, or
// changes asked for, saying what in its discussion. The freelancer is told;
// asking for changes is what taking the report back to correct it answers.
func (s *Service) Review(ctx context.Context, clientID, id, review, body string) (Report, error) {
	r, err := s.repo.Get(ctx, clientID, id)
	if err != nil {
		return Report{}, err
	}
	if r.Client == nil || r.Client.ID != clientID {
		return Report{}, ErrNotReviewer
	}
	if !CanReview(r, clientID) {
		return Report{}, ErrNotReviewable
	}
	// The same review again with nothing to add changes nothing.
	if r.Review == review && body == "" {
		return s.read(ctx, clientID, id)
	}
	if err := s.repo.SetReview(ctx, r.ID, clientID, review, body, time.Now()); err != nil {
		return Report{}, err
	}
	event, kind := EventApproved, notifications.KindReportApproved
	if review == ReviewChangesRequested {
		event, kind = EventChangesRequested, notifications.KindReportChangesRequested
	}
	if r.Review != review {
		s.record(ctx, r.ID, clientID, event, nil)
	}
	s.notify.Notify(ctx, notifications.Notification{
		UserID:    r.Freelancer.ID,
		ActorID:   clientID,
		Kind:      kind,
		ReportID:  r.ID,
		ProjectID: soleProject(r),
		Subject:   r.Title,
		Detail:    comments.Excerpt(body),
	})
	return s.read(ctx, clientID, id)
}

// KeepAsIs answers the changes the client asked for with the report as it
// is: their review is cleared for them to give again, and why goes to its
// discussion.
func (r *Repository) KeepAsIs(ctx context.Context, id, freelancerID, body string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var got string
	err = tx.QueryRow(ctx, `UPDATE reports SET client_review = NULL, client_reviewed_at = NULL, client_review_note = '', review_answer = $2
		WHERE id = $1 AND status = 'sent' AND client_review = 'changes_requested' RETURNING id`, id, body).Scan(&got)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNoChangesAsked
	}
	if err != nil {
		return fmt.Errorf("keep report as it is: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO report_comments (report_id, author_id, body) VALUES ($1, $2, $3)`, id, freelancerID, body); err != nil {
		return fmt.Errorf("add answer comment: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// KeepAsIs is the freelancer's other answer to changes asked for, besides
// taking the report back to correct it: it's right as it is, and why. The
// client is told and reviews it again.
func (s *Service) KeepAsIs(ctx context.Context, freelancerID, id, body string) (Report, error) {
	r, err := s.repo.Get(ctx, freelancerID, id)
	if err != nil {
		return Report{}, err
	}
	if r.Freelancer.ID != freelancerID {
		return Report{}, ErrNotFound
	}
	if !CanAnswer(r, freelancerID) {
		return Report{}, ErrNoChangesAsked
	}
	if err := s.repo.KeepAsIs(ctx, r.ID, freelancerID, body); err != nil {
		return Report{}, err
	}
	s.record(ctx, r.ID, freelancerID, EventChangesDeclined, nil)
	s.notify.Notify(ctx, notifications.Notification{
		UserID:    r.Client.ID,
		ActorID:   freelancerID,
		Kind:      notifications.KindReportChangesDeclined,
		ReportID:  r.ID,
		ProjectID: soleProject(r),
		Subject:   r.Title,
		Detail:    comments.Excerpt(body),
	})
	return s.read(ctx, freelancerID, id)
}
