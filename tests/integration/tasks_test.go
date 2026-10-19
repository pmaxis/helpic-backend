package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
)

func do(t *testing.T, c *http.Client, method, url string, body any) *http.Response {
	t.Helper()
	var buf []byte
	if body != nil {
		buf, _ = json.Marshal(body)
	}
	req, err := http.NewRequest(method, url, bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	return resp
}

func newClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar}
}

func field(body map[string]any, key string) map[string]any {
	m, _ := body[key].(map[string]any)
	return m
}

// signUpFreelancer registers a freelancer and returns a signed-in client.
func signUpFreelancer(t *testing.T, srv *httptest.Server, email string) *http.Client {
	t.Helper()
	c := newClient()
	confirmed(t, c, srv.URL, email, "supersecret1")
	return c
}

// invite creates an invitation and returns its token, read from the email
// sent to the client: the API never gives it to the freelancer.
func invite(t *testing.T, srv *httptest.Server, freelancer *http.Client, email string) string {
	t.Helper()
	resp := post(t, freelancer, srv.URL+"/api/v1/invitations", map[string]string{"email": email, "name": "Acme"})
	wantStatus(t, resp, http.StatusCreated)
	if _, leaked := field(decode(t, resp), "invitation")["token"]; leaked {
		t.Fatal("the invitation's token reached the freelancer")
	}
	return linkToken(t, lastEmailTo(t, strings.ToLower(email)), "/invite/")
}

// lookupInvite opens the invitation page for the token.
func lookupInvite(t *testing.T, srv *httptest.Server, token string) *http.Response {
	t.Helper()
	return post(t, newClient(), srv.URL+"/api/v1/invites/lookup", map[string]string{"token": token})
}

// acceptInvite accepts the invitation with the token and the form's fields.
func acceptInvite(t *testing.T, srv *httptest.Server, c *http.Client, token string, form map[string]string) *http.Response {
	t.Helper()
	body := map[string]string{"token": token}
	for k, v := range form {
		body[k] = v
	}
	return post(t, c, srv.URL+"/api/v1/invites/accept", body)
}

// acceptNew accepts an invitation with a new client account and returns the
// signed-in client.
func acceptNew(t *testing.T, srv *httptest.Server, token string) *http.Client {
	t.Helper()
	c := newClient()
	resp := acceptInvite(t, srv, c, token, map[string]string{
		"name": "Acme Inc", "password": "clientsecret1", "password_confirmation": "clientsecret1",
	})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	return c
}

func myID(t *testing.T, srv *httptest.Server, c *http.Client) string {
	t.Helper()
	resp := get(t, c, srv.URL+"/api/v1/me")
	wantStatus(t, resp, http.StatusOK)
	return field(decode(t, resp), "user")["id"].(string)
}

// --- invitations --------------------------------------------------------------

func TestInvitation_NewClientAcceptsAndIsLinked(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	token := invite(t, srv, freelancer, "Client@Example.com")

	resp := lookupInvite(t, srv, token)
	wantStatus(t, resp, http.StatusOK)
	inv := field(decode(t, resp), "invitation")
	if inv["email"] != "client@example.com" || inv["account_exists"] != false || inv["name"] != "Acme" {
		t.Fatalf("lookup = %v", inv)
	}

	client := acceptNew(t, srv, token)
	resp = get(t, client, srv.URL+"/api/v1/me")
	wantStatus(t, resp, http.StatusOK)
	if u := field(decode(t, resp), "user"); u["role"] != "client" || u["name"] != "Acme Inc" {
		t.Fatalf("me = %v", u)
	}

	resp = get(t, freelancer, srv.URL+"/api/v1/clients")
	wantStatus(t, resp, http.StatusOK)
	if list, _ := decode(t, resp)["clients"].([]any); len(list) != 1 {
		t.Fatalf("clients = %v, want 1", list)
	}
	resp = get(t, client, srv.URL+"/api/v1/freelancers")
	wantStatus(t, resp, http.StatusOK)
	if list, _ := decode(t, resp)["freelancers"].([]any); len(list) != 1 {
		t.Fatalf("freelancers = %v, want 1", list)
	}

	// The link is single-use.
	wantStatus(t, lookupInvite(t, srv, token), http.StatusNotFound)
	resp = get(t, freelancer, srv.URL+"/api/v1/invitations")
	wantStatus(t, resp, http.StatusOK)
	if list, _ := decode(t, resp)["invitations"].([]any); len(list) != 0 {
		t.Fatalf("pending invitations = %v, want none", list)
	}
}

