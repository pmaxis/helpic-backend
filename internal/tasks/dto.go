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
	FreelancerID string `json:"freelancer_id"`
	ClientID     string `json:"client_id"`
	// ProjectID puts the task in a project; on edit null keeps the current
	// project and "" takes the task out of it.
	ProjectID *string `json:"project_id"`
	// StatusID creates the task straight in a board column (create only).
	StatusID    string  `json:"status_id"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	DueOn       *string `json:"due_on"`
	// Price is an amount like "1200.50", set by a freelancer on create only;
	// PUT /api/v1/tasks/{id}/price changes it later.
	Price *string `json:"price"`
	// Priority is set on create only ("none" when left out); PUT
	// /api/v1/tasks/{id}/priority changes it later.
	Priority string `json:"priority"`
}

// Params is the validated task input.
type Params struct {
	FreelancerID string
	ClientID     string
	ProjectID    *string
	StatusID     string
	Title        string
	Description  string
	DueOn        *time.Time
	PriceCents   *int64
	Priority     string
}

// Validate checks the payload. creatorRole is the creating user's role on
// create (a client must name the freelancer unless the task goes into a
// project); it is empty on edit.
func (r Request) Validate(creatorRole string) (Params, map[string]string) {
	fields := map[string]string{}
	p := Params{
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
	if creatorRole == user.RoleClient && p.FreelancerID == "" && (p.ProjectID == nil || *p.ProjectID == "") {
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
	return p, fields
}

// PriceRequest is the JSON body for PUT /api/v1/tasks/{id}/price; a null or
// empty price clears it.
type PriceRequest struct {
	Price *string `json:"price"`
}

// Validate returns the price in cents (nil = no price) and per-field errors.
func (r PriceRequest) Validate() (*int64, map[string]string) {
	if r.Price == nil || strings.TrimSpace(*r.Price) == "" {
		return nil, nil
	}
	cents, err := money.ParsePrice(*r.Price)
	if err != nil {
		return nil, map[string]string{"price": "Enter an amount like 1200 or 99.50"}
	}
	return &cents, nil
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
	ID         string   `json:"id"`
	Freelancer PartyDTO `json:"freelancer"`
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
	PriceCents   *int64   `json:"price_cents"`
	Currency     string   `json:"currency"`
	Priority     string   `json:"priority"`
	Labels       []Label  `json:"labels"`
	NextStatuses []string `json:"next_statuses"`
	CanEdit      bool     `json:"can_edit"`
	CanDelete    bool     `json:"can_delete"`
	CanSetPrice  bool     `json:"can_set_price"`
	// CanSeePrice is false for a member of the client's team: the price is
	// left out for them.
	CanSeePrice bool      `json:"can_see_price"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ToDTO maps a task for the viewer.
func ToDTO(t Task, role, viewerID string) DTO {
	d := DTO{
		ID:           t.ID,
		Freelancer:   PartyDTO(t.Freelancer),
		CreatedBy:    t.CreatedBy,
		Creator:      PartyDTO(t.Creator),
		StatusID:     t.StatusID,
		StatusName:   t.StatusName,
		Position:     t.Position,
		Title:        t.Title,
		Description:  t.Description,
		Status:       t.Status,
		PriceCents:   t.PriceCents,
		Currency:     t.Currency,
		Priority:     t.Priority,
		Labels:       t.Labels,
		NextStatuses: NextStatuses(t, role, viewerID),
		CanEdit:      CanEdit(t, viewerID),
		CanDelete:    CanDelete(t, viewerID),
		CanSetPrice:  CanSetPrice(t, viewerID),
		CanSeePrice:  CanSeePrice(t, viewerID),
		CreatedAt:    t.CreatedAt,
		UpdatedAt:    t.UpdatedAt,
	}
	if t.Client != nil {
		c := PartyDTO(*t.Client)
		d.Client = &c
	}
	if t.Project != nil {
		d.Project = &ProjectDTO{ID: t.Project.ID, Name: t.Project.Name}
	}
	if !d.CanSeePrice {
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
