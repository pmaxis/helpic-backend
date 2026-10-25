package telegram

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/pmaxis/helpic/internal/i18n"
	"github.com/pmaxis/helpic/internal/notifications"
)

const (
	// pollTimeout is how long one getUpdates call waits for messages.
	pollTimeout = 30 * time.Second
	// pollLock is the advisory lock only one API copy holds while it reads
	// the bot's messages. Two copies would steal updates from each other.
	pollLock int64 = 740021
	// retryDelay is the pause after the Bot API fails.
	retryDelay = 5 * time.Second
	// queueSize bounds the notifications waiting to be sent; past it they are
	// dropped (they are in the app anyway).
	queueSize = 256
	// sendTimeout bounds one sendMessage.
	sendTimeout = 10 * time.Second
)

// Status is what the settings page shows.
type Status struct {
	// Enabled is false when the server has no bot.
	Enabled bool
	// Link is set when the user's chat is connected.
	Link *Link
}

// Service connects chats and sends notifications to them.
type Service struct {
	repo *Repository
	// bot is nil when there is no bot token.
	bot *Client
	// appURL is the frontend, for "Open in Helpic" buttons.
	appURL string
	// username is the bot's, known once it has called getMe.
	username atomic.Value
	queue    chan notifications.Notification
	// act is how the bot acts for the user; nil while it only tells.
	act *Actions
}

// NewService creates a telegram Service; bot is nil to turn Telegram off.
func NewService(repo *Repository, bot *Client, appURL string) *Service {
	s := &Service{repo: repo, bot: bot, appURL: strings.TrimRight(appURL, "/"), queue: make(chan notifications.Notification, queueSize)}
	s.username.Store("")
	return s
}

func (s *Service) botUsername() string {
	return s.username.Load().(string)
}

// Status returns whether Telegram is on and the user's connected chat.
func (s *Service) Status(ctx context.Context, userID string) (Status, error) {
	if s.bot == nil {
		return Status{}, nil
	}
	l, err := s.repo.Get(ctx, userID)
	if errors.Is(err, ErrNotConnected) {
		return Status{Enabled: true}, nil
	}
	if err != nil {
		return Status{}, err
	}
	return Status{Enabled: true, Link: &l}, nil
}

// ConnectURL returns a one-time t.me link that connects the chat it is opened
// in to the user, and when it stops working.
func (s *Service) ConnectURL(ctx context.Context, userID string) (string, time.Time, error) {
	if s.bot == nil {
		return "", time.Time{}, ErrDisabled
	}
	bot := s.botUsername()
	if bot == "" {
		return "", time.Time{}, ErrNotReady
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", time.Time{}, fmt.Errorf("generate telegram code: %w", err)
	}
	// /start takes up to 64 characters of A-Z, a-z, 0-9, _ and -.
	code := base64.RawURLEncoding.EncodeToString(b)
	expires := time.Now().Add(codeTTL)
	if err := s.repo.NewCode(ctx, userID, hashCode(code), expires); err != nil {
		return "", time.Time{}, err
	}
	return "https://t.me/" + bot + "?start=" + code, expires, nil
}

// SetKinds chooses which notification kinds go to the user's chat.
func (s *Service) SetKinds(ctx context.Context, userID string, kinds []string) (Link, error) {
	clean := make([]string, 0, len(kinds))
	for _, k := range kinds {
		if !slices.Contains(Kinds, k) {
			return Link{}, ErrUnknownKind
		}
		if !slices.Contains(clean, k) {
			clean = append(clean, k)
		}
	}
	return s.repo.SetKinds(ctx, userID, clean)
}

// Disconnect stops sending to the user's chat and says so there.
func (s *Service) Disconnect(ctx context.Context, userID string) error {
	l, err := s.repo.Disconnect(ctx, userID)
	if err != nil {
		return err
	}
	if s.bot != nil {
		s.reply(ctx, l.ChatID, i18n.T(l.Locale, "You disconnected Telegram in Helpic. No more notifications will come here."))
	}
	return nil
}

// Cleanup removes connect links nobody opened in time, and forgets old
// messages and drafts.
func (s *Service) Cleanup(ctx context.Context) error {
	return errors.Join(s.repo.DeleteExpiredCodes(ctx), s.repo.DeleteStale(ctx, messageTTL, draftTTL))
}

// Enqueue takes a stored notification to send to its recipient's chat, if
// they want it there. It never blocks: when the queue is full the
// notification is dropped (it is in the app anyway). Register it with
// notifications.Service.OnCreate.
func (s *Service) Enqueue(n notifications.Notification) {
	if s.bot == nil || !slices.Contains(Kinds, n.Kind) {
		return
	}
	select {
	case s.queue <- n:
	default:
		slog.Warn("telegram queue full, notification dropped", "kind", n.Kind)
	}
}

