package integration

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const (
	freelancerChat = 7001
	clientChat     = 7002
)

// botPair is a freelancer and their client, both with Telegram connected.
func botPair(t *testing.T) (srv *httptest.Server, bot *fakeBot, freelancer, client *http.Client) {
	t.Helper()
	srv, bot = newAPIWithBot(t)
	freelancer = signUpFreelancer(t, srv, "tg-free@example.com")
	client = acceptNew(t, srv, invite(t, srv, freelancer, "tg-client@example.com"))
	connectTelegram(t, srv, bot, freelancer, freelancerChat)
	connectTelegram(t, srv, bot, client, clientChat)
	return srv, bot, freelancer, client
}

// eventually retries check until it holds, for what the bot does in the background.
func eventually(t *testing.T, what string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !check() {
		if time.Now().After(deadline) {
			t.Fatalf("never: %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestTelegramActions_ReplyCommentsAndButtonsTime(t *testing.T) {
	srv, bot, freelancer, client := botPair(t)
	task := newTask(t, srv, client, map[string]any{"freelancer_id": myID(t, srv, freelancer), "title": "Logo"})
	taskID := task["id"].(string)

	note := bot.waitMessage(t, freelancerChat, "added a task “Logo”")
	start := note.data("ts:")
	if start != "ts:"+taskID {
		t.Fatalf("buttons = %+v", note.Buttons)
	}

	// A reply to the notification is a comment on the task.
	bot.userReplies(freelancerChat, note.ID, "On it, draft by Friday")
	bot.waitSent(t, freelancerChat, "Commented on “Logo”")
	resp := get(t, client, srv.URL+"/api/v1/comments?task="+taskID)
	wantStatus(t, resp, http.StatusOK)
	list := decode(t, resp)["comments"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["body"] != "On it, draft by Friday" {
		t.Fatalf("comments = %v", list)
	}
	// The client hears of it, and may answer the same way.
	reply := bot.waitMessage(t, clientChat, "<i>On it, draft by Friday</i>")
	bot.userReplies(clientChat, reply.ID, "Thanks!")
	bot.waitSent(t, clientChat, "Commented on “Logo”")

	// The button starts the timer and turns into one that stops it.
	bot.userPresses(freelancerChat, note.ID, start)
	bot.waitAnswer(t, "Timer started on “Logo”")
	eventually(t, "the button turns to stop", func() bool { return bot.message(freelancerChat, note.ID).data("tp") != "" })
	resp = get(t, freelancer, srv.URL+"/api/v1/timer")
	wantStatus(t, resp, http.StatusOK)
	if timer := field(decode(t, resp), "timer"); timer["task_id"] != taskID {
		t.Fatalf("timer = %v", timer)
	}

	bot.userSends(freelancerChat, "max", "/timer")
	shown := bot.waitMessage(t, freelancerChat, "The timer is on “Logo”")
	bot.userPresses(freelancerChat, shown.ID, "tp")
	bot.waitAnswer(t, "⏹")
	resp = get(t, freelancer, srv.URL+"/api/v1/timer")
	wantStatus(t, resp, http.StatusOK)
	if timer := decode(t, resp)["timer"]; timer != nil {
		t.Fatalf("timer still running: %v", timer)
	}
	bot.userSends(freelancerChat, "max", "/timer")
	bot.waitSent(t, freelancerChat, "No timer is running")
}

func TestTelegramActions_SendBackAndPay(t *testing.T) {
	srv, bot, freelancer, client := botPair(t)
	clientID := myID(t, srv, client)
	today := time.Now().Format("2006-01-02")
	task := newTask(t, srv, freelancer, map[string]any{"client_id": clientID, "title": "Banner", "price": "300"})
	taskURL := srv.URL + "/api/v1/tasks/" + task["id"].(string)

	// Done work comes to the client with a button to send it back.
	resp := post(t, freelancer, taskURL+"/status", map[string]string{"status": "done"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	done := bot.waitMessage(t, clientChat, "moved “Banner”")
	bot.userPresses(clientChat, done.ID, done.data("tr:"))
	bot.waitAnswer(t, "“Banner” is back in progress")
	resp = get(t, client, taskURL)
	wantStatus(t, resp, http.StatusOK)
	if status := field(decode(t, resp), "task")["status"]; status != "in_progress" {
		t.Fatalf("status = %v", status)
	}

	// A sent report comes with a button to mark it paid, which asks first.
	resp = post(t, freelancer, taskURL+"/status", map[string]string{"status": "done"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	resp = post(t, freelancer, srv.URL+"/api/v1/reports", map[string]string{"client_id": clientID, "from": today, "to": today})
	wantStatus(t, resp, http.StatusCreated)
	reportURL := srv.URL + "/api/v1/reports/" + field(decode(t, resp), "report")["id"].(string)
	resp = post(t, freelancer, reportURL+"/send", nil)
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	sent := bot.waitMessage(t, clientChat, "sent you a report")
	bot.userPresses(clientChat, sent.ID, sent.data("rp:"))
	eventually(t, "the question", func() bool { return bot.message(clientChat, sent.ID).data("ry:") != "" })
	// Cancel puts the button back; nothing is paid.
	bot.userPresses(clientChat, sent.ID, bot.message(clientChat, sent.ID).data("rn:"))
	eventually(t, "the button back", func() bool { return bot.message(clientChat, sent.ID).data("rp:") != "" })
	resp = get(t, client, reportURL)
	wantStatus(t, resp, http.StatusOK)
	if status := field(decode(t, resp), "report")["status"]; status != "sent" {
		t.Fatalf("status = %v", status)
	}

	bot.userPresses(clientChat, sent.ID, bot.message(clientChat, sent.ID).data("rp:"))
	eventually(t, "the question again", func() bool { return bot.message(clientChat, sent.ID).data("ry:") != "" })
	bot.userPresses(clientChat, sent.ID, bot.message(clientChat, sent.ID).data("ry:"))
	bot.waitAnswer(t, "is marked paid")
	resp = get(t, client, reportURL)
	wantStatus(t, resp, http.StatusOK)
	if status := field(decode(t, resp), "report")["status"]; status != "paid" {
		t.Fatalf("status = %v", status)
	}
	bot.waitSent(t, freelancerChat, "as paid")
}

func TestTelegramActions_ForwardedMessageBecomesATask(t *testing.T) {
	srv, bot, freelancer, client := botPair(t)

	bot.userForwards(freelancerChat, "Olena", "Fix the footer\nIt overlaps the form on phones")
	offer := bot.waitMessage(t, freelancerChat, "Make a task “Fix the footer”?")
	var names []string
	for _, row := range offer.Buttons {
		names = append(names, row[0].Text)
	}
	if strings.Join(names, ", ") != "👤 Personal, Acme Inc, Cancel" {
		t.Fatalf("choices = %v", names)
	}
	bot.userPresses(freelancerChat, offer.ID, offer.Buttons[1][0].Data)
	bot.waitSent(t, freelancerChat, "Task “Fix the footer” made: Acme Inc")

	got := map[string]any{}
	eventually(t, "the client sees the task", func() bool {
		resp := get(t, client, srv.URL+"/api/v1/tasks")
		for _, x := range decode(t, resp)["tasks"].([]any) {
			if task := x.(map[string]any); task["title"] == "Fix the footer" {
				got = task
			}
		}
		return got["id"] != nil
	})
	if desc := got["description"].(string); !strings.Contains(desc, "It overlaps the form on phones") || !strings.Contains(desc, "Forwarded from Olena") {
		t.Fatalf("description = %q", desc)
	}
	// The made task's message offers its timer; a second press makes nothing more.
	if bot.message(freelancerChat, offer.ID).data("ts:") == "" {
		t.Fatalf("buttons = %+v", bot.message(freelancerChat, offer.ID).Buttons)
	}

	// A plain message, kept to oneself; another, dropped.
	bot.userSends(freelancerChat, "max", "Renew the domain")
	mine := bot.waitMessage(t, freelancerChat, "Make a task “Renew the domain”?")
	bot.userPresses(freelancerChat, mine.ID, mine.Buttons[0][0].Data)
	bot.waitSent(t, freelancerChat, "Task “Renew the domain” made: personal")
	bot.userSends(freelancerChat, "max", "Never mind")
	drop := bot.waitMessage(t, freelancerChat, "Make a task “Never mind”?")
	bot.userPresses(freelancerChat, drop.ID, drop.data("nx:"))
	bot.waitSent(t, freelancerChat, "No task made.")

	titles := taskTitles(t, freelancer, srv.URL)
	if len(titles) != 2 {
		t.Fatalf("freelancer's tasks = %v", titles)
	}
}

func TestTelegramActions_StrangersAndHelp(t *testing.T) {
	_, bot, _, _ := botPair(t)

	// A chat that isn't connected is told how to connect.
	bot.userSends(9999, "nobody", "Make me a task")
	bot.waitSent(t, 9999, "Connect Telegram")
	// A reply to something that isn't about a task makes a task of it instead.
	bot.userReplies(freelancerChat, 1, "Call the printer")
	bot.waitSent(t, freelancerChat, "Make a task “Call the printer”?")
	// An unknown command gets the help.
	bot.userSends(freelancerChat, "max", "/what")
	bot.waitSent(t, freelancerChat, "Here's what I can do")
}

func TestTelegramActions_TaskFromAMessageGoesIntoAProject(t *testing.T) {
	srv, bot, freelancer, client := botPair(t)
	clientID := myID(t, srv, client)
	site := createProject(t, srv, freelancer, map[string]any{"client_id": clientID, "name": "Site"})
	createProject(t, srv, freelancer, map[string]any{"name": "Portfolio"})

	// With the client: their shared project, or none.
	bot.userSends(freelancerChat, "max", "New hero banner")
	offer := bot.waitMessage(t, freelancerChat, "Make a task “New hero banner”?")
	bot.userPresses(freelancerChat, offer.ID, offer.Buttons[1][0].Data)
	eventually(t, "the project question", func() bool {
		return strings.Contains(bot.message(freelancerChat, offer.ID).Text, "Which project is it in?")
	})
	asked := bot.message(freelancerChat, offer.ID)
	var names []string
	for _, row := range asked.Buttons {
		names = append(names, row[0].Text)
	}
	if strings.Join(names, ", ") != "📁 Site, No project, Cancel" {
		t.Fatalf("projects = %v", names)
	}
	bot.userPresses(freelancerChat, offer.ID, asked.Buttons[0][0].Data)
	bot.waitSent(t, freelancerChat, "Task “New hero banner” made: Acme Inc · Site")
	resp := get(t, client, srv.URL+"/api/v1/tasks?project="+site["id"].(string))
	wantStatus(t, resp, http.StatusOK)
	if list := decode(t, resp)["tasks"].([]any); len(list) != 1 || list[0].(map[string]any)["title"] != "New hero banner" {
		t.Fatalf("project tasks = %v", list)
	}

	// Personal: the freelancer's own projects.
	bot.userSends(freelancerChat, "max", "Update the case study")
	mine := bot.waitMessage(t, freelancerChat, "Make a task “Update the case study”?")
	bot.userPresses(freelancerChat, mine.ID, mine.Buttons[0][0].Data)
	eventually(t, "the personal project question", func() bool {
		return bot.message(freelancerChat, mine.ID).data("np:") != ""
	})
	if first := bot.message(freelancerChat, mine.ID).Buttons[0][0].Text; first != "📁 Portfolio" {
		t.Fatalf("first project = %q", first)
	}
	bot.userPresses(freelancerChat, mine.ID, bot.message(freelancerChat, mine.ID).Buttons[1][0].Data)
	bot.waitSent(t, freelancerChat, "Task “Update the case study” made: personal.")
}
