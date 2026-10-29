package tasks

import (
	"context"
	"log/slog"
	"slices"
	"time"

	"github.com/pmaxis/helpic/internal/notifications"

	"github.com/pmaxis/helpic/internal/user"
)

// notifier tells the other side of a task what happened to it. Implemented
// by *notifications.Service.
type notifier interface {
	Notify(ctx context.Context, n notifications.Notification)
}

// DeleteHook runs before a task is deleted; the func it returns runs once
// the task is gone (e.g. to remove the task's files).
type DeleteHook func(ctx context.Context, taskID string) (afterDelete func())

// Service holds the task rules: who may create, edit, delete and move a task.
type Service struct {
	repo     *Repository
	notify   notifier
	onDelete []DeleteHook
	loc      *time.Location
}

// UseLocation sets the time zone a task's creation day is read in, against
// its due date; UTC without it.
func (s *Service) UseLocation(loc *time.Location) {
	s.loc = loc
}

// OnDelete registers a hook run around deleting a task.
func (s *Service) OnDelete(h DeleteHook) {
	s.onDelete = append(s.onDelete, h)
}

// NewService creates a tasks Service.
func NewService(repo *Repository, notify notifier) *Service {
	return &Service{repo: repo, notify: notify}
}

// Tell notifies the other side of the task — nobody for a personal task. The
// client side is the company's owner, plus their people on the task (who set
// it, were added or took part) while they're still in the company. Anyone in
// skip is left out: they hear about it some other way.
func (s *Service) Tell(ctx context.Context, actorID string, t Task, kind, detail string, withTask bool, skip ...string) {
	if t.Client == nil {
		return
	}
	recipients := []string{}
	if t.Freelancer.ID != "" && actorID != t.Freelancer.ID {
		recipients = append(recipients, t.Freelancer.ID)
	}
	if actorID == t.Freelancer.ID || t.Freelancer.ID == "" {
		recipients = []string{t.Client.ID}
		people, err := s.repo.ClientSideParticipants(ctx, t.ID, t.Client.ID)
		if err != nil {
			slog.Error("failed to find who to notify", "task_id", t.ID, "error", err)
		}
		recipients = append(recipients, people...)
	}
	recipients = slices.DeleteFunc(recipients, func(id string) bool { return slices.Contains(skip, id) })
	s.TellPeople(ctx, actorID, t, recipients, kind, detail, withTask)
}

// TellPeople notifies the given people about the task.
func (s *Service) TellPeople(ctx context.Context, actorID string, t Task, to []string, kind, detail string, withTask bool) {
	for _, id := range to {
		n := notifications.Notification{UserID: id, ActorID: actorID, Kind: kind, Subject: t.Title, Detail: detail}
		if withTask {
			n.TaskID = t.ID
		}
		if t.Project != nil {
			n.ProjectID = t.Project.ID
		}
		s.notify.Notify(ctx, n)
	}
}

// Readers lists everyone who sees the task: its freelancer, and on the
// client's side the owner and the company's people who see it (a contractor
// only once they're on it). A personal task is its owner's alone.
func (s *Service) Readers(ctx context.Context, t Task) ([]Person, error) {
	return s.repo.Readers(ctx, t.ID)
}

