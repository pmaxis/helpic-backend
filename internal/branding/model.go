// Package branding is how a freelancer's reports look to their clients: a
// logo over the title, an accent color, and their business details (name,
// tax number, bank account…) at the end, written as they want them printed.
// A draft shows the look as it is now; a sent report keeps the one it was
// sent with (see package reports).
package branding

import (
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("logo not found")
	// ErrNotImage is returned for a logo that isn't a PNG or JPEG image.
	ErrNotImage = errors.New("not a PNG or JPEG image")
	// ErrTooLarge is returned for a logo file over MaxLogoBytes.
	ErrTooLarge = errors.New("logo is too large")
	// ErrTooBig is returned for a logo over MaxLogoSide pixels on a side.
	ErrTooBig = errors.New("logo has too many pixels")
)

const (
	// MaxLogoBytes caps a logo's file: it's printed small, at the top of a page.
	MaxLogoBytes = 512 << 10
	// MaxLogoSide caps a logo's width and height in pixels.
	MaxLogoSide = 4000
)

// Branding is a freelancer's look on their reports.
type Branding struct {
	// Color is the accent, "#rrggbb"; nil for the plain black.
	Color *string
	// Details are printed at the end of a report, line by line as written.
	Details string
	// LogoID is "" without a logo.
	LogoID string
}

// Logo is a logo's file.
type Logo struct {
	ID          string
	ContentType string
	Data        []byte
	CreatedAt   time.Time
}
