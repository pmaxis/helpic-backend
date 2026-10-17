package team

import (
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pmaxis/helpic/internal/auth"
)

const maxCompanyName = 120

// NameRequest is the JSON body for PUT /api/v1/team.
type NameRequest struct {
	Name string `json:"name"`
}

// Validate returns the trimmed name and per-field errors.
func (r NameRequest) Validate() (string, map[string]string) {
	name := strings.Join(strings.Fields(r.Name), " ")
	if utf8.RuneCountInString(name) > maxCompanyName {
		return name, map[string]string{"name": "The name is too long"}
	}
	return name, nil
}

// InviteRequest is the JSON body for POST /api/v1/team/invites.
type InviteRequest struct {
	Email string `json:"email"`
	Name  string `json:"name"`
	// Role is "member" (the default) or "contractor".
	Role string `json:"role"`
}

// InviteParams is the validated invitation input.
type InviteParams struct {
	Email string
	Name  string
	Role  string
}

// Validate checks the payload and returns per-field errors. The name is
// optional; it pre-fills the sign-up form.
func (r InviteRequest) Validate() (InviteParams, map[string]string) {
	fields := map[string]string{}
	p := InviteParams{Email: auth.NormalizeEmail(r.Email), Name: strings.TrimSpace(r.Name), Role: r.Role}
	if p.Role == "" {
		p.Role = RoleMember
	}
	if !ValidRole(p.Role) {
		fields["role"] = "Choose member or contractor"
	}
	if p.Email == "" {
		fields["email"] = "Email is required"
	} else if _, err := mail.ParseAddress(p.Email); err != nil {
		fields["email"] = "Enter a valid email address"
	}
	if utf8.RuneCountInString(p.Name) > auth.MaxNameLength {
		fields["name"] = "Name is too long"
	}
	return p, fields
}

// RoleRequest is the JSON body for PUT /api/v1/team/members/{id}.
type RoleRequest struct {
	// Role is "member" or "contractor".
	Role string `json:"role"`
}

// JoinRequest is the JSON body for POST /api/v1/team-invites/{token}/accept.
type JoinRequest struct {
	Name                 string `json:"name"`
	Password             string `json:"password"`
	PasswordConfirmation string `json:"password_confirmation"`
}

// JoinParams is the validated input for a member's new account.
type JoinParams struct {
	Name     string
	Password string
}

// Validate checks the payload and returns per-field errors.
func (r JoinRequest) Validate() (JoinParams, map[string]string) {
	fields := map[string]string{}
	name := strings.TrimSpace(r.Name)
	if msg := auth.ValidateName(name); msg != "" {
		fields["name"] = msg
	}
	auth.ValidateNewPassword(fields, r.Password, r.PasswordConfirmation)
	return JoinParams{Name: name, Password: r.Password}, fields
}

// PersonDTO is someone on the team.
type PersonDTO struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// MemberDTO is a member of the company.
type MemberDTO struct {
	PersonDTO
	// Role is "member" or "contractor".
	Role     string    `json:"role"`
	JoinedAt time.Time `json:"joined_at"`
}

// InviteDTO is a pending invitation; Token builds the join link.
type InviteDTO struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	Role      string    `json:"role"`
	Token     string    `json:"token"`
	Expired   bool      `json:"expired"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

// OverviewDTO is the team as the viewer sees it. Members and invites are
// for the owner only.
type OverviewDTO struct {
	Role    string      `json:"role"`
	Name    string      `json:"name"`
	Owner   PersonDTO   `json:"owner"`
	Members []MemberDTO `json:"members,omitempty"`
	Invites []InviteDTO `json:"invites,omitempty"`
}

func toInviteDTO(i Invite) InviteDTO {
	return InviteDTO{ID: i.ID, Email: i.Email, Name: i.Name, Role: i.Role, Token: i.Token, Expired: i.Expired(), ExpiresAt: i.ExpiresAt, CreatedAt: i.CreatedAt}
}

func toOverviewDTO(o Overview) OverviewDTO {
	d := OverviewDTO{Role: o.Role, Name: o.Name, Owner: PersonDTO(o.Owner)}
	if o.Role != RoleOwner {
		return d
	}
	d.Members = make([]MemberDTO, 0, len(o.Members))
	for _, m := range o.Members {
		d.Members = append(d.Members, MemberDTO{PersonDTO: PersonDTO(m.Person), Role: m.Role, JoinedAt: m.JoinedAt})
	}
	d.Invites = make([]InviteDTO, 0, len(o.Invites))
	for _, i := range o.Invites {
		d.Invites = append(d.Invites, toInviteDTO(i))
	}
	return d
}

// LookupDTO is what the join page shows.
type LookupDTO struct {
	Email   string `json:"email"`
	Name    string `json:"name"`
	Company string `json:"company"`
	// Role is what they join as: "member" or "contractor".
	Role      string    `json:"role"`
	Owner     PersonDTO `json:"owner"`
	ExpiresAt time.Time `json:"expires_at"`
}
