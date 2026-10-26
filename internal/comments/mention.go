package comments

import (
	"regexp"
	"slices"
	"strings"

	"github.com/pmaxis/helpic/internal/tasks"
)

// A mention is written into a comment as @[Name](user ID): the ID says who,
// the name is how they were called when it was written, for the comment's
// text read outside the app (a notification's excerpt, an email).
var mentionPattern = regexp.MustCompile(`@\[([^\[\]\n]{1,200})\]\(([0-9a-fA-F-]{36})\)`)

// Mentions returns the IDs mentioned in the text, each once, in order.
func Mentions(body string) []string {
	var out []string
	for _, m := range mentionPattern.FindAllStringSubmatch(body, -1) {
		if id := strings.ToLower(m[2]); !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// Plain is the text with each mention as @Name.
func Plain(body string) string {
	return mentionPattern.ReplaceAllString(body, "@$1")
}

// ResolveMentions keeps the mentions of people who see the task, under their
// name as it is now; anyone else's becomes plain text, mentioning nobody.
func ResolveMentions(body string, readers []tasks.Person) string {
	return mentionPattern.ReplaceAllStringFunc(body, func(s string) string {
		m := mentionPattern.FindStringSubmatch(s)
		i := slices.IndexFunc(readers, func(p tasks.Person) bool { return strings.EqualFold(p.ID, m[2]) })
		if i < 0 {
			return "@" + m[1]
		}
		return "@[" + mentionName(readers[i]) + "](" + readers[i].ID + ")"
	})
}

// mentionName is how a mention calls the person: their name, or their email
// when they have none, without the brackets that would end the mention.
func mentionName(p tasks.Person) string {
	clean := strings.NewReplacer("[", "", "]", "")
	name := clean.Replace(strings.Join(strings.Fields(p.Name), " "))
	if name == "" {
		name = clean.Replace(p.Email)
	}
	if r := []rune(name); len(r) > 200 {
		name = string(r[:200])
	}
	return name
}
