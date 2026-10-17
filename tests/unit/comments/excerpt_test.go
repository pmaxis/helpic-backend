package comments_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/pmaxis/helpic/internal/comments"
)

func TestExcerpt(t *testing.T) {
	if got := comments.Excerpt("  Looks good,\n\nship it  "); got != "Looks good, ship it" {
		t.Errorf("short = %q", got)
	}
	long := strings.Repeat("слово ", 40)
	got := comments.Excerpt(long)
	if utf8.RuneCountInString(got) > 120 || !strings.HasSuffix(got, "…") {
		t.Errorf("long = %q (%d runes)", got, utf8.RuneCountInString(got))
	}
}
