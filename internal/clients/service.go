package clients

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/pmaxis/helpic/internal/auth"
	"github.com/pmaxis/helpic/internal/notifications"
	"github.com/pmaxis/helpic/internal/user"
)

// accounts is the part of the auth service invitations rely on. Implemented by
// *auth.Service.
type accounts interface {
	Register(ctx context.Context, p auth.RegisterParams, role string) (user.User, error)
	Discard(ctx context.Context, userID string) error
	Login(ctx context.Context, p auth.LoginParams, c auth.Client) (user.User, auth.Tokens, error)
	StartSession(ctx context.Context, u user.User, c auth.Client) (auth.Tokens, error)
}

// notifier tells the freelancer their invitation was accepted. Implemented by
// *notifications.Service.
type notifier interface {
	Notify(ctx context.Context, n notifications.Notification)
}

// Service holds the invitation rules.
type Service struct {
	repo     *Repository
	accounts accounts
	notify   notifier
	// mail sends invitations; appURL is the frontend their links open.
	mail   auth.Mailer
	appURL string
}

// NewService creates a clients Service.
func NewService(repo *Repository, accounts accounts, notify notifier, mail auth.Mailer, appURL string) *Service {
	return &Service{repo: repo, accounts: accounts, notify: notify, mail: mail, appURL: strings.TrimRight(appURL, "/")}
}

// joined tells the freelancer that the client accepted their invitation.
func (s *Service) joined(ctx context.Context, l Lookup, clientID string) {
	s.notify.Notify(ctx, notifications.Notification{
		UserID:  l.Invitation.FreelancerID,
		ActorID: clientID,
		Kind:    notifications.KindClientJoined,
	})
}

// Invite creates an invitation for the email. The email must not belong to the
// freelancer, to another freelancer, or to someone already their client.
func (s *Service) Invite(ctx context.Context, freelancerID string, p InviteParams) (Invitation, error) {
	existing, err := s.repo.UserByEmail(ctx, p.Email)
	switch {
	case errors.Is(err, user.ErrNotFound):
	case err != nil:
		return Invitation{}, err
	case existing.ID == freelancerID:
		return Invitation{}, ErrSelfInvite
	case existing.Role != user.RoleClient:
		return Invitation{}, ErrNotClientEmail
	default:
		member, err := s.repo.IsTeamMember(ctx, existing.ID)
		if err != nil {
			return Invitation{}, err
		}
		if member {
			return Invitation{}, ErrTeamMember
		}
		linked, err := s.repo.IsLinked(ctx, freelancerID, existing.ID)
		if err != nil {
			return Invitation{}, err
		}
		if linked {
			return Invitation{}, ErrAlreadyClient
		}
	}

	token, hash, err := auth.NewToken()
	if err != nil {
		return Invitation{}, err
	}
	inv, err := s.repo.CreateInvitation(ctx, freelancerID, p.Email, p.Name, hash, time.Now().Add(InvitationTTL))
	if err != nil {
		return Invitation{}, err
	}
	if err := s.emailInvitation(ctx, freelancerID, inv, token); err != nil {
		if delErr := s.repo.DeleteInvitation(ctx, freelancerID, inv.ID); delErr != nil {
			slog.Error("failed to roll back invitation", "error", delErr)
		}
		return Invitation{}, err
	}
	return inv, nil
}

// Lookup is what the invitation page shows before the client accepts.
type Lookup struct {
	Invitation    Invitation
	Freelancer    Party
	AccountExists bool
}

// Find resolves an invitation token. Accepted and expired invitations yield
// ErrInvitationInvalid.
func (s *Service) Find(ctx context.Context, token string) (Lookup, error) {
	inv, freelancer, err := s.repo.InvitationByToken(ctx, auth.HashToken(token))
	if errors.Is(err, ErrNotFound) {
		return Lookup{}, ErrInvitationInvalid
	}
	if err != nil {
		return Lookup{}, err
	}
	if inv.AcceptedAt != nil || inv.Expired() {
		return Lookup{}, ErrInvitationInvalid
	}

	existing, err := s.repo.UserByEmail(ctx, inv.Email)
	if err != nil && !errors.Is(err, user.ErrNotFound) {
		return Lookup{}, err
	}
	if err == nil && existing.Role != user.RoleClient {
		return Lookup{}, ErrNotClientEmail
	}
	if err == nil {
		// Joined a client's team after the invitation went out.
		member, err := s.repo.IsTeamMember(ctx, existing.ID)
		if err != nil {
			return Lookup{}, err
		}
		if member {
			return Lookup{}, ErrNotClientEmail
		}
	}
	return Lookup{Invitation: inv, Freelancer: freelancer, AccountExists: err == nil}, nil
}

// Session is the signed-in client after accepting an invitation.
type Session struct {
	User   user.User
	Tokens auth.Tokens
}

// AcceptNew creates the client account for a looked-up invitation and links it.
func (s *Service) AcceptNew(ctx context.Context, l Lookup, p AcceptParams, c auth.Client) (Session, error) {
	u, err := s.accounts.Register(ctx, auth.RegisterParams{
		Email:    l.Invitation.Email,
		Name:     p.Name,
		Password: p.Password,
	}, user.RoleClient)
	if err != nil {
		return Session{}, err
	}
	if err := s.repo.Accept(ctx, l.Invitation.ID, u.ID); err != nil {
		// The invitation was used up or withdrawn meanwhile: the account
		// was only for it.
		if delErr := s.accounts.Discard(ctx, u.ID); delErr != nil {
			slog.Error("failed to discard an unused account", "error", delErr)
		}
		return Session{}, err
	}
	s.joined(ctx, l, u.ID)
	tokens, err := s.accounts.StartSession(ctx, u, c)
	if err != nil {
		return Session{}, err
	}
	return Session{User: u, Tokens: tokens}, nil
}

// AcceptExisting signs in the existing client account the invitation was sent
// to and links it. A wrong password yields auth.ErrInvalidCredentials.
func (s *Service) AcceptExisting(ctx context.Context, l Lookup, password string, c auth.Client) (Session, error) {
	u, tokens, err := s.accounts.Login(ctx, auth.LoginParams{Email: l.Invitation.Email, Password: password}, c)
	if err != nil {
		return Session{}, err
	}
	if u.Role != user.RoleClient {
		return Session{}, ErrNotClientEmail
	}
	if err := s.repo.Accept(ctx, l.Invitation.ID, u.ID); err != nil {
		return Session{}, err
	}
	s.joined(ctx, l, u.ID)
	return Session{User: u, Tokens: tokens}, nil
}
