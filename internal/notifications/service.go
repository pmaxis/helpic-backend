package notifications

import (
	"context"
	"log/slog"
	"slices"
)

// CreateHook hears of every notification once it is stored, e.g. to send it
// on to another channel. It must not block: it runs in the request that
// caused the notification.
type CreateHook func(n Notification)

// Service stores notifications and serves them to their recipients.
type Service struct {
	repo     *Repository
	onCreate []CreateHook
}

// OnCreate registers a hook called after each notification is stored.
func (s *Service) OnCreate(h CreateHook) {
	s.onCreate = append(s.onCreate, h)
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
	stored, err := s.repo.Create(ctx, n)
	if err != nil {
		slog.Error("failed to store notification", "kind", n.Kind, "error", err)
		return
	}
	for _, h := range s.onCreate {
		h(stored)
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

// AppKinds returns the kinds the bell shows the user.
func (s *Service) AppKinds(ctx context.Context, userID string) ([]string, error) {
	return s.repo.AppKinds(ctx, userID)
}

// SetAppKinds chooses which kinds the bell shows the user from now on; the
// others still go to the channels that want them.
func (s *Service) SetAppKinds(ctx context.Context, userID string, kinds []string) ([]string, error) {
	clean, err := cleanKinds(kinds)
	if err != nil {
		return nil, err
	}
	return clean, s.repo.SetAppKinds(ctx, userID, clean)
}

// cleanKinds drops repeats, or returns ErrUnknownKind for a kind that doesn't exist.
func cleanKinds(kinds []string) ([]string, error) {
	clean := make([]string, 0, len(kinds))
	for _, k := range kinds {
		if !slices.Contains(Kinds, k) {
			return nil, ErrUnknownKind
		}
		if !slices.Contains(clean, k) {
			clean = append(clean, k)
		}
	}
	return clean, nil
}

// Noop drops notifications; for code paths and tests that don't need them.
type Noop struct{}

// Notify does nothing.
func (Noop) Notify(context.Context, Notification) {}
