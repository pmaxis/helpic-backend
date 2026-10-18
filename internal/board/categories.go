package board

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/pmaxis/helpic/internal/request"
	"github.com/pmaxis/helpic/internal/response"
)

const categoryFields = `id, freelancer_id, coalesce(client_id::text, ''), coalesce(project_id::text, ''), name,
	counts_open, counts_done, allows_delete, participants_can_move, position, coalesce(system_key, '')`

func scanCategory(row pgx.Row) (Category, error) {
	var c Category
	err := row.Scan(&c.ID, &c.Pair.FreelancerID, &c.Pair.ClientID, &c.Pair.ProjectID, &c.Name, &c.CountsOpen, &c.CountsDone,
		&c.AllowsDelete, &c.ParticipantsCanMove, &c.Position, &c.SystemKey)
	return c, err
}

// Categories returns a board's categories in order.
func (r *Repository) Categories(ctx context.Context, p Pair) ([]Category, error) {
	rows, err := r.db.Query(ctx, `SELECT `+categoryFields+` FROM workflow_categories
		WHERE `+workflowOf+`
		ORDER BY position, created_at`, p.FreelancerID, p.ClientID, p.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("list categories: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Category, error) { return scanCategory(row) })
	if err != nil {
		return nil, fmt.Errorf("scan categories: %w", err)
	}
	return out, nil
}

// Category returns one category, or ErrCategoryNotFound.
func (r *Repository) Category(ctx context.Context, id string) (Category, error) {
	c, err := scanCategory(r.db.QueryRow(ctx, `SELECT `+categoryFields+` FROM workflow_categories WHERE id::text = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Category{}, ErrCategoryNotFound
	}
	if err != nil {
		return Category{}, fmt.Errorf("get category: %w", err)
	}
	return c, nil
}

// CategoryByBucket returns the seeded category for a status bucket, or the
// first category that maps to it.
func (r *Repository) CategoryByBucket(ctx context.Context, p Pair, bucket string) (Category, error) {
	c, err := scanCategory(r.db.QueryRow(ctx, `SELECT `+categoryFields+` FROM workflow_categories
		WHERE `+workflowOf+`
		  AND (system_key = $4 OR CASE
				WHEN counts_done THEN 'done'
				WHEN NOT counts_open THEN 'cancelled'
				WHEN allows_delete THEN 'new'
				ELSE 'in_progress'
			END = $4)
		ORDER BY system_key = $4 DESC, position
		LIMIT 1`, p.FreelancerID, p.ClientID, p.ProjectID, bucket))
	if errors.Is(err, pgx.ErrNoRows) {
		return Category{}, ErrCategoryNotFound
	}
	if err != nil {
		return Category{}, fmt.Errorf("category by bucket: %w", err)
	}
	return c, nil
}

// CreateCategory appends a category. A duplicate name returns ErrCategoryExists.
func (r *Repository) CreateCategory(ctx context.Context, p Pair, c Category) (Category, error) {
	row := r.db.QueryRow(ctx, `INSERT INTO workflow_categories (
			freelancer_id, client_id, project_id, name, counts_open, counts_done, allows_delete, participants_can_move, position)
		SELECT $1::uuid, NULLIF($2, '')::uuid, NULLIF($3, '')::uuid, $4, $5, $6, $7, $8, coalesce(max(position) + 1, 0)
		FROM workflow_categories
		WHERE `+workflowOf+`
		RETURNING `+categoryFields,
		p.FreelancerID, p.ClientID, p.ProjectID, c.Name, c.CountsOpen, c.CountsDone, c.AllowsDelete, c.ParticipantsCanMove)
	out, err := scanCategory(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return Category{}, ErrCategoryExists
		}
		return Category{}, fmt.Errorf("create category: %w", err)
	}
	return out, nil
}

// UpdateCategory saves a category's name and settings, and the tasks in its
// columns take the new bucket.
func (r *Repository) UpdateCategory(ctx context.Context, c Category) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	tag, err := tx.Exec(ctx, `UPDATE workflow_categories
		SET name = $2, counts_open = $3, counts_done = $4, allows_delete = $5, participants_can_move = $6
		WHERE id::text = $1`, c.ID, c.Name, c.CountsOpen, c.CountsDone, c.AllowsDelete, c.ParticipantsCanMove)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return ErrCategoryExists
		}
		return fmt.Errorf("update category: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrCategoryNotFound
	}
	bucket := c.Bucket()
	if _, err := tx.Exec(ctx, `UPDATE workflow_statuses SET category = $2 WHERE category_id::text = $1 AND category <> $2`, c.ID, bucket); err != nil {
		return fmt.Errorf("recategorise columns: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE tasks SET status = $2,
			completed_at = CASE WHEN $2 = 'done' THEN coalesce(completed_at, now()) END
		WHERE status_id IN (SELECT id FROM workflow_statuses WHERE category_id::text = $1)
		  AND status <> $2`, c.ID, bucket); err != nil {
		return fmt.Errorf("recategorise tasks: %w", err)
	}
	return tx.Commit(ctx)
}

// CategoryColumnCount returns how many columns use a category.
func (r *Repository) CategoryColumnCount(ctx context.Context, id string) (int, error) {
	var n int
	if err := r.db.QueryRow(ctx, `SELECT count(*) FROM workflow_statuses WHERE category_id::text = $1`, id).Scan(&n); err != nil {
		return 0, fmt.Errorf("count category columns: %w", err)
	}
	return n, nil
}

// DeleteCategory removes a category that no column uses.
func (r *Repository) DeleteCategory(ctx context.Context, id string) error {
	tag, err := r.db.Exec(ctx, `DELETE FROM workflow_categories WHERE id::text = $1`, id)
	if err != nil {
		return fmt.Errorf("delete category: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrCategoryNotFound
	}
	return nil
}

// categoryFor resolves the column's category: by id, or by the older bucket name.
func (s *Service) categoryFor(ctx context.Context, p Pair, cp ColumnParams) (Category, error) {
	if cp.CategoryID != "" {
		c, err := s.repo.Category(ctx, cp.CategoryID)
		if err != nil {
			return Category{}, err
		}
		if c.Pair.FreelancerID != p.FreelancerID || c.Pair.ClientID != p.ClientID || c.Pair.ProjectID != p.ProjectID {
			return Category{}, ErrCategoryNotFound
		}
		return c, nil
	}
	return s.repo.CategoryByBucket(ctx, p, cp.Category)
}

// Categories returns a board's categories, creating the defaults first.
func (s *Service) Categories(ctx context.Context, p Pair) ([]Category, error) {
	if _, err := s.repo.Columns(ctx, p); err != nil {
		return nil, err
	}
	return s.repo.Categories(ctx, p)
}

// CreateCategory adds a category. The freelancer may, and so may the author
// of a project on the board.
func (s *Service) CreateCategory(ctx context.Context, userID, role string, sc Scope, in CategoryInput) (Category, error) {
	p, err := s.Pair(ctx, userID, role, sc)
	if err != nil {
		return Category{}, err
	}
	if !CanManage(p, userID) {
		return Category{}, ErrNotManager
	}
	if _, err := s.repo.Columns(ctx, p); err != nil {
		return Category{}, err
	}
	c := Category{Name: in.Name}
	c.Apply(in.Effect, in.ParticipantsCanMove, in.AllowsDelete)
	return s.repo.CreateCategory(ctx, p, c)
}

// UpdateCategory renames a category or changes what its tasks count as.
func (s *Service) UpdateCategory(ctx context.Context, userID, id string, in CategoryInput) (Category, error) {
	c, err := s.managedCategory(ctx, userID, id)
	if err != nil {
		return Category{}, err
	}
	c.Name = in.Name
	c.Apply(in.Effect, in.ParticipantsCanMove, in.AllowsDelete)
	if err := s.repo.UpdateCategory(ctx, c); err != nil {
		return Category{}, err
	}
	return c, nil
}

// DeleteCategory removes a category that has no columns, unless it is the last one.
func (s *Service) DeleteCategory(ctx context.Context, userID, id string) error {
	c, err := s.managedCategory(ctx, userID, id)
	if err != nil {
		return err
	}
	cats, err := s.repo.Categories(ctx, c.Pair)
	if err != nil {
		return err
	}
	if len(cats) <= 1 {
		return ErrLastCategory
	}
	n, err := s.repo.CategoryColumnCount(ctx, id)
	if err != nil {
		return err
	}
	if n > 0 {
		return ErrCategoryInUse
	}
	return s.repo.DeleteCategory(ctx, id)
}

func (s *Service) managedCategory(ctx context.Context, userID, id string) (Category, error) {
	c, err := s.repo.Category(ctx, id)
	if err != nil {
		return Category{}, err
	}
	if err := s.managesPair(ctx, userID, c.Pair); err != nil {
		return Category{}, err
	}
	return c, nil
}

// managesPair reports whether the user may change this workflow. The
// freelancer may, and so may the author of the project it belongs to. The
// pair's client gets ErrNotManager; anyone else gets ErrNotFound.
func (s *Service) managesPair(ctx context.Context, userID string, p Pair) error {
	if userID == p.FreelancerID {
		return nil
	}
	if p.ProjectID != "" {
		author, err := s.repo.ProjectAuthor(ctx, p.ProjectID)
		if err != nil {
			return err
		}
		if author == userID {
			return nil
		}
	}
	if userID == p.ClientID {
		return ErrNotManager
	}
	return ErrNotFound
}

// CategoryInput is a validated category.
type CategoryInput struct {
	Name                string
	Effect              string
	ParticipantsCanMove bool
	AllowsDelete        bool
}

// CategoryRequest is the JSON body for creating or editing a category.
type CategoryRequest struct {
	With                string `json:"with"`
	ProjectID           string `json:"project_id"`
	Name                string `json:"name"`
	Effect              string `json:"effect"`
	ParticipantsCanMove bool   `json:"participants_can_move"`
	AllowsDelete        bool   `json:"allows_delete"`
}

// Validate checks the payload and returns per-field error messages.
func (r CategoryRequest) Validate() (CategoryInput, map[string]string) {
	fields := map[string]string{}
	in := CategoryInput{
		Name:                strings.TrimSpace(r.Name),
		Effect:              strings.TrimSpace(r.Effect),
		ParticipantsCanMove: r.ParticipantsCanMove,
		AllowsDelete:        r.AllowsDelete,
	}
	if in.Name == "" {
		fields["name"] = "Name is required"
	} else if utf8.RuneCountInString(in.Name) > maxColumnName {
		fields["name"] = "Name is too long"
	}
	switch in.Effect {
	case EffectOpen, EffectDone, EffectClosed:
	default:
		fields["effect"] = "Choose what tasks in this category count as"
	}
	return in, fields
}

func (h *Handler) categoryRoutes(r chi.Router) {
	r.Post("/categories", h.CreateCategory)
	r.Put("/categories/{id}", h.UpdateCategory)
	r.Delete("/categories/{id}", h.DeleteCategory)
}

// CreateCategory handles POST /api/v1/board/categories.
func (h *Handler) CreateCategory(w http.ResponseWriter, r *http.Request) {
	userID, role, ok := request.UserRole(w, r)
	if !ok {
		return
	}
	var req CategoryRequest
	if !request.Decode(w, r, &req) {
		return
	}
	in, fields := req.Validate()
	if len(fields) > 0 {
		response.ValidationError(w, fields)
		return
	}
	c, err := h.svc.CreateCategory(r.Context(), userID, role, Scope{With: req.With, ProjectID: req.ProjectID}, in)
	if err != nil {
		writeErr(w, err)
		return
	}
	response.JSON(w, http.StatusCreated, map[string]CategoryDTO{"category": categoryDTO(c)})
}

// UpdateCategory handles PUT /api/v1/board/categories/{id}.
func (h *Handler) UpdateCategory(w http.ResponseWriter, r *http.Request) {
	userID, _, ok := request.UserRole(w, r)
	if !ok {
		return
	}
	var req CategoryRequest
	if !request.Decode(w, r, &req) {
		return
	}
	in, fields := req.Validate()
	if len(fields) > 0 {
		response.ValidationError(w, fields)
		return
	}
	c, err := h.svc.UpdateCategory(r.Context(), userID, chi.URLParam(r, "id"), in)
	if err != nil {
		writeErr(w, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]CategoryDTO{"category": categoryDTO(c)})
}

// DeleteCategory handles DELETE /api/v1/board/categories/{id}.
func (h *Handler) DeleteCategory(w http.ResponseWriter, r *http.Request) {
	userID, _, ok := request.UserRole(w, r)
	if !ok {
		return
	}
	writeDone(w, http.StatusNoContent, h.svc.DeleteCategory(r.Context(), userID, chi.URLParam(r, "id")))
}

func categoryDTO(c Category) CategoryDTO {
	return CategoryDTO{
		ID: c.ID, Name: c.Name, Effect: c.Effect(),
		ParticipantsCanMove: c.ParticipantsCanMove, AllowsDelete: c.AllowsDelete,
	}
}
