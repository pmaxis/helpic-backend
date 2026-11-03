package tasks

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/pmaxis/helpic/internal/notifications"
)

// A task's freelancer can estimate it: the time they expect it to take, and
// at a fixed price the price. They send the estimate to the client, who
// approves it or asks for changes. Work goes on either way; the estimate is
// what the two agreed on, or haven't yet.

// What the client made of an estimate.
const (
	EstimateApproved         = "approved"
	EstimateChangesRequested = "changes_requested"
)

// MaxEstimateSeconds is the longest estimate: ten thousand hours.
const MaxEstimateSeconds = 10_000 * 3600

var (
	// ErrEstimateNotAllowed is returned when anyone but the task's freelancer
	// estimates it, sends or takes back its estimate.
	ErrEstimateNotAllowed = errors.New("only the freelancer estimates the task")
	// ErrNothingToEstimate is returned for sending an estimate with nothing
	// in it: a fixed price task without a price, or one billed by the hour
	// without an estimated time.
	ErrNothingToEstimate = errors.New("nothing to send: no price or estimated time")
	// ErrNoClient is returned for sending a personal task's estimate: nobody
	// approves it.
	ErrNoClient = errors.New("a personal task has no client to approve it")
	// ErrEstimateSent is returned for sending an estimate already sent as it
	// is, which the client has yet to answer or has approved.
	ErrEstimateSent = errors.New("the estimate is already sent")
	// ErrNoEstimate is returned for reviewing or taking back an estimate that
	// wasn't sent, or one the client has answered.
	ErrNoEstimate = errors.New("no estimate to answer")
	// ErrNotEstimateReviewer is returned for reviewing an estimate by anyone
	// but the task's client.
	ErrNotEstimateReviewer = errors.New("only the client reviews the estimate")
	// ErrEstimateOutdated is returned for reviewing an estimate the task has
	// changed since: the freelancer sends it again.
	ErrEstimateOutdated = errors.New("the task changed since the estimate was sent")
)

// Estimate is what the freelancer sent the client to approve: the task's
// billing, estimated time and price as they were then.
type Estimate struct {
	Billing string `json:"billing"`
	// Seconds is the estimated time; nil at a fixed price without one.
	Seconds *int64 `json:"seconds"`
	// Cents is the fixed price, or the time at the freelancer's rate when
	// sent; nil by the hour without a rate.
	Cents    *int64    `json:"cents"`
	Currency string    `json:"currency"`
	SentAt   time.Time `json:"sent_at"`
	// Review is "" while the client hasn't answered, then "approved" or
	// "changes_requested", with what they wrote in Note.
	Review     string     `json:"review"`
	Note       string     `json:"note"`
	ReviewedAt *time.Time `json:"reviewed_at"`
	Reviewer   *Party     `json:"reviewer"`
}

// EstimateOutdated reports whether the task changed since its estimate was
// sent: its billing, its estimated time, its currency or its fixed price.
// An hourly task's price follows its time, so only the estimate counts.
func EstimateOutdated(t Task) bool {
	e := t.Estimate
	if e == nil {
		return false
	}
	if e.Billing != t.Billing || e.Currency != t.Currency || !samePrice(e.Seconds, t.EstimateSeconds) {
		return true
	}
	return t.Billing == BillingFixed && !samePrice(e.Cents, t.PriceCents)
}

// CanEstimate reports whether the viewer may set the task's estimated time:
// whoever sets its price.
func CanEstimate(t Task, viewerID string) bool {
	return CanSetPrice(t, viewerID)
}

// CanSendEstimate reports whether the viewer may send the task's estimate
// to its client now: its freelancer, on shared work with something to send,
// unless it's sent as it is and the client hasn't asked for changes.
func CanSendEstimate(t Task, viewerID string) bool {
	if !CanEstimate(t, viewerID) || t.Client == nil || !hasEstimate(t) {
		return false
	}
	return t.Estimate == nil || EstimateOutdated(t) || t.Estimate.Review == EstimateChangesRequested
}

// CanWithdrawEstimate reports whether the viewer may take back the estimate
// they sent: while the client hasn't answered it.
func CanWithdrawEstimate(t Task, viewerID string) bool {
	return CanEstimate(t, viewerID) && t.Estimate != nil && t.Estimate.Review == ""
}

