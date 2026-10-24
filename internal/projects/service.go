package projects

import (
	"context"

	"github.com/pmaxis/helpic/internal/notifications"

	"github.com/pmaxis/helpic/internal/user"
)

// notifier tells someone the author invited them to a project. Implemented by
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
// project. A client company's contractor can't.
func (s *Service) Create(ctx context.Context, userID, role string, p Params) (Project, error) {
	var (
		id  string
		err error
	)
	if role == user.RoleClient {
		contractor, err := s.repo.IsContractor(ctx, userID)
		if err != nil {
			return Project{}, err
		}
		if contractor {
			return Project{}, ErrContractor
		}
	}
	switch {
	case role == user.RoleClient:
		// A company member starts projects for the company: its owner is the client.
		var clientID string
		if clientID, err = s.repo.ActingClient(ctx, userID); err == nil {
			id, err = s.repo.Create(ctx, p.FreelancerIDs, clientID, userID, p)
		}
	case p.ClientID == "":
		id, err = s.repo.CreatePersonal(ctx, userID, p)
	default:
		id, err = s.repo.Create(ctx, []string{userID}, p.ClientID, userID, p)
	}
	if err != nil {
		return Project{}, err
	}
	created, err := s.repo.Get(ctx, userID, id)
	if err != nil {
		return Project{}, err
	}
	// Choosing freelancers at creation puts them on the project, the same as an invitation.
	for _, person := range created.Participants {
		if person.Role == "freelancer" && person.ID != userID {
			s.notify.Notify(ctx, notifications.Notification{
				UserID: person.ID, ActorID: userID, Kind: notifications.KindProjectInvited, ProjectID: created.ID, Subject: created.Name,
			})
		}
	}
	return created, nil
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

// People lists the project's participants the user may see, and, for its
// author, who they can still invite: the freelancer, the client, and the client's employees and contractors.
func (s *Service) People(ctx context.Context, userID, id string) (participants, candidates []Person, err error) {
	p, err := s.repo.Get(ctx, userID, id)
	if err != nil {
		return nil, nil, err
	}
	if participants, err = s.repo.Participants(ctx, userID, id); err != nil {
		return nil, nil, err
	}
	if p.CreatedBy != userID {
		return participants, []Person{}, nil
	}
	candidates, err = s.repo.Candidates(ctx, id)
	return participants, candidates, err
}

// Invite adds the people the author chose. Each one is told, and the project
// is theirs to see from now on. The role doesn't matter: a freelancer, an
// employee and a contractor are invited the same way.
func (s *Service) Invite(ctx context.Context, userID, id string, personIDs []string) ([]Person, []Person, error) {
	p, err := s.owned(ctx, userID, id)
	if err != nil {
		return nil, nil, err
	}
	if len(personIDs) == 0 {
		return nil, nil, ErrNotInvitable
	}
	seen := map[string]bool{}
	for _, personID := range personIDs {
		if personID == "" || seen[personID] {
			continue
		}
		seen[personID] = true
		added, err := s.repo.Invite(ctx, id, personID, userID)
		if err != nil {
			return nil, nil, err
		}
		if added && personID != userID {
			s.notify.Notify(ctx, notifications.Notification{
				UserID: personID, ActorID: userID, Kind: notifications.KindProjectInvited, ProjectID: p.ID, Subject: p.Name,
			})
		}
	}
	return s.People(ctx, userID, id)
}

// RemoveParticipant takes someone off the project; only its author may, and
// the author stays. They no longer see the project.
func (s *Service) RemoveParticipant(ctx context.Context, userID, id, personID string) ([]Person, []Person, error) {
	p, err := s.owned(ctx, userID, id)
	if err != nil {
		return nil, nil, err
	}
	if personID == p.CreatedBy {
		return nil, nil, ErrKeepAuthor
	}
	if _, err := s.repo.RemoveParticipant(ctx, id, personID); err != nil {
		return nil, nil, err
	}
	return s.People(ctx, userID, id)
}

// SetRate sets or clears the hourly rate a freelancer on the project bills
// their tasks in it at; their open hourly tasks there are priced again.
func (s *Service) SetRate(ctx context.Context, userID, id string, cents *int64) (Project, error) {
	p, err := s.repo.Get(ctx, userID, id)
	if err != nil {
		return Project{}, err
	}
	if !CanSetRate(p, userID) {
		return Project{}, ErrNotProjectFreelancer
	}
	if err := s.repo.SetRate(ctx, p.ID, userID, cents); err != nil {
		return Project{}, err
	}
	return s.repo.Get(ctx, userID, id)
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
