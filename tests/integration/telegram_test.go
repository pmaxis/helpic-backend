package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pmaxis/helpic/internal/config"
)

// fakeBot is the Telegram Bot API, played by a test server: it hands the bot
// what users "send" and records what the bot sends back.
type fakeBot struct {
	mu      sync.Mutex
	nextID  int64
	pending []map[string]any
	sent    []sentMessage
	blocked map[int64]bool
	// nextMessage numbers the messages the bot sends; answers are the notes
	// it showed on button presses.
	nextMessage int64
	answers     []string
}

type sentMessage struct {
	ID     int64
	ChatID int64
	Text   string
	// Buttons are the message's buttons as they are now: rows of text and
	// callback_data (or url).
	Buttons [][]fakeButton
}

type fakeButton struct {
	Text string `json:"text"`
	Data string `json:"callback_data"`
	URL  string `json:"url"`
}

func buttonsOf(params map[string]any) [][]fakeButton {
	var markup struct {
		InlineKeyboard [][]fakeButton `json:"inline_keyboard"`
	}
	raw, _ := json.Marshal(params["reply_markup"])
	_ = json.Unmarshal(raw, &markup)
	return markup.InlineKeyboard
}

func newFakeBot(t *testing.T) (*fakeBot, *httptest.Server) {
	t.Helper()
	b := &fakeBot{blocked: map[int64]bool{}}
	srv := httptest.NewServer(b)
	t.Cleanup(srv.Close)
	return b, srv
}

func (b *fakeBot) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, "/bottest-token/") {
		http.Error(w, "wrong token", http.StatusNotFound)
		return
	}
	var params map[string]any
	_ = json.NewDecoder(r.Body).Decode(&params)
	reply := func(v any) { _ = json.NewEncoder(w).Encode(v) }

	switch strings.TrimPrefix(r.URL.Path, "/bottest-token/") {
	case "getMe":
		reply(map[string]any{"ok": true, "result": map[string]any{"id": 1, "username": "helpic_test_bot"}})
	case "getUpdates":
		offset := int64(params["offset"].(float64))
		b.mu.Lock()
		var out []map[string]any
		for _, u := range b.pending {
			if int64(u["update_id"].(float64)) >= offset {
				out = append(out, u)
			}
		}
		b.mu.Unlock()
		if len(out) == 0 {
			time.Sleep(20 * time.Millisecond) // a very short long poll
		}
		reply(map[string]any{"ok": true, "result": out})
	case "sendMessage":
		chatID := int64(params["chat_id"].(float64))
		b.mu.Lock()
		defer b.mu.Unlock()
		if b.blocked[chatID] {
			reply(map[string]any{"ok": false, "error_code": 403, "description": "Forbidden: bot was blocked by the user"})
			return
		}
		b.nextMessage++
		b.sent = append(b.sent, sentMessage{ID: b.nextMessage, ChatID: chatID, Text: params["text"].(string), Buttons: buttonsOf(params)})
		reply(map[string]any{"ok": true, "result": map[string]any{"message_id": b.nextMessage, "chat": map[string]any{"id": chatID, "type": "private"}}})
	case "editMessageText", "editMessageReplyMarkup":
		chatID, id := int64(params["chat_id"].(float64)), int64(params["message_id"].(float64))
		b.mu.Lock()
		defer b.mu.Unlock()
		for i, m := range b.sent {
			if m.ChatID == chatID && m.ID == id {
				if text, ok := params["text"].(string); ok {
					b.sent[i].Text = text
				}
				b.sent[i].Buttons = buttonsOf(params)
			}
		}
		reply(map[string]any{"ok": true, "result": true})
	case "answerCallbackQuery":
		b.mu.Lock()
		defer b.mu.Unlock()
		text, _ := params["text"].(string)
		b.answers = append(b.answers, text)
		reply(map[string]any{"ok": true, "result": true})
	case "setMyCommands":
		reply(map[string]any{"ok": true, "result": true})
	default:
		reply(map[string]any{"ok": false, "error_code": 404, "description": "Not Found"})
	}
}

// userSends queues a private message from a Telegram user.
func (b *fakeBot) userSends(chatID int64, username, text string) {
	b.userSendsIn(chatID, username, "", text)
}