// Run serves the bot until ctx ends: it reads the chats' messages and sends
// queued notifications. It returns at once without a bot.
func (s *Service) Run(ctx context.Context) {
	if s.bot == nil {
		return
	}
	go s.send(ctx)
	s.poll(ctx)
}

// send delivers queued notifications one at a time, in order.
func (s *Service) send(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case n := <-s.queue:
			s.deliver(ctx, n)
		}
	}
}

func (s *Service) deliver(ctx context.Context, n notifications.Notification) {
	l, err := s.repo.Get(ctx, n.UserID)
	if errors.Is(err, ErrNotConnected) {
		return
	}
	if err != nil {
		slog.Error("telegram: load chat", "error", err)
		return
	}
	if !l.Wants(n.Kind) {
		return
	}
	text, button := s.message(n, l.Locale)
	k := keyboard(s.actionButtons(ctx, l, n), button)
	sendCtx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	id, err := s.bot.Send(sendCtx, l.ChatID, text, k)
	if err == nil {
		// A reply to it comments on its task; its buttons act for this user.
		if err := s.repo.Remember(ctx, l.ChatID, id, l.UserID, n.TaskID); err != nil {
			slog.Error("telegram: remember message", "error", err)
		}
		return
	}
	if IsBlocked(err) {
		// The user blocked the bot: stop trying.
		if _, err := s.repo.DisconnectChat(ctx, l.ChatID); err != nil {
			slog.Error("telegram: disconnect blocked chat", "error", err)
		}
		return
	}
	if err != nil {
		slog.Error("telegram: send notification", "kind", n.Kind, "error", err)
	}
}

// poll is the one reader of the bot's messages. Another copy waits.
func (s *Service) poll(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		release, ok, err := s.repo.TryLock(ctx, pollLock)
		if err != nil {
			slog.Error("telegram: lock", "error", err)
			if !sleep(ctx, retryDelay) {
				return
			}
			continue
		}
		if !ok {
			if !sleep(ctx, retryDelay) {
				return
			}
			continue
		}
		s.readUpdates(ctx)
		release()
		return
	}
}

// readUpdates introduces the bot to Telegram, then reads its messages until
// ctx ends. The caller holds the poll lock.
func (s *Service) readUpdates(ctx context.Context) {
	for s.botUsername() == "" {
		me, err := s.bot.GetMe(ctx)
		if err == nil {
			s.username.Store(me.Username)
			slog.Info("telegram bot ready", "username", me.Username)
			s.setCommands(ctx)
			break
		}
		slog.Error("telegram: getMe", "error", err)
		if !sleep(ctx, retryDelay) {
			return
		}
	}

	var offset int64
	for {
		updates, err := s.bot.GetUpdates(ctx, offset, pollTimeout)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			slog.Error("telegram: getUpdates", "error", err)
			if !sleep(ctx, retryDelay) {
				return
			}
			continue
		}
		for _, u := range updates {
			offset = u.UpdateID + 1
			switch {
			case u.Message != nil:
				s.handle(ctx, *u.Message)
			case u.CallbackQuery != nil:
				s.press(ctx, *u.CallbackQuery)
			}
		}
	}
}

