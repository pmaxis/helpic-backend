package projects

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/pmaxis/helpic/internal/user"
)

// Repository handles PostgreSQL access for projects. Every read is scoped to
// projects the user takes part in.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository creates a projects Repository backed by the given pool.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

const selectProject = `SELECT p.id, p.created_by, p.name, p.description, p.created_at, p.updated_at,
		f.id, f.name, f.email, c.id, c.name, c.email,
		(SELECT count(*) FROM tasks t WHERE t.project_id = p.id AND t.status IN ('new', 'in_progress')),
		(SELECT count(*) FROM tasks t WHERE t.project_id = p.id)
	FROM projects p
	JOIN users f ON f.id = p.freelancer_id
	LEFT JOIN users c ON c.id = p.client_id`

func scanProject(row pgx.Row) (Project, error) {
	var (
		p                          Project
		clientID, clientName, mail *string
	)
	err := row.Scan(&p.ID, &p.CreatedBy, &p.Name, &p.Description, &p.CreatedAt, &p.UpdatedAt,
		&p.Freelancer.ID, &p.Freelancer.Name, &p.Freelancer.Email,
		&clientID, &clientName, &mail, &p.OpenTasks, &p.TotalTasks)
	if err == nil && clientID != nil {
		p.Client = &Party{ID: *clientID, Name: *clientName, Email: *mail}
	}
	return p, err
}

// List returns the user's projects, alphabetically. counterpartID keeps the
// projects with one client (for a freelancer) or one freelancer (for a
// client); PersonalFilter keeps a freelancer's personal projects.
func (r *Repository) List(ctx context.Context, userID, role, counterpartID string) ([]Project, error) {
	// A client acts for their company, so a member sees the owner's projects.
	own, other := "p.freelancer_id = $1", "p.client_id"
	if role == user.RoleClient {
		own, other = "p.client_id = acting_client($1)", "p.freelancer_id"
	}
	rows, err := r.db.Query(ctx, selectProject+` WHERE `+own+`
		AND ($2 = '' OR `+other+`::text = $2 OR ($2 = '`+PersonalFilter+`' AND `+other+` IS NULL))
		ORDER BY lower(p.name), p.created_at`, userID, counterpartID)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Project, error) { return scanProject(row) })
	if err != nil {
		return nil, fmt.Errorf("scan projects: %w", err)
	}
	return out, nil
}

// Get loads a project the user takes part in, or ErrNotFound.
func (r *Repository) Get(ctx context.Context, userID, id string) (Project, error) {
	p, err := scanProject(r.db.QueryRow(ctx, selectProject+`
		WHERE p.id::text = $1 AND (p.freelancer_id = $2 OR p.client_id = acting_client($2))`, id, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	if err != nil {
		return Project{}, fmt.Errorf("get project: %w", err)
	}
	return p, nil
}

// ActingClient is the client the user acts as: their company's owner for a
// member, themselves otherwise.
func (r *Repository) ActingClient(ctx context.Context, userID string) (string, error) {
	var id string
	if err := r.db.QueryRow(ctx, `SELECT acting_client($1::uuid)::text`, userID).Scan(&id); err != nil {
		return "", fmt.Errorf("acting client: %w", err)
	}
	return id, nil
}

// CreatePersonal inserts a freelancer's personal project.
func (r *Repository) CreatePersonal(ctx context.Context, freelancerID string, p Params) (string, error) {
	var id string
	err := r.db.QueryRow(ctx, `INSERT INTO projects (freelancer_id, created_by, name, description)
		VALUES ($1, $1, $2, $3) RETURNING id`, freelancerID, p.Name, p.Description).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("create personal project: %w", err)
	}
	return id, nil
}

// Create inserts a project between a linked freelancer and client; when they
// aren't linked it returns ErrNotLinked.
func (r *Repository) Create(ctx context.Context, freelancerID, clientID, createdBy string, p Params) (string, error) {
	var id string
	err := r.db.QueryRow(ctx, `INSERT INTO projects (freelancer_id, client_id, created_by, name, description)
		SELECT fc.freelancer_id, fc.client_id, $3, $4, $5
		FROM freelancer_clients fc
		WHERE fc.freelancer_id::text = $1 AND fc.client_id::text = $2
		RETURNING id`, freelancerID, clientID, createdBy, p.Name, p.Description).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotLinked
	}
	if err != nil {
		return "", fmt.Errorf("create project: %w", err)
	}
	return id, nil
}

// Update rewrites the project's name and description.
func (r *Repository) Update(ctx context.Context, id string, p Params) error {
	if _, err := r.db.Exec(ctx, `UPDATE projects SET name = $2, description = $3 WHERE id = $1`,
		id, p.Name, p.Description); err != nil {
		return fmt.Errorf("update project: %w", err)
	}
	return nil
}

// Delete removes the project; its tasks stay, without a project.
func (r *Repository) Delete(ctx context.Context, id string) error {
	if _, err := r.db.Exec(ctx, `DELETE FROM projects WHERE id = $1`, id); err != nil {
		return fmt.Errorf("delete project: %w", err)
	}
	return nil
}
