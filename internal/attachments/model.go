// Package attachments stores files on tasks — mockups, specs, results. Both
// sides of a task see and download them; whoever uploaded a file may delete
// it. The bytes live on disk, one file per attachment, named by its ID. With
// a virus scanner set up, a new file downloads only once it was checked.
//
// A file can get new versions, each a file of its own in the same series;
// the client's side approves the latest version or asks for changes to it.
package attachments

import (
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("attachment not found")
	// ErrNotAllowed is returned when deleting someone else's file.
	ErrNotAllowed = errors.New("only the uploader deletes a file")
	// ErrTooLarge is returned for a file over the size limit.
	ErrTooLarge = errors.New("file is too large")
	// ErrQuota is returned when a file would take the uploader past the space
	// they have for files.
	ErrQuota = errors.New("upload quota used up")
	// ErrEmpty is returned for an upload without a file in it.
	ErrEmpty = errors.New("no file in the upload")
	// ErrScanPending is returned for a file not checked for malware yet.
	ErrScanPending = errors.New("file is being checked")
	// ErrInfected is returned for a file that had malware in it.
	ErrInfected = errors.New("file had malware")
	// ErrNotReviewer is returned when anyone but the task's client side
	// reviews a file: the freelancer, or on a personal task.
	ErrNotReviewer = errors.New("only the client side reviews a file")
	// ErrNotLatest is returned for reviewing a version a newer one replaced.
	ErrNotLatest = errors.New("a newer version replaced this file")
)

// What the client's side thinks of a version.
const (
	ReviewApproved         = "approved"
	ReviewChangesRequested = "changes_requested"
)

// Where a file is in its check for malware.
const (
	// ScanUnscanned is a file stored without a scanner set up: it downloads,
	// and is checked once there is one.
	ScanUnscanned = "unscanned"
	// ScanPending is waiting for the check and doesn't download yet.
	ScanPending  = "pending"
	ScanClean    = "clean"
	ScanInfected = "infected"
)

// Uploader is who added a file.
type Uploader struct {
	ID    string
	Name  string
	Email string
}

// Attachment is one file on a task.
type Attachment struct {
	ID          string
	TaskID      string
	Uploader    Uploader
	FileName    string
	ContentType string
	SizeBytes   int64
	CreatedAt   time.Time
	// Scan is one of the Scan… states; Threat names the malware in an
	// infected file.
	Scan   string
	Threat string
	// SeriesID is the first version's ID, shared by all versions of the
	// file; Version counts them from 1. Latest is whether no newer version
	// replaced this one.
	SeriesID string
	Version  int
	Latest   bool
	// Review is "" until the client's side approves the version or asks for
	// changes; Reviewer is who did, ReviewedAt when.
	Review     string
	Reviewer   *Uploader
	ReviewedAt *time.Time
}

// inlineTypes are the images a browser may show in place; anything else is
// only ever downloaded, so an uploaded page or script never runs on our origin.
var inlineTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true}

// IsImage reports whether the file is an image shown as a preview.
func (a Attachment) IsImage() bool { return inlineTypes[a.ContentType] }
