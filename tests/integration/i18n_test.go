package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pmaxis/helpic/internal/mail"
	notifs "github.com/pmaxis/helpic/internal/notifications"
)

// inUkrainian does a request as a client whose interface is in Ukrainian.
func inUkrainian(t *testing.T, c *http.Client, method, url string, body any) *http.Response {
	t.Helper()
	buf, _ := json.Marshal(body)
	req, err := http.NewRequest(method, url, bytes.NewReader(buf))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Language", "uk-UA,uk;q=0.9,en;q=0.8")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func userLocale(t *testing.T, srv *httptest.Server, c *http.Client) string {
	t.Helper()
	return field(decode(t, get(t, c, srv.URL+"/api/v1/me")), "user")["locale"].(string)
}

func TestI18n_ErrorsSpeakTheRequestLanguage(t *testing.T) {
	srv, c := newAPI(t)

	resp := inUkrainian(t, c, http.MethodPost, srv.URL+"/api/v1/auth/register", map[string]string{"email": "nope", "name": "", "password": "short", "password_confirmation": "other"})
	wantStatus(t, resp, http.StatusBadRequest)
	e := decode(t, resp)["error"].(map[string]any)
	fields := e["fields"].(map[string]any)
	if e["message"] != "Деякі поля заповнено неправильно" || fields["email"] != "Вкажіть коректну адресу пошти" || fields["password"] != "Пароль має містити щонайменше 8 символів" {
		t.Fatalf("Ukrainian validation error = %v", e)
	}

	// Without the header, English.
	resp = post(t, c, srv.URL+"/api/v1/auth/login", map[string]string{"email": "a@example.com", "password": "whatever1"})
	wantStatus(t, resp, http.StatusUnauthorized)
	if msg := decode(t, resp)["error"].(map[string]any)["message"]; msg != "Invalid email or password" {
		t.Fatalf("English error = %v", msg)
	}
	resp = inUkrainian(t, c, http.MethodPost, srv.URL+"/api/v1/auth/login", map[string]string{"email": "a@example.com", "password": "whatever1"})
	wantStatus(t, resp, http.StatusUnauthorized)
	if msg := decode(t, resp)["error"].(map[string]any)["message"]; msg != "Неправильна пошта або пароль" {
		t.Fatalf("Ukrainian error = %v", msg)
	}
}

func TestI18n_AccountLanguage(t *testing.T) {
	srv, _ := newAPI(t)

	// Sign-up keeps the language the interface was in.
	uk := newClient()
	resp := inUkrainian(t, uk, http.MethodPost, srv.URL+"/api/v1/auth/register", map[string]string{
		"email": "olena@example.com", "name": "Олена", "password": "supersecret", "password_confirmation": "supersecret",
	})
	wantStatus(t, resp, http.StatusCreated)
	if got := field(decode(t, resp), "user")["locale"]; got != "uk" {
		t.Fatalf("locale after a Ukrainian sign-up = %v", got)
	}
	en := signUpFreelancer(t, srv, "max@example.com")
	if got := userLocale(t, srv, en); got != "en" {
		t.Fatalf("locale after an English sign-up = %v", got)
	}

	// It can be changed, to a language Helpic speaks.
	resp = do(t, en, http.MethodPut, srv.URL+"/api/v1/me/locale", map[string]string{"locale": "de"})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()
	resp = do(t, en, http.MethodPut, srv.URL+"/api/v1/me/locale", map[string]string{"locale": "uk"})
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "user")["locale"]; got != "uk" {
		t.Fatalf("locale after changing = %v", got)
	}

	// Email goes out in the account's language.
	forgot(t, srv.URL, "olena@example.com")
	if e := lastEmailTo(t, "olena@example.com"); e.Subject != "Скидання пароля в Helpic" || !strings.Contains(e.Text, "Задати новий пароль") || !strings.Contains(e.HTML, "Або відкрийте посилання") {
		t.Fatalf("Ukrainian reset email = %+v", e)
	}
}

func TestI18n_NotificationEmailInRecipientLanguage(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	resp := do(t, freelancer, http.MethodPut, srv.URL+"/api/v1/me/locale", map[string]string{"locale": "uk"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	newTask(t, srv, client, map[string]any{"freelancer_id": myID(t, srv, freelancer), "title": "Logo"})

	ctx := context.Background()
	if _, err := testPool.Exec(ctx, `UPDATE notifications SET created_at = created_at - interval '6 minutes' WHERE emailed_at IS NULL`); err != nil {
		t.Fatal(err)
	}
	if err := notifs.NewEmailer(testPool, mail.NewOutbox(testPool, mail.Log{}), "http://app.test").Run(ctx); err != nil {
		t.Fatal(err)
	}
	e := lastEmailTo(t, "free@example.com")
	if e.Subject != "2 оновлення у Helpic" || !strings.Contains(e.Text, "Нова задача від Acme Inc: “Logo”") || !strings.Contains(e.HTML, `lang="uk"`) {
		t.Fatalf("Ukrainian digest = %q\n%s", e.Subject, e.Text)
	}
}

func TestI18n_TelegramSpeaksTheirLanguage(t *testing.T) {
	_, bot := newAPIWithBot(t)
	bot.userSendsIn(77, "olena", "uk", "/start")
	bot.waitSent(t, 77, "Налаштування → Сповіщення")
	bot.userSendsIn(77, "olena", "uk", "/stop")
	bot.waitSent(t, 77, "Цей чат не підключено до Helpic.")
}
