package branding

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	maxDetails      = 1000
	maxDetailsLines = 15
)

var hexColor = regexp.MustCompile(`^#[0-9a-f]{6}$`)

// Request is the JSON body for PUT /api/v1/me/branding.
type Request struct {
	// Color is "#rrggbb"; null or "" for none.
	Color   *string `json:"color"`
	Details string  `json:"details"`
}

// Params is the validated look; a nil Color clears it.
type Params struct {
	Color   *string
	Details string
}

// Validate returns the color in lower case, the details trimmed, and
// per-field errors.
func (r Request) Validate() (Params, map[string]string) {
	fields := map[string]string{}
	p := Params{Details: strings.TrimSpace(strings.ReplaceAll(r.Details, "\r\n", "\n"))}
	if r.Color != nil {
		if c := strings.ToLower(strings.TrimSpace(*r.Color)); c != "" {
			if !hexColor.MatchString(c) {
				fields["color"] = "Choose a color like #1d4ed8"
			}
			p.Color = &c
		}
	}
	switch {
	case utf8.RuneCountInString(p.Details) > maxDetails:
		fields["details"] = "Business details are too long"
	case strings.Count(p.Details, "\n")+1 > maxDetailsLines:
		fields["details"] = "Keep business details to 15 lines"
	}
	return p, fields
}

// DTO is the look as the API shows it, on its own and on a report.
type DTO struct {
	Color   *string `json:"color"`
	Details string  `json:"details"`
	// LogoID changes with every new logo, so its address can be cached;
	// null without one.
	LogoID *string `json:"logo_id"`
}

// ToDTO maps a look.
func ToDTO(b Branding) DTO {
	d := DTO{Color: b.Color, Details: b.Details}
	if b.LogoID != "" {
		id := b.LogoID
		d.LogoID = &id
	}
	return d
}

// Response wraps a look.
type Response struct {
	Branding DTO `json:"branding"`
}
