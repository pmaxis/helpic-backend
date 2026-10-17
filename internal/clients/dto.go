package clients

import (
	"net/mail"
	"strings"
	"time"

	"github.com/pmaxis/helpic/internal/auth"
)

// InviteRequest is the JSON body for POST /api/invitations.
type InviteRequest struct {
	Email string `json:"email"`
	Name  string `json:"name"`
}

// InviteParams is the validated input to Service.Invite.
type InviteParams struct {
	Email string
	Name  string
}

// Validate checks the payload and returns per-field error messages.
func (r InviteRequest) Validate() (InviteParams, map[string]string) {
	fields := map[string]string{}
	p := InviteParams{Email: auth.NormalizeEmail(r.Email), Name: strings.TrimSpace(r.Name)}

	if p.Email == "" {
		fields["email"] = "Email is required"
	} else if _, err := mail.ParseAddress(p.Email); err != nil {
		fields["email"] = "Enter a valid email address"
	}
	if msg := auth.ValidateName(p.Name); msg != "" {
		fields["name"] = msg
	}
	return p, fields
}

// AcceptRequest is the JSON body for POST /api/invites/{token}/accept. A new
// account needs all three fields; an existing one only the password.
type AcceptRequest struct {
	Name                 string `json:"name"`
	Password             string `json:"password"`
	PasswordConfirmation string `json:"password_confirmation"`
}

// AcceptParams is the validated input for creating a client account.
type AcceptParams struct {
	Name     string
	Password string
}

// ValidateNew checks the payload for a new client account.
func (r AcceptRequest) ValidateNew() (AcceptParams, map[string]string) {
	fields := map[string]string{}
	name := strings.TrimSpace(r.Name)
	if msg := auth.ValidateName(name); msg != "" {
		fields["name"] = msg
	}
	auth.ValidateNewPassword(fields, r.Password, r.PasswordConfirmation)
	return AcceptParams{Name: name, Password: r.Password}, fields
}

// PartyDTO is the public view of the other side of a link.
type PartyDTO struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

func toPartyDTO(p Party) PartyDTO {
	return PartyDTO{ID: p.ID, Name: p.Name, Email: p.Email}
}

// InvitationDTO is a pending invitation as its freelancer sees it. The token is
// included so the freelancer can copy the link again.
type InvitationDTO struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	Token     string    `json:"token"`
	Expired   bool      `json:"expired"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

func toInvitationDTO(i Invitation) InvitationDTO {
	return InvitationDTO{
		ID: i.ID, Email: i.Email, Name: i.Name, Token: i.Token,
		Expired: i.Expired(), ExpiresAt: i.ExpiresAt, CreatedAt: i.CreatedAt,
	}
}

// LookupDTO is what the public invitation page receives.
type LookupDTO struct {
	Email         string    `json:"email"`
	Name          string    `json:"name"`
	Freelancer    PartyDTO  `json:"freelancer"`
	AccountExists bool      `json:"account_exists"`
	ExpiresAt     time.Time `json:"expires_at"`
}

// LinkDTO is a client (for a freelancer) or a freelancer (for a client).
type LinkDTO struct {
	PartyDTO
	LinkedAt  time.Time `json:"linked_at"`
	OpenTasks int       `json:"open_tasks"`
}

func toLinkDTOs(links []Link) []LinkDTO {
	out := make([]LinkDTO, 0, len(links))
	for _, l := range links {
		out = append(out, LinkDTO{PartyDTO: toPartyDTO(l.Party), LinkedAt: l.LinkedAt, OpenTasks: l.OpenTasks})
	}
	return out
}
