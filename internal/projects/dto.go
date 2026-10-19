package projects

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pmaxis/helpic/internal/user"
)

const (
	maxName        = 120
	maxDescription = 5000
)

// Request is the JSON body for creating or editing a project. On create a
// client names the freelancers (one or several) and a freelancer names the
// client, or leaves it empty for a personal project; neither is read on edit.
type Request struct {
	FreelancerID  string   `json:"freelancer_id"`
	FreelancerIDs []string `json:"freelancer_ids"`
	ClientID      string   `json:"client_id"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
}

// Params is the validated project input.
type Params struct {
	// FreelancerIDs are the freelancers a client starts the project with.
	// The first is stored on the project; every one becomes a participant.
	FreelancerIDs []string
	ClientID      string
	Name          string
	Description   string
}

// freelancerIDs collects the freelancers a client named, without blanks or repeats.
func freelancerIDs(r Request) []string {
	seen := map[string]bool{}
	var ids []string
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		ids = append(ids, id)
	}
	for _, id := range r.FreelancerIDs {
		add(id)
	}
	add(r.FreelancerID)
	return ids
}

// Validate checks the payload. creatorRole is the creating user's role on
// create (a client must name a freelancer); it is empty on edit.
func (r Request) Validate(creatorRole string) (Params, map[string]string) {
	fields := map[string]string{}
	p := Params{
		FreelancerIDs: freelancerIDs(r),
		ClientID:      strings.TrimSpace(r.ClientID),
		Name:          strings.TrimSpace(r.Name),
		Description:   strings.TrimSpace(r.Description),
	}
	if creatorRole == user.RoleClient && len(p.FreelancerIDs) == 0 {
		fields["freelancer_id"] = "Choose a freelancer"
		fields["freelancer_ids"] = "Choose a freelancer"
	}
	if p.Name == "" {
		fields["name"] = "Name is required"
	} else if utf8.RuneCountInString(p.Name) > maxName {
		fields["name"] = "Name is too long"
	}
	if utf8.RuneCountInString(p.Description) > maxDescription {
		fields["description"] = "Description is too long"
	}
	return p, fields
}

// PartyDTO is one side of a project. Role is set for a participant.
type PartyDTO struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
	Role  string `json:"role,omitempty"`
}

// DTO is a project as one of its sides sees it.
type DTO struct {
	ID           string     `json:"id"`
	Freelancer   PartyDTO   `json:"freelancer"`
	Client       *PartyDTO  `json:"client"`
	CreatedBy    string     `json:"created_by"`
	Name         string     `json:"name"`
	Description  string     `json:"description"`
	OpenTasks    int        `json:"open_tasks"`
	TotalTasks   int        `json:"total_tasks"`
	Participants []PartyDTO `json:"participants"`
	CanEdit      bool       `json:"can_edit"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// PersonDTO is someone on a project, or someone the author can invite.
type PersonDTO struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
	// Role is "freelancer", "owner", "member" or "contractor".
	Role string `json:"role"`
}

// InviteRequest is the JSON body for POST /api/v1/projects/{id}/participants.
// user_id invites one person; user_ids invites several at once.
type InviteRequest struct {
	UserID  string   `json:"user_id"`
	UserIDs []string `json:"user_ids"`
}

// IDs is every person to invite, without blanks or repeats.
func (r InviteRequest) IDs() []string {
	seen := map[string]bool{}
	var ids []string
	for _, id := range append(r.UserIDs, r.UserID) {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids
}

// ToDTO maps a project for the viewer.
func ToDTO(p Project, viewerID string) DTO {
	d := DTO{
		ID:          p.ID,
		Freelancer:  PartyDTO(p.Freelancer),
		CreatedBy:   p.CreatedBy,
		Name:        p.Name,
		Description: p.Description,
		OpenTasks:   p.OpenTasks,
		TotalTasks:  p.TotalTasks,
		CanEdit:     p.CreatedBy == viewerID,
		CreatedAt:   p.CreatedAt,
		UpdatedAt:   p.UpdatedAt,
	}
	d.Participants = make([]PartyDTO, len(p.Participants))
	for i, person := range p.Participants {
		d.Participants[i] = PartyDTO(person)
	}
	if p.Client != nil {
		c := PartyDTO(*p.Client)
		d.Client = &c
	}
	return d
}