// CanReviewEstimate reports whether the viewer may approve the estimate or
// ask for changes to it: the task's client — the company's owner, who sees
// the price — while it's what the task still says.
func CanReviewEstimate(t Task, viewerID string) bool {
	return !t.Archived() && t.Client != nil && t.Client.ID == viewerID && t.Estimate != nil && !EstimateOutdated(t)
}

// hasEstimate reports whether there is something to send: the price at a
// fixed price, the estimated time by the hour.
func hasEstimate(t Task) bool {
	if t.Billing == BillingHourly {
		return t.EstimateSeconds != nil
	}
	return t.PriceCents != nil
}

// estimateNow is the estimate the task makes as it is now.
func estimateNow(t Task) Estimate {
	e := Estimate{Billing: t.Billing, Seconds: t.EstimateSeconds, Currency: t.Currency}
	switch {
	case t.Billing == BillingFixed:
		e.Cents = t.PriceCents
	case t.EstimateSeconds != nil && t.HourlyRateCents != nil:
		cents := HourlyCents(*t.EstimateSeconds, *t.HourlyRateCents)
		e.Cents = &cents
	}
	return e
}

// HourlyCents is what a time comes to at an hourly rate, by the whole
// minute, as the database bills tracked time.
func HourlyCents(seconds, rateCents int64) int64 {
	minutes := (seconds + 30) / 60
	return (minutes*rateCents + 30) / 60
}

// SetEstimateSeconds sets the task's estimated time; nil clears it.
func (r *Repository) SetEstimateSeconds(ctx context.Context, id string, seconds *int64) error {
	if _, err := r.db.Exec(ctx, `UPDATE tasks SET estimate_seconds = $2 WHERE id = $1`, id, seconds); err != nil {
		return fmt.Errorf("set task estimate: %w", err)
	}
	return nil
}

// SendEstimate stores the estimate sent for the task, in place of any sent
// before: the client's review starts over.
func (r *Repository) SendEstimate(ctx context.Context, taskID, sentBy string, e Estimate) error {
	if _, err := r.db.Exec(ctx, `INSERT INTO task_estimates (task_id, billing, seconds, cents, currency, sent_by)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (task_id) DO UPDATE SET billing = EXCLUDED.billing, seconds = EXCLUDED.seconds,
			cents = EXCLUDED.cents, currency = EXCLUDED.currency, sent_by = EXCLUDED.sent_by, sent_at = now(),
			review = NULL, note = '', reviewed_by = NULL, reviewed_at = NULL`,
		taskID, e.Billing, e.Seconds, e.Cents, e.Currency, sentBy); err != nil {
		return fmt.Errorf("send estimate: %w", err)
	}
	return nil
}

// DeleteEstimate takes back the task's estimate.
func (r *Repository) DeleteEstimate(ctx context.Context, taskID string) error {
	if _, err := r.db.Exec(ctx, `DELETE FROM task_estimates WHERE task_id = $1`, taskID); err != nil {
		return fmt.Errorf("delete estimate: %w", err)
	}
	return nil
}

// ReviewEstimate stores the client's answer to the task's estimate.
func (r *Repository) ReviewEstimate(ctx context.Context, taskID, reviewerID, review, note string, at time.Time) error {
	var got string
	err := r.db.QueryRow(ctx, `UPDATE task_estimates SET review = $3, note = $4, reviewed_by = $2, reviewed_at = $5
		WHERE task_id = $1 RETURNING task_id`, taskID, reviewerID, review, note, at).Scan(&got)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNoEstimate
	}
	if err != nil {
		return fmt.Errorf("review estimate: %w", err)
	}
	return nil
}

// SetEstimateSeconds sets how long the freelancer expects the task to take;
// nil clears it. A sent estimate it changes goes out of date.
func (s *Service) SetEstimateSeconds(ctx context.Context, userID, id string, seconds *int64) (Task, error) {
	t, err := s.repo.Get(ctx, userID, id)
	if err != nil {
		return Task{}, err
	}
	if t.Archived() {
		return Task{}, ErrArchived
	}
	if !CanEstimate(t, userID) {
		return Task{}, ErrEstimateNotAllowed
	}
	if samePrice(t.EstimateSeconds, seconds) {
		return t, nil
	}
	if err := s.repo.SetEstimateSeconds(ctx, id, seconds); err != nil {
		return Task{}, err
	}
	s.Record(ctx, t.ID, userID, EventEstimate, change(centsOrNil(t.EstimateSeconds), centsOrNil(seconds)))
	return s.repo.Get(ctx, userID, id)
}

