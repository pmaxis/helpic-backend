package board

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/pmaxis/helpic/internal/request"
	"github.com/pmaxis/helpic/internal/response"
)

// Labels tag tasks. Like the columns, a set of labels belongs to a pair's
// board; unlike the columns, both sides make, rename and remove them.

var (
	ErrLabelNotFound = errors.New("label not found")
	// ErrLabelExists is returned for a second label with the same name.
	ErrLabelExists = errors.New("label already exists")
	// ErrLabelsNotAllowed is returned when someone outside a pair, or a
	// contractor, changes its labels.
	ErrLabelsNotAllowed = errors.New("not allowed to change these labels")
)

// LabelColors is the palette a label's color comes from; the frontend maps
// each name to its shades.
var LabelColors = []string{"gray", "red", "orange", "amber", "green", "teal", "blue", "violet", "pink"}

const maxLabelName = 40

// Label is one tag of a pair's board, with how many tasks carry it.
type Label struct {
	ID        string
	Pair      Pair
	Name      string
	Color     string
	TaskCount int
}

// --- repository ------------------------------------------------------------------

const uniqueViolation = "23505"

// Labels returns a pair's labels by name.
func (r *Repository) Labels(ctx context.Context, p Pair) ([]Label, error) {
	rows, err := r.db.Query(ctx, `SELECT l.id, l.freelancer_id, coalesce(l.client_id::text, ''), l.name, l.color,
			(SELECT count(*) FROM task_labels tl WHERE tl.label_id = l.id)
		FROM labels l
		WHERE l.freelancer_id::text = $1 AND l.client_id IS NOT DISTINCT FROM NULLIF($2, '')::uuid
		ORDER BY lower(l.name)`, p.FreelancerID, p.ClientID)
	if err != nil {
		return nil, fmt.Errorf("list labels: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Label, error) {
		var l Label
		err := row.Scan(&l.ID, &l.Pair.FreelancerID, &l.Pair.ClientID, &l.Name, &l.Color, &l.TaskCount)
		return l, err
	})
	if err != nil {
		return nil, fmt.Errorf("scan labels: %w", err)
	}
	return out, nil
}

// Label loads a label one of whose sides is the user, or ErrLabelNotFound.
func (r *Repository) Label(ctx context.Context, userID, id string) (Label, error) {
	var l Label
	err := r.db.QueryRow(ctx, `SELECT id, freelancer_id, coalesce(client_id::text, ''), name, color
		FROM labels WHERE id::text = $1 AND (freelancer_id = $2 OR client_id = acting_client($2))`, id, userID).
		Scan(&l.ID, &l.Pair.FreelancerID, &l.Pair.ClientID, &l.Name, &l.Color)
	if errors.Is(err, pgx.ErrNoRows) {
		return Label{}, ErrLabelNotFound
	}
	if err != nil {
		return Label{}, fmt.Errorf("get label: %w", err)
	}
	return l, nil
}

// CreateLabel adds a label to a pair's board.
func (r *Repository) CreateLabel(ctx context.Context, p Pair, name, color string) (string, error) {
	var id string
	err := r.db.QueryRow(ctx, `INSERT INTO labels (freelancer_id, client_id, name, color)
		VALUES ($1, NULLIF($2, '')::uuid, $3, $4) RETURNING id`, p.FreelancerID, p.ClientID, name, color).Scan(&id)
	if isUnique(err) {
		return "", ErrLabelExists
	}
	if err != nil {
		return "", fmt.Errorf("create label: %w", err)
	}
	return id, nil
}

// UpdateLabel renames or recolors a label.
func (r *Repository) UpdateLabel(ctx context.Context, id, name, color string) error {
	_, err := r.db.Exec(ctx, `UPDATE labels SET name = $2, color = $3 WHERE id = $1`, id, name, color)
	if isUnique(err) {
		return ErrLabelExists
	}
	if err != nil {
		return fmt.Errorf("update label: %w", err)
	}
	return nil
}

// DeleteLabel removes a label from the board and from its tasks.
func (r *Repository) DeleteLabel(ctx context.Context, id string) error {
	if _, err := r.db.Exec(ctx, `DELETE FROM labels WHERE id = $1`, id); err != nil {
		return fmt.Errorf("delete label: %w", err)
	}
	return nil
}

func isUnique(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == uniqueViolation
}

// --- service ------------------------------------------------------------------------

// Labels returns the labels of the board the user asked for.
func (s *Service) Labels(ctx context.Context, userID, role string, sc Scope) ([]Label, error) {
	p, err := s.Pair(ctx, userID, role, sc)
	if err != nil {
		return nil, err
	}
	return s.repo.Labels(ctx, p)
}

