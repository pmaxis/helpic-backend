package team

import (
	"context"
	"strings"
	"time"

	"github.com/pmaxis/helpic/internal/auth"
	"github.com/pmaxis/helpic/internal/notifications"
	"github.com/pmaxis/helpic/internal/user"
)

// accounts makes and signs in member accounts. Implemented by *auth.Service.
type accounts interface {
	Register(ctx context.Context, p auth.RegisterParams, role string) (user.User, error)
	StartSession(ctx context.Context, u user.User, c auth.Client) (auth.Tokens, error)
}

// notifier tells the owner someone joined. Implemented by
// *notifications.Service.
type notifier interface {
	Notify(ctx context.Context, n notifications.Notification)
}

// Service holds the team rules: a client who isn't anyone's member owns their
// company and runs its team; members only see it.
type Service struct {
	repo     *Repository
	accounts accounts
	notify   notifier
	// mail sends invitations; appURL is the frontend their links open.
	mail   auth.Mailer
	appURL string
}

// NewService creates a team Service.
func NewService(repo *Repository, accounts accounts, notify notifier, mail auth.Mailer, appURL string) *Service {
	return &Service{repo: repo, accounts: accounts, notify: notify, mail: mail, appURL: strings.TrimRight(appURL, "/")}
}

// Overview returns the team as the client sees it.
func (s *Service) Overview(ctx context.Context, userID string) (Overview, error) {
	m, member, err := s.repo.MembershipOf(ctx, userID)
	if err != nil {
		return Overview{}, err
	}
	if member {
		return Overview{Role: m.Role, Name: m.Company, Owner: m.Owner}, nil
	}
	me, err := s.repo.Person(ctx, userID)
	if err != nil {
		return Overview{}, err
	}
	o := Overview{Role: RoleOwner, Owner: me, Members: []Member{}, Invites: []Invite{}}
	id, name, ok, err := s.repo.Company(ctx, userID)
	if err != nil || !ok {
		return o, err
	}
	o.Name = name
	if o.Members, err = s.repo.Members(ctx, id); err != nil {
		return Overview{}, err
	}
	if o.Invites, err = s.repo.Invites(ctx, id); err != nil {
		return Overview{}, err
	}
	return o, nil
}

// owned returns the company the user owns, making it the first time; a
// member gets ErrNotOwner.
func (s *Service) owned(ctx context.Context, userID string) (string, error) {
	if _, member, err := s.repo.MembershipOf(ctx, userID); err != nil || member {
		if err == nil {
			err = ErrNotOwner
		}
		return "", err
	}
	return s.repo.EnsureCompany(ctx, userID)
}

// Rename names the owner's company.
func (s *Service) Rename(ctx context.Context, userID, name string) (Overview, error) {
	if _, err := s.owned(ctx, userID); err != nil {
		return Overview{}, err
	}
	if err := s.repo.Rename(ctx, userID, name); err != nil {
		return Overview{}, err
	}
	return s.Overview(ctx, userID)
}

// Invite asks someone to join the owner's company. They join with a new
// account, so an email that already has one is refused.
func (s *Service) Invite(ctx context.Context, userID string, p InviteParams) (Invite, error) {
	companyID, err := s.owned(ctx, userID)
	if err != nil {
		return Invite{}, err
	}
	taken, err := s.repo.EmailTaken(ctx, p.Email)
	if err != nil {
		return Invite{}, err
	}
	if taken {
		return Invite{}, ErrAccountExists
	}
	token, hash, err := auth.NewToken()
	if err != nil {
		return Invite{}, err
	}
	inv, err := s.repo.CreateInvite(ctx, companyID, p.Email, p.Name, p.Role, hash, time.Now().Add(InviteTTL))
	if err != nil {
		return Invite{}, err
	}
	s.emailInvite(ctx, userID, inv, token)
	return inv, nil
}

// RevokeInvite deletes one of the owner's pending invitations.
func (s *Service) RevokeInvite(ctx context.Context, userID, id string) error {
	companyID, err := s.owned(ctx, userID)
	if err != nil {
		return err
	}
	return s.repo.DeleteInvite(ctx, companyID, id)
}

// RemoveMember takes someone off the owner's team.
func (s *Service) RemoveMember(ctx context.Context, userID, memberID string) error {
	companyID, err := s.owned(ctx, userID)
	if err != nil {
		return err
	}
	return s.repo.RemoveMember(ctx, companyID, memberID)
}

// SetRole makes someone on the owner's team a member or a contractor.
func (s *Service) SetRole(ctx context.Context, userID, memberID, role string) (Overview, error) {
	companyID, err := s.owned(ctx, userID)
	if err != nil {
		return Overview{}, err
	}
	if err := s.repo.SetRole(ctx, companyID, memberID, role); err != nil {
		return Overview{}, err
	}
	return s.Overview(ctx, userID)
}

// Lookup is what the invitation page shows before someone joins.
type Lookup struct {
	Invite  Invite
	Company string
	Owner   Person
}

// Find resolves an invitation token for the join page.
func (s *Service) Find(ctx context.Context, token string) (Lookup, error) {
	inv, company, owner, err := s.repo.InviteByToken(ctx, auth.HashToken(token))
	if err != nil {
		return Lookup{}, err
	}
	// Someone made an account with this email since: the link no longer fits.
	taken, err := s.repo.EmailTaken(ctx, inv.Email)
	if err != nil {
		return Lookup{}, err
	}
	if taken {
		return Lookup{}, ErrInviteInvalid
	}
	return Lookup{Invite: inv, Company: company, Owner: owner}, nil
}

// Session is the new member, signed in.
type Session struct {
	User   user.User
	Tokens auth.Tokens
}

// Join creates the member's account, adds it to the company and signs it in.
func (s *Service) Join(ctx context.Context, token string, p JoinParams, c auth.Client) (Session, error) {
	l, err := s.Find(ctx, token)
	if err != nil {
		return Session{}, err
	}
	u, err := s.accounts.Register(ctx, auth.RegisterParams{Email: l.Invite.Email, Name: p.Name, Password: p.Password}, user.RoleClient)
	if err != nil {
		return Session{}, err
	}
	if err := s.repo.Join(ctx, l.Invite.ID, l.Invite.CompanyID, u.ID, l.Invite.Role); err != nil {
		return Session{}, err
	}
	s.notify.Notify(ctx, notifications.Notification{UserID: l.Owner.ID, ActorID: u.ID, Kind: notifications.KindMemberJoined})
	tokens, err := s.accounts.StartSession(ctx, u, c)
	if err != nil {
		return Session{}, err
	}
	// The account was read before it joined; say it's a member now.
	u.TeamOwnerID, u.TeamName, u.TeamRole = &l.Owner.ID, l.Company, l.Invite.Role
	return Session{User: u, Tokens: tokens}, nil
}
