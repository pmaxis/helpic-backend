package integration

import (
	"net/http"
	"slices"
	"testing"
)

func TestComments(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	client := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	outsider := signUpFreelancer(t, srv, "other@example.com")
	task := newTask(t, srv, client, map[string]any{"freelancer_id": myID(t, srv, freelancer), "title": "Logo"})
	listURL := srv.URL + "/api/v1/comments?task=" + task["id"].(string)

	// Both sides talk.
	resp := post(t, client, listURL, map[string]string{"body": "  Can we try a darker blue?  "})
	wantStatus(t, resp, http.StatusCreated)
	first := field(decode(t, resp), "comment")
	if first["body"] != "Can we try a darker blue?" || first["can_edit"] != true || first["edited"] != false {
		t.Fatalf("comment = %v", first)
	}
	firstURL := srv.URL + "/api/v1/comments/" + first["id"].(string)
	resp = post(t, freelancer, listURL, map[string]string{"body": "Sure, two options by Friday"})
	wantStatus(t, resp, http.StatusCreated)
	resp.Body.Close()
	resp = post(t, freelancer, listURL, map[string]string{"body": "   "})
	wantStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()

	resp = get(t, freelancer, listURL)
	wantStatus(t, resp, http.StatusOK)
	list, _ := decode(t, resp)["comments"].([]any)
	if len(list) != 2 || list[0].(map[string]any)["can_edit"] != false || list[1].(map[string]any)["body"] != "Sure, two options by Friday" {
		t.Fatalf("freelancer sees = %v", list)
	}

	// The freelancer hears about the client's comment, with its text.
	notes, _ := notifications(t, srv, freelancer)
	found := false
	for _, n := range notes {
		if n.Kind == "comment_added" && n.Subject == "Logo" {
			found = true
		}
	}
	if !found {
		t.Fatalf("freelancer notifications = %+v", notes)
	}

	// Only the author edits or deletes.
	resp = do(t, freelancer, http.MethodPut, firstURL, map[string]string{"body": "Hijacked"})
	wantStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()
	resp = do(t, client, http.MethodPut, firstURL, map[string]string{"body": "Can we try a darker navy?"})
	wantStatus(t, resp, http.StatusOK)
	if got := field(decode(t, resp), "comment"); got["body"] != "Can we try a darker navy?" || got["edited"] != true {
		t.Fatalf("edited = %v", got)
	}
	resp = do(t, freelancer, http.MethodDelete, firstURL, nil)
	wantStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()
	resp = do(t, client, http.MethodDelete, firstURL, nil)
	wantStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()

	// Someone outside the task sees nothing and can't write.
	wantStatus(t, get(t, outsider, listURL), http.StatusNotFound)
	resp = post(t, outsider, listURL, map[string]string{"body": "Hi"})
	wantStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()
}

func TestCommentMentions(t *testing.T) {
	srv, _ := newAPI(t)
	freelancer := signUpFreelancer(t, srv, "free@example.com")
	owner := acceptNew(t, srv, invite(t, srv, freelancer, "client@example.com"))
	resp := do(t, owner, http.MethodPut, srv.URL+"/api/v1/team", map[string]string{"name": "Acme"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	member := joinTeam(t, srv, owner, "member@example.com")
	contractor := joinTeamAs(t, srv, owner, "contractor@example.com", "contractor")
	outsider := signUpFreelancer(t, srv, "other@example.com")
	freelancerID, ownerID, memberID, contractorID := myID(t, srv, freelancer), myID(t, srv, owner), myID(t, srv, member), myID(t, srv, contractor)
	resp = get(t, freelancer, srv.URL+"/api/v1/me")
	wantStatus(t, resp, http.StatusOK)
	freelancerName := field(decode(t, resp), "user")["name"].(string)
	task := newTask(t, srv, owner, map[string]any{"freelancer_id": freelancerID, "title": "Logo"})
	listURL := srv.URL + "/api/v1/comments?task=" + task["id"].(string)
	peopleURL := srv.URL + "/api/v1/comments/people?task=" + task["id"].(string)

	// Whoever sees the task can be mentioned: not the contractor, who isn't on it.
	resp = get(t, owner, peopleURL)
	wantStatus(t, resp, http.StatusOK)
	var ids []string
	for _, p := range decode(t, resp)["people"].([]any) {
		ids = append(ids, p.(map[string]any)["id"].(string))
	}
	if len(ids) != 3 || !slices.Contains(ids, freelancerID) || !slices.Contains(ids, ownerID) || !slices.Contains(ids, memberID) {
		t.Fatalf("people = %v", ids)
	}
	wantStatus(t, get(t, outsider, peopleURL), http.StatusNotFound)

	// Mentions keep the current names; one of someone who can't see the task is plain text.
	resp = post(t, owner, listURL, map[string]string{
		"body": "@[F](" + freelancerID + ") and @[M](" + memberID + "), please look. cc @[C](" + contractorID + ")",
	})
	wantStatus(t, resp, http.StatusCreated)
	want := "@[" + freelancerName + "](" + freelancerID + ") and @[Max Member](" + memberID + "), please look. cc @C"
	if got := field(decode(t, resp), "comment")["body"]; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}

	// The mentioned hear they were, once; the contractor hears nothing.
	count := func(c *http.Client, kind string) int {
		notes, _ := notifications(t, srv, c)
		n := 0
		for _, x := range notes {
			if x.Kind == kind {
				n++
				if x.Detail != "@"+freelancerName+" and @Max Member, please look. cc @C" {
					t.Errorf("detail = %q", x.Detail)
				}
			}
		}
		return n
	}
	for _, c := range []*http.Client{freelancer, member} {
		if count(c, "comment_mention") != 1 || count(c, "comment_added") != 0 {
			t.Fatal("a mentioned person wasn't told once, as a mention")
		}
	}
	if notes, _ := notifications(t, srv, contractor); len(notes) != 0 {
		t.Fatalf("contractor was told: %+v", notes)
	}

	// Editing tells only whoever is mentioned for the first time.
	resp = post(t, freelancer, listURL, map[string]string{"body": "Will do"})
	wantStatus(t, resp, http.StatusCreated)
	replyURL := srv.URL + "/api/v1/comments/" + field(decode(t, resp), "comment")["id"].(string)
	resp = do(t, freelancer, http.MethodPut, replyURL, map[string]string{"body": "Will do, @[Owner](" + ownerID + ") @[Me](" + freelancerID + ")"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	resp = do(t, freelancer, http.MethodPut, replyURL, map[string]string{"body": "Will do, @[Owner](" + ownerID + ") — by Friday"})
	wantStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	notes, _ := notifications(t, srv, owner)
	mentions := 0
	for _, n := range notes {
		if n.Kind == "comment_mention" {
			mentions++
		}
	}
	if mentions != 1 {
		t.Fatalf("owner mentions = %d: %+v", mentions, notes)
	}
	if count(freelancer, "comment_mention") != 1 {
		t.Fatal("mentioning yourself told you")
	}
}
