package reports

import (
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	dateLayout = "2006-01-02"
	maxTitle   = 200
	maxSummary = 20000
	maxNotes   = 20000
	// maxProjects caps the projects one report covers.
	maxProjects = 50
	// maxPeriod keeps a report to a sensible span.
	maxPeriod = 366 * 24 * time.Hour
)

// CreateRequest is the JSON body for POST /api/v1/reports.
type CreateRequest struct {
	ClientID string `json:"client_id"`
	// ProjectIDs limits the report to these projects; none means all.
	ProjectIDs []string `json:"project_ids"`
	// ProjectID is the one-project form of ProjectIDs, still accepted.
	ProjectID string `json:"project_id"`
	From      string `json:"from"`
	To        string `json:"to"`
	Title     string `json:"title"`
	Summary   string `json:"summary"`
	// Kind is "basic" (the default) or "detailed".
	Kind string `json:"kind"`
}

// CreateParams is the validated input for a new report. An empty client makes
// a report on personal work; an empty title gets a default.
type CreateParams struct {
	ClientID   string
	ProjectIDs []string
	From       time.Time
	To         time.Time
	Title      string
	Summary    string
	Kind       string
}

// Validate checks the payload and returns per-field error messages.
func (r CreateRequest) Validate() (CreateParams, map[string]string) {
	fields := map[string]string{}
	p := CreateParams{
		ClientID: strings.TrimSpace(r.ClientID),
		Title:    strings.TrimSpace(r.Title),
		Summary:  strings.TrimSpace(r.Summary),
		Kind:     KindBasic,
	}
	for _, id := range append(r.ProjectIDs, r.ProjectID) {
		if id = strings.TrimSpace(id); id != "" && !slices.Contains(p.ProjectIDs, id) {
			p.ProjectIDs = append(p.ProjectIDs, id)
		}
	}
	if len(p.ProjectIDs) > maxProjects {
		fields["project_ids"] = "That's too many projects for one report"
	}
	if k := strings.TrimSpace(r.Kind); k != "" {
		p.Kind = k
	}
	checkKind(fields, p.Kind)
	var err error
	if p.From, err = time.Parse(dateLayout, strings.TrimSpace(r.From)); err != nil {
		fields["from"] = "Choose the first day"
	}
	if p.To, err = time.Parse(dateLayout, strings.TrimSpace(r.To)); err != nil {
		fields["to"] = "Choose the last day"
	}
	if fields["from"] == "" && fields["to"] == "" {
		switch {
		case p.To.Before(p.From):
			fields["to"] = "The period ends before it starts"
		case p.To.Sub(p.From) > maxPeriod:
			fields["to"] = "Keep the period to a year at most"
		}
	}
	checkText(fields, p.Title, p.Summary)
	return p, fields
}

// UpdateRequest is the JSON body for PUT /api/v1/reports/{id}.
type UpdateRequest struct {
	Title   string `json:"title"`
	Summary string `json:"summary"`
	// Kind switches between "basic" and "detailed"; omitted, it stays.
	Kind *string `json:"kind"`
}

// UpdateParams is the validated input for editing a draft; an empty Kind
// keeps the current one.
type UpdateParams struct {
	Title   string
	Summary string
	Kind    string
}

// Validate checks the payload and returns per-field error messages.
func (r UpdateRequest) Validate() (UpdateParams, map[string]string) {
	fields := map[string]string{}
	p := UpdateParams{Title: strings.TrimSpace(r.Title), Summary: strings.TrimSpace(r.Summary)}
	if p.Title == "" {
		fields["title"] = "Title is required"
	}
	if r.Kind != nil {
		p.Kind = strings.TrimSpace(*r.Kind)
		checkKind(fields, p.Kind)
	}
	checkText(fields, p.Title, p.Summary)
	return p, fields
}

// ItemRequest is the JSON body for PUT /api/v1/reports/{id}/items/{itemID}.
type ItemRequest struct {
	Notes string `json:"notes"`
}

// Validate returns the trimmed notes and per-field errors.
func (r ItemRequest) Validate() (string, map[string]string) {
	notes := strings.TrimSpace(r.Notes)
	if utf8.RuneCountInString(notes) > maxNotes {
		return notes, map[string]string{"notes": "Notes are too long"}
	}
	return notes, nil
}

