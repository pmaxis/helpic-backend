package telegram

import (
	"html"
	"net/url"

	"github.com/pmaxis/helpic/internal/i18n"
	"github.com/pmaxis/helpic/internal/notifications"
)

// message words a notification for Telegram in the locale, as the app's bell
// does, with a button to open what it is about.
func (s *Service) message(n notifications.Notification, locale string) (string, *Button) {
	who := n.ActorName
	if who == "" {
		who = n.ActorEmail
	}
	text := notifications.Phrase(n, locale,
		"<b>"+html.EscapeString(who)+"</b>",
		"“"+html.EscapeString(n.Subject)+"”",
		html.EscapeString(notifications.Detail(n, locale)))
	switch n.Kind {
	case notifications.KindTaskDueSoon:
		text = "⏰ " + text
	case notifications.KindTaskOverdue:
		text = "⚠️ " + text
	case notifications.KindReportPaid:
		text += " 💸"
	case notifications.KindCommentAdded:
		if n.Detail != "" {
			text += "\n\n<i>" + html.EscapeString(n.Detail) + "</i>"
		}
	}
	return text, s.button(n, locale)
}

// button opens what the notification is about in the app. Telegram refuses
// buttons to local addresses, so there is none in development.
func (s *Service) button(n notifications.Notification, locale string) *Button {
	u, err := url.Parse(s.appURL)
	if err != nil || u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "" {
		return nil
	}
	return &Button{Text: i18n.T(locale, "Open in Helpic"), URL: s.appURL + notifications.Path(n)}
}