// userSendsIn is userSends from a Telegram app in this language ("uk").
func (b *fakeBot) userSendsIn(chatID int64, username, lang, text string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextID++
	// Round-trip through JSON so update_id reads back as float64, like the rest.
	raw, _ := json.Marshal(map[string]any{
		"update_id": b.nextID,
		"message": map[string]any{
			"text": text,
			"chat": map[string]any{"id": chatID, "type": "private"},
			"from": map[string]any{"id": chatID, "username": username, "first_name": "Max", "language_code": lang},
		},
	})
	var u map[string]any
	_ = json.Unmarshal(raw, &u)
	b.pending = append(b.pending, u)
}

// queue adds an update, round-tripped through JSON so numbers read back as
// float64, like the rest.
func (b *fakeBot) queue(update map[string]any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextID++
	update["update_id"] = b.nextID
	raw, _ := json.Marshal(update)
	var u map[string]any
	_ = json.Unmarshal(raw, &u)
	b.pending = append(b.pending, u)
}

func privateMessage(chatID int64, text string) map[string]any {
	return map[string]any{
		"message_id": 100000 + chatID,
		"text":       text,
		"chat":       map[string]any{"id": chatID, "type": "private"},
		"from":       map[string]any{"id": chatID, "username": "max", "first_name": "Max"},
	}
}

// userReplies answers one of the bot's messages.
func (b *fakeBot) userReplies(chatID, messageID int64, text string) {
	m := privateMessage(chatID, text)
	m["reply_to_message"] = map[string]any{"message_id": messageID, "chat": map[string]any{"id": chatID, "type": "private"}}
	b.queue(map[string]any{"message": m})
}

// userForwards forwards a message someone else wrote to the bot.
func (b *fakeBot) userForwards(chatID int64, from, text string) {
	m := privateMessage(chatID, text)
	m["forward_origin"] = map[string]any{"type": "hidden_user", "sender_user_name": from}
	b.queue(map[string]any{"message": m})
}

// userPresses presses the button with this data under one of the bot's messages.
func (b *fakeBot) userPresses(chatID, messageID int64, data string) {
	var buttons [][]fakeButton
	for _, m := range b.messages() {
		if m.ChatID == chatID && m.ID == messageID {
			buttons = m.Buttons
		}
	}
	b.queue(map[string]any{"callback_query": map[string]any{
		"id":   fmt.Sprintf("cb-%d", messageID),
		"from": map[string]any{"id": chatID, "first_name": "Max"},
		"data": data,
		"message": map[string]any{
			"message_id":   messageID,
			"chat":         map[string]any{"id": chatID, "type": "private"},
			"reply_markup": map[string]any{"inline_keyboard": buttons},
		},
	}})
}

// waitMessage waits for a message to the chat containing want, and returns it.
func (b *fakeBot) waitMessage(t *testing.T, chatID int64, want string) sentMessage {
	t.Helper()
	b.waitSent(t, chatID, want)
	msgs := b.messages()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].ChatID == chatID && strings.Contains(msgs[i].Text, want) {
			return msgs[i]
		}
	}
	return sentMessage{}
}

// message is one of the bot's messages as it is now.
func (b *fakeBot) message(chatID, id int64) sentMessage {
	for _, m := range b.messages() {
		if m.ChatID == chatID && m.ID == id {
			return m
		}
	}
	return sentMessage{}
}