func checkKind(fields map[string]string, kind string) {
	if kind != KindBasic && kind != KindDetailed {
		fields["kind"] = "Choose a basic or a detailed report"
	}
}

func checkText(fields map[string]string, title, summary string) {
	if utf8.RuneCountInString(title) > maxTitle {
		fields["title"] = "Title is too long"
	}
	if utf8.RuneCountInString(summary) > maxSummary {
		fields["summary"] = "Summary is too long"
	}
}

// PartyDTO is one side of a report.
type PartyDTO struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// ItemDTO is a completed task in a report.
type ItemDTO struct {
	ID          string  `json:"id"`
	TaskID      *string `json:"task_id"`
	Title       string  `json:"title"`
	ProjectName string  `json:"project_name"`
	StatusName  string  `json:"status_name"`
	CompletedOn string  `json:"completed_on"`
	// PriceCents is null for a task without a price.
	PriceCents *int64 `json:"price_cents"`
	// Notes say what was done in the task (shown in a detailed report).
	Notes string `json:"notes"`
}

// DTO is a report as the viewer sees it. Items are present on a single report.
type DTO struct {
	ID         string   `json:"id"`
	Freelancer PartyDTO `json:"freelancer"`
	// Client is null for a report on personal work.
	Client *PartyDTO `json:"client"`
	// Projects the report covers, named as they were; empty means all.
	Projects  []ProjectRef `json:"projects"`
	Title     string       `json:"title"`
	Summary   string       `json:"summary"`
	Kind      string       `json:"kind"`
	From      string       `json:"from"`
	To        string       `json:"to"`
	Status    string       `json:"status"`
	SentAt    *time.Time   `json:"sent_at"`
	PaidAt    *time.Time   `json:"paid_at"`
	ItemCount int          `json:"item_count"`
	Items     []ItemDTO    `json:"items,omitempty"`
	Currency  string       `json:"currency"`
	// TotalCents sums the items' prices, on a list too; null when none
	// has a price.
	TotalCents *int64 `json:"total_cents"`
	// CanEdit is true for the freelancer while the report is a draft.
	CanEdit bool `json:"can_edit"`
	// CanMarkPaid is true for the client of a sent, unpaid report.
	CanMarkPaid bool `json:"can_mark_paid"`
	// CanDelete is true for the freelancer while the report is a draft.
	CanDelete bool      `json:"can_delete"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ListResponse is the reports the viewer may see.
type ListResponse struct {
	Reports []DTO `json:"reports"`
}

// ToDTO maps a report for the viewer.
func ToDTO(r Report, viewerID string) DTO {
	d := DTO{
		ID:          r.ID,
		Freelancer:  PartyDTO(r.Freelancer),
		Projects:    r.Projects,
		Title:       r.Title,
		Summary:     r.Summary,
		Kind:        r.Kind,
		From:        r.From.Format(dateLayout),
		To:          r.To.Format(dateLayout),
		Status:      r.Status,
		SentAt:      r.SentAt,
		PaidAt:      r.PaidAt,
		ItemCount:   r.ItemCount,
		Currency:    r.Currency,
		CanEdit:     r.Freelancer.ID == viewerID && r.Status == StatusDraft,
		CanDelete:   r.Freelancer.ID == viewerID && r.Status == StatusDraft,
		CanMarkPaid: r.Client != nil && r.Client.ID == viewerID && r.Status == StatusSent,
		TotalCents:  r.ListTotal,
		CreatedAt:   r.CreatedAt,
		UpdatedAt:   r.UpdatedAt,
	}
	if d.Projects == nil {
		d.Projects = []ProjectRef{}
	}
	if r.Client != nil {
		c := PartyDTO(*r.Client)
		d.Client = &c
	}
	if r.Items != nil {
		d.Items = make([]ItemDTO, 0, len(r.Items))
		for _, it := range r.Items {
			item := ItemDTO{ID: it.ID, Title: it.Title, ProjectName: it.ProjectName, StatusName: it.StatusName,
				CompletedOn: it.CompletedOn.Format(dateLayout), PriceCents: it.PriceCents, Notes: it.Notes}
			if it.TaskID != "" {
				id := it.TaskID
				item.TaskID = &id
			}
			d.Items = append(d.Items, item)
		}
	}
	return d
}