func TestInvitation_ExistingClientSignsInToAccept(t *testing.T) {
	srv, _ := newAPI(t)
	first := signUpFreelancer(t, srv, "first@example.com")
	acceptNew(t, srv, invite(t, srv, first, "client@example.com"))

	second := signUpFreelancer(t, srv, "second@example.com")
	token := invite(t, srv, second, "client@example.com")

	resp := lookupInvite(t, srv, token)
	wantStatus(t, resp, http.StatusOK)
	if field(decode(t, resp), "invitation")["account_exists"] != true {
		t.Fatal("account_exists = false, want true")
	}

	c := newClient()
	wrong := acceptInvite(t, srv, c, token, map[string]string{"password": "nope-nope"})
	wantStatus(t, wrong, http.StatusBadRequest)
	wrong.Body.Close()

	resp = acceptInvite(t, srv, c, token, map[string]string{"password": "clientsecret1"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	resp = get(t, c, srv.URL+"/api/v1/freelancers")
	wantStatus(t, resp, http.StatusOK)
	if list, _ := decode(t, resp)["freelancers"].([]any); len(list) != 2 {
		t.Fatalf("freelancers = %v, want 2", list)
	}
}

func TestInvitation_Rejections(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	signUpFreelancer(t, srv, "other@example.com")

	for name, email := range map[string]string{
		"self":               "free@example.com",
		"another freelancer": "other@example.com",
	} {
		resp := post(t, freelancer, srv.URL+"/api/v1/invitations", map[string]string{"email": email, "name": "X"})
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, resp.StatusCode)
		}
		resp.Body.Close()
	}

	invite(t, srv, freelancer, "c@example.com")
	resp := post(t, freelancer, srv.URL+"/api/v1/invitations", map[string]string{"email": "c@example.com", "name": "X"})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()
}

func TestRoles_AreEnforced(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))

	resp := post(t, client, srv.URL+"/api/v1/invitations", map[string]string{"email": "x@example.com", "name": "X"})
	wantStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()

	resp = get(t, freelancer, srv.URL+"/api/v1/freelancers")
	wantStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()
}

// --- tasks --------------------------------------------------------------------

