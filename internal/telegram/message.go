package telegram

import (
	"fmt"
	"html"
	"net/url"

	"github.com/pmaxis/helpic/internal/notifications"
)

// message words a notification for Telegram, as the app's bell does, with a
// button to open what it is about.
func (s *Service) message(n notifications.Notification) (string, *Button) {
	who := n.ActorName
	if who == "" {
		who = n.ActorEmail
	}
	subject := "“" + html.EscapeString(n.Subject) + "”"
	detail := html.EscapeString(n.Detail)

	var what string
	switch n.Kind {
	case notifications.KindTaskCreated:
		what = "added a task " + subject
	case notifications.KindTaskStatus:
		what = fmt.Sprintf("moved %s to <b>%s</b>", subject, detail)
	case notifications.KindTaskUpdated:
		what = "edited " + subject
	case notifications.KindTaskDeleted:
		what = "deleted " + subject
	case notifications.KindProjectCreated:
		what = "started a project " + subject
	case notifications.KindClientJoined:
		what = "accepted your invitation"
	case notifications.KindMemberJoined:
		what = "joined your team"
	case notifications.KindReportSent:
		what = "sent you a report " + subject
	case notifications.KindReportPaid:
		what = "marked " + subject + " as paid 💸"
	case notifications.KindCommentAdded:
		what = "commented on " + subject
	case notifications.KindAttachmentAdded:
		what = fmt.Sprintf("attached “%s” to %s", detail, subject)
	default:
		what = "changed " + subject
	}

	text := "<b>" + html.EscapeString(who) + "</b> " + what
	if n.Kind == notifications.KindCommentAdded && n.Detail != "" {
		text += "\n\n<i>" + detail + "</i>"
	}
	return text, s.button(n)
}

// button opens what the notification is about in the app. Telegram refuses
// buttons to local addresses, so there is none in development.
func (s *Service) button(n notifications.Notification) *Button {
	u, err := url.Parse(s.appURL)
	if err != nil || u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "" {
		return nil
	}
	var path string
	switch {
	case n.ReportID != "":
		path = "/reports/" + n.ReportID
	case n.TaskID != "":
		path = "/tasks/" + n.TaskID
	case n.ProjectID != "":
		path = "/projects/" + n.ProjectID
	case n.Kind == notifications.KindClientJoined:
		path = "/clients"
	case n.Kind == notifications.KindMemberJoined:
		path = "/team"
	default:
		path = "/dashboard"
	}
	return &Button{Text: "Open in Helpic", URL: s.appURL + path}
}