// editsLabels reports whether the user may change a pair's labels: its
// freelancer, or its client and their company — but not a contractor, who
// works on only some of the pair's tasks. Labels are the pair's, so taking
// part in one of its projects doesn't make them yours.
func (s *Service) editsLabels(ctx context.Context, userID string, p Pair) error {
	if userID == p.FreelancerID {
		return nil
	}
	if p.ClientID != "" {
		acting, err := s.repo.ActingClient(ctx, userID)
		if err != nil {
			return err
		}
		if acting == p.ClientID {
			contractor, err := s.repo.IsContractor(ctx, userID)
			if err != nil || !contractor {
				return err
			}
		}
	}
	return ErrLabelsNotAllowed
}

// CreateLabel adds a label to the board the user asked for.
func (s *Service) CreateLabel(ctx context.Context, userID, role string, sc Scope, lp LabelParams) (Label, error) {
	p, err := s.Pair(ctx, userID, role, sc)
	if err != nil {
		return Label{}, err
	}
	if err := s.editsLabels(ctx, userID, p); err != nil {
		return Label{}, err
	}
	id, err := s.repo.CreateLabel(ctx, p, lp.Name, lp.Color)
	if err != nil {
		return Label{}, err
	}
	return s.repo.Label(ctx, userID, id)
}

// UpdateLabel renames or recolors a label of one of the user's boards.
func (s *Service) UpdateLabel(ctx context.Context, userID, id string, lp LabelParams) (Label, error) {
	l, err := s.repo.Label(ctx, userID, id)
	if err != nil {
		return Label{}, err
	}
	if err := s.editsLabels(ctx, userID, l.Pair); err != nil {
		return Label{}, err
	}
	if err := s.repo.UpdateLabel(ctx, l.ID, lp.Name, lp.Color); err != nil {
		return Label{}, err
	}
	return s.repo.Label(ctx, userID, id)
}

// DeleteLabel removes a label of one of the user's boards, and takes it off
// every task.
func (s *Service) DeleteLabel(ctx context.Context, userID, id string) error {
	l, err := s.repo.Label(ctx, userID, id)
	if err != nil {
		return err
	}
	if err := s.editsLabels(ctx, userID, l.Pair); err != nil {
		return err
	}
	return s.repo.DeleteLabel(ctx, l.ID)
}

// --- input and output -------------------------------------------------------------

// LabelRequest is the JSON body for creating or editing a label. With and
// ProjectID pick the board on create, like the GET query.
type LabelRequest struct {
	With      string `json:"with"`
	ProjectID string `json:"project_id"`
	Name      string `json:"name"`
	Color     string `json:"color"`
}

// LabelParams is the validated label input.
type LabelParams struct {
	Name  string
	Color string
}

// Validate checks the payload and returns per-field error messages.
func (r LabelRequest) Validate() (LabelParams, map[string]string) {
	fields := map[string]string{}
	p := LabelParams{Name: strings.Join(strings.Fields(r.Name), " "), Color: strings.TrimSpace(r.Color)}
	if p.Name == "" {
		fields["name"] = "Name the label"
	} else if utf8.RuneCountInString(p.Name) > maxLabelName {
		fields["name"] = "The name is too long"
	}
	if !slices.Contains(LabelColors, p.Color) {
		fields["color"] = "Choose a color"
	}
	return p, fields
}

// LabelDTO is a label of a board.
type LabelDTO struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Color     string `json:"color"`
	TaskCount int    `json:"task_count"`
}

// LabelsResponse is a board's labels and the palette their colors come from.
type LabelsResponse struct {
	Labels []LabelDTO `json:"labels"`
	Colors []string   `json:"colors"`
}

func toLabelDTO(l Label) LabelDTO {
	return LabelDTO{ID: l.ID, Name: l.Name, Color: l.Color, TaskCount: l.TaskCount}
}

// --- handlers -------------------------------------------------------------------------

func (h *Handler) labelRoutes(r chi.Router) {
	r.Get("/labels", h.Labels)
	r.Post("/labels", h.CreateLabel)
	r.Put("/labels/{id}", h.UpdateLabel)
	r.Delete("/labels/{id}", h.DeleteLabel)
}

