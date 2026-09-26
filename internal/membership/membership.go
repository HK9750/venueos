// Package membership contains tenant membership and fixed-role authorization
// use cases. Identity-provider verification remains an adapter concern.
package membership

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/HK9750/venueos/internal/access"
	"github.com/HK9750/venueos/internal/platform/apperror"
	"github.com/HK9750/venueos/internal/platform/clock"
	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/internal/platform/validation"
)

type Status string

const (
	StatusActive  Status = "active"
	StatusRevoked Status = "revoked"
	DefaultLimit         = int32(50)
	MaxLimit             = int32(100)
)

var (
	ErrNotFound        = errors.New("membership not found")
	ErrVersionConflict = errors.New("membership version conflict")
	ErrLastOwner       = errors.New("last active owner cannot be removed")
)

type Membership struct {
	ID             identifier.ID
	OrganizationID identifier.ID
	UserID         identifier.ID
	Role           access.Role
	Status         Status
	VenueScope     []identifier.ID
	Version        int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
	RevokedAt      *time.Time
}

type Cursor struct {
	OrganizationID identifier.ID
	CreatedAt      time.Time
	ID             identifier.ID
}

type Page struct {
	Items      []Membership
	NextCursor *Cursor
}

type ChangeRoleInput struct {
	OrganizationID identifier.ID
	MembershipID   identifier.ID
	Role           access.Role
	Version        int64
}

type RevokeInput struct {
	OrganizationID identifier.ID
	MembershipID   identifier.ID
	Version        int64
}

type ChangeRoleRecord struct {
	OrganizationID identifier.ID
	MembershipID   identifier.ID
	Role           access.Role
	Version        int64
	AuditID        identifier.ID
	OutboxID       identifier.ID
	ActorType      string
	ActorID        string
	OccurredAt     time.Time
}

type RevokeRecord struct {
	OrganizationID identifier.ID
	MembershipID   identifier.ID
	Version        int64
	AuditID        identifier.ID
	OutboxID       identifier.ID
	ActorType      string
	ActorID        string
	OccurredAt     time.Time
}

type Repository interface {
	List(context.Context, identifier.ID, int32, *Cursor) (Page, error)
	ChangeRole(context.Context, ChangeRoleRecord) (Membership, error)
	Revoke(context.Context, RevokeRecord) (Membership, error)
}

type Service struct {
	repository Repository
	clock      clock.Clock
}

func NewService(repository Repository, timeSource clock.Clock) *Service {
	return &Service{repository: repository, clock: timeSource}
}

func (service *Service) List(ctx context.Context, organizationID identifier.ID, limit int32, after *Cursor) (Page, error) {
	authorization, err := scopedAuthorization(ctx, organizationID, access.PermissionMembershipRead)
	if err != nil {
		return Page{}, err
	}
	if limit == 0 {
		limit = DefaultLimit
	}
	if limit < 1 || limit > MaxLimit {
		return Page{}, apperror.New(apperror.CodeValidationFailed, "The membership list request is invalid.", apperror.Detail{
			Field: "limit", Code: "out_of_range", Message: fmt.Sprintf("Use a limit between 1 and %d.", MaxLimit),
		})
	}
	if after != nil && after.OrganizationID != authorization.OrganizationID() {
		return Page{}, apperror.New(apperror.CodeInvalidCursor, "The membership cursor is invalid.")
	}
	return service.repository.List(ctx, authorization.OrganizationID(), limit, after)
}

func (service *Service) ChangeRole(ctx context.Context, input ChangeRoleInput) (Membership, error) {
	authorization, err := scopedAuthorization(ctx, input.OrganizationID, access.PermissionMembershipManage)
	if err != nil {
		return Membership{}, err
	}
	if input.MembershipID.IsZero() || input.Version < 1 || !input.Role.Valid() {
		collector := validation.Collector{}
		if input.MembershipID.IsZero() {
			collector.Add("membership_id", "required", "A membership identifier is required.")
		}
		if input.Version < 1 {
			collector.Add("version", "required", "A positive membership version is required.")
		}
		if !input.Role.Valid() {
			collector.Add("role", "invalid", "The requested membership role is not supported.")
		}
		return Membership{}, collector.Error("The membership role change is invalid.")
	}
	auditID, err := identifier.New()
	if err != nil {
		return Membership{}, apperror.Wrap(err, apperror.CodeInternal, "A membership audit identifier could not be created.")
	}
	outboxID, err := identifier.New()
	if err != nil {
		return Membership{}, apperror.Wrap(err, apperror.CodeInternal, "A membership event identifier could not be created.")
	}
	now := service.clock.Now().UTC()
	updated, err := service.repository.ChangeRole(ctx, ChangeRoleRecord{
		OrganizationID: authorization.OrganizationID(), MembershipID: input.MembershipID,
		Role: input.Role, Version: input.Version, AuditID: auditID, OutboxID: outboxID,
		ActorType: actorType(authorization.Principal().Type()), ActorID: authorization.Principal().ID(),
		OccurredAt: now,
	})
	if err != nil {
		return Membership{}, mapMutationError(err)
	}
	return updated, nil
}

