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
}

// ToDTO maps a comment for the viewer.
func ToDTO(c Comment, viewerID string) DTO {
	return DTO{
		ID:        c.ID,
		TaskID:    c.TaskID,
		Author:    AuthorDTO(c.Author),
		Body:      c.Body,
		Edited:    c.Edited(),
		CanEdit:   c.Author.ID == viewerID,
		CreatedAt: c.CreatedAt,
		UpdatedAt: c.UpdatedAt,
	}
}
