// Package invitation contains the provider-neutral staff invitation lifecycle.
package invitation

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/HK9750/venueos/internal/access"
	"github.com/HK9750/venueos/internal/platform/apperror"
	"github.com/HK9750/venueos/internal/platform/clock"
	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/internal/platform/validation"
)

type Status string

const (
	StatusInvited   Status = "invited"
	StatusAccepted  Status = "accepted"
	StatusExpired   Status = "expired"
	StatusRevoked   Status = "revoked"
	StatusCancelled Status = "cancelled"
	DefaultTTL             = 7 * 24 * time.Hour
)

var (
	ErrNotFound             = errors.New("invitation not found")
	ErrActiveExists         = errors.New("an active invitation already exists")
	ErrExpired              = errors.New("invitation expired")
	ErrAlreadyUsed          = errors.New("invitation already used")
	ErrEmailMismatch        = errors.New("invitation email does not match authenticated user")
	ErrAlreadyMember        = errors.New("user is already a member of the organization")
	ErrOrganizationInactive = errors.New("organization is not active")
)

type Invitation struct {
	ID             identifier.ID
	OrganizationID identifier.ID
	Email          string
	Role           access.Role
	Status         Status
	ExpiresAt      time.Time
	CreatedByType  string
	CreatedByID    string
	MembershipID   *identifier.ID
	AcceptedAt     *time.Time
	RevokedAt      *time.Time
	Version        int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type IssueInput struct {
	OrganizationID identifier.ID
	Email          string
	Role           access.Role
}

type IssueResult struct {
	Invitation Invitation
	Token      string
}

type AcceptInput struct{ Token string }

type RevokeInput struct {
	OrganizationID identifier.ID
	InvitationID   identifier.ID
	Version        int64
}

type IssueRecord struct {
	Invitation Invitation
	TokenHash  [32]byte
	AuditID    identifier.ID
	OutboxID   identifier.ID
	ActorType  string
	ActorID    string
}

type AcceptRecord struct {
	TokenHash          [32]byte
	UserID             identifier.ID
	InvitationAuditID  identifier.ID
	InvitationOutboxID identifier.ID
	MembershipID       identifier.ID
	MembershipAuditID  identifier.ID
	MembershipOutboxID identifier.ID
	ActorType          string
	ActorID            string
	OccurredAt         time.Time
}

type RevokeRecord struct {
	OrganizationID identifier.ID
	InvitationID   identifier.ID
	Version        int64
	AuditID        identifier.ID
	OutboxID       identifier.ID
	ActorType      string
	ActorID        string
	OccurredAt     time.Time
}

type Repository interface {
	Issue(context.Context, IssueRecord) (Invitation, error)
	Accept(context.Context, AcceptRecord) (Invitation, error)
	Revoke(context.Context, RevokeRecord) (Invitation, error)
	ExpireDue(context.Context, time.Time, int32) (int, error)
}

type Service struct {
	repository Repository
	clock      clock.Clock
}

func NewService(repository Repository, timeSource clock.Clock) *Service {
	return &Service{repository: repository, clock: timeSource}
}

func (service *Service) Issue(ctx context.Context, input IssueInput) (IssueResult, error) {
	authorization, err := scopedAuthorization(ctx, input.OrganizationID)
	if err != nil {
		return IssueResult{}, err
	}
	email, validationErr := normalizeEmail(input.Email)
	if validationErr != nil {
		return IssueResult{}, validationErr
	}
	if !input.Role.Valid() || input.Role == access.RoleOwner {
		return IssueResult{}, apperror.New(apperror.CodeValidationFailed, "The invitation request is invalid.", apperror.Detail{
			Field: "role", Code: "invalid", Message: "Invite a supported non-owner role; ownership transfer is a separate audited operation.",
		})
	}
	now := service.clock.Now().UTC()
	invitationID, auditID, outboxID, err := newIDs()
	if err != nil {
		return IssueResult{}, apperror.Wrap(err, apperror.CodeInternal, "An invitation identifier could not be created.")
	}
	token, tokenHash, err := newToken()
	if err != nil {
		return IssueResult{}, apperror.Wrap(err, apperror.CodeInternal, "An invitation token could not be created.")
	}
	created, err := service.repository.Issue(ctx, IssueRecord{
		Invitation: Invitation{ID: invitationID, OrganizationID: authorization.OrganizationID(), Email: email,
			Role: input.Role, Status: StatusInvited, ExpiresAt: now.Add(DefaultTTL),
			CreatedByType: actorType(authorization.Principal().Type()), CreatedByID: authorization.Principal().ID(),
			Version: 1, CreatedAt: now, UpdatedAt: now},
		TokenHash: tokenHash, AuditID: auditID, OutboxID: outboxID,
		ActorType: actorType(authorization.Principal().Type()), ActorID: authorization.Principal().ID(),
	})
	if err != nil {
		if errors.Is(err, ErrActiveExists) {
			return IssueResult{}, apperror.Wrap(err, apperror.CodeConflict, "An active invitation already exists for this email.")
		}
		return IssueResult{}, err
	}
	return IssueResult{Invitation: created, Token: token}, nil
}

func (service *Service) Accept(ctx context.Context, input AcceptInput) (Invitation, error) {
	principal, err := access.PrincipalFromContext(ctx)
	if errors.Is(err, access.ErrUnauthenticated) {
		return Invitation{}, apperror.Wrap(err, apperror.CodeUnauthenticated, "Authentication is required to accept an invitation.")
	}
	if err != nil || principal.Type() != access.PrincipalUser {
		return Invitation{}, apperror.New(apperror.CodePermissionDenied, "A user identity is required to accept an invitation.")
	}
	userID, err := identifier.Parse(principal.ID())
	if err != nil {
		return Invitation{}, apperror.New(apperror.CodeUnauthenticated, "The authenticated user identity is invalid.")
	}
	token := strings.TrimSpace(input.Token)
	if len(token) < 40 || len(token) > 128 {
		return Invitation{}, apperror.New(apperror.CodeValidationFailed, "The invitation token is invalid.", apperror.Detail{Field: "token", Code: "invalid", Message: "Provide the invitation token supplied to the intended recipient."})
	}
	digest, err := tokenHash(token)
	if err != nil {
		return Invitation{}, apperror.New(apperror.CodeValidationFailed, "The invitation token is invalid.", apperror.Detail{Field: "token", Code: "invalid", Message: "Provide the invitation token supplied to the intended recipient."})
	}
	ids, err := newIDsN(5)
	if err != nil {
		return Invitation{}, apperror.Wrap(err, apperror.CodeInternal, "Invitation acceptance identifiers could not be created.")
	}
	accepted, err := service.repository.Accept(ctx, AcceptRecord{TokenHash: digest, UserID: userID,
		InvitationAuditID: ids[0], InvitationOutboxID: ids[1], MembershipID: ids[2], MembershipAuditID: ids[3], MembershipOutboxID: ids[4],
		ActorType: string(principal.Type()), ActorID: principal.ID(), OccurredAt: service.clock.Now().UTC()})
	if err != nil {
		return Invitation{}, mapLifecycleError(err)
	}
	return accepted, nil
}

func (service *Service) Revoke(ctx context.Context, input RevokeInput) (Invitation, error) {
	authorization, err := scopedAuthorization(ctx, input.OrganizationID)
	if err != nil {
		return Invitation{}, err
	}
	if input.InvitationID.IsZero() || input.Version < 1 {
		collector := validation.Collector{}
		if input.InvitationID.IsZero() {
			collector.Add("invitation_id", "required", "An invitation identifier is required.")
		}
		if input.Version < 1 {
			collector.Add("version", "required", "A positive invitation version is required.")
		}
		return Invitation{}, collector.Error("The invitation revocation is invalid.")
	}
	ids, err := newIDsN(2)
	if err != nil {
		return Invitation{}, apperror.Wrap(err, apperror.CodeInternal, "Invitation revocation identifiers could not be created.")
	}
	value, err := service.repository.Revoke(ctx, RevokeRecord{OrganizationID: authorization.OrganizationID(), InvitationID: input.InvitationID,
		Version: input.Version, AuditID: ids[0], OutboxID: ids[1], ActorType: actorType(authorization.Principal().Type()), ActorID: authorization.Principal().ID(), OccurredAt: service.clock.Now().UTC()})
	if err != nil {
		return Invitation{}, mapLifecycleError(err)
	}
	return value, nil
}

// ExpireDue is intended for the durable worker path. It is bounded so a large
// backlog cannot hold one transaction or lock the invitation table indefinitely.
func (service *Service) ExpireDue(ctx context.Context, limit int32) (int, error) {
	if limit < 1 || limit > 500 {
		return 0, apperror.New(apperror.CodeValidationFailed, "The invitation expiry batch is invalid.", apperror.Detail{Field: "limit", Code: "out_of_range", Message: "Use a limit between 1 and 500."})
	}
	return service.repository.ExpireDue(ctx, service.clock.Now().UTC(), limit)
}

func scopedAuthorization(ctx context.Context, organizationID identifier.ID) (access.Authorization, error) {
	authorization, err := access.Require(ctx, access.PermissionMembershipManage)
	if err != nil {
		if errors.Is(err, access.ErrUnauthenticated) {
			return access.Authorization{}, apperror.Wrap(err, apperror.CodeUnauthenticated, "Authentication is required.")
		}
		return access.Authorization{}, apperror.Wrap(err, apperror.CodePermissionDenied, "Membership management is not permitted.")
	}
	if organizationID.IsZero() || authorization.OrganizationID() != organizationID {
		return access.Authorization{}, apperror.Wrap(ErrNotFound, apperror.CodeNotFound, "The requested invitation does not exist.")
	}
	return authorization, nil
}

func mapLifecycleError(err error) error {
	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrEmailMismatch):
		return apperror.Wrap(err, apperror.CodeNotFound, "The invitation is invalid or unavailable.")
	case errors.Is(err, ErrExpired):
		return apperror.Wrap(err, apperror.CodeConflict, "The invitation has expired.")
	case errors.Is(err, ErrAlreadyUsed), errors.Is(err, ErrAlreadyMember):
		return apperror.Wrap(err, apperror.CodeConflict, "The invitation has already been used.")
	case errors.Is(err, ErrOrganizationInactive):
		return apperror.Wrap(err, apperror.CodeConflict, "The organization cannot accept invitations in its current state.")
	default:
		return err
	}
}

