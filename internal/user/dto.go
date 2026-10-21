package user

import "time"

// DTO is the public representation of a user. It deliberately omits the
// password hash and any other sensitive field.
type DTO struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Name      string `json:"name"`
	Role      string `json:"role"`
	Currency  string `json:"currency"`
	// HourlyRateCents is a freelancer's rate for an hour of work; null when unset.
	HourlyRateCents *int64 `json:"hourly_rate_cents"`
	// Locale is the language they read Helpic in: "en" or "uk".
	Locale string `json:"locale"`
	// MemberOf is set for a member of a client's company; null otherwise.
	MemberOf  *TeamDTO  `json:"member_of"`
	CreatedAt time.Time `json:"created_at"`
}

// TeamDTO is the client company a member belongs to.
type TeamDTO struct {
	OwnerID string `json:"owner_id"`
	Name    string `json:"name"`
	// Role is "member" (sees all the company's work) or "contractor" (only
	// the tasks they set or were added to).
	Role string `json:"role"`
}

// Response wraps a user DTO, matching the API contract: {"user": {...}}.
type Response struct {
	User DTO `json:"user"`
}

// ToDTO maps a domain user to its public representation.
func ToDTO(u User) DTO {
	d := DTO{ID: u.ID, Email: u.Email, FirstName: u.FirstName, LastName: u.LastName, Name: u.Name, Role: u.Role, Currency: u.Currency, HourlyRateCents: u.HourlyRateCents, Locale: u.Locale, CreatedAt: u.CreatedAt}
	if u.TeamOwnerID != nil {
		d.MemberOf = &TeamDTO{OwnerID: *u.TeamOwnerID, Name: u.TeamName, Role: u.TeamRole}
	}
	return d
}

// NewResponse builds the standard {"user": {...}} response body.
func NewResponse(u User) Response {
	return Response{User: ToDTO(u)}
}
