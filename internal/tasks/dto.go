package tasks

import (
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pmaxis/helpic/internal/money"
	"github.com/pmaxis/helpic/internal/user"
)

const (
	dateLayout     = "2006-01-02"
	maxTitle       = 200
	maxDescription = 20000
)

// Request is the JSON body for creating or editing a task. On create a client
// names the freelancer and a freelancer names the client, or leaves it empty
// for a personal task; neither is read on edit.
type Request struct {
	// Personal makes a task with no other side: a client's own (a freelancer's
	// is made by leaving client_id empty). Create only.
	Personal     bool   `json:"personal"`
	FreelancerID string `json:"freelancer_id"`
	ClientID     string `json:"client_id"`
	// ProjectID puts the task in a project; on edit null keeps the current
	// project and "" takes the task out of it.
	ProjectID *string `json:"project_id"`
	// StatusID creates the task straight in a board column (create only).
	StatusID string `json:"status_id"`
	// ParentID makes the task a subtask of another (create only): it takes
	// the parent's client and project, and a client may leave the freelancer
	// out for the parent's.
	ParentID    string  `json:"parent_id"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	DueOn       *string `json:"due_on"`
	// CreatedAt moves when the task counts as created, as an RFC 3339 time
	// no later than now nor, in its own offset, than the due date (edit
	// only); null keeps it.
	CreatedAt *string `json:"created_at"`
	// Price is an amount like "1200.50", set by a freelancer on create only;
	// PUT /api/v1/tasks/{id}/price changes it later.
	Price *string `json:"price"`
	// Priority is set on create only ("none" when left out); PUT
	// /api/v1/tasks/{id}/priority changes it later.
	Priority string `json:"priority"`
}

// Params is the validated task input.
type Params struct {
	Personal     bool
	FreelancerID string
	ClientID     string
	ProjectID    *string
	StatusID     string
	ParentID     string
	Title        string
	Description  string
	DueOn        *time.Time
	CreatedAt    *time.Time
	PriceCents   *int64
	Priority     string
}

// Validate checks the payload. creatorRole is the creating user's role on
// create (a client must name the freelancer unless the task goes into a
// project); it is empty on edit.
func (r Request) Validate(creatorRole string) (Params, map[string]string) {
	fields := map[string]string{}
	p := Params{
		Personal:     r.Personal && creatorRole != "",
		FreelancerID: strings.TrimSpace(r.FreelancerID),
		ClientID:     strings.TrimSpace(r.ClientID),
		ProjectID:    r.ProjectID,
		StatusID:     strings.TrimSpace(r.StatusID),
		Title:        strings.TrimSpace(r.Title),
		Description:  strings.TrimSpace(r.Description),
		Priority:     PriorityNone,
	}
	if pr := strings.TrimSpace(r.Priority); pr != "" && creatorRole != "" {
		if !slices.Contains(Priorities, pr) {
			fields["priority"] = "Choose a priority"
		}
		p.Priority = pr
	}

	if p.ProjectID != nil {
		trimmed := strings.TrimSpace(*p.ProjectID)
		p.ProjectID = &trimmed
	}
	inProject := p.ProjectID != nil && *p.ProjectID != ""
	if creatorRole != "" {
		p.ParentID = strings.TrimSpace(r.ParentID)
	}
	switch {
	case p.ParentID != "":
		// Who it's with and its project come from the parent.
		if p.Personal || p.ClientID != "" || p.ProjectID != nil {
			fields["parent_id"] = "A subtask is with its parent's client, in its project"
		}
	case p.Personal && (p.FreelancerID != "" || p.ClientID != "" || inProject):
		fields["freelancer_id"] = "A personal task has nobody else on it and no project"
	case creatorRole == user.RoleClient && !p.Personal && p.FreelancerID == "" && !inProject:
		fields["freelancer_id"] = "Choose a freelancer"
	}
	if p.Title == "" {
		fields["title"] = "Title is required"
	} else if utf8.RuneCountInString(p.Title) > maxTitle {
		fields["title"] = "Title is too long"
	}
	if utf8.RuneCountInString(p.Description) > maxDescription {
		fields["description"] = "Description is too long"
	}
	// The price is read on create only.
	if creatorRole != "" && r.Price != nil && strings.TrimSpace(*r.Price) != "" {
		if creatorRole != user.RoleFreelancer {
			fields["price"] = "Only the freelancer sets the price"
		} else if cents, err := money.ParsePrice(*r.Price); err != nil {
			fields["price"] = "Enter an amount like 1200 or 99.50"
		} else {
			p.PriceCents = &cents
		}
	}
	if r.DueOn != nil && strings.TrimSpace(*r.DueOn) != "" {
		d, err := time.Parse(dateLayout, strings.TrimSpace(*r.DueOn))
		if err != nil {
			fields["due_on"] = "Enter a valid date (YYYY-MM-DD)"
		} else {
			p.DueOn = &d
		}
	}
	// The creation date is moved on edit only, and never into the future.
	if creatorRole == "" && r.CreatedAt != nil && strings.TrimSpace(*r.CreatedAt) != "" {
		c, err := time.Parse(time.RFC3339, strings.TrimSpace(*r.CreatedAt))
		switch {
		case err != nil:
			fields["created_at"] = "Enter a valid date and time"
		case c.After(time.Now().Add(time.Minute)):
			fields["created_at"] = "The date can't be in the future"
		case p.DueOn != nil && c.Format(dateLayout) > p.DueOn.Format(dateLayout):
			fields["created_at"] = "Can't be after the due date"
		default:
			c = c.UTC()
			p.CreatedAt = &c
		}
	}
	return p, fields
}

// PriceRequest is the JSON body for PUT /api/v1/tasks/{id}/price. Billing is
// "fixed" (the default) or "hourly"; at a fixed price a null or empty price
// clears it, by the hour the price comes from the time and is not read.
type PriceRequest struct {
	Billing string  `json:"billing"`
	Price   *string `json:"price"`
}

// Validate returns the billing, the price in cents (nil = no price) and
// per-field errors.
func (r PriceRequest) Validate() (string, *int64, map[string]string) {
	billing := strings.TrimSpace(r.Billing)
	switch billing {
	case "", BillingFixed:
		billing = BillingFixed
	case BillingHourly:
		return billing, nil, nil
	default:
		return billing, nil, map[string]string{"billing": "Choose a fixed price or by the hour"}
	}
	if r.Price == nil || strings.TrimSpace(*r.Price) == "" {
		return billing, nil, nil
	}
	cents, err := money.ParsePrice(*r.Price)
	if err != nil {
		return billing, nil, map[string]string{"price": "Enter an amount like 1200 or 99.50"}
	}
	return billing, &cents, nil
}

// PriorityRequest is the JSON body for PUT /api/v1/tasks/{id}/priority.
type PriorityRequest struct {
	Priority string `json:"priority"`
}

// Validate returns the priority and per-field errors.
func (r PriorityRequest) Validate() (string, map[string]string) {
	p := strings.TrimSpace(r.Priority)
	if !slices.Contains(Priorities, p) {
		return p, map[string]string{"priority": "Choose a priority"}
	}
	return p, nil
}

// LabelsRequest is the JSON body for PUT /api/v1/tasks/{id}/labels: the task's
// whole set of labels.
type LabelsRequest struct {
	LabelIDs []string `json:"label_ids"`
}

// maxLabels caps the labels on one task.
const maxLabels = 20

// Validate returns the distinct label IDs and per-field errors.
func (r LabelsRequest) Validate() ([]string, map[string]string) {
	ids := []string{}
	for _, id := range r.LabelIDs {
		if id = strings.TrimSpace(id); id != "" && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	if len(ids) > maxLabels {
		return ids, map[string]string{"label_ids": "That's too many labels for one task"}
	}
	return ids, nil
}

// StatusRequest is the JSON body for POST /api/v1/tasks/{id}/status.
type StatusRequest struct {
	// Status moves the task to the first column of a category; StatusID to an
	// exact column.
	Status   string `json:"status"`
	StatusID string `json:"status_id"`
}

// ParseStatuses reads a comma-separated status filter, dropping unknown values.
func ParseStatuses(raw string) []string {
	var out []string
	for _, s := range strings.Split(raw, ",") {
		s = strings.TrimSpace(s)
		if slices.Contains(Statuses, s) {
			out = append(out, s)
		}
	}
	return out
}

// PartyDTO is one side of a task.
type PartyDTO struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// PersonDTO is someone on the task's client side who can be on the task.
type PersonDTO struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
	// Role is "owner", "member" or "contractor".
	Role string `json:"role"`
	// OnTask is whether they're a participant already.
	OnTask bool `json:"on_task"`
}

// ParticipantRequest is the JSON body for POST /api/v1/tasks/{id}/participants.
type ParticipantRequest struct {
	UserID string `json:"user_id"`
}

// ProjectDTO names a task's project.
type ProjectDTO struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// DTO is a task as one of its sides sees it. NextStatuses, CanEdit and
// CanDelete describe what that viewer may do.
type DTO struct {
	ID string `json:"id"`
	// Freelancer is null while the task has no assignee.
	Freelancer *PartyDTO `json:"freelancer"`
	// Client is null for a personal task.
	Client *PartyDTO `json:"client"`
	// Project is null for a task outside any project.
	Project   *ProjectDTO `json:"project"`
	CreatedBy string      `json:"created_by"`
	// Creator is who set the task — the freelancer, the client, or a member
	// of the client's company.
	Creator PartyDTO `json:"creator"`
	// Participants are who took part: the freelancer first, then everyone who
	// set, commented on, attached to or changed the task.
	Participants []PartyDTO `json:"participants"`
	// Status is the category; StatusID and StatusName the exact workflow
	// status (board column), Position the card's place in it.
	StatusID    string  `json:"status_id"`
	StatusName  string  `json:"status_name"`
	Position    *int    `json:"position"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	Status      string  `json:"status"`
	DueOn       *string `json:"due_on"`
	// PriceCents is null while the task has no price; Currency is the
	// freelancer's.
	PriceCents *int64 `json:"price_cents"`
	Currency   string `json:"currency"`
	// Billing is "fixed" or "hourly": then the price is TrackedSeconds at
	// HourlyRateCents. Like the price, the time and the rate are null for
	// whoever doesn't see the price.
	Billing         string   `json:"billing"`
	TrackedSeconds  *int64   `json:"tracked_seconds"`
	HourlyRateCents *int64   `json:"hourly_rate_cents"`
	Priority        string   `json:"priority"`
	Labels          []Label  `json:"labels"`
	NextStatuses    []string `json:"next_statuses"`
	CanEdit         bool     `json:"can_edit"`
	CanDelete       bool     `json:"can_delete"`
	CanSetPrice     bool     `json:"can_set_price"`
	// CanSeePrice is false for a member of the client's team: the price is
	// left out for them.
	CanSeePrice bool `json:"can_see_price"`
	// CanTrackTime is true for the task's freelancer, who writes time on it.
	CanTrackTime bool `json:"can_track_time"`
	// Billed is true once the task is on a sent report: its time and an
	// hourly price stay as they were billed.
	Billed bool `json:"billed"`
	// CanArchive is true on the viewer's own personal task, which they may
	// put in the archive by hand.
	CanArchive bool `json:"can_archive"`
	// CanShare is true for whoever may turn the task's public link on or
	// off: its creator or its freelancer, archived or not.
	CanShare  bool      `json:"can_share"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	// ArchivedAt is set once the task is archived: off the boards and closed
	// to changes. ArchivedBy is the paid report that archived it, null when
	// its owner did.
	ArchivedAt *time.Time `json:"archived_at"`
	ArchivedBy *ReportDTO `json:"archived_by"`
	// ParentID is the task a subtask belongs to, null on a top-level task;
	// Parent names it, null when the viewer doesn't see it.
	ParentID *string    `json:"parent_id"`
	Parent   *ParentDTO `json:"parent"`
	// Subtasks counts the subtasks the viewer sees, and the closed ones.
	Subtasks SubtaskCount `json:"subtasks"`
	// CanAddSubtask is true while the task can be split: it's on the boards
	// and isn't a subtask itself.
	CanAddSubtask bool `json:"can_add_subtask"`
}

// ParentDTO names the task a subtask belongs to.
type ParentDTO struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// ReportDTO names the report whose payment archived a task.
type ReportDTO struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// ToDTO maps a task for the viewer.
func ToDTO(t Task, role, viewerID string) DTO {
	d := DTO{
		ID:            t.ID,
		CreatedBy:     t.CreatedBy,
		Creator:       PartyDTO(t.Creator),
		StatusID:      t.StatusID,
		StatusName:    t.StatusName,
		Position:      t.Position,
		Title:         t.Title,
		Description:   t.Description,
		Status:        t.Status,
		PriceCents:    t.PriceCents,
		Currency:      t.Currency,
		Billing:       t.Billing,
		Priority:      t.Priority,
		Labels:        t.Labels,
		NextStatuses:  NextStatuses(t, role, viewerID),
		CanEdit:       CanEdit(t, viewerID),
		CanDelete:     CanDelete(t, viewerID),
		CanSetPrice:   CanSetPrice(t, viewerID),
		CanSeePrice:   CanSeePrice(t, viewerID),
		CanTrackTime:  CanTrackTime(t, viewerID),
		Billed:        t.Billed,
		CanArchive:    CanArchive(t, viewerID),
		CanShare:      CanShare(t, viewerID),
		CreatedAt:     t.CreatedAt,
		UpdatedAt:     t.UpdatedAt,
		ArchivedAt:    t.ArchivedAt,
		Subtasks:      t.Subtasks,
		CanAddSubtask: CanAddSubtask(t),
	}
	if t.ParentID != "" {
		id := t.ParentID
		d.ParentID = &id
	}
	if t.Parent != nil {
		d.Parent = &ParentDTO{ID: t.Parent.ID, Title: t.Parent.Title}
	}
	if t.ArchivedBy != nil {
		d.ArchivedBy = &ReportDTO{ID: t.ArchivedBy.ID, Title: t.ArchivedBy.Title}
	}
	if t.Freelancer.ID != "" {
		f := PartyDTO(t.Freelancer)
		d.Freelancer = &f
	}
	if t.Client != nil {
		c := PartyDTO(*t.Client)
		d.Client = &c
	}
	if t.Project != nil {
		d.Project = &ProjectDTO{ID: t.Project.ID, Name: t.Project.Name}
	}
	if d.CanSeePrice {
		d.TrackedSeconds, d.HourlyRateCents = t.TrackedSeconds, t.HourlyRateCents
	} else {
		d.PriceCents, d.Currency = nil, ""
	}
	d.Participants = make([]PartyDTO, 0, len(t.Participants))
	for _, p := range t.Participants {
		d.Participants = append(d.Participants, PartyDTO(p))
	}
	if d.Labels == nil {
		d.Labels = []Label{}
	}
	if d.NextStatuses == nil {
		d.NextStatuses = []string{}
	}
	if t.DueOn != nil {
		s := t.DueOn.Format(dateLayout)
		d.DueOn = &s
	}
	return d
}