func normalizeEmail(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) > 254 || !utf8.ValidString(value) {
		return "", apperror.New(apperror.CodeValidationFailed, "The invitation request is invalid.", apperror.Detail{Field: "email", Code: "invalid", Message: "Provide a valid email address."})
	}
	parsed, err := mail.ParseAddress(value)
	if err != nil || parsed.Address != value {
		return "", apperror.New(apperror.CodeValidationFailed, "The invitation request is invalid.", apperror.Detail{Field: "email", Code: "invalid", Message: "Provide a valid email address."})
	}
	return value, nil
}

func newToken() (string, [32]byte, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", [32]byte{}, err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), sha256.Sum256(raw[:]), nil
}

func newIDs() (identifier.ID, identifier.ID, identifier.ID, error) {
	ids, err := newIDsN(3)
	if err != nil {
		return identifier.ID{}, identifier.ID{}, identifier.ID{}, err
	}
	return ids[0], ids[1], ids[2], nil
}

func newIDsN(count int) ([]identifier.ID, error) {
	ids := make([]identifier.ID, count)
	for index := range ids {
		id, err := identifier.New()
		if err != nil {
			return nil, err
		}
		ids[index] = id
	}
	return ids, nil
}

func actorType(principalType access.PrincipalType) string {
	if principalType == access.PrincipalPlatform {
		return "platform"
	}
	return string(principalType)
}

func tokenHash(token string) ([32]byte, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 {
		return [32]byte{}, fmt.Errorf("invalid invitation token")
	}
	return sha256.Sum256(raw), nil
}
