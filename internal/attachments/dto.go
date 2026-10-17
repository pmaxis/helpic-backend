package attachments

import "time"

// UploaderDTO is who added a file.
type UploaderDTO struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// DTO is a file as a side of its task sees it.
type DTO struct {
	ID          string `json:"id"`
	TaskID      string `json:"task_id"`
	FileName    string `json:"file_name"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
	// IsImage marks a picture the browser may preview.
	IsImage    bool        `json:"is_image"`
	UploadedBy UploaderDTO `json:"uploaded_by"`
	// CanDelete is true for the uploader.
	CanDelete bool      `json:"can_delete"`
	CreatedAt time.Time `json:"created_at"`
}

// ListResponse is a task's files and the largest file the server takes.
type ListResponse struct {
	Attachments  []DTO `json:"attachments"`
	MaxSizeBytes int64 `json:"max_size_bytes"`
}

// ToDTO maps a file for the viewer.
func ToDTO(a Attachment, viewerID string) DTO {
	return DTO{
		ID:          a.ID,
		TaskID:      a.TaskID,
		FileName:    a.FileName,
		ContentType: a.ContentType,
		SizeBytes:   a.SizeBytes,
		IsImage:     a.IsImage(),
		UploadedBy:  UploaderDTO(a.Uploader),
		CanDelete:   a.Uploader.ID == viewerID,
		CreatedAt:   a.CreatedAt,
	}
}
