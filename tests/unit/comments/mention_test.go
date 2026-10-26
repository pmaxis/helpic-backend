package comments_test

import (
	"slices"
	"testing"

	"github.com/pmaxis/helpic/internal/comments"
	"github.com/pmaxis/helpic/internal/tasks"
)

const (
	annaID = "0b6e2c1a-7f43-4c2e-9d55-3a1f6c8e2b10"
	maxID  = "5d9a4e7b-2c18-4b6f-8e03-9f7c1a2d4e65"
)

func TestMentions(t *testing.T) {
	body := "@[Anna](" + annaID + ") and @[Max](" + maxID + "), again @[Anna K](" + annaID + "); not @[x](nope) or [Anna](" + annaID + ")"
	if got := comments.Mentions(body); !slices.Equal(got, []string{annaID, maxID}) {
		t.Errorf("mentions = %v", got)
	}
	if got := comments.Plain(body); got != "@Anna and @Max, again @Anna K; not @[x](nope) or [Anna]("+annaID+")" {
		t.Errorf("plain = %q", got)
	}
}

func TestResolveMentions(t *testing.T) {
	readers := []tasks.Person{
		{ID: annaID, Name: "Anna  [Design]  Kovalenko"},
		{ID: maxID, Email: "max@example.com"},
	}
	body := "@[Annie](" + annaID + ") @[M](" + maxID + ") @[Outsider](9e1b3c5d-7a2f-4e68-b0d4-1c3e5a7b9d2f)"
	want := "@[Anna Design Kovalenko](" + annaID + ") @[max@example.com](" + maxID + ") @Outsider"
	if got := comments.ResolveMentions(body, readers); got != want {
		t.Errorf("resolved = %q", got)
	}
}

func TestExcerptShowsMentionsByName(t *testing.T) {
	if got := comments.Excerpt("@[Anna](" + annaID + ") can you check?"); got != "@Anna can you check?" {
		t.Errorf("excerpt = %q", got)
	}
}