// SendEstimate sends the task's estimate, as the task is now, to its client
// to approve; the client is told. Sending it again after they asked for
// changes, or after the task changed, starts their review over.
func (s *Service) SendEstimate(ctx context.Context, userID, id string) (Task, error) {
	t, err := s.repo.Get(ctx, userID, id)
	if err != nil {
		return Task{}, err
	}
	if t.Archived() {
		return Task{}, ErrArchived
	}
	switch {
	case !CanEstimate(t, userID):
		return Task{}, ErrEstimateNotAllowed
	case t.Client == nil:
		return Task{}, ErrNoClient
	case !hasEstimate(t):
		return Task{}, ErrNothingToEstimate
	case !CanSendEstimate(t, userID):
		return Task{}, ErrEstimateSent
	}
	e := estimateNow(t)
	if err := s.repo.SendEstimate(ctx, t.ID, userID, e); err != nil {
		return Task{}, err
	}
	data := map[string]any{"billing": e.Billing, "seconds": centsOrNil(e.Seconds), "cents": centsOrNil(e.Cents), "currency": e.Currency}
	s.Record(ctx, t.ID, userID, EventEstimateSent, data)
	// Only the client sees the price, so only they are asked.
	s.TellPeople(ctx, userID, t, []string{t.Client.ID}, notifications.KindEstimateSent, "", true)
	return s.repo.Get(ctx, userID, id)
}

// WithdrawEstimate takes back an estimate the client hasn't answered.
func (s *Service) WithdrawEstimate(ctx context.Context, userID, id string) (Task, error) {
	t, err := s.repo.Get(ctx, userID, id)
	if err != nil {
		return Task{}, err
	}
	if t.Archived() {
		return Task{}, ErrArchived
	}
	if !CanEstimate(t, userID) {
		return Task{}, ErrEstimateNotAllowed
	}
	if !CanWithdrawEstimate(t, userID) {
		return Task{}, ErrNoEstimate
	}
	if err := s.repo.DeleteEstimate(ctx, t.ID); err != nil {
		return Task{}, err
	}
	s.Record(ctx, t.ID, userID, EventEstimateWithdrawn, nil)
	return s.repo.Get(ctx, userID, id)
}

// ReviewEstimate records what the client made of the task's estimate:
// approved, or changes asked for, saying what in note. They may change
// their mind while the estimate stands. The freelancer is told.
func (s *Service) ReviewEstimate(ctx context.Context, userID, id, review, note string) (Task, error) {
	t, err := s.repo.Get(ctx, userID, id)
	if err != nil {
		return Task{}, err
	}
	if t.Archived() {
		return Task{}, ErrArchived
	}
	switch {
	case t.Client == nil || t.Client.ID != userID:
		return Task{}, ErrNotEstimateReviewer
	case t.Estimate == nil:
		return Task{}, ErrNoEstimate
	case EstimateOutdated(t):
		return Task{}, ErrEstimateOutdated
	}
	if review == EstimateApproved {
		note = ""
	}
	if t.Estimate.Review == review && t.Estimate.Note == note {
		return t, nil
	}
	if err := s.repo.ReviewEstimate(ctx, t.ID, userID, review, note, time.Now()); err != nil {
		return Task{}, err
	}
	s.Record(ctx, t.ID, userID, EventEstimateReviewed, map[string]any{"review": review})
	kind := notifications.KindEstimateApproved
	if review == EstimateChangesRequested {
		kind = notifications.KindEstimateChangesRequested
	}
	if t.Freelancer.ID != "" {
		s.TellPeople(ctx, userID, t, []string{t.Freelancer.ID}, kind, excerpt(note), true)
	}
	return s.repo.Get(ctx, userID, id)
}

// excerpt is the start of what the client wrote, on one line, for a
// notification.
func excerpt(note string) string {
	s := strings.Join(strings.Fields(note), " ")
	if utf8.RuneCountInString(s) <= excerptLength {
		return s
	}
	return strings.TrimSpace(string([]rune(s)[:excerptLength-1])) + "…"
}

// excerptLength is how many characters of a note a notification quotes, as
// of a comment.
const excerptLength = 120
