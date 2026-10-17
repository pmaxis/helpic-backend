package user

import "context"

// Service holds user-related business logic.
type Service struct {
	repo *Repository
}

// NewService creates a user Service.
func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

// GetByID returns the user with the given ID, or ErrNotFound.
func (s *Service) GetByID(ctx context.Context, id string) (User, error) {
	return s.repo.GetByID(ctx, id)
}
