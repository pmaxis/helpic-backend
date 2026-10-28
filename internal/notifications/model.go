// Package notifications tells a user what the other side of their shared work
// did: added, moved, edited or deleted a task, started a project, accepted an
// invitation. Packages that make those changes call Notify; it never fails
// their request — a notification that can't be stored is logged and dropped.
package notifications

import (
	"errors"
	"time"
)

// Kinds of notification.
const (
	KindTaskCreated    = "task_created"
	KindTaskStatus     = "task_status"
	KindTaskUpdated    = "task_updated"
	KindTaskDeleted    = "task_deleted"
	KindProjectCreated = "project_created"
	KindClientJoined   = "client_joined"
	// KindFreelancerJoined: a freelancer accepted the client's invitation.
	KindFreelancerJoined = "freelancer_joined"
	// KindMemberJoined: someone joined the client's company.
	KindMemberJoined = "member_joined"
	KindReportSent   = "report_sent"
	KindReportPaid   = "report_paid"
	// KindAttachmentAdded: a file was added to a task; Detail is its name.
	KindAttachmentAdded = "attachment_added"
	// KindCommentAdded: a comment on a task; Detail is its start.
	KindCommentAdded = "comment_added"
	// KindCommentMention: a comment on a task mentions the user, who isn't
	// told about it as KindCommentAdded too; Detail is its start.
	KindCommentMention = "comment_mention"
	// KindTaskDueSoon: an open task is due tomorrow; KindTaskOverdue: it was
	// due yesterday. Detail is the due date (YYYY-MM-DD); there is no actor.
	KindTaskDueSoon = "task_due_soon"
	KindTaskOverdue = "task_overdue"
	// KindParticipantAdded tells someone they were added to a task — for a
	// contractor, that it's now theirs to see.
	KindParticipantAdded = "participant_added"
	// KindProjectInvited tells someone the project's author invited them, so
	// the project is now theirs to see.
	KindProjectInvited = "project_invited"
)

var ErrNotFound = errors.New("notification not found")

// Notification is one thing that happened, for one recipient.
type Notification struct {
	ID string
	// UserID receives the notification; ActorID did the thing, and is empty
	// for a reminder, which nobody did.
	UserID    string
	ActorID   string
	Kind      string
	TaskID    string
	ProjectID string
	ReportID  string
	// Subject is the task's or project's title, Detail extra context (the new
	// status), both as they were at the time.
	Subject   string
	Detail    string
	ReadAt    *time.Time
	CreatedAt time.Time

	// Actor's name and email, filled when listing.
	ActorName  string
	ActorEmail string
}