// waitAnswer waits for a button press answered with a note containing want.
func (b *fakeBot) waitAnswer(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		b.mu.Lock()
		answers := append([]string(nil), b.answers...)
		b.mu.Unlock()
		for _, a := range answers {
			if strings.Contains(a, want) {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	t.Fatalf("no answer containing %q; answers: %q", want, b.answers)
}

// data is the callback data of the message's button starting with prefix, "" for none.
func (m sentMessage) data(prefix string) string {
	for _, row := range m.Buttons {
		for _, btn := range row {
			if strings.HasPrefix(btn.Data, prefix) {
				return btn.Data
			}
		}
	}
	return ""
}

func (b *fakeBot) block(chatID int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.blocked[chatID] = true
}

// waitSent waits for a message to the chat containing want.
func (b *fakeBot) waitSent(t *testing.T, chatID int64, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if b.hasSent(chatID, want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no message to %d containing %q; sent: %+v", chatID, want, b.messages())
}

func (b *fakeBot) hasSent(chatID int64, want string) bool {
	for _, m := range b.messages() {
		if m.ChatID == chatID && strings.Contains(m.Text, want) {
			return true
		}
	}
	return false
}

func (b *fakeBot) messages() []sentMessage {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]sentMessage(nil), b.sent...)
}

// newAPIWithBot starts the API with a bot talking to the fake Telegram.
func newAPIWithBot(t *testing.T) (*httptest.Server, *fakeBot) {
	t.Helper()
	bot, tg := newFakeBot(t)
	srv, _, app := newAPIWith(t, func(cfg *config.Config) {
		cfg.TelegramBotToken = "test-token"
		cfg.TelegramAPIURL = tg.URL
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		app.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return srv, bot
}

func telegramStatus(t *testing.T, srv *httptest.Server, c *http.Client) map[string]any {
	t.Helper()
	resp := get(t, c, srv.URL+"/api/v1/me/telegram")
	wantStatus(t, resp, http.StatusOK)
	return field(decode(t, resp), "telegram")
}

// connectCode asks for a connect link, waiting for the bot to be ready, and
// returns the /start code in it.
func connectCode(t *testing.T, srv *httptest.Server, c *http.Client) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp := post(t, c, srv.URL+"/api/v1/me/telegram/link", nil)
		if resp.StatusCode == http.StatusOK {
			url := field(decode(t, resp), "link")["url"].(string)
			code, ok := strings.CutPrefix(url, "https://t.me/helpic_test_bot?start=")
			if !ok {
				t.Fatalf("link = %q", url)
			}
			return code
		}
		resp.Body.Close()
		if time.Now().After(deadline) {
			t.Fatalf("link status = %d", resp.StatusCode)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// connectTelegram connects the user to the chat and waits until it is.
func connectTelegram(t *testing.T, srv *httptest.Server, bot *fakeBot, c *http.Client, chatID int64) {
	t.Helper()
	bot.userSends(chatID, "max", "/start "+connectCode(t, srv, c))
	bot.waitSent(t, chatID, "Connected to Helpic")
}

func TestTelegram_OffWithoutToken(t *testing.T) {
	srv, _ := newAPI(t)
	c := signUpFreelancer(t, srv, "notg@example.com")

	if s := telegramStatus(t, srv, c); s["enabled"] != false || s["connected"] != false {
		t.Fatalf("status = %v", s)
	}
	resp := post(t, c, srv.URL+"/api/v1/me/telegram/link", nil)
	wantStatus(t, resp, http.StatusServiceUnavailable)
	resp.Body.Close()
}

func TestTelegram_ConnectAndReceive(t *testing.T) {
	srv, bot := newAPIWithBot(t)
	freelancer := signUpFreelancer(t, srv, "tg-free@example.com")
	const chat = 4242

	code := connectCode(t, srv, freelancer)
	bot.userSends(chat, "max", "/start "+code)
	bot.waitSent(t, chat, "Connected to Helpic as <b>Test User</b>")

	s := telegramStatus(t, srv, freelancer)
	if s["enabled"] != true || s["connected"] != true || s["username"] != "@max" {
		t.Fatalf("status = %v", s)
	}
	if kinds := s["kinds"].([]any); len(kinds) != len(s["default_kinds"].([]any)) {
		t.Fatalf("kinds = %v, want the defaults", kinds)
	}

	// The link works once.
	bot.userSends(chat, "max", "/start "+code)
	bot.waitSent(t, chat, "expired or was already used")

	// What the other side does arrives in the chat.
	client := acceptNew(t, srv, invite(t, srv, freelancer, "tg-client@example.com"))
	bot.waitSent(t, chat, "<b>Acme Inc</b> accepted your invitation")
	task := newTask(t, srv, client, map[string]any{"freelancer_id": myID(t, srv, freelancer), "title": "Logo <v2>"})
	bot.waitSent(t, chat, "added a task “Logo &lt;v2&gt;”")

	// Only the kinds chosen: a new task stays in the app, a comment comes.
	resp := do(t, freelancer, http.MethodPut, srv.URL+"/api/v1/me/telegram/kinds", map[string]any{"kinds": []string{"comment_added"}})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	newTask(t, srv, client, map[string]any{"freelancer_id": myID(t, srv, freelancer), "title": "Quiet task"})
	resp = post(t, client, srv.URL+"/api/v1/comments?task="+task["id"].(string), map[string]string{"body": "Darker blue?"})
	wantStatus(t, resp, http.StatusCreated)
	resp.Body.Close()
	bot.waitSent(t, chat, "<i>Darker blue?</i>")
	if bot.hasSent(chat, "Quiet task") {
		t.Fatal("a kind the user turned off was sent")
	}

	// The actor's own changes don't come back to them.
	for _, m := range bot.messages() {
		if strings.HasPrefix(m.Text, "<b>Test User</b>") {
			t.Fatalf("the user was told about their own change: %q", m.Text)
		}
	}
}

func TestTelegram_Kinds(t *testing.T) {
	srv, bot := newAPIWithBot(t)
	c := signUpFreelancer(t, srv, "tg-kinds@example.com")

	resp := do(t, c, http.MethodPut, srv.URL+"/api/v1/me/telegram/kinds", map[string]any{"kinds": []string{"comment_added"}})
	wantStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()

	connectTelegram(t, srv, bot, c, 7)
	resp = do(t, c, http.MethodPut, srv.URL+"/api/v1/me/telegram/kinds", map[string]any{"kinds": []string{"nope"}})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()

	resp = do(t, c, http.MethodPut, srv.URL+"/api/v1/me/telegram/kinds", map[string]any{"kinds": []string{"report_paid", "report_paid", "task_status"}})
	wantStatus(t, resp, http.StatusOK)
	if got := fmt.Sprint(field(decode(t, resp), "telegram")["kinds"]); got != "[report_paid task_status]" {
		t.Fatalf("kinds = %s", got)
	}

	// Reconnecting keeps the choice.
	connectTelegram(t, srv, bot, c, 8)
	if got := fmt.Sprint(telegramStatus(t, srv, c)["kinds"]); got != "[report_paid task_status]" {
		t.Fatalf("kinds after reconnecting = %s", got)
	}
}

func TestTelegram_Disconnect(t *testing.T) {
	srv, bot := newAPIWithBot(t)
	freelancer := signUpFreelancer(t, srv, "tg-bye@example.com")

	// From the app: a goodbye in the chat.
	connectTelegram(t, srv, bot, freelancer, 11)
	resp := do(t, freelancer, http.MethodDelete, srv.URL+"/api/v1/me/telegram", nil)
	wantStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
	bot.waitSent(t, 11, "You disconnected Telegram")
	if telegramStatus(t, srv, freelancer)["connected"] != false {
		t.Fatal("still connected after disconnecting")
	}
	resp = do(t, freelancer, http.MethodDelete, srv.URL+"/api/v1/me/telegram", nil)
	wantStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()

	// From the chat: /stop.
	connectTelegram(t, srv, bot, freelancer, 12)
	bot.userSends(12, "max", "/stop")
	bot.waitSent(t, 12, "Disconnected.")
	if telegramStatus(t, srv, freelancer)["connected"] != false {
		t.Fatal("still connected after /stop")
	}

	// Blocking the bot: the next notification fails and the chat is dropped.
	connectTelegram(t, srv, bot, freelancer, 13)
	bot.block(13)
	acceptNew(t, srv, invite(t, srv, freelancer, "tg-blocked@example.com"))
	deadline := time.Now().Add(5 * time.Second)
	for telegramStatus(t, srv, freelancer)["connected"] != false {
		if time.Now().After(deadline) {
			t.Fatal("a blocked chat stayed connected")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestTelegram_StartWithoutCode(t *testing.T) {
	_, bot := newAPIWithBot(t)
	bot.userSends(99, "someone", "/start")
	bot.waitSent(t, 99, "Settings → Integrations")
	// Anything else from a chat that isn't connected says how to connect it.
	bot.userSends(99, "someone", "hello")
	eventually(t, "a second answer", func() bool {
		n := 0
		for _, m := range bot.messages() {
			if m.ChatID == 99 && strings.Contains(m.Text, "Connect Telegram") {
				n++
			}
		}
		return n == 2
	})
}