func (service *Service) Revoke(ctx context.Context, input RevokeInput) (Membership, error) {
	authorization, err := scopedAuthorization(ctx, input.OrganizationID, access.PermissionMembershipManage)
	if err != nil {
		return Membership{}, err
	}
	if input.MembershipID.IsZero() || input.Version < 1 {
		collector := validation.Collector{}
		if input.MembershipID.IsZero() {
			collector.Add("membership_id", "required", "A membership identifier is required.")
		}
		if input.Version < 1 {
			collector.Add("version", "required", "A positive membership version is required.")
		}
		return Membership{}, collector.Error("The membership revocation is invalid.")
	}
	auditID, err := identifier.New()
	if err != nil {
		return Membership{}, apperror.Wrap(err, apperror.CodeInternal, "A membership audit identifier could not be created.")
	}
	outboxID, err := identifier.New()
	if err != nil {
		return Membership{}, apperror.Wrap(err, apperror.CodeInternal, "A membership event identifier could not be created.")
	}
	updated, err := service.repository.Revoke(ctx, RevokeRecord{
		OrganizationID: authorization.OrganizationID(), MembershipID: input.MembershipID,
		Version: input.Version, AuditID: auditID, OutboxID: outboxID,
		ActorType: actorType(authorization.Principal().Type()), ActorID: authorization.Principal().ID(),
		OccurredAt: service.clock.Now().UTC(),
	})
	if err != nil {
		return Membership{}, mapMutationError(err)
	}
	return updated, nil
}

func scopedAuthorization(ctx context.Context, organizationID identifier.ID, permission access.Permission) (access.Authorization, error) {
	authorization, err := access.Require(ctx, permission)
	if err != nil {
		if errors.Is(err, access.ErrUnauthenticated) {
			return access.Authorization{}, apperror.Wrap(err, apperror.CodeUnauthenticated, "Authentication is required.")
		}
		return access.Authorization{}, apperror.Wrap(err, apperror.CodePermissionDenied, "Membership access is not permitted.")
	}
	if organizationID.IsZero() || authorization.OrganizationID() != organizationID {
		return access.Authorization{}, apperror.Wrap(ErrNotFound, apperror.CodeNotFound, "The requested membership does not exist.")
	}
	return authorization, nil
}

func mapMutationError(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return apperror.Wrap(err, apperror.CodeNotFound, "The requested membership does not exist.")
	case errors.Is(err, ErrVersionConflict):
		return apperror.Wrap(err, apperror.CodePreconditionFailed, "The membership changed; reload it and retry.")
	case errors.Is(err, ErrLastOwner):
		return apperror.Wrap(err, apperror.CodeConflict, "The last active owner cannot be removed or demoted.")
	default:
		return err
	}
}

func actorType(principalType access.PrincipalType) string {
	if principalType == access.PrincipalPlatform {
		return "platform"
	}
	return string(principalType)
}

type cursorPayload struct {
	Version        int    `json:"v"`
	OrganizationID string `json:"organization_id"`
	CreatedAt      string `json:"created_at"`
	ID             string `json:"id"`
}

// EncodeCursor keeps the transport opaque and binds it to the organization.
// The repository query remains tenant-scoped even if a client tampers with it;
// a keyed cursor signer will be added with the API secret/configuration slice.
func EncodeCursor(cursor Cursor) (string, error) {
	if cursor.OrganizationID.IsZero() || cursor.ID.IsZero() || cursor.CreatedAt.IsZero() {
		return "", errors.New("cursor fields are required")
	}
	body, err := json.Marshal(cursorPayload{Version: 1, OrganizationID: cursor.OrganizationID.String(), CreatedAt: cursor.CreatedAt.UTC().Format(time.RFC3339Nano), ID: cursor.ID.String()})
	if err != nil {
		return "", fmt.Errorf("encode membership cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(body), nil
}

func DecodeCursor(value string) (Cursor, error) {
	if len(value) == 0 || len(value) > 512 {
		return Cursor{}, apperror.New(apperror.CodeInvalidCursor, "The membership cursor is invalid.")
	}
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return Cursor{}, apperror.Wrap(err, apperror.CodeInvalidCursor, "The membership cursor is invalid.")
	}
	var payload cursorPayload
	if err := json.Unmarshal(body, &payload); err != nil || payload.Version != 1 {
		return Cursor{}, apperror.New(apperror.CodeInvalidCursor, "The membership cursor is invalid.")
	}
	organizationID, err := identifier.Parse(payload.OrganizationID)
	if err != nil {
		return Cursor{}, apperror.New(apperror.CodeInvalidCursor, "The membership cursor is invalid.")
	}
	id, err := identifier.Parse(payload.ID)
	if err != nil {
		return Cursor{}, apperror.New(apperror.CodeInvalidCursor, "The membership cursor is invalid.")
	}
	createdAt, err := time.Parse(time.RFC3339Nano, payload.CreatedAt)
	if err != nil {
		return Cursor{}, apperror.New(apperror.CodeInvalidCursor, "The membership cursor is invalid.")
	}
	return Cursor{OrganizationID: organizationID, CreatedAt: createdAt.UTC(), ID: id}, nil
}
