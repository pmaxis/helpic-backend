package shares

import (
	"time"

	"github.com/pmaxis/helpic/internal/attachments"
)

// DTO is a task's link, as its creator or its freelancer sees it.
type DTO struct {
	// URL is the page the link opens, to copy and send.
	URL       string    `json:"url"`
	CreatedAt time.Time `json:"created_at"`
}

// Response is the task's link; share is null when the task has none.
type Response struct {
	Share *DTO `json:"share"`
}

// SubtaskDTO is a subtask under a shared task.
type SubtaskDTO struct {
	Title      string `json:"title"`
	Status     string `json:"status" enums:"new,in_progress,done"`
	StatusName string `json:"status_name"`
}

// FileDTO is a file on a shared task. Who uploaded it isn't said.
type FileDTO struct {
	ID          string `json:"id"`
	FileName    string `json:"file_name"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
	IsImage     bool   `json:"is_image"`
	// Scan: "pending" doesn't download yet, "infected" never does.
	Scan string `json:"scan" enums:"pending,clean,infected,unscanned"`
}

// TaskDTO is a task as its link shows it: no people, price, time or
// comments.
type TaskDTO struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status" enums:"new,in_progress,done"`
	StatusName  string `json:"status_name"`
	// DueOn is YYYY-MM-DD, or null.
	DueOn *string `json:"due_on"`
	// Archived is true once the task is closed: paid, or put away.
	Archived bool         `json:"archived"`
	Subtasks []SubtaskDTO `json:"subtasks"`
	Files    []FileDTO    `json:"files"`
}

// SharedResponse is what GET /shared/{token} answers.
type SharedResponse struct {
	Task TaskDTO `json:"task"`
}

func toTaskDTO(t Task, subtasks []Subtask, files []attachments.Attachment) TaskDTO {
	d := TaskDTO{
		Title:       t.Title,
		Description: t.Description,
		Status:      t.Status,
		StatusName:  t.StatusName,
		Archived:    t.Archived,
		Subtasks:    make([]SubtaskDTO, 0, len(subtasks)),
		Files:       make([]FileDTO, 0, len(files)),
	}
	if t.DueOn != nil {
		due := t.DueOn.Format("2006-01-02")
		d.DueOn = &due
	}
	for _, s := range subtasks {
		d.Subtasks = append(d.Subtasks, SubtaskDTO(s))
	}
	for _, a := range files {
		d.Files = append(d.Files, FileDTO{
			ID:          a.ID,
			FileName:    a.FileName,
			ContentType: a.ContentType,
			SizeBytes:   a.SizeBytes,
			IsImage:     a.IsImage(),
			Scan:        a.Scan,
		})
	}
	return d
}
