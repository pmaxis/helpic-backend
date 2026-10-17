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

// Message is an incoming message.
type Message struct {
	Text string `json:"text"`
	Chat Chat   `json:"chat"`
	From *User  `json:"from"`
}

// Update is something that happened to the bot; only messages matter here.
type Update struct {
	UpdateID int64    `json:"update_id"`
	Message  *Message `json:"message"`
}

// Button opens a URL under a message.
type Button struct {
	Text string `json:"text"`
	URL  string `json:"url"`
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
		"allowed_updates": []string{"message"},
	}, &updates)
	return updates, err
}

// SendMessage sends an HTML-formatted message, with a button under it when
// one is given.
func (c *Client) SendMessage(ctx context.Context, chatID int64, html string, button *Button) error {
	params := map[string]any{
		"chat_id":                  chatID,
		"text":                     html,
		"parse_mode":               "HTML",
		"disable_web_page_preview": true,
	}
	if button != nil {
		params["reply_markup"] = map[string]any{"inline_keyboard": [][]Button{{*button}}}
	}
	return c.call(ctx, "sendMessage", params, nil)
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
