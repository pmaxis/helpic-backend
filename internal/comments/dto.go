package comments

import (
	"strings"
	"time"
	"unicode/utf8"
)

const maxBody = 10000

// Request is the JSON body for writing or editing a comment.
type Request struct {
	Body string `json:"body"`
	// Pin puts a new comment on a spot of one of the task's images; an
	// edit keeps the comment where it was.
	Pin *PinRequest `json:"pin,omitempty"`
}

// PinRequest is a spot on an image: x and y are fractions of its width and
// height, from 0 to 1, from the top left corner.
type PinRequest struct {
	AttachmentID string  `json:"attachment_id"`
	X            float64 `json:"x"`
	Y            float64 `json:"y"`
}

// ValidatePin returns the pin, nil without one, and per-field errors.
func (r Request) ValidatePin() (*Pin, map[string]string) {
	if r.Pin == nil {
		return nil, nil
	}
	p := Pin{AttachmentID: strings.TrimSpace(r.Pin.AttachmentID), X: r.Pin.X, Y: r.Pin.Y}
	if p.AttachmentID == "" || p.X < 0 || p.X > 1 || p.Y < 0 || p.Y > 1 {
		return nil, map[string]string{"pin": "Point at a spot on the image"}
	}
	return &p, nil
}

// Validate returns the trimmed text and per-field errors.
func (r Request) Validate() (string, map[string]string) {
	body := strings.TrimSpace(r.Body)
	switch {
	case body == "":
		return body, map[string]string{"body": "Write something first"}
	case utf8.RuneCountInString(body) > maxBody:
		return body, map[string]string{"body": "The comment is too long"}
	}
	return body, nil
}

// AuthorDTO is who wrote a comment.
type AuthorDTO struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// PersonDTO is someone a comment can mention.
type PersonDTO struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// DTO is a comment as a side of its task sees it.
type DTO struct {
	ID     string    `json:"id"`
	TaskID string    `json:"task_id"`
	Author AuthorDTO `json:"author"`
	Body   string    `json:"body"`
	Edited bool      `json:"edited"`
	// CanEdit is true for the author, who may edit and delete it.
	CanEdit   bool      `json:"can_edit"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	// Pin is the spot on an image the comment is about; null for the task
	// as a whole. Its attachment_id is null once the image was deleted.
	Pin *PinDTO `json:"pin"`
}

// PinDTO is a spot on an image of the task.
type PinDTO struct {
	AttachmentID *string `json:"attachment_id"`
	X            float64 `json:"x"`
	Y            float64 `json:"y"`
}

// ToDTO maps a comment for the viewer.
func ToDTO(c Comment, viewerID string) DTO {
	d := DTO{
		ID:        c.ID,
		TaskID:    c.TaskID,
		Author:    AuthorDTO(c.Author),
		Body:      c.Body,
		Edited:    c.Edited(),
		CanEdit:   c.Author.ID == viewerID,
		CreatedAt: c.CreatedAt,
		UpdatedAt: c.UpdatedAt,
	}
	if c.Pin != nil {
		d.Pin = &PinDTO{X: c.Pin.X, Y: c.Pin.Y}
		if c.Pin.AttachmentID != "" {
			id := c.Pin.AttachmentID
			d.Pin.AttachmentID = &id
		}
	}
	return d
}