func TestTasks_ClientSetsAndFreelancerWorks(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	freelancerID := myID(t, srv, freelancer)

	resp := post(t, client, srv.URL+"/api/v1/tasks", map[string]any{
		"freelancer_id": freelancerID, "title": "Landing page", "description": "Hero + pricing", "due_on": "2030-01-15",
	})
	wantStatus(t, resp, http.StatusCreated)
	task := field(decode(t, resp), "task")
	id := task["id"].(string)
	if task["status"] != "new" || task["due_on"] != "2030-01-15" || task["can_edit"] != true {
		t.Fatalf("created task = %v", task)
	}

	// The freelancer sees it and can start it. A status that isn't one of
	// the three is refused.
	resp = get(t, freelancer, srv.URL+"/api/v1/tasks")
	wantStatus(t, resp, http.StatusOK)
	if list, _ := decode(t, resp)["tasks"].([]any); len(list) != 1 {
		t.Fatalf("freelancer tasks = %v, want 1", list)
	}
	resp = post(t, freelancer, srv.URL+"/api/v1/tasks/"+id+"/status", map[string]string{"status": "cancelled"})
	wantStatus(t, resp, http.StatusConflict)
	resp.Body.Close()
	resp = post(t, freelancer, srv.URL+"/api/v1/tasks/"+id+"/status", map[string]string{"status": "in_progress"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// Started tasks can't be deleted.
	resp = do(t, client, http.MethodDelete, srv.URL+"/api/v1/tasks/"+id, nil)
	wantStatus(t, resp, http.StatusConflict)
	resp.Body.Close()

	resp = post(t, freelancer, srv.URL+"/api/v1/tasks/"+id+"/status", map[string]string{"status": "done"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// Done tasks stay editable: the client fixes their wording, and the
	// freelancer edits what the client set.
	resp = do(t, client, http.MethodPut, srv.URL+"/api/v1/tasks/"+id, map[string]any{"title": "Landing page", "description": "Hero and pricing"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	resp = do(t, freelancer, http.MethodPut, srv.URL+"/api/v1/tasks/"+id, map[string]any{"title": "Landing page (final)", "description": "Hero and pricing"})
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "task"); got["title"] != "Landing page (final)" || got["can_edit"] != true {
		t.Fatalf("freelancer's edit of a done client task = %v", got)
	}
	resp = post(t, client, srv.URL+"/api/v1/tasks/"+id+"/status", map[string]string{"status": "in_progress"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	resp = do(t, client, http.MethodPut, srv.URL+"/api/v1/tasks/"+id, map[string]any{"title": "Landing page v2"})
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "task")["title"]; got != "Landing page v2" {
		t.Fatalf("title = %v", got)
	}

	resp = get(t, client, srv.URL+"/api/v1/tasks?status=done")
	wantStatus(t, resp, http.StatusOK)
	if list, _ := decode(t, resp)["tasks"].([]any); len(list) != 0 {
		t.Fatalf("done tasks = %v, want none", list)
	}
}

func TestTasks_OnlyForLinkedFreelancers(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	stranger := signUpFreelancer(t, srv, "stranger@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))

	resp := post(t, client, srv.URL+"/api/v1/tasks", map[string]any{"freelancer_id": myID(t, srv, stranger), "title": "Nope"})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()

	resp = post(t, client, srv.URL+"/api/v1/tasks", map[string]any{"freelancer_id": myID(t, srv, freelancer), "title": "Mine"})
	wantStatus(t, resp, http.StatusCreated)
	id := field(decode(t, resp), "task")["id"].(string)

	// Another freelancer can neither see nor move it.
	wantStatus(t, get(t, stranger, srv.URL+"/api/v1/tasks/"+id), http.StatusNotFound)
	resp = post(t, stranger, srv.URL+"/api/v1/tasks/"+id+"/status", map[string]string{"status": "in_progress"})
	wantStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()

	// A new task can be deleted by its client.
	resp = do(t, client, http.MethodDelete, srv.URL+"/api/v1/tasks/"+id, nil)
	wantStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
}

func TestTasks_FreelancerCreatesForClient(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	clientID := myID(t, srv, client)

	resp := post(t, freelancer, srv.URL+"/api/v1/tasks", map[string]any{"client_id": clientID, "title": "Set up hosting"})
	wantStatus(t, resp, http.StatusCreated)
	task := field(decode(t, resp), "task")
	id := task["id"].(string)
	if task["created_by"] != myID(t, srv, freelancer) || task["can_edit"] != true || task["can_delete"] != true {
		t.Fatalf("created task = %v", task)
	}

	// The client sees it but can't edit or delete what the freelancer created.
	resp = get(t, client, srv.URL+"/api/v1/tasks/"+id)
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "task"); got["can_edit"] != false || got["can_delete"] != false {
		t.Fatalf("client view = %v", got)
	}
	resp = do(t, client, http.MethodPut, srv.URL+"/api/v1/tasks/"+id, map[string]any{"title": "Changed"})
	wantStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()

	// The freelancer edits their own task, then deletes it.
	resp = do(t, freelancer, http.MethodPut, srv.URL+"/api/v1/tasks/"+id, map[string]any{"title": "Set up hosting and DNS"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	resp = do(t, freelancer, http.MethodDelete, srv.URL+"/api/v1/tasks/"+id, nil)
	wantStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
}

func TestTasks_FreelancerCannotCancelClientTask(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))

	resp := post(t, client, srv.URL+"/api/v1/tasks", map[string]any{"freelancer_id": myID(t, srv, freelancer), "title": "Logo"})
	wantStatus(t, resp, http.StatusCreated)
	id := field(decode(t, resp), "task")["id"].(string)

	resp = post(t, freelancer, srv.URL+"/api/v1/tasks/"+id+"/status", map[string]string{"status": "cancelled"})
	wantStatus(t, resp, http.StatusConflict)
	resp.Body.Close()
}

func TestTasks_FreelancerOnlyForOwnClients(t *testing.T) {
	srv, _ := newAPI(t)
	owner := signUpFreelancer(t, srv, "owner@example.com")
	client := acceptNew(t, srv, invite(t, srv, owner, "client@example.com"))
	other := signUpFreelancer(t, srv, "other@example.com")

	resp := post(t, other, srv.URL+"/api/v1/tasks", map[string]any{"client_id": myID(t, srv, client), "title": "Sneaky"})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()
}

func TestTasks_PersonalTasks(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))

	resp := post(t, freelancer, srv.URL+"/api/v1/tasks", map[string]any{"title": "Renew domain"})
	wantStatus(t, resp, http.StatusCreated)
	task := field(decode(t, resp), "task")
	id := task["id"].(string)
	if task["client"] != nil {
		t.Fatalf("client = %v, want null", task["client"])
	}

	resp = post(t, freelancer, srv.URL+"/api/v1/tasks", map[string]any{"client_id": myID(t, srv, client), "title": "Client work"})
	wantStatus(t, resp, http.StatusCreated)
	resp.Body.Close()

	// Only the freelancer sees it.
	wantStatus(t, get(t, client, srv.URL+"/api/v1/tasks/"+id), http.StatusNotFound)
	resp = get(t, client, srv.URL+"/api/v1/tasks")
	wantStatus(t, resp, http.StatusOK)
	if list, _ := decode(t, resp)["tasks"].([]any); len(list) != 1 {
		t.Fatalf("client tasks = %v, want only the client one", list)
	}

	resp = get(t, freelancer, srv.URL+"/api/v1/tasks?with=personal")
	wantStatus(t, resp, http.StatusOK)
	list, _ := decode(t, resp)["tasks"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["id"] != id {
		t.Fatalf("personal filter = %v", list)
	}

	// The whole lifecycle is the freelancer's.
	for _, status := range []string{"in_progress", "done", "in_progress", "new"} {
		resp = post(t, freelancer, srv.URL+"/api/v1/tasks/"+id+"/status", map[string]string{"status": status})
		wantStatus(t, resp, http.StatusOK)
		resp.Body.Close()
	}

	// It stays editable once done.
	resp = post(t, freelancer, srv.URL+"/api/v1/tasks/"+id+"/status", map[string]string{"status": "done"})
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "task"); got["can_edit"] != true {
		t.Fatalf("done personal task = %v, want can_edit", got)
	}
	resp = do(t, freelancer, http.MethodPut, srv.URL+"/api/v1/tasks/"+id, map[string]any{"title": "Renew domain for 2 years"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// A client can't create a task without a freelancer.
	resp = post(t, client, srv.URL+"/api/v1/tasks", map[string]any{"title": "Mine"})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()
}
