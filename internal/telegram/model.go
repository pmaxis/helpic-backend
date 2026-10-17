// Package telegram sends a user's notifications to their Telegram chat with
// the bot. The user connects from the app: it hands out a one-time link to
// the bot, and opening it there (/start <code>) ties the chat to the account.
// They pick which kinds of notification go to Telegram; /stop in the chat or
// disconnecting in the app ends it. Without a bot token the feature is off.
package telegram

import (
	"errors"
	"slices"
	"time"

	"github.com/pmaxis/helpic/internal/notifications"
)

var (
	// ErrDisabled is returned when the server has no bot.
	ErrDisabled = errors.New("telegram is not configured")

	// ErrNotReady is returned while the bot hasn't introduced itself to
	// Telegram yet, so there is no username to link to.
	ErrNotReady = errors.New("telegram bot is not ready")

	// ErrNotConnected is returned when the user has no chat connected.
	ErrNotConnected = errors.New("telegram is not connected")

	// ErrUnknownKind is returned for a notification kind that doesn't exist.
	ErrUnknownKind = errors.New("unknown notification kind")
)

// codeTTL is how long a connect link works.
const codeTTL = 15 * time.Minute

// Kinds are the notification kinds that can go to Telegram.
var Kinds = []string{
	notifications.KindTaskCreated,
	notifications.KindTaskStatus,
	notifications.KindTaskUpdated,
	notifications.KindTaskDeleted,
	notifications.KindCommentAdded,
	notifications.KindAttachmentAdded,
	notifications.KindProjectCreated,
	notifications.KindReportSent,
	notifications.KindReportPaid,
	notifications.KindClientJoined,
	notifications.KindMemberJoined,
}

// DefaultKinds are the important ones, on when a chat is connected: new
// work, its progress, the discussion, money and new people. Edits, deleted
// tasks, files and new projects stay in the app.
var DefaultKinds = []string{
	notifications.KindTaskCreated,
	notifications.KindTaskStatus,
	notifications.KindCommentAdded,
	notifications.KindReportSent,
	notifications.KindReportPaid,
	notifications.KindClientJoined,
	notifications.KindMemberJoined,
}

// Link is a user's connected chat.
type Link struct {
	UserID   string
	ChatID   int64
	Username string
	Kinds    []string
	LinkedAt time.Time
}

// Wants reports whether the user wants this kind of notification in Telegram.
func (l Link) Wants(kind string) bool {
	return slices.Contains(l.Kinds, kind)
}
