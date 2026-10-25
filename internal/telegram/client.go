package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// Client calls the Telegram Bot API: the few methods the bot needs.
type Client struct {
	// base is the API URL with the bot token: never log it.
	base string
	http *http.Client
}

// NewClient creates a Client for the bot with this token. apiURL is
// https://api.telegram.org, or a fake one in tests.
func NewClient(apiURL, token string) *Client {
	// Long polls hold the request for pollTimeout; leave room on top.
	return &Client{base: apiURL + "/bot" + token, http: &http.Client{Timeout: pollTimeout + 15*time.Second}}
}

// APIError is an error the Bot API answered with.
type APIError struct {
	Code        int
	Description string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("telegram: %d %s", e.Code, e.Description)
}

// IsBlocked reports whether the user blocked the bot or deleted the chat:
// nothing can be sent there any more.
func IsBlocked(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Code == http.StatusForbidden
}

// User is a Telegram account.
type User struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	// LanguageCode is the language their Telegram is in, e.g. "uk".
	LanguageCode string `json:"language_code"`
}

// Chat is where a message was sent.
type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

// Message is an incoming message, or one the bot sent.
type Message struct {
	MessageID int64  `json:"message_id"`
	Text      string `json:"text"`
	// Caption is the text under a photo or a file.
	Caption string `json:"caption"`
	Chat    Chat   `json:"chat"`
	From    *User  `json:"from"`
	// ReplyTo is the message this one answers.
	ReplyTo *Message `json:"reply_to_message"`
	// ForwardOrigin is set on a forwarded message: who wrote it first.
	ForwardOrigin *ForwardOrigin `json:"forward_origin"`
	// ReplyMarkup holds the buttons under one of the bot's messages.
	ReplyMarkup *struct {
		InlineKeyboard [][]Button `json:"inline_keyboard"`
	} `json:"reply_markup"`
}

// Buttons are the buttons under the message, nil for none.
func (m Message) Buttons() Keyboard {
	if m.ReplyMarkup == nil {
		return nil
	}
	return Keyboard(m.ReplyMarkup.InlineKeyboard)
}

// ForwardOrigin is who first wrote a forwarded message: a user, a user who
// hides their account, a chat or a channel.
type ForwardOrigin struct {
	Type           string `json:"type"`
	SenderUser     *User  `json:"sender_user"`
	SenderUserName string `json:"sender_user_name"`
	Chat           *struct {
		Title string `json:"title"`
	} `json:"chat"`
}

// Name is how the origin calls itself, "" when it doesn't say.
func (o ForwardOrigin) Name() string {
	switch {
	case o.SenderUser != nil:
		if o.SenderUser.Username != "" {
			return "@" + o.SenderUser.Username
		}
		return o.SenderUser.FirstName
	case o.SenderUserName != "":
		return o.SenderUserName
	case o.Chat != nil:
		return o.Chat.Title
	}
	return ""
}

// CallbackQuery is a press on a button under one of the bot's messages.
type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}

// Update is something that happened to the bot: a message, or a button pressed.
type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message"`
	CallbackQuery *CallbackQuery `json:"callback_query"`
}

// Button under a message: it opens a URL, or sends the bot its data
// (at most 64 bytes) when pressed.
type Button struct {
	Text         string `json:"text"`
	URL          string `json:"url,omitempty"`
	CallbackData string `json:"callback_data,omitempty"`
}

// Keyboard is rows of buttons under a message; nil for none.
type Keyboard [][]Button

// markup is a keyboard as reply_markup; an empty one clears the buttons.
func (k Keyboard) markup() map[string]any {
	rows := [][]Button(k)
	if rows == nil {
		rows = [][]Button{}
	}
	return map[string]any{"inline_keyboard": rows}
}

// GetMe returns the bot's own account.
func (c *Client) GetMe(ctx context.Context) (User, error) {
	var me User
	err := c.call(ctx, "getMe", struct{}{}, &me)
	return me, err
}

// GetUpdates waits up to timeout for updates after offset.
func (c *Client) GetUpdates(ctx context.Context, offset int64, timeout time.Duration) ([]Update, error) {
	var updates []Update
	err := c.call(ctx, "getUpdates", map[string]any{
		"offset":          offset,
		"timeout":         int(timeout.Seconds()),
		"allowed_updates": []string{"message", "callback_query"},
	}, &updates)
	return updates, err
}

// SendMessage sends an HTML-formatted message, with a button under it when
// one is given.
func (c *Client) SendMessage(ctx context.Context, chatID int64, html string, button *Button) error {
	var k Keyboard
	if button != nil {
		k = Keyboard{{*button}}
	}
	_, err := c.Send(ctx, chatID, html, k)
	return err
}

// Send sends an HTML-formatted message with buttons under it, and returns
// its ID.
func (c *Client) Send(ctx context.Context, chatID int64, html string, k Keyboard) (int64, error) {
	params := map[string]any{
		"chat_id":                  chatID,
		"text":                     html,
		"parse_mode":               "HTML",
		"disable_web_page_preview": true,
	}
	if k != nil {
		params["reply_markup"] = k.markup()
	}
	var sent Message
	err := c.call(ctx, "sendMessage", params, &sent)
	return sent.MessageID, err
}

// EditText rewrites one of the bot's messages and its buttons.
func (c *Client) EditText(ctx context.Context, chatID, messageID int64, html string, k Keyboard) error {
	return c.call(ctx, "editMessageText", map[string]any{
		"chat_id":                  chatID,
		"message_id":               messageID,
		"text":                     html,
		"parse_mode":               "HTML",
		"disable_web_page_preview": true,
		"reply_markup":             k.markup(),
	}, nil)
}

// EditButtons replaces the buttons under one of the bot's messages.
func (c *Client) EditButtons(ctx context.Context, chatID, messageID int64, k Keyboard) error {
	return c.call(ctx, "editMessageReplyMarkup", map[string]any{
		"chat_id":      chatID,
		"message_id":   messageID,
		"reply_markup": k.markup(),
	}, nil)
}

// Command is one of the bot's commands, for the menu Telegram shows.
type Command struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

// SetCommands sets the bot's command menu, for Telegram apps in a language
// ("" for the rest).
func (c *Client) SetCommands(ctx context.Context, commands []Command, language string) error {
	params := map[string]any{"commands": commands}
	if language != "" {
		params["language_code"] = language
	}
	return c.call(ctx, "setMyCommands", params, nil)
}

// AnswerCallback ends a button's loading state, with a short note shown
// over the chat ("" for none).
func (c *Client) AnswerCallback(ctx context.Context, id, text string) error {
	params := map[string]any{"callback_query_id": id}
	if text != "" {
		params["text"] = text
	}
	return c.call(ctx, "answerCallbackQuery", params, nil)
}

func (c *Client) call(ctx context.Context, method string, params, result any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("telegram %s: %w", method, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/"+method, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telegram %s: build request", method)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		// A *url.Error quotes the URL, and the URL holds the token.
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return fmt.Errorf("telegram %s: %w", method, err)
	}
	defer resp.Body.Close()

	var out struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		ErrorCode   int             `json:"error_code"`
		Description string          `json:"description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return fmt.Errorf("telegram %s: decode response (HTTP %d): %w", method, resp.StatusCode, err)
	}
	if !out.OK {
		return &APIError{Code: out.ErrorCode, Description: out.Description}
	}
	if result == nil {
		return nil
	}
	if err := json.Unmarshal(out.Result, result); err != nil {
		return fmt.Errorf("telegram %s: decode result: %w", method, err)
	}
	return nil
}
