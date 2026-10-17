package notifications

import (
	"time"

	"github.com/pmaxis/helpic/internal/i18n"
)

// Kinds are every kind of notification, as the channels outside the app
// (Telegram, email) offer them.
var Kinds = []string{
	KindTaskCreated,
	KindTaskStatus,
	KindTaskUpdated,
	KindTaskDeleted,
	KindTaskDueSoon,
	KindTaskOverdue,
	KindCommentAdded,
	KindAttachmentAdded,
	KindProjectCreated,
	KindReportSent,
	KindReportPaid,
	KindClientJoined,
	KindMemberJoined,
}

// DefaultKinds are the important ones, on in a channel until the user says
// otherwise: new work, its progress and deadlines, the discussion, money and
// new people. Edits, deleted tasks, files and new projects stay in the app.
var DefaultKinds = []string{
	KindTaskCreated,
	KindTaskStatus,
	KindTaskDueSoon,
	KindTaskOverdue,
	KindCommentAdded,
	KindReportSent,
	KindReportPaid,
	KindClientJoined,
	KindMemberJoined,
}

// Path is where in the app the notification leads: what it is about.
func Path(n Notification) string {
	switch {
	case n.ReportID != "":
		return "/reports/" + n.ReportID
	case n.TaskID != "":
		return "/tasks/" + n.TaskID
	case n.ProjectID != "":
		return "/projects/" + n.ProjectID
	case n.Kind == KindClientJoined:
		return "/clients"
	case n.Kind == KindMemberJoined:
		return "/team"
	default:
		return "/dashboard"
	}
}

// Describe is the notification as one plain sentence in the locale, worded
// like the app's bell: "Anna moved “Logo” to Done", "“Logo” is due tomorrow".
func Describe(n Notification, locale string) string {
	who := n.ActorName
	if who == "" {
		who = n.ActorEmail
	}
	return Phrase(n, locale, who, "“"+n.Subject+"”", Detail(n, locale))
}

// defaultColumns are the names Helpic gives the columns it makes.
var defaultColumns = map[string]bool{"New": true, "In progress": true, "Done": true, "Cancelled": true, "Paid": true}

// Detail is the notification's detail as shown: a status change names a
// column, and a column Helpic made by default is named in the locale; one the
// user named stays as they wrote it.
func Detail(n Notification, locale string) string {
	if n.Kind == KindTaskStatus && defaultColumns[n.Detail] {
		return i18n.T(locale, n.Detail)
	}
	return n.Detail
}

// Phrase words the notification in the locale from pieces the caller has
// already dressed (escaped, made bold): who did it, the quoted subject, and
// the detail (the new status, the file's name). Reminders need no who.
func Phrase(n Notification, locale, who, subject, detail string) string {
	t := func(text string, args ...any) string { return i18n.T(locale, text, args...) }
	switch n.Kind {
	case KindTaskDueSoon:
		return t("%s is due tomorrow", subject)
	case KindTaskOverdue:
		return t("%s is overdue: it was due %s", subject, DueDate(locale, n.Detail))
	case KindTaskCreated:
		return t("%s added a task %s", who, subject)
	case KindTaskStatus:
		return t("%s moved %s to %s", who, subject, detail)
	case KindTaskUpdated:
		return t("%s edited %s", who, subject)
	case KindTaskDeleted:
		return t("%s deleted %s", who, subject)
	case KindProjectCreated:
		return t("%s started a project %s", who, subject)
	case KindClientJoined:
		return t("%s accepted your invitation", who)
	case KindMemberJoined:
		return t("%s joined your team", who)
	case KindReportSent:
		return t("%s sent you a report %s", who, subject)
	case KindReportPaid:
		return t("%s marked %s as paid", who, subject)
	case KindCommentAdded:
		return t("%s commented on %s", who, subject)
	case KindAttachmentAdded:
		return t("%s attached “%s” to %s", who, detail, subject)
	default:
		return t("%s changed %s", who, subject)
	}
}

// DueDate shows a reminder's YYYY-MM-DD due date: "17 Oct 2026", "17 жовтня 2026".
func DueDate(locale, iso string) string {
	d, err := time.Parse(time.DateOnly, iso)
	if err != nil {
		return iso
	}
	return i18n.Date(locale, d)
}
