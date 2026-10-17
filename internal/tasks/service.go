package tasks

import (
	"context"
	"slices"

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
// client side is the company's owner, plus the member who set the task while
// they're still in the company.
func (s *Service) Tell(ctx context.Context, actorID string, t Task, kind, detail string, withTask bool) {
	if t.Client == nil {
		return
	}
	recipients := []string{t.Freelancer.ID}
	if actorID == t.Freelancer.ID {
		recipients = []string{t.Client.ID}
		if t.CreatedBy != t.Client.ID && t.CreatedBy != t.Freelancer.ID {
			if acting, err := s.repo.ActingClient(ctx, t.CreatedBy); err == nil && acting == t.Client.ID {
				recipients = append(recipients, t.CreatedBy)
			}
		}
	}
	for _, to := range recipients {
		n := notifications.Notification{UserID: to, ActorID: actorID, Kind: kind, Subject: t.Title, Detail: detail}
		if withTask {
			n.TaskID = t.ID
		}
		if t.Project != nil {
			n.ProjectID = t.Project.ID
		}
		s.notify.Notify(ctx, n)
	}
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
// takes the project's freelancer and client instead.
func (s *Service) Create(ctx context.Context, userID, role string, p Params) (Task, error) {
	freelancerID, clientID := p.FreelancerID, userID
	if role == user.RoleFreelancer {
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
		pf, pc, err := s.repo.ProjectPair(ctx, userID, projectID)
		if err != nil {
			return Task{}, err
		}
		freelancerID, clientID = pf, deref(pc)
	}

	// Straight into a board column: it must be on this pair's board, and its
	// category one the creator could move a new task to.
	if p.StatusID != "" {
		sf, sc, category, err := s.repo.Status(ctx, p.StatusID)
		if err != nil {
			return Task{}, err
		}
		if sf != freelancerID || deref(sc) != clientID {
			return Task{}, ErrStatusMismatch
		}
		draft := Task{Status: StatusNew, CreatedBy: userID}
		if clientID != "" {
			draft.Client = &Party{ID: clientID}
		}
		if category != StatusNew && !CanTransition(draft, role, userID, category) {
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
		s.Tell(ctx, userID, t, notifications.KindTaskCreated, "", true)
	}
	return t, err
}

// Update edits a task's content. Its creator or its freelancer may, in any
// status.
func (s *Service) Update(ctx context.Context, userID, id string, p Params) (Task, error) {
	t, err := s.repo.Get(ctx, userID, id)
	if err != nil {
		return Task{}, err
	}
	if !CanEdit(t, userID) {
		return Task{}, ErrEditNotAllowed
	}

	// A nil ProjectID keeps the current project; "" takes the task out of it.
	projectID := ""
	if t.Project != nil {
		projectID = t.Project.ID
	}
	if p.ProjectID != nil && *p.ProjectID != projectID {
		projectID = *p.ProjectID
		if projectID != "" {
			pf, pc, err := s.repo.ProjectPair(ctx, userID, projectID)
			if err != nil {
				return Task{}, err
			}
			taskClient := ""
			if t.Client != nil {
				taskClient = t.Client.ID
			}
			if pf != t.Freelancer.ID || deref(pc) != taskClient {
				return Task{}, ErrProjectMismatch
			}
		}
	}
	if err := s.repo.Update(ctx, id, projectID, p); err != nil {
		return Task{}, err
	}
	before := t
	t, err = s.repo.Get(ctx, userID, id)
	if err == nil {
		s.recordEdit(ctx, userID, before, t)
		s.Tell(ctx, userID, t, notifications.KindTaskUpdated, "", true)
	}
	return t, err
}

// SetPrice sets or clears the task's price. Only its freelancer may, in any
// status: the price is what they bill for the work.
func (s *Service) SetPrice(ctx context.Context, userID, id string, cents *int64) (Task, error) {
	t, err := s.repo.Get(ctx, userID, id)
	if err != nil {
		return Task{}, err
	}
	if !CanSetPrice(t, userID) {
		return Task{}, ErrPriceNotAllowed
	}
	if err := s.repo.SetPrice(ctx, id, cents); err != nil {
		return Task{}, err
	}
	before := t
	t, err = s.repo.Get(ctx, userID, id)
	if err == nil && !samePrice(before.PriceCents, t.PriceCents) {
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

// Delete removes a task its creator added by mistake: only before work on it
// has started.
func (s *Service) Delete(ctx context.Context, userID, id string) error {
	t, err := s.repo.Get(ctx, userID, id)
	if err != nil {
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
	s.Tell(ctx, userID, t, notifications.KindTaskDeleted, "", false)
	return nil
}

// SetStatus moves the task, if the user's role allows that change.
func (s *Service) SetStatus(ctx context.Context, userID, role, id, status string) (Task, error) {
	t, err := s.repo.Get(ctx, userID, id)
	if err != nil {
		return Task{}, err
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
// Changing category follows the role rules; moving between columns of the
// same category is always allowed.
func (s *Service) MoveTo(ctx context.Context, userID, role, id, statusID string) (Task, error) {
	t, err := s.repo.Get(ctx, userID, id)
	if err != nil {
		return Task{}, err
	}
	sf, sc, category, err := s.repo.Status(ctx, statusID)
	if err != nil {
		return Task{}, err
	}
	taskClient := ""
	if t.Client != nil {
		taskClient = t.Client.ID
	}
	if sf != t.Freelancer.ID || deref(sc) != taskClient {
		return Task{}, ErrStatusMismatch
	}
	if statusID == t.StatusID {
		return t, nil
	}
	if category != t.Status && !CanTransition(t, role, userID, category) {
		return Task{}, ErrTransition
	}
	if err := s.repo.MoveTo(ctx, id, t.StatusID, statusID, category); err != nil {
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

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
