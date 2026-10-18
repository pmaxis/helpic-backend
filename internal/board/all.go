package board

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/pmaxis/helpic/internal/tasks"
	"github.com/pmaxis/helpic/internal/user"
)

// The "all" board is every task the user can see on one board: a freelancer's
// work with every client and their personal tasks, or a client's work with
// every freelancer. Each pair keeps its own columns, so the all board merges
// columns that share a category and a name ("Done" of every pair is one lane).
// A card dropped on a lane goes to the task's own pair's column of that name,
// or else to its first column of that category.

// AllWith is the Scope.With value that asks for the all board.
const AllWith = "all"

// NoProject is the project filter value that keeps tasks outside any project.
const NoProject = "none"

// laneKey names a merged lane: its category and lower-cased name.
func laneKey(category, name string) string {
	return category + ":" + strings.ToLower(strings.TrimSpace(name))
}

// splitLaneKey reads a lane key back; ok is false for anything else.
func splitLaneKey(key string) (category, name string, ok bool) {
	category, name, ok = strings.Cut(key, ":")
	return category, name, ok && slices.Contains(tasks.Statuses, category)
}

// AllColumns lists every workflow status of the freelancer's pairs.
func (r *Repository) AllColumns(ctx context.Context, freelancerID string) ([]Column, error) {
	if _, err := r.db.Exec(ctx, `SELECT ensure_workflow($1::uuid, NULL)`, freelancerID); err != nil {
		return nil, fmt.Errorf("ensure workflow: %w", err)
	}
	rows, err := r.db.Query(ctx, `SELECT `+columnFields+` FROM `+columnFrom+`
		WHERE ws.freelancer_id = $1 ORDER BY ws.position, ws.created_at`, freelancerID)
	if err != nil {
		return nil, fmt.Errorf("list all columns: %w", err)
	}
	defer rows.Close()
	var out []Column
	for rows.Next() {
		c, err := scanColumn(rows)
		if err != nil {
			return nil, fmt.Errorf("scan column: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ClientColumns lists every column of the client's boards with freelancers,
// creating each board's defaults the first time.
func (r *Repository) ClientColumns(ctx context.Context, clientID string) ([]Column, error) {
	if _, err := r.db.Exec(ctx, `SELECT ensure_workflow(freelancer_id, client_id)
		FROM freelancer_clients WHERE client_id = $1::uuid`, clientID); err != nil {
		return nil, fmt.Errorf("ensure workflows: %w", err)
	}
	rows, err := r.db.Query(ctx, `SELECT `+columnFields+` FROM `+columnFrom+`
		WHERE ws.client_id = $1::uuid ORDER BY ws.position, ws.created_at`, clientID)
	if err != nil {
		return nil, fmt.Errorf("list client columns: %w", err)
	}
	defer rows.Close()
	var out []Column
	for rows.Next() {
		c, err := scanColumn(rows)
		if err != nil {
			return nil, fmt.Errorf("scan column: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// mergeColumns makes the all board's lanes: one per category and name, in
// category order, then by where the name first comes on any board.
func mergeColumns(cols []Column) []Column {
	type lane struct {
		col   Column
		order int
	}
	byKey := map[string]*lane{}
	var lanes []*lane
	for _, c := range cols {
		key := laneKey(c.Category, c.Name)
		if l, ok := byKey[key]; ok {
			l.order = min(l.order, c.Position)
			continue
		}
		l := &lane{col: Column{ID: key, Name: c.Name, Category: c.Category, Position: c.Position}, order: c.Position}
		byKey[key] = l
		lanes = append(lanes, l)
	}
	slices.SortStableFunc(lanes, func(a, b *lane) int {
		if d := slices.Index(tasks.Statuses, a.col.Category) - slices.Index(tasks.Statuses, b.col.Category); d != 0 {
			return d
		}
		return a.order - b.order
	})
	out := make([]Column, len(lanes))
	for i, l := range lanes {
		out[i] = l.col
		out[i].Position = i
	}
	return out
}

// AllBoard returns the user's all board, optionally only some projects' tasks.
func (s *Service) AllBoard(ctx context.Context, userID, role string, sc Scope) ([]Lane, error) {
	cols, err := s.allColumns(ctx, userID, role)
	if err != nil {
		return nil, err
	}
	list, err := s.tasks.List(ctx, userID, role, tasks.Filter{})
	if err != nil {
		return nil, err
	}
	list = filterProjects(list, sc.Projects)
	return placeOnMerged(cols, list), nil
}

// allColumns lists the columns the all board merges.
func (s *Service) allColumns(ctx context.Context, userID, role string) ([]Column, error) {
	if role == user.RoleFreelancer {
		return s.repo.AllColumns(ctx, userID)
	}
	if role != user.RoleClient {
		return nil, ErrNoBoard
	}
	clientID, err := s.repo.ActingClient(ctx, userID)
	if err != nil {
		return nil, err
	}
	return s.repo.ClientColumns(ctx, clientID)
}

// placeOnMerged lays tasks onto lanes merged by category and column name,
// then gives each task its real status id back.
func placeOnMerged(cols []Column, list []tasks.Task) []Lane {
	placed := make([]tasks.Task, len(list))
	real := make(map[string]string, len(list))
	for i, t := range list {
		real[t.ID] = t.StatusID
		t.StatusID = laneKey(t.Status, t.StatusName)
		placed[i] = t
	}
	lanes := Arrange(mergeColumns(cols), placed)
	for i := range lanes {
		for j := range lanes[i].Tasks {
			lanes[i].Tasks[j].StatusID = real[lanes[i].Tasks[j].ID]
		}
	}
	return lanes
}

// MoveToLane puts a task into the all board's lane: its own pair's column of
// that name, or else that pair's first column of the lane's category.
func (s *Service) MoveToLane(ctx context.Context, userID, role, taskID, key string) (tasks.Task, error) {
	category, name, ok := splitLaneKey(key)
	if !ok {
		return tasks.Task{}, ErrNotFound
	}
	t, err := s.tasks.Get(ctx, userID, taskID)
	if err != nil {
		return tasks.Task{}, err
	}
	p := Pair{FreelancerID: t.Freelancer.ID}
	if t.Client != nil {
		p.ClientID = t.Client.ID
	}
	if t.Project != nil {
		p.ProjectID = t.Project.ID
	}
	cols, err := s.repo.Columns(ctx, p)
	if err != nil {
		return tasks.Task{}, err
	}
	target := ""
	for _, c := range cols {
		if c.Category != category {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(c.Name), name) {
			target = c.ID
			break
		}
		if target == "" {
			target = c.ID
		}
	}
	if target == "" {
		return tasks.Task{}, ErrNotFound
	}
	return s.tasks.MoveTo(ctx, userID, role, taskID, target)
}

// filterProjects keeps the tasks in any of the projects; NoProject keeps
// tasks outside any project. No filter keeps them all.
func filterProjects(list []tasks.Task, projects []string) []tasks.Task {
	if len(projects) == 0 {
		return list
	}
	out := list[:0:0]
	for _, t := range list {
		id := NoProject
		if t.Project != nil {
			id = t.Project.ID
		}
		if slices.Contains(projects, id) {
			out = append(out, t)
		}
	}
	return out
}

// parseProjects reads the comma-separated projects filter.
func parseProjects(raw string) []string {
	var out []string
	for _, id := range strings.Split(raw, ",") {
		if id = strings.TrimSpace(id); id != "" && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}
