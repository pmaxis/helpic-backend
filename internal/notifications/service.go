package notifications

import (
	"context"
	"log/slog"
)

// Service stores notifications and serves them to their recipients.
type Service struct {
	repo *Repository
}

// NewService creates a notifications Service.
func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

// Notify records a notification. Nothing is sent when there is no recipient
// (personal work) or the recipient is the actor. A failure is logged, never
// returned: notifying must not undo the change it reports.
func (s *Service) Notify(ctx context.Context, n Notification) {
	if n.UserID == "" || n.UserID == n.ActorID {
		return
	}
	if err := s.repo.Create(ctx, n); err != nil {
		slog.Error("failed to store notification", "kind", n.Kind, "error", err)
	}
}

// listLimit caps how many notifications one listing returns.
const listLimit = 50

// List returns the user's latest notifications and how many are unread.
func (s *Service) List(ctx context.Context, userID string) ([]Notification, int, error) {
	list, err := s.repo.List(ctx, userID, listLimit)
	if err != nil {
		return nil, 0, err
	}
	unread, err := s.repo.UnreadCount(ctx, userID)
	if err != nil {
		return nil, 0, err
	}
	return list, unread, nil
}

// UnreadCount returns how many notifications the user hasn't read.
func (s *Service) UnreadCount(ctx context.Context, userID string) (int, error) {
	return s.repo.UnreadCount(ctx, userID)
}

// MarkRead marks one notification read.
func (s *Service) MarkRead(ctx context.Context, userID, id string) error {
	return s.repo.MarkRead(ctx, userID, id)
}

// MarkAllRead marks all the user's notifications read.
func (s *Service) MarkAllRead(ctx context.Context, userID string) error {
	return s.repo.MarkAllRead(ctx, userID)
}

// Noop drops notifications; for code paths and tests that don't need them.
type Noop struct{}

// Notify does nothing.
func (Noop) Notify(context.Context, Notification) {}
