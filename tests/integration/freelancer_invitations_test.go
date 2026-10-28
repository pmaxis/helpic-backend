package integration

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// signUpClient registers a client on their own and returns a signed-in client.
func signUpClient(t *testing.T, srv *httptest.Server, email string) *http.Client {
	t.Helper()
	c := newClient()
	resp := post(t, c, srv.URL+"/api/v1/auth/register", map[string]string{
		"email": email, "name": "Olena", "role": "client",
		"password": "clientsecret1", "password_confirmation": "clientsecret1",
	})
	wantStatus(t, resp, http.StatusAccepted)
	resp.Body.Close()
	resp = confirmSignup(t, c, srv.URL, email)
	wantStatus(t, resp, http.StatusCreated)
	resp.Body.Close()
	return c
}

// inviteFreelancer creates a client's invitation and returns its token, read
// from the email sent to the freelancer: the API never gives it to the client.
func inviteFreelancer(t *testing.T, srv *httptest.Server, client *http.Client, email string) string {
	t.Helper()
	resp := post(t, client, srv.URL+"/api/v1/freelancer-invitations", map[string]string{"email": email, "name": "Oleh"})
	wantStatus(t, resp, http.StatusCreated)
	if _, leaked := field(decode(t, resp), "invitation")["token"]; leaked {
		t.Fatal("the invitation's token reached the client")
	}
	return linkToken(t, lastEmailTo(t, strings.ToLower(email)), "/freelancer-invite/")
}

func lookupFreelancerInvite(t *testing.T, srv *httptest.Server, token string) *http.Response {
	t.Helper()
	return post(t, newClient(), srv.URL+"/api/v1/freelancer-invites/lookup", map[string]string{"token": token})
}

func acceptFreelancerInvite(t *testing.T, srv *httptest.Server, c *http.Client, token string, form map[string]string) *http.Response {
	t.Helper()
	body := map[string]string{"token": token}
	for k, v := range form {
		body[k] = v
	}
	return post(t, c, srv.URL+"/api/v1/freelancer-invites/accept", body)
}

func pendingFreelancerInvitations(t *testing.T, srv *httptest.Server, client *http.Client) []any {
	t.Helper()
	resp := get(t, client, srv.URL+"/api/v1/freelancer-invitations")
	wantStatus(t, resp, http.StatusOK)
	list, _ := decode(t, resp)["invitations"].([]any)
	return list
}