// handle answers a message to the bot: /start with a code connects the chat,
// /stop disconnects it, /timer shows the running timer, and anything else
// acts (see respond). Only private chats count. Until the chat is connected
// it answers in the language the sender's Telegram is in.
func (s *Service) handle(ctx context.Context, m Message) {
	if m.Chat.Type != "private" {
		return
	}
	locale := i18n.Default
	if m.From != nil {
		locale = i18n.Normalize(m.From.LanguageCode)
	}
	t := func(text string) string { return i18n.T(locale, text) }
	command, arg, _ := strings.Cut(strings.TrimSpace(m.Text), " ")
	switch command {
	case "/start":
		if arg == "" {
			s.reply(ctx, m.Chat.ID, t("Hi! To use Helpic here, open Helpic → Settings → Integrations and press <b>Connect Telegram</b>."))
			return
		}
		s.connect(ctx, m, strings.TrimSpace(arg), locale)
	case "/stop":
		n, err := s.repo.DisconnectChat(ctx, m.Chat.ID)
		if err != nil {
			slog.Error("telegram: disconnect chat", "error", err)
			s.reply(ctx, m.Chat.ID, t("Something went wrong. Please try again."))
			return
		}
		if n == 0 {
			s.reply(ctx, m.Chat.ID, t("This chat isn't connected to Helpic."))
			return
		}
		s.reply(ctx, m.Chat.ID, t("Disconnected. No more Helpic notifications will come here."))
	default:
		if s.act == nil {
			s.reply(ctx, m.Chat.ID, t("I only send Helpic notifications. Send /stop to turn them off."))
			return
		}
		var replyTo int64
		if m.ReplyTo != nil {
			replyTo = m.ReplyTo.MessageID
		}
		l, err := s.repo.ByChat(ctx, m.Chat.ID, replyTo)
		if errors.Is(err, ErrNotConnected) {
			s.reply(ctx, m.Chat.ID, t("Hi! To use Helpic here, open Helpic → Settings → Integrations and press <b>Connect Telegram</b>."))
			return
		}
		if err != nil {
			slog.Error("telegram: load chat", "error", err)
			s.reply(ctx, m.Chat.ID, t("Something went wrong. Please try again."))
			return
		}
		switch command {
		case "/timer":
			s.showTimer(ctx, l, m.Chat.ID)
		case "/help":
			s.reply(ctx, m.Chat.ID, translate(l.Locale)(helpText))
		default:
			if strings.HasPrefix(command, "/") {
				s.reply(ctx, m.Chat.ID, translate(l.Locale)(helpText))
				return
			}
			s.respond(ctx, l, m)
		}
	}
}

// helpText says what the bot does.
const helpText = "Here's what I can do:\n\n" +
	"• <b>Reply</b> to a notification about a task, and your reply becomes a comment on it.\n" +
	"• <b>Send or forward</b> me a message, and it becomes a task: choose who it's with.\n" +
	"• Buttons under notifications start the timer, send done work back, or mark a report paid.\n" +
	"• /timer shows the running timer, /stop turns notifications off."

// connect ties the chat to the account the code was made for. Until then it
// speaks the sender's language; once connected, the account's.
func (s *Service) connect(ctx context.Context, m Message, code, locale string) {
	userID, name, userLocale, ok, err := s.repo.TakeCode(ctx, hashCode(code))
	if err != nil {
		slog.Error("telegram: take code", "error", err)
		s.reply(ctx, m.Chat.ID, i18n.T(locale, "Something went wrong. Please try again."))
		return
	}
	if !ok {
		s.reply(ctx, m.Chat.ID, i18n.T(locale, "This link has expired or was already used. Get a new one in Helpic → Settings → Integrations."))
		return
	}
	locale = userLocale
	username := ""
	if m.From != nil {
		username = m.From.FirstName
		if m.From.Username != "" {
			username = "@" + m.From.Username
		}
	}
	if err := s.repo.Connect(ctx, userID, m.Chat.ID, username, DefaultKinds); err != nil {
		slog.Error("telegram: connect", "error", err)
		s.reply(ctx, m.Chat.ID, i18n.T(locale, "Something went wrong. Please try again."))
		return
	}
	text := i18n.T(locale,
		"✅ Connected to Helpic as <b>%s</b>.\n\nImportant notifications will come here. Choose which in Settings → Notifications, or send /stop to turn them off.",
		html.EscapeString(name))
	if s.act != nil {
		text += "\n\n" + i18n.T(locale, "You can act here too: reply to a notification to comment, or send me a message to make a task. /help says more.")
	}
	s.reply(ctx, m.Chat.ID, text)
}

// setCommands puts the bot's commands in Telegram's menu, in English and in
// Ukrainian; a failure is logged.
func (s *Service) setCommands(ctx context.Context) {
	for _, locale := range []string{i18n.Default, i18n.UK} {
		t := translate(locale)
		commands := []Command{
			{Command: "timer", Description: t("The running timer")},
			{Command: "help", Description: t("What I can do")},
			{Command: "stop", Description: t("Turn notifications off")},
		}
		language := ""
		if locale == i18n.UK {
			language = i18n.UK
		}
		if err := s.bot.SetCommands(ctx, commands, language); err != nil {
			slog.Error("telegram: set commands", "error", err)
		}
	}
}

// reply sends a message to a chat; a failure is logged.
func (s *Service) reply(ctx context.Context, chatID int64, text string) {
	sendCtx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	if err := s.bot.SendMessage(sendCtx, chatID, text, nil); err != nil {
		slog.Error("telegram: reply", "error", err)
	}
}

func hashCode(code string) []byte {
	sum := sha256.Sum256([]byte(code))
	return sum[:]
}

// sleep waits d, or less if ctx ends; it reports whether ctx is still alive.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
