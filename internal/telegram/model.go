// Package telegram sends a user's notifications to their Telegram chat with
// the bot. The user connects from the app: it hands out a one-time link to
// the bot, and opening it there (/start <code>) ties the chat to the account.
// They pick which kinds of notification go to Telegram; /stop in the chat or
// disconnecting in the app ends it. Without a bot token the feature is off.
//
// The bot acts too (see actions.go): a reply to a notification comments on
// its task, buttons under one start the timer, send done work back or mark a
// report paid, /timer shows the running timer, and any other message, sent
// or forwarded, becomes a task once its sender picks who it's with.
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

// Kinds are the notification kinds that can go to Telegram; DefaultKinds
// are on when a chat is connected.
var (
	Kinds        = notifications.Kinds
	DefaultKinds = notifications.DefaultKinds
)

// Link is a user's connected chat.
type Link struct {
	UserID   string
	ChatID   int64
	Username string
	Kinds    []string
	LinkedAt time.Time
	// Locale is the user's language, for what the bot says to them.
	Locale string
	// Role is the user's: "freelancer" or "client".
	Role string
}

// Target is someone a task from the bot can be set with.
type Target struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Draft is a task to be, from a message sent to the bot, until its sender
// picks who it's with — personal, or one of Targets — and then, when that
// pair has any, a project from Projects or none.
type Draft struct {
	Title       string
	Description string
	Targets     []Target
	// Choice is who they picked: "p" for personal, or a target's place; ""
	// until they do.
	Choice   string
	Projects []Target
}

// Wants reports whether the user wants this kind of notification in Telegram.
func (l Link) Wants(kind string) bool {
	return slices.Contains(l.Kinds, kind)
}
