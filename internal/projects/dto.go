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
// client names the freelancer and a freelancer names the client, or leaves it
// empty for a personal project; neither is read on edit.
type Request struct {
	FreelancerID string `json:"freelancer_id"`
	ClientID     string `json:"client_id"`
	Name         string `json:"name"`
	Description  string `json:"description"`
}

// Params is the validated project input.
type Params struct {
	FreelancerID string
	ClientID     string
	Name         string
	Description  string
}

// Validate checks the payload. creatorRole is the creating user's role on
// create (a client must name the freelancer); it is empty on edit.
func (r Request) Validate(creatorRole string) (Params, map[string]string) {
	fields := map[string]string{}
	p := Params{
		FreelancerID: strings.TrimSpace(r.FreelancerID),
		ClientID:     strings.TrimSpace(r.ClientID),
		Name:         strings.TrimSpace(r.Name),
		Description:  strings.TrimSpace(r.Description),
	}
	if creatorRole == user.RoleClient && p.FreelancerID == "" {
		fields["freelancer_id"] = "Choose a freelancer"
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

// PartyDTO is one side of a project.
type PartyDTO struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// DTO is a project as one of its sides sees it.
type DTO struct {
	ID          string    `json:"id"`
	Freelancer  PartyDTO  `json:"freelancer"`
	Client      *PartyDTO `json:"client"`
	CreatedBy   string    `json:"created_by"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	OpenTasks   int       `json:"open_tasks"`
	TotalTasks  int       `json:"total_tasks"`
	CanEdit     bool      `json:"can_edit"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
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
	if p.Client != nil {
		c := PartyDTO(*p.Client)
		d.Client = &c
	}
	return d
}