// Labels handles GET /api/v1/board/labels?with=…&project=….
//
//	@Summary	A board's labels
//	@Tags		labels
//	@Produce	json
//	@Param		with	query		string					false	"A counterpart's ID, or "personal""
//	@Param		project	query		string					false	"A project ID"
//	@Success	200		{object}	LabelsResponse			"OK"
//	@Failure	401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure	404		{object}	response.ErrorEnvelope	"Not found"
//	@Router		/board/labels [get]
func (h *Handler) Labels(w http.ResponseWriter, r *http.Request) {
	userID, role, ok := request.UserRole(w, r)
	if !ok {
		return
	}
	list, err := h.svc.Labels(r.Context(), userID, role, scopeOf(r))
	if err != nil {
		writeLabelErr(w, err)
		return
	}
	out := make([]LabelDTO, 0, len(list))
	for _, l := range list {
		out = append(out, toLabelDTO(l))
	}
	response.JSON(w, http.StatusOK, LabelsResponse{Labels: out, Colors: LabelColors})
}

// CreateLabel handles POST /api/v1/board/labels.
//
//	@Summary		Make a label
//	@Description	Either side of the board.
//	@Tags			labels
//	@Accept			json
//	@Produce		json
//	@Param			body	body		LabelRequest			true	"Request body"
//	@Success		201		{object}	map[string]LabelDTO		"label"
//	@Failure		400		{object}	response.ErrorEnvelope	"Invalid input"
//	@Failure		401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		404		{object}	response.ErrorEnvelope	"Not found"
//	@Router			/board/labels [post]
func (h *Handler) CreateLabel(w http.ResponseWriter, r *http.Request) {
	userID, role, ok := request.UserRole(w, r)
	if !ok {
		return
	}
	var req LabelRequest
	if !request.Decode(w, r, &req) {
		return
	}
	lp, fields := req.Validate()
	if len(fields) > 0 {
		response.ValidationError(w, fields)
		return
	}
	l, err := h.svc.CreateLabel(r.Context(), userID, role, Scope{With: req.With, ProjectID: req.ProjectID}, lp)
	if err != nil {
		writeLabelErr(w, err)
		return
	}
	response.JSON(w, http.StatusCreated, map[string]LabelDTO{"label": toLabelDTO(l)})
}

// UpdateLabel handles PUT /api/v1/board/labels/{id}.
//
//	@Summary	Rename or recolor a label
//	@Tags		labels
//	@Accept		json
//	@Produce	json
//	@Param		id		path		string					true	"Label ID"
//	@Param		body	body		LabelRequest			true	"Request body"
//	@Success	200		{object}	map[string]LabelDTO		"label"
//	@Failure	400		{object}	response.ErrorEnvelope	"Invalid input"
//	@Failure	401		{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure	404		{object}	response.ErrorEnvelope	"Not found"
//	@Router		/board/labels/{id} [put]
func (h *Handler) UpdateLabel(w http.ResponseWriter, r *http.Request) {
	userID, _, ok := request.UserRole(w, r)
	if !ok {
		return
	}
	var req LabelRequest
	if !request.Decode(w, r, &req) {
		return
	}
	lp, fields := req.Validate()
	if len(fields) > 0 {
		response.ValidationError(w, fields)
		return
	}
	l, err := h.svc.UpdateLabel(r.Context(), userID, chi.URLParam(r, "id"), lp)
	if err != nil {
		writeLabelErr(w, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]LabelDTO{"label": toLabelDTO(l)})
}

// DeleteLabel handles DELETE /api/v1/board/labels/{id}.
//
//	@Summary		Delete a label
//	@Description	Takes it off every task too.
//	@Tags			labels
//	@Param			id	path	string	true	"Label ID"
//	@Success		204	"No content"
//	@Failure		401	{object}	response.ErrorEnvelope	"Not signed in"
//	@Failure		404	{object}	response.ErrorEnvelope	"Not found"
//	@Router			/board/labels/{id} [delete]
func (h *Handler) DeleteLabel(w http.ResponseWriter, r *http.Request) {
	userID, _, ok := request.UserRole(w, r)
	if !ok {
		return
	}
	if err := h.svc.DeleteLabel(r.Context(), userID, chi.URLParam(r, "id")); err != nil {
		writeLabelErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeLabelErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrLabelNotFound):
		response.Error(w, http.StatusNotFound, response.CodeNotFound, "Label not found")
	case errors.Is(err, ErrLabelsNotAllowed):
		response.Error(w, http.StatusForbidden, response.CodeForbidden, "Only the board's freelancer and the client's team change its labels")
	case errors.Is(err, ErrLabelExists):
		response.ValidationError(w, map[string]string{"name": "There's already a label with this name"})
	default:
		writeErr(w, err)
	}
}
