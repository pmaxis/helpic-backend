package attachments

import (
	"strings"
	"time"
)

// ReviewRequest is the JSON body for PUT /api/v1/attachments/{id}/review.
type ReviewRequest struct {
	// Review is "approved", "changes_requested", or "" to take it back.
	Review string `json:"review" enums:"approved,changes_requested,"`
}

// Validate returns the review and per-field errors.
func (r ReviewRequest) Validate() (string, map[string]string) {
	review := strings.TrimSpace(r.Review)
	if review != "" && review != ReviewApproved && review != ReviewChangesRequested {
		return "", map[string]string{"review": "Approve the file or ask for changes"}
	}
	return review, nil
}

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
	// Scan is the check for malware: "pending" doesn't download yet,
	// "infected" never does (Threat names what was found); "clean" and
	// "unscanned" (stored without a scanner) download.
	Scan   string `json:"scan" enums:"pending,clean,infected,unscanned"`
	Threat string `json:"threat,omitempty"`
	// SeriesID is shared by every version of the file (the first one's
	// ID); Version counts them from 1; Latest is the version shown.
	SeriesID string `json:"series_id"`
	Version  int    `json:"version"`
	Latest   bool   `json:"latest"`
	// Review is what the client's side thinks of the version: "approved",
	// "changes_requested", or null.
	Review     *string      `json:"review" enums:"approved,changes_requested"`
	ReviewedBy *UploaderDTO `json:"reviewed_by"`
	ReviewedAt *time.Time   `json:"reviewed_at"`
	// CanReview is true for the task's client side on the latest version.
	CanReview bool `json:"can_review"`
}

// ListResponse is a task's files and the largest file the server takes.
type ListResponse struct {
	Attachments  []DTO `json:"attachments"`
	MaxSizeBytes int64 `json:"max_size_bytes"`
}

// ToDTO maps a file for the viewer.
func ToDTO(a Attachment, viewerID string) DTO {
	d := DTO{
		ID:          a.ID,
		TaskID:      a.TaskID,
		FileName:    a.FileName,
		ContentType: a.ContentType,
		SizeBytes:   a.SizeBytes,
		IsImage:     a.IsImage(),
		UploadedBy:  UploaderDTO(a.Uploader),
		CanDelete:   a.Uploader.ID == viewerID,
		CreatedAt:   a.CreatedAt,
		Scan:        a.Scan,
		Threat:      a.Threat,
		SeriesID:    a.SeriesID,
		Version:     a.Version,
		Latest:      a.Latest,
		ReviewedAt:  a.ReviewedAt,
	}
	if a.Review != "" {
		review := a.Review
		d.Review = &review
	}
	if a.Reviewer != nil {
		rv := UploaderDTO(*a.Reviewer)
		d.ReviewedBy = &rv
	}
	return d
}
