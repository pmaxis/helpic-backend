package projects

import (
	"context"

	"github.com/pmaxis/helpic/internal/notifications"

	"github.com/pmaxis/helpic/internal/user"
)

// notifier tells the other side about a new shared project. Implemented by
// *notifications.Service.
type notifier interface {
	Notify(ctx context.Context, n notifications.Notification)
}

// Service holds the project rules: who may create, edit and delete a project.
type Service struct {
	repo   *Repository
	notify notifier
}

// NewService creates a projects Service.
func NewService(repo *Repository, notify notifier) *Service {
	return &Service{repo: repo, notify: notify}
}

// List returns the user's projects.
func (s *Service) List(ctx context.Context, userID, role, counterpartID string) ([]Project, error) {
	return s.repo.List(ctx, userID, role, counterpartID)
}

// Get returns a project the user takes part in.
func (s *Service) Get(ctx context.Context, userID, id string) (Project, error) {
	return s.repo.Get(ctx, userID, id)
}

// Create adds a project: a client names the freelancer (p.FreelancerID), a
// freelancer names the client (p.ClientID) or leaves it empty for a personal
// project.
func (s *Service) Create(ctx context.Context, userID, role string, p Params) (Project, error) {
	var (
		id  string
		err error
	)
	switch {
	case role == user.RoleClient:
		// A company member starts projects for the company: its owner is the client.
		var clientID string
		if clientID, err = s.repo.ActingClient(ctx, userID); err == nil {
			id, err = s.repo.Create(ctx, p.FreelancerID, clientID, userID, p)
		}
	case p.ClientID == "":
		id, err = s.repo.CreatePersonal(ctx, userID, p)
	default:
		id, err = s.repo.Create(ctx, userID, p.ClientID, userID, p)
	}
	if err != nil {
		return Project{}, err
	}
	proj, err := s.repo.Get(ctx, userID, id)
	if err == nil && proj.Client != nil {
		// The other side: the freelancer, or the client company's owner.
		n := notifications.Notification{ActorID: userID, Kind: notifications.KindProjectCreated, ProjectID: proj.ID, Subject: proj.Name}
		n.UserID = proj.Freelancer.ID
		if userID == proj.Freelancer.ID {
			n.UserID = proj.Client.ID
		}
		s.notify.Notify(ctx, n)
	}
	return proj, err
}

// Update renames or re-describes a project; only its creator may.
func (s *Service) Update(ctx context.Context, userID, id string, p Params) (Project, error) {
	if _, err := s.owned(ctx, userID, id); err != nil {
		return Project{}, err
	}
	if err := s.repo.Update(ctx, id, p); err != nil {
		return Project{}, err
	}
	return s.repo.Get(ctx, userID, id)
}

// Delete removes a project; only its creator may. Its tasks are kept.
func (s *Service) Delete(ctx context.Context, userID, id string) error {
	if _, err := s.owned(ctx, userID, id); err != nil {
		return err
	}
	return s.repo.Delete(ctx, id)
}

func (s *Service) owned(ctx context.Context, userID, id string) (Project, error) {
	p, err := s.repo.Get(ctx, userID, id)
	if err != nil {
		return Project{}, err
	}
	if p.CreatedBy != userID {
		return Project{}, ErrNotOwner
	}
	return p, nil
}
