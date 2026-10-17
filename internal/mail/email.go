// Package mail sends email. Every email has the same shape — a heading, a
// few paragraphs, maybe a button, small print — so one layout renders them
// all, as HTML with a plain-text twin. Outgoing email goes through an outbox
// table: a request only stores it, a background job sends it and tries again
// when the mail server fails. Without SMTP settings nothing is sent and the
// job just logs what would have been.
package mail

import (
	"bytes"
	"embed"
	"fmt"
	htmltemplate "html/template"
	texttemplate "text/template"

	"github.com/pmaxis/helpic/internal/i18n"
)

// Email is what to say to someone, already in their language.
type Email struct {
	To string
	// Locale is the email's language, for the layout's own words.
	Locale  string
	Subject string
	Heading string
	// Paragraphs are plain text; each becomes a paragraph.
	Paragraphs []string
	Button     *Button
	// Note is small print under the button, e.g. when the link stops working.
	Note string
}

// Button is a link that does the email's one thing.
type Button struct {
	Text string
	URL  string
}

// Message is an email rendered and ready to send.
type Message struct {
	To      string
	Subject string
	HTML    string
	Text    string
}

//go:embed templates
var templates embed.FS

var (
	htmlLayout = htmltemplate.Must(htmltemplate.ParseFS(templates, "templates/layout.html"))
	textLayout = texttemplate.Must(texttemplate.ParseFS(templates, "templates/layout.txt"))
)

// page is what the layout reads: the email and the layout's own words.
type page struct {
	Email
	OrOpen string
	Footer string
}

// Render turns the email into HTML and plain text.
func Render(e Email) (Message, error) {
	p := page{
		Email:  e,
		OrOpen: i18n.T(e.Locale, "Or open this link:"),
		Footer: i18n.T(e.Locale, "You got this email because of your Helpic account. Freelancers and their clients, on the same page."),
	}
	var h, t bytes.Buffer
	if err := htmlLayout.Execute(&h, p); err != nil {
		return Message{}, fmt.Errorf("render email html: %w", err)
	}
	if err := textLayout.Execute(&t, p); err != nil {
		return Message{}, fmt.Errorf("render email text: %w", err)
	}
	return Message{To: e.To, Subject: e.Subject, HTML: h.String(), Text: t.String()}, nil
}
