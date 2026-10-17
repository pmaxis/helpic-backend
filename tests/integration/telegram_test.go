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
}

type sentMessage struct {
	ChatID int64
	Text   string
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
		b.sent = append(b.sent, sentMessage{ChatID: chatID, Text: params["text"].(string)})
		reply(map[string]any{"ok": true, "result": map[string]any{}})
	default:
		reply(map[string]any{"ok": false, "error_code": 404, "description": "Not Found"})
	}
}

// userSends queues a private message from a Telegram user.
func (b *fakeBot) userSends(chatID int64, username, text string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextID++
	// Round-trip through JSON so update_id reads back as float64, like the rest.
	raw, _ := json.Marshal(map[string]any{
		"update_id": b.nextID,
		"message": map[string]any{
			"text": text,
			"chat": map[string]any{"id": chatID, "type": "private"},
			"from": map[string]any{"id": chatID, "username": username, "first_name": "Max"},
		},
	})
	var u map[string]any
	_ = json.Unmarshal(raw, &u)
	b.pending = append(b.pending, u)
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
	bot.waitSent(t, 99, "Settings → Telegram")
	bot.userSends(99, "someone", "hello")
	bot.waitSent(t, 99, "I only send Helpic notifications")
}
