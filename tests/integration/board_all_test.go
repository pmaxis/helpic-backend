package integration

import (
	"net/http"
	"slices"
	"testing"
)

func TestBoard_All(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	clientID := myID(t, srv, client)
	website := createProject(t, srv, freelancer, map[string]any{"client_id": clientID, "name": "Website"})

	newTask(t, srv, freelancer, map[string]any{"project_id": website["id"], "title": "Hero"})
	landing := newTask(t, srv, freelancer, map[string]any{"client_id": clientID, "title": "Landing"})
	personal := newTask(t, srv, freelancer, map[string]any{"title": "Renew domain"})

	// The client's board gets a Review column; the personal board has none.
	resp := post(t, freelancer, srv.URL+"/api/board/columns", map[string]string{"with": clientID, "name": "Review", "category": "in_progress"})
	wantStatus(t, resp, http.StatusCreated)
	resp.Body.Close()

	// Every task, in lanes merged across the boards, in category order.
	b := getBoard(t, srv, freelancer, "?with=all")
	names := []string{}
	total := 0
	for _, c := range b.Columns {
		names = append(names, c.Name)
		total += len(c.Tasks)
	}
	if b.CanManage || total != 3 || !slices.Equal(names, []string{"New", "In progress", "Review", "Done", "Cancelled"}) {
		t.Fatalf("all board = %+v", b.Columns)
	}
	if got := column(b, "New").Tasks; len(got) != 3 {
		t.Fatalf("New lane = %v", got)
	}

	// The project filter, with tasks outside any project.
	if got := column(getBoard(t, srv, freelancer, "?with=all&projects="+website["id"].(string)), "New").Tasks; !slices.Equal(got, []string{"Hero"}) {
		t.Fatalf("Website only = %v", got)
	}
	if got := column(getBoard(t, srv, freelancer, "?with=all&projects=none"), "New").Tasks; len(got) != 2 {
		t.Fatalf("no project = %v", got)
	}
	if got := column(getBoard(t, srv, freelancer, "?with="+clientID+"&projects=none"), "New").Tasks; !slices.Equal(got, []string{"Landing"}) {
		t.Fatalf("client board, no project = %v", got)
	}

	// A card dropped on a lane goes to its own board's column of that name,
	// or its first column of that category.
	move := func(task map[string]any, lane string) map[string]any {
		resp := post(t, freelancer, srv.URL+"/api/board/moves", map[string]string{"task_id": task["id"].(string), "lane": lane})
		wantStatus(t, resp, http.StatusOK)
		return field(decode(t, resp), "task")
	}
	if got := move(landing, column(b, "Review").ID); got["status_name"] != "Review" {
		t.Fatalf("landing moved to = %v", got["status_name"])
	}
	if got := move(personal, column(b, "Review").ID); got["status_name"] != "In progress" || got["status"] != "in_progress" {
		t.Fatalf("personal task moved to = %v", got["status_name"])
	}
	resp = post(t, freelancer, srv.URL+"/api/board/moves", map[string]string{"task_id": personal["id"].(string), "lane": "nonsense"})
	wantStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()

	// The all board is the freelancer's.
	wantStatus(t, get(t, client, srv.URL+"/api/board?with=all"), http.StatusNotFound)
}
