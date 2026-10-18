package board

import (
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/pmaxis/helpic/internal/tasks"
)

const maxColumnName = 60

// ColumnRequest is the JSON body for creating or editing a column. With and
// ProjectID pick the board on create, like the GET query. CategoryID is one
// of the board's categories; Category is the older bucket name (new,
// in_progress, done, cancelled) and still selects that seeded category.
type ColumnRequest struct {
	With       string `json:"with"`
	ProjectID  string `json:"project_id"`
	Name       string `json:"name"`
	CategoryID string `json:"category_id"`
	Category   string `json:"category"`
}

// ColumnParams is the validated column input.
type ColumnParams struct {
	Name       string
	CategoryID string
	Category   string
}

// Validate checks the payload and returns per-field error messages.
func (r ColumnRequest) Validate() (ColumnParams, map[string]string) {
	fields := map[string]string{}
	p := ColumnParams{
		Name:       strings.TrimSpace(r.Name),
		CategoryID: strings.TrimSpace(r.CategoryID),
		Category:   strings.TrimSpace(r.Category),
	}
	if p.Name == "" {
		fields["name"] = "Name is required"
	} else if utf8.RuneCountInString(p.Name) > maxColumnName {
		fields["name"] = "Name is too long"
	}
	if p.CategoryID == "" && !slices.Contains(tasks.Statuses, p.Category) {
		fields["category"] = "Choose a category"
	}
	return p, fields
}

// OrderRequest is the JSON body for PUT /api/v1/board/columns.
type OrderRequest struct {
	IDs []string `json:"ids"`
}

// MoveRequest is the JSON body for POST /api/v1/board/moves.
type MoveRequest struct {
	TaskID   string `json:"task_id"`
	ColumnID string `json:"column_id"`
	// AfterTaskID is the card the moved one goes right after; empty puts it
	// at the top of the column.
	AfterTaskID string `json:"after_task_id"`
	// Lane, instead of ColumnID, moves the card to a lane of the all board.
	Lane string `json:"lane"`
}

// ColumnDTO is a column, with its tasks on the board endpoint.
type ColumnDTO struct {
	ID                  string       `json:"id"`
	Name                string       `json:"name"`
	Category            string       `json:"category"`
	CategoryID          string       `json:"category_id"`
	CategoryName        string       `json:"category_name"`
	ParticipantsCanMove bool         `json:"participants_can_move"`
	Tasks               *[]tasks.DTO `json:"tasks,omitempty"`
}

// CategoryDTO is a category the board's manager configured.
type CategoryDTO struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	Effect              string `json:"effect"`
	ParticipantsCanMove bool   `json:"participants_can_move"`
	AllowsDelete        bool   `json:"allows_delete"`
}

// BoardDTO is a board as the viewer sees it.
type BoardDTO struct {
	// CanManage is true for the freelancer, and for the author of the project
	// the board was opened for.
	CanManage  bool          `json:"can_manage"`
	Columns    []ColumnDTO   `json:"columns"`
	Categories []CategoryDTO `json:"categories,omitempty"`
	// Merged is true for the all board: its columns are lanes merging the
	// pairs' columns of one name, moved to with MoveRequest.Lane.
	Merged bool `json:"merged"`
}

func columnDTO(c Column) ColumnDTO {
	return ColumnDTO{
		ID: c.ID, Name: c.Name, Category: c.Category,
		CategoryID: c.CategoryID, CategoryName: c.CategoryName,
		ParticipantsCanMove: c.ParticipantsCanMove,
	}
}

// CategoryDTOs maps a board's categories.
func CategoryDTOs(cats []Category) []CategoryDTO {
	out := make([]CategoryDTO, 0, len(cats))
	for _, c := range cats {
		out = append(out, CategoryDTO{
			ID: c.ID, Name: c.Name, Effect: c.Effect(),
			ParticipantsCanMove: c.ParticipantsCanMove, AllowsDelete: c.AllowsDelete,
		})
	}
	return out
}

// LanesDTO maps a board with its tasks for the viewer.
func LanesDTO(p Pair, lanes []Lane, role, viewerID string) BoardDTO {
	out := BoardDTO{CanManage: CanManage(p, viewerID), Columns: make([]ColumnDTO, 0, len(lanes))}
	for _, l := range lanes {
		c := columnDTO(l.Column)
		ts := make([]tasks.DTO, 0, len(l.Tasks))
		for _, t := range l.Tasks {
			ts = append(ts, tasks.ToDTO(t, role, viewerID))
		}
		c.Tasks = &ts
		out.Columns = append(out.Columns, c)
	}
	return out
}

// MergedDTO maps the all board: nobody manages its lanes.
func MergedDTO(lanes []Lane, role, viewerID string) BoardDTO {
	out := LanesDTO(Pair{}, lanes, role, viewerID)
	out.Merged = true
	return out
}

// ColumnsDTO maps a board's columns without tasks.
func ColumnsDTO(p Pair, cols []Column, viewerID string) BoardDTO {
	out := BoardDTO{CanManage: CanManage(p, viewerID), Columns: make([]ColumnDTO, 0, len(cols))}
	for _, c := range cols {
		out.Columns = append(out.Columns, columnDTO(c))
	}
	return out
}
