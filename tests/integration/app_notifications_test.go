package integration

import (
	"net/http"
	"testing"
)

func TestAppNotifications_TheBellShowsTheKindsChosen(t *testing.T) {
	srv, bot := newAPIWithBot(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	connectTelegram(t, srv, bot, client, 8001)
	task := newTask(t, srv, client, map[string]any{"freelancer_id": myID(t, srv, freelancer), "title": "Logo"})
	taskID := task["id"].(string)

	// Everything until they choose.
	resp := get(t, client, srv.URL+"/api/v1/me/app-notifications")
	wantStatus(t, resp, http.StatusOK)
	prefs := field(decode(t, resp), "app_notifications")
	if len(prefs["kinds"].([]any)) != len(prefs["available"].([]any)) {
		t.Fatalf("prefs = %v", prefs)
	}

	resp = do(t, client, http.MethodPut, srv.URL+"/api/v1/me/app-notifications", map[string]any{"kinds": []string{"task_status"}})
	wantStatus(t, resp, http.StatusOK)
	if kinds := field(decode(t, resp), "app_notifications")["kinds"].([]any); len(kinds) != 1 || kinds[0] != "task_status" {
		t.Fatalf("kinds = %v", kinds)
	}
	resp = do(t, client, http.MethodPut, srv.URL+"/api/v1/me/app-notifications", map[string]any{"kinds": []string{"nope"}})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()

	// A comment stays out of the bell, but still reaches Telegram; a move shows.
	resp = post(t, freelancer, srv.URL+"/api/v1/comments?task="+taskID, map[string]string{"body": "Darker?"})
	wantStatus(t, resp, http.StatusCreated)
	resp.Body.Close()
	bot.waitSent(t, 8001, "<i>Darker?</i>")
	resp = post(t, freelancer, srv.URL+"/api/v1/tasks/"+taskID+"/status", map[string]string{"status": "in_progress"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	list, unread := notifications(t, srv, client)
	if len(list) != 1 || list[0].Kind != "task_status" || unread != 1 {
		t.Fatalf("bell = %+v, unread = %d", list, unread)
	}
}