func TestFreelancerInvitation_NewFreelancerAcceptsAndIsLinked(t *testing.T) {
	srv, _ := newAPI(t)
	client := signUpClient(t, srv, "owner@acme.test")
	clientID := myID(t, srv, client)
	resp := do(t, client, http.MethodPut, srv.URL+"/api/v1/team", map[string]string{"name": "Acme Inc"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	token := inviteFreelancer(t, srv, client, "Dev@Example.com")
	if e := lastEmailTo(t, "dev@example.com"); !strings.Contains(e.Subject, "Olena from Acme Inc") {
		t.Fatalf("subject = %q, want the client and their company", e.Subject)
	}
	if list := pendingFreelancerInvitations(t, srv, client); len(list) != 1 || list[0].(map[string]any)["email"] != "dev@example.com" {
		t.Fatalf("pending = %v", list)
	}

	resp = lookupFreelancerInvite(t, srv, token)
	wantStatus(t, resp, http.StatusOK)
	inv := field(decode(t, resp), "invitation")
	if inv["email"] != "dev@example.com" || inv["name"] != "Oleh" || inv["account_exists"] != false ||
		inv["company"] != "Acme Inc" || field(inv, "client")["id"] != clientID {
		t.Fatalf("lookup = %v", inv)
	}

	freelancer := newClient()
	resp = acceptFreelancerInvite(t, srv, freelancer, token, map[string]string{
		"name": "Oleh Bondar", "password": "freelancersecret1", "password_confirmation": "freelancersecret1",
	})
	wantStatus(t, resp, http.StatusOK)
	if u := field(decode(t, resp), "user"); u["role"] != "freelancer" || u["name"] != "Oleh Bondar" {
		t.Fatalf("new account = %v", u)
	}

	resp = get(t, freelancer, srv.URL+"/api/v1/clients")
	wantStatus(t, resp, http.StatusOK)
	if list, _ := decode(t, resp)["clients"].([]any); len(list) != 1 || list[0].(map[string]any)["id"] != clientID {
		t.Fatalf("clients = %v", list)
	}
	resp = get(t, client, srv.URL+"/api/v1/freelancers")
	wantStatus(t, resp, http.StatusOK)
	if list, _ := decode(t, resp)["freelancers"].([]any); len(list) != 1 {
		t.Fatalf("freelancers = %v, want 1", list)
	}

	// The client hears it, and the work starts: a task for the freelancer.
	list, unread := notifications(t, srv, client)
	if unread != 1 || list[0].Kind != "freelancer_joined" || list[0].Actor != "dev@example.com" {
		t.Fatalf("client's notifications = %+v (unread %d)", list, unread)
	}
	newTask(t, srv, client, map[string]any{"freelancer_id": myID(t, srv, freelancer), "title": "Logo"})

	// The link is single-use.
	wantStatus(t, lookupFreelancerInvite(t, srv, token), http.StatusNotFound)
	if list := pendingFreelancerInvitations(t, srv, client); len(list) != 0 {
		t.Fatalf("pending = %v, want none", list)
	}
}

func TestFreelancerInvitation_ExistingFreelancerSignsInToAccept(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := signUpClient(t, srv, "owner@acme.test")

	token := inviteFreelancer(t, srv, client, "free@example.com")
	resp := lookupFreelancerInvite(t, srv, token)
	wantStatus(t, resp, http.StatusOK)
	if inv := field(decode(t, resp), "invitation"); inv["account_exists"] != true || inv["company"] != "" {
		t.Fatalf("lookup = %v", inv)
	}

	c := newClient()
	resp = acceptFreelancerInvite(t, srv, c, token, map[string]string{"password": "wrong-password1"})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()
	resp = acceptFreelancerInvite(t, srv, c, token, map[string]string{"password": "supersecret1"})
	wantStatus(t, resp, http.StatusOK)
	if u := field(decode(t, resp), "user"); u["role"] != "freelancer" || u["email"] != "free@example.com" {
		t.Fatalf("signed in as %v", u)
	}

	resp = get(t, freelancer, srv.URL+"/api/v1/clients")
	wantStatus(t, resp, http.StatusOK)
	if list, _ := decode(t, resp)["clients"].([]any); len(list) != 1 {
		t.Fatalf("clients = %v, want 1", list)
	}

	// Working together already: no second invitation.
	resp = post(t, client, srv.URL+"/api/v1/freelancer-invitations", map[string]string{"email": "free@example.com", "name": "Oleh"})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()
}

func TestFreelancerInvitation_Refusals(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := signUpClient(t, srv, "owner@acme.test")
	signUpClient(t, srv, "other@client.test")
	invitePath := srv.URL + "/api/v1/freelancer-invitations"

	inviteFreelancer(t, srv, client, "dev@example.com")
	// Yourself, another client, a pending invitation, not an email.
	for _, email := range []string{"owner@acme.test", "other@client.test", "dev@example.com", "not-an-email"} {
		resp := post(t, client, invitePath, map[string]string{"email": email, "name": "Oleh"})
		wantStatus(t, resp, http.StatusBadRequest)
		if fields := field(field(decode(t, resp), "error"), "fields"); fields["email"] == nil {
			t.Fatalf("%s: fields = %v, want an email error", email, fields)
		}
	}

	// Freelancers invite clients, not freelancers.
	resp := post(t, freelancer, invitePath, map[string]string{"email": "dev2@example.com", "name": "Oleh"})
	wantStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()

	// A company's member works with its freelancers but doesn't choose them.
	member := joinTeam(t, srv, client, "max@acme.test")
	resp = post(t, member, invitePath, map[string]string{"email": "dev2@example.com", "name": "Oleh"})
	wantStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()
	wantStatus(t, get(t, member, invitePath), http.StatusForbidden)
}

func TestFreelancerInvitation_Revoke(t *testing.T) {
	srv, _ := newAPI(t)
	client := signUpClient(t, srv, "owner@acme.test")
	other := signUpClient(t, srv, "other@client.test")
	token := inviteFreelancer(t, srv, client, "dev@example.com")
	id := pendingFreelancerInvitations(t, srv, client)[0].(map[string]any)["id"].(string)

	// Only its client revokes it.
	resp := do(t, other, http.MethodDelete, srv.URL+"/api/v1/freelancer-invitations/"+id, nil)
	wantStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()

	resp = do(t, client, http.MethodDelete, srv.URL+"/api/v1/freelancer-invitations/"+id, nil)
	wantStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
	wantStatus(t, lookupFreelancerInvite(t, srv, token), http.StatusNotFound)
}

func TestFreelancerInvitation_EmailTakenByAClientMeanwhile(t *testing.T) {
	srv, _ := newAPI(t)
	client := signUpClient(t, srv, "owner@acme.test")
	token := inviteFreelancer(t, srv, client, "dev@example.com")

	// The address signs up as a client before opening the link.
	signUpClient(t, srv, "dev@example.com")
	wantStatus(t, lookupFreelancerInvite(t, srv, token), http.StatusNotFound)
	resp := acceptFreelancerInvite(t, srv, newClient(), token, map[string]string{"password": "clientsecret1"})
	wantStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()
}