// People lists who on the task's client side can be on it, for the viewer to
// pick from; a personal task has nobody. The freelancers who could take the
// task are only for whoever picks its freelancer: anyone else, the freelancer
// first, sees none but the task's own.
func (s *Service) People(ctx context.Context, userID, id string) ([]Person, error) {
	t, err := s.repo.Get(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	if t.Client == nil {
		return []Person{}, nil
	}
	people, err := s.repo.ClientPeople(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	assigns, err := s.canAssign(ctx, t, userID)
	if err != nil || assigns {
		return people, err
	}
	return slices.DeleteFunc(people, func(p Person) bool { return p.Role == "freelancer" && p.ID != t.Freelancer.ID }), nil
}

// canChangePeople reports whether the user may change who is on the task:
// anyone who sees it, except a contractor on a task they didn't set.
func (s *Service) canChangePeople(ctx context.Context, t Task, userID string) (bool, error) {
	if userID == t.Freelancer.ID || userID == t.CreatedBy {
		return true, nil
	}
	contractor, err := s.repo.IsContractor(ctx, userID)
	return !contractor, err
}

// AddParticipant puts someone from the task's client side on it; they're
// told, and a contractor sees the task from now on.
func (s *Service) AddParticipant(ctx context.Context, userID, id, personID string) (Task, error) {
	t, err := s.repo.Get(ctx, userID, id)
	if err != nil {
		return Task{}, err
	}
	if t.Archived() {
		return Task{}, ErrArchived
	}
	if ok, err := s.canChangePeople(ctx, t, userID); err != nil || !ok {
		if err == nil {
			err = ErrPeopleNotAllowed
		}
		return Task{}, err
	}
	people, err := s.People(ctx, userID, id)
	if err != nil {
		return Task{}, err
	}
	i := slices.IndexFunc(people, func(p Person) bool { return p.ID == personID })
	if i < 0 || people[i].Role == "freelancer" {
		return Task{}, ErrNotClientSide
	}
	if people[i].OnTask {
		return t, nil
	}
	if err := s.repo.AddParticipant(ctx, t.ID, personID); err != nil {
		return Task{}, err
	}
	if personID != userID {
		n := notifications.Notification{UserID: personID, ActorID: userID, Kind: notifications.KindParticipantAdded, TaskID: t.ID, Subject: t.Title}
		if t.Project != nil {
			n.ProjectID = t.Project.ID
		}
		s.notify.Notify(ctx, n)
	}
	return s.repo.Get(ctx, userID, id)
}

// canAssign reports whether the user may pick the task's freelancer: its
// client and their company, except a contractor on a task they didn't set.
// The freelancer doesn't hand their work to someone else.
func (s *Service) canAssign(ctx context.Context, t Task, userID string) (bool, error) {
	acting, err := s.repo.ActingClient(ctx, userID)
	if err != nil || acting != t.Client.ID {
		return false, err
	}
	if userID == t.CreatedBy {
		return true, nil
	}
	contractor, err := s.repo.IsContractor(ctx, userID)
	return !contractor, err
}

// AssignFreelancer makes a freelancer the task's assignee, or clears the
// assignee when freelancerID is empty. The creator stays a participant either
// way. A personal task keeps its freelancer, and so does a task outside any
// project: its board is its pair's. A new freelancer starts without a price:
// the old one was the previous freelancer's, in their currency. By the hour,
// the time is priced at the new freelancer's rate.
func (s *Service) AssignFreelancer(ctx context.Context, userID, id, freelancerID string) (Task, error) {
	t, err := s.repo.Get(ctx, userID, id)
	if err != nil {
		return Task{}, err
	}
	if t.Archived() {
		return Task{}, ErrArchived
	}
	if t.Client == nil {
		return Task{}, ErrKeepParticipant
	}
	if ok, err := s.canAssign(ctx, t, userID); err != nil || !ok {
		if err == nil {
			err = ErrAssignNotAllowed
		}
		return Task{}, err
	}
	if freelancerID == t.Freelancer.ID {
		return t, nil
	}
	if freelancerID == "" && t.Project == nil {
		return Task{}, ErrNeedsFreelancer
	}
	if freelancerID != "" {
		if t.Project != nil {
			on, err := s.repo.IsFreelancerParticipant(ctx, t.Project.ID, freelancerID)
			if err != nil {
				return Task{}, err
			}
			if !on {
				return Task{}, ErrFreelancerNotOnProject
			}
		} else {
			linked, err := s.repo.Linked(ctx, freelancerID, t.Client.ID)
			if err != nil {
				return Task{}, err
			}
			if !linked {
				return Task{}, ErrNotLinked
			}
		}
	}
	if err := s.repo.AssignFreelancer(ctx, t.ID, freelancerID); err != nil {
		return Task{}, err
	}
	// An hourly task is priced again at the new freelancer's rate.
	if t.PriceCents != nil && t.Billing == BillingFixed {
		data := change(centsOrNil(t.PriceCents), nil)
		data["currency"] = t.Currency
		s.Record(ctx, t.ID, userID, EventPrice, data)
	}
	if freelancerID != "" && freelancerID != userID {
		n := notifications.Notification{UserID: freelancerID, ActorID: userID, Kind: notifications.KindParticipantAdded, TaskID: t.ID, Subject: t.Title}
		if t.Project != nil {
			n.ProjectID = t.Project.ID
		}
		s.notify.Notify(ctx, n)
	}
	return s.repo.Get(ctx, userID, id)
}

// RemoveParticipant takes someone off the task. Whoever may add people may,
// and anyone may take themselves off; the freelancer and the creator stay.
// A contractor taken off no longer sees the task.
func (s *Service) RemoveParticipant(ctx context.Context, userID, id, personID string) error {
	t, err := s.repo.Get(ctx, userID, id)
	if err != nil {
		return err
	}
	if t.Archived() {
		return ErrArchived
	}
	if personID == t.Freelancer.ID || personID == t.CreatedBy {
		return ErrKeepParticipant
	}
	if personID != userID {
		if ok, err := s.canChangePeople(ctx, t, userID); err != nil || !ok {
			if err == nil {
				err = ErrPeopleNotAllowed
			}
			return err
		}
	}
	return s.repo.RemoveParticipant(ctx, t.ID, personID)
}

// List returns the user's tasks.
func (s *Service) List(ctx context.Context, userID, role string, f Filter) ([]Task, error) {
	return s.repo.List(ctx, userID, role, f)
}

// Get returns a task the user takes part in.
func (s *Service) Get(ctx context.Context, userID, id string) (Task, error) {
	return s.repo.Get(ctx, userID, id)
}

// Create adds a task between the user and someone they work with: a client
// names the freelancer (p.FreelancerID), a freelancer names the client
// (p.ClientID) or leaves it empty for a personal task. A task in a project
// takes that project's client, and a freelancer who is on the project. A
// subtask (p.ParentID) takes its parent's client and project, and by default
// its freelancer.
func (s *Service) Create(ctx context.Context, userID, role string, p Params) (Task, error) {
	var parent *Task
	if p.ParentID != "" {
		t, err := s.repo.Get(ctx, userID, p.ParentID)
		if err != nil {
			return Task{}, err
		}
		if t.Archived() {
			return Task{}, ErrArchived
		}
		if t.IsSubtask() {
			return Task{}, ErrNestedSubtask
		}
		parent = &t
		p.ParentID = t.ID
		if t.Client == nil {
			p.Personal = true
		} else {
			p.ClientID = t.Client.ID
			if p.FreelancerID == "" {
				p.FreelancerID = t.Freelancer.ID
			}
		}
		if t.Project != nil {
			p.ProjectID = &t.Project.ID
		}
	}
	freelancerID, clientID := p.FreelancerID, userID
	if p.Personal {
		// Anyone's own task: its owner stands where the freelancer would.
		freelancerID, clientID = userID, ""
	} else if role == user.RoleFreelancer {
		freelancerID, clientID = userID, p.ClientID
	} else {
		// A company member sets tasks for the company: its owner is the client.
		acting, err := s.repo.ActingClient(ctx, userID)
		if err != nil {
			return Task{}, err
		}
		clientID = acting
	}
	projectID := deref(p.ProjectID)
	if projectID != "" {
		pc, err := s.repo.ProjectClient(ctx, userID, projectID)
		if err != nil {
			return Task{}, err
		}
		clientID = deref(pc)
		if role == user.RoleFreelancer {
			freelancerID = userID
		} else if p.FreelancerID != "" {
			freelancerID = p.FreelancerID
		} else {
			ids, err := s.repo.FreelancerParticipants(ctx, projectID)
			if err != nil {
				return Task{}, err
			}
			switch len(ids) {
			case 0:
				return Task{}, ErrNoProjectFreelancer
			case 1:
				freelancerID = ids[0]
			default:
				return Task{}, ErrChooseFreelancer
			}
		}
		on, err := s.repo.IsFreelancerParticipant(ctx, projectID, freelancerID)
		if err != nil {
			return Task{}, err
		}
		if !on {
			return Task{}, ErrFreelancerNotOnProject
		}
	}

	// Straight into a board column: it must be on this task's board. The project's
	// author and the freelancer may use any status; everyone else only one
	// that isn't done.
	if p.StatusID != "" {
		st, err := s.repo.Status(ctx, p.StatusID)
		if err != nil {
			return Task{}, err
		}
		if !statusOnTask(st, freelancerID, clientID, projectID) {
			return Task{}, ErrStatusMismatch
		}
		manager := role == user.RoleFreelancer
		if !manager && projectID != "" {
			manager, err = s.repo.IsProjectAuthor(ctx, userID, projectID)
			if err != nil {
				return Task{}, err
			}
		}
		if !MayEnter(clientID == "", false, manager, st.ParticipantsCanMove) {
			return Task{}, ErrTransition
		}
	}

	var (
		id  string
		err error
	)
	if clientID == "" {
		id, err = s.repo.CreatePersonal(ctx, freelancerID, projectID, p.StatusID, p)
	} else {
		id, err = s.repo.Create(ctx, freelancerID, clientID, userID, projectID, p.StatusID, p)
	}
	if err != nil {
		return Task{}, err
	}
	t, err := s.repo.Get(ctx, userID, id)
	if err == nil {
		s.Record(ctx, t.ID, userID, EventCreated, nil)
		if parent != nil {
			s.Record(ctx, parent.ID, userID, EventSubtaskAdded, map[string]any{"title": t.Title})
		}
		s.Tell(ctx, userID, t, notifications.KindTaskCreated, "", true)
	}
	return t, err
}

// Subtasks returns the subtasks of a task the user sees: the ones they see
// too, archived ones included.
func (s *Service) Subtasks(ctx context.Context, userID, id string) ([]Task, error) {
	t, err := s.repo.Get(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	return s.repo.Subtasks(ctx, userID, t.ID)
}

// Archive puts the user's own personal task away for good: it leaves the
// boards and nothing on it changes any more. Shared work is archived when
// its report is paid (see the reports package).
func (s *Service) Archive(ctx context.Context, userID, id string) (Task, error) {
	t, err := s.repo.Get(ctx, userID, id)
	if err != nil {
		return Task{}, err
	}
	if t.Archived() {
		return Task{}, ErrArchived
	}
	if !CanArchive(t, userID) {
		return Task{}, ErrArchiveNotAllowed
	}
	subtasks, err := s.repo.Archive(ctx, t.ID)
	if err != nil {
		return Task{}, err
	}
	s.Record(ctx, t.ID, userID, EventArchived, nil)
	for _, id := range subtasks {
		s.Record(ctx, id, userID, EventArchived, nil)
	}
	return s.repo.Get(ctx, userID, id)
}

// ListArchive returns a page of the archived tasks the user sees and how many there are in all.
func (s *Service) ListArchive(ctx context.Context, userID, role string, f ArchiveFilter) ([]Task, int, error) {
	return s.repo.ListArchive(ctx, userID, role, f)
}

// Update edits a task's content. Its creator or its freelancer may, in any
// status.
func (s *Service) Update(ctx context.Context, userID, id string, p Params) (Task, error) {
	t, err := s.repo.Get(ctx, userID, id)
	if err != nil {
		return Task{}, err
	}
	if t.Archived() {
		return Task{}, ErrArchived
	}
	if !CanEdit(t, userID) {
		return Task{}, ErrEditNotAllowed
	}
	// A due date that moves can't go before the day the task was created; a
	// moved creation date was checked against it already. A task made with
	// a past due date keeps it until the due date changes.
	if p.CreatedAt == nil && p.DueOn != nil && isoDate(p.DueOn) != isoDate(t.DueOn) {
		loc := s.loc
		if loc == nil {
			loc = time.UTC
		}
		if t.CreatedAt.In(loc).Format(dateLayout) > p.DueOn.Format(dateLayout) {
			return Task{}, ErrDueBeforeCreated
		}
	}

	// A nil ProjectID keeps the current project; "" takes the task out of it,
	// and its open subtasks follow.
	var subtasks []OpenSubtask
	projectID := ""
	if t.Project != nil {
		projectID = t.Project.ID
	}
	if p.ProjectID != nil && *p.ProjectID != projectID {
		if t.IsSubtask() {
			return Task{}, ErrSubtaskProject
		}
		projectID = *p.ProjectID
		if projectID != "" {
			pc, err := s.repo.ProjectClient(ctx, userID, projectID)
			if err != nil {
				return Task{}, err
			}
			taskClient := ""
			if t.Client != nil {
				taskClient = t.Client.ID
			}
			if deref(pc) != taskClient {
				return Task{}, ErrProjectMismatch
			}
			if t.Freelancer.ID != "" {
				on, err := s.repo.IsFreelancerParticipant(ctx, projectID, t.Freelancer.ID)
				if err != nil {
					return Task{}, err
				}
				if !on {
					return Task{}, ErrFreelancerNotOnProject
				}
			}
		} else if t.Freelancer.ID == "" {
			return Task{}, ErrNeedsFreelancer
		}
		subtasks, err = s.repo.OpenSubtasks(ctx, t.ID)
		if err != nil {
			return Task{}, err
		}
		if err := s.subtasksMayMove(ctx, subtasks, projectID); err != nil {
			return Task{}, err
		}
	}
	if err := s.repo.Update(ctx, id, projectID, p); err != nil {
		return Task{}, err
	}
	before := t
	t, err = s.repo.Get(ctx, userID, id)
	if err == nil {
		s.recordEdit(ctx, userID, before, t)
		if from, to := projectName(before.Project), projectName(t.Project); from != to {
			for _, sub := range subtasks {
				s.Record(ctx, sub.ID, userID, EventProject, change(from, to))
			}
		}
		s.Tell(ctx, userID, t, notifications.KindTaskUpdated, "", true)
	}
	return t, err
}

// subtasksMayMove checks that a task's open subtasks can follow it to a
// project ("" = out of projects): each one's freelancer must be on it, and
// outside a project each one needs a freelancer.
func (s *Service) subtasksMayMove(ctx context.Context, subtasks []OpenSubtask, projectID string) error {
	for _, sub := range subtasks {
		if sub.FreelancerID == "" {
			if projectID == "" {
				return ErrNeedsFreelancer
			}
			continue
		}
		if projectID == "" {
			continue
		}
		on, err := s.repo.IsFreelancerParticipant(ctx, projectID, sub.FreelancerID)
		if err != nil {
			return err
		}
		if !on {
			return ErrFreelancerNotOnProject
		}
	}
	return nil
}

// SetPrice sets how the task is billed and, at a fixed price, sets or clears
// the price; by the hour the price follows the time. Only its freelancer
// may, in any status: the price is what they bill for the work.
func (s *Service) SetPrice(ctx context.Context, userID, id, billing string, cents *int64) (Task, error) {
	t, err := s.repo.Get(ctx, userID, id)
	if err != nil {
		return Task{}, err
	}
	if t.Archived() {
		return Task{}, ErrArchived
	}
	if !CanSetPrice(t, userID) {
		return Task{}, ErrPriceNotAllowed
	}
	if err := s.repo.SetPrice(ctx, id, billing, cents); err != nil {
		return Task{}, err
	}
	before := t
	t, err = s.repo.Get(ctx, userID, id)
	if err == nil && before.Billing != t.Billing {
		s.Record(ctx, t.ID, userID, EventBilling, change(before.Billing, t.Billing))
	}
	// By the hour the price follows the time from now on: the switch is the event.
	if err == nil && t.Billing == BillingFixed && !samePrice(before.PriceCents, t.PriceCents) {
		data := change(centsOrNil(before.PriceCents), centsOrNil(t.PriceCents))
		data["currency"] = t.Currency
		s.Record(ctx, t.ID, userID, EventPrice, data)
		s.Tell(ctx, userID, t, notifications.KindTaskUpdated, "", true)
	}
	return t, err
}

// SetPriority sets how urgent the task is. Either side may, in any status:
// the client flags what's pressing, the freelancer plans the work.
func (s *Service) SetPriority(ctx context.Context, userID, id, priority string) (Task, error) {
	t, err := s.repo.Get(ctx, userID, id)
	if err != nil {
		return Task{}, err
	}
	if t.Archived() {
		return Task{}, ErrArchived
	}
	if t.Priority == priority {
		return t, nil
	}
	if err := s.repo.SetPriority(ctx, id, priority); err != nil {
		return Task{}, err
	}
	before := t
	t, err = s.repo.Get(ctx, userID, id)
	if err == nil {
		s.Record(ctx, t.ID, userID, EventPriority, change(before.Priority, t.Priority))
		s.Tell(ctx, userID, t, notifications.KindTaskUpdated, "", true)
	}
	return t, err
}

// SetLabels replaces the task's labels with labels of its pair's board.
// Either side may, in any status.
func (s *Service) SetLabels(ctx context.Context, userID, id string, labelIDs []string) (Task, error) {
	t, err := s.repo.Get(ctx, userID, id)
	if err != nil {
		return Task{}, err
	}
	if t.Archived() {
		return Task{}, ErrArchived
	}
	if err := s.repo.SetLabels(ctx, id, labelIDs); err != nil {
		return Task{}, err
	}
	before := t
	t, err = s.repo.Get(ctx, userID, id)
	if err == nil {
		if added, removed := labelDiff(before.Labels, t.Labels); len(added)+len(removed) > 0 {
			s.Record(ctx, t.ID, userID, EventLabels, map[string]any{"added": added, "removed": removed})
		}
	}
	return t, err
}

// labelDiff names the labels added and removed between two sets.
func labelDiff(before, after []Label) (added, removed []string) {
	added, removed = []string{}, []string{}
	has := func(list []Label, id string) bool {
		return slices.ContainsFunc(list, func(l Label) bool { return l.ID == id })
	}
	for _, l := range after {
		if !has(before, l.ID) {
			added = append(added, l.Name)
		}
	}
	for _, l := range before {
		if !has(after, l.ID) {
			removed = append(removed, l.Name)
		}
	}
	return added, removed
}

// Delete removes a task its creator added by mistake: any time before it's
// done, with its comments, files and time.
func (s *Service) Delete(ctx context.Context, userID, id string) error {
	t, err := s.repo.Get(ctx, userID, id)
	if err != nil {
		return err
	}
	if t.Archived() {
		return ErrArchived
	}
	// Its subtasks would go with it, the ones the user doesn't see too.
	if has, err := s.repo.HasSubtasks(ctx, t.ID); err != nil || has {
		if err == nil {
			err = ErrHasSubtasks
		}
		return err
	}
	if !CanDelete(t, userID) {
		return ErrNotEditable
	}
	after := make([]func(), 0, len(s.onDelete))
	for _, h := range s.onDelete {
		after = append(after, h(ctx, id))
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		return err
	}
	for _, f := range after {
		f()
	}
	if t.IsSubtask() {
		s.Record(ctx, t.ParentID, userID, EventSubtaskRemoved, map[string]any{"title": t.Title})
	}
	s.Tell(ctx, userID, t, notifications.KindTaskDeleted, "", false)
	return nil
}

// SetStatus moves the task, if the user's role allows that change.
func (s *Service) SetStatus(ctx context.Context, userID, role, id, status string) (Task, error) {
	t, err := s.repo.Get(ctx, userID, id)
	if err != nil {
		return Task{}, err
	}
	if t.Archived() {
		return Task{}, ErrArchived
	}
	if !CanTransition(t, role, userID, status) {
		return Task{}, ErrTransition
	}
	if err := s.repo.SetStatus(ctx, id, t.Status, status); err != nil {
		return Task{}, err
	}
	before := t
	t, err = s.repo.Get(ctx, userID, id)
	if err == nil {
		s.recordStatus(ctx, userID, before, t)
		s.Tell(ctx, userID, t, notifications.KindTaskStatus, t.StatusName, true)
	}
	return t, err
}

// MoveTo puts the task into a status (board column) of its pair's workflow.
// Staying in the same bucket is always allowed. Entering another: the
// freelancer and the project's author may enter any, and everyone else only
// one that isn't done.
func (s *Service) MoveTo(ctx context.Context, userID, role, id, statusID string) (Task, error) {
	t, err := s.repo.Get(ctx, userID, id)
	if err != nil {
		return Task{}, err
	}
	if t.Archived() {
		return Task{}, ErrArchived
	}
	st, err := s.repo.Status(ctx, statusID)
	if err != nil {
		return Task{}, err
	}
	taskClient := ""
	if t.Client != nil {
		taskClient = t.Client.ID
	}
	taskProject := ""
	if t.Project != nil {
		taskProject = t.Project.ID
	}
	if !statusOnTask(st, t.Freelancer.ID, taskClient, taskProject) {
		return Task{}, ErrStatusMismatch
	}
	if statusID == t.StatusID {
		return t, nil
	}
	cur, err := s.repo.Status(ctx, t.StatusID)
	if err != nil {
		return Task{}, err
	}
	manager := userID == t.Freelancer.ID
	if !manager && t.Project != nil {
		manager, err = s.repo.IsProjectAuthor(ctx, userID, t.Project.ID)
		if err != nil {
			return Task{}, err
		}
	}
	if !MayEnter(t.Client == nil, st.Bucket == cur.Bucket, manager, st.ParticipantsCanMove) {
		return Task{}, ErrTransition
	}
	if err := s.repo.MoveTo(ctx, id, t.StatusID, statusID, st.Bucket); err != nil {
		return Task{}, err
	}
	before := t
	t, err = s.repo.Get(ctx, userID, id)
	if err == nil {
		s.recordStatus(ctx, userID, before, t)
		s.Tell(ctx, userID, t, notifications.KindTaskStatus, t.StatusName, true)
	}
	return t, err
}

// recordStatus records a move between workflow statuses, by name.
func (s *Service) recordStatus(ctx context.Context, actorID string, before, after Task) {
	if before.StatusID != after.StatusID {
		data := change(before.StatusName, after.StatusName)
		data["category"] = after.Status
		s.Record(ctx, after.ID, actorID, EventStatus, data)
	}
}

func samePrice(a, b *int64) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

// statusOnTask reports whether a column is on the task's board: the project's
// columns when it is in one, the pair's columns otherwise.
func statusOnTask(st WorkflowStatus, freelancerID, clientID, projectID string) bool {
	if projectID != "" {
		return st.ProjectID == projectID
	}
	return st.FreelancerID == freelancerID && deref(st.ClientID) == clientID && st.ProjectID == ""
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
