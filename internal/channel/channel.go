// Package channel owns tenant-scoped sales channels.
package channel

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/HK9750/venueos/internal/access"
	"github.com/HK9750/venueos/internal/platform/apperror"
	"github.com/HK9750/venueos/internal/platform/clock"
	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/internal/platform/validation"
)

type Type string

const (
	TypePublic      Type = "public"
	TypeBoxOffice   Type = "box_office"
	TypePartner     Type = "partner"
	TypePrivateLink Type = "private_link"
	DefaultLimit         = int32(50)
	MaxLimit             = int32(100)
)

type Status string

const (
	StatusActive   Status = "active"
	StatusInactive Status = "inactive"
	StatusArchived Status = "archived"
)

var (
	ErrNotFound           = errors.New("sales channel not found")
	ErrKeyConflict        = errors.New("sales channel key already exists")
	ErrAllocationConflict = errors.New("session channel allocation conflicts with capacity")
	ErrAllocationExists   = errors.New("session channel allocation already exists")
	channelKeyPattern     = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
)

type SalesChannel struct {
	ID             identifier.ID
	OrganizationID identifier.ID
	Key            string
	DisplayName    string
	Type           Type
	Configuration  map[string]any
	Status         Status
	Version        int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type Cursor struct {
	OrganizationID identifier.ID
	CreatedAt      time.Time
	ID             identifier.ID
}

type Page struct {
	Items      []SalesChannel
	NextCursor *Cursor
}

type CreateInput struct {
	OrganizationID identifier.ID
	Key            string
	DisplayName    string
	Type           Type
	Configuration  map[string]any
}

type CreateRecord struct {
	Channel   SalesChannel
	AuditID   identifier.ID
	OutboxID  identifier.ID
	ActorType string
	ActorID   string
}

type AllocationMode string

const (
	AllocationHardReserved AllocationMode = "hard_reserved"
	AllocationSoftPolicy   AllocationMode = "soft_policy"
)

type Allocation struct {
	ID             identifier.ID
	OrganizationID identifier.ID
	SessionID      identifier.ID
	ChannelID      identifier.ID
	ScopeKey       string
	Mode           AllocationMode
	Quantity       int64
	Version        int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type AllocationCursor struct {
	OrganizationID identifier.ID
	SessionID      identifier.ID
	CreatedAt      time.Time
	ID             identifier.ID
}

type AllocationPage struct {
	Items      []Allocation
	NextCursor *AllocationCursor
}

type CreateAllocationInput struct {
	OrganizationID identifier.ID
	EventID        identifier.ID
	SessionID      identifier.ID
	ChannelID      identifier.ID
	ScopeKey       string
	Mode           AllocationMode
	Quantity       int64
}

type CreateAllocationRecord struct {
	Allocation Allocation
	EventID    identifier.ID
	AuditID    identifier.ID
	OutboxID   identifier.ID
	ActorType  string
	ActorID    string
}

type Repository interface {
	Create(context.Context, CreateRecord) (SalesChannel, error)
	List(context.Context, identifier.ID, int32, *Cursor) (Page, error)
	CreateAllocation(context.Context, CreateAllocationRecord) (Allocation, error)
	ListAllocations(context.Context, identifier.ID, identifier.ID, int32, *AllocationCursor) (AllocationPage, error)
}

type Service struct {
	repository Repository
	clock      clock.Clock
}

func NewService(repository Repository, timeSource clock.Clock) *Service {
	return &Service{repository: repository, clock: timeSource}
}

func (service *Service) Create(ctx context.Context, input CreateInput) (SalesChannel, error) {
	authorization, err := service.requireOrganization(ctx, input.OrganizationID, access.PermissionCatalogManage)
	if err != nil {
		return SalesChannel{}, err
	}
	normalized, err := validate(input)
	if err != nil {
		return SalesChannel{}, err
	}
	channelID, auditID, outboxID, err := newIDs3()
	if err != nil {
		return SalesChannel{}, apperror.Wrap(err, apperror.CodeInternal, "A sales channel identifier could not be created.")
	}
	now := service.clock.Now().UTC()
	created, err := service.repository.Create(ctx, CreateRecord{Channel: SalesChannel{ID: channelID, OrganizationID: authorization.OrganizationID(), Key: normalized.Key, DisplayName: normalized.DisplayName, Type: normalized.Type, Configuration: normalized.Configuration, Status: StatusActive, Version: 1, CreatedAt: now, UpdatedAt: now}, AuditID: auditID, OutboxID: outboxID, ActorType: actorType(authorization.Principal().Type()), ActorID: authorization.Principal().ID()})
	return created, mapError(err)
}

func (service *Service) List(ctx context.Context, organizationID identifier.ID, limit int32, after *Cursor) (Page, error) {
	authorization, err := service.requireOrganization(ctx, organizationID, access.PermissionCatalogRead)
	if err != nil {
		return Page{}, err
	}
	if limit == 0 {
		limit = DefaultLimit
	}
	if limit < 1 || limit > MaxLimit {
		return Page{}, apperror.New(apperror.CodeValidationFailed, "The sales channel list request is invalid.", apperror.Detail{Field: "limit", Code: "out_of_range", Message: fmt.Sprintf("Use a limit between 1 and %d.", MaxLimit)})
	}
	if after != nil && after.OrganizationID != authorization.OrganizationID() {
		return Page{}, apperror.New(apperror.CodeInvalidCursor, "The sales channel cursor is invalid.")
	}
	return service.repository.List(ctx, authorization.OrganizationID(), limit, after)
}

func (service *Service) CreateAllocation(ctx context.Context, input CreateAllocationInput) (Allocation, error) {
	authorization, err := service.requireOrganization(ctx, input.OrganizationID, access.PermissionCatalogManage)
	if err != nil {
		return Allocation{}, err
	}
	normalized, err := validateAllocation(input)
	if err != nil {
		return Allocation{}, err
	}
	allocationID, auditID, outboxID, err := newIDs3()
	if err != nil {
		return Allocation{}, apperror.Wrap(err, apperror.CodeInternal, "An allocation identifier could not be created.")
	}
	now := service.clock.Now().UTC()
	created, err := service.repository.CreateAllocation(ctx, CreateAllocationRecord{Allocation: Allocation{ID: allocationID, OrganizationID: authorization.OrganizationID(), SessionID: normalized.SessionID, ChannelID: normalized.ChannelID, ScopeKey: normalized.ScopeKey, Mode: normalized.Mode, Quantity: normalized.Quantity, Version: 1, CreatedAt: now, UpdatedAt: now}, EventID: normalized.EventID, AuditID: auditID, OutboxID: outboxID, ActorType: actorType(authorization.Principal().Type()), ActorID: authorization.Principal().ID()})
	return created, mapAllocationError(err)
}

func (service *Service) ListAllocations(ctx context.Context, organizationID, sessionID identifier.ID, limit int32, after *AllocationCursor) (AllocationPage, error) {
	authorization, err := service.requireOrganization(ctx, organizationID, access.PermissionCatalogRead)
	if err != nil {
		return AllocationPage{}, err
	}
	if limit == 0 {
		limit = DefaultLimit
	}
	if limit < 1 || limit > MaxLimit {
		return AllocationPage{}, apperror.New(apperror.CodeValidationFailed, "The allocation list request is invalid.", apperror.Detail{Field: "limit", Code: "out_of_range", Message: fmt.Sprintf("Use a limit between 1 and %d.", MaxLimit)})
	}
	if after != nil && (after.OrganizationID != authorization.OrganizationID() || after.SessionID != sessionID) {
		return AllocationPage{}, apperror.New(apperror.CodeInvalidCursor, "The allocation cursor is invalid.")
	}
	return service.repository.ListAllocations(ctx, authorization.OrganizationID(), sessionID, limit, after)
}

func validate(input CreateInput) (CreateInput, error) {
	input.Key = strings.ToLower(strings.TrimSpace(input.Key))
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	if input.Configuration == nil {
		input.Configuration = map[string]any{}
	}
	collector := validation.Collector{}
	if len(input.Key) < 2 || len(input.Key) > 63 || !channelKeyPattern.MatchString(input.Key) {
		collector.Add("key", "invalid", "Use 2 to 63 lowercase letters, digits, or single hyphens.")
	}
	if input.DisplayName == "" || len(input.DisplayName) > 160 {
		collector.Add("display_name", "invalid", "Use 1 to 160 visible characters.")
	}
	switch input.Type {
	case TypePublic, TypeBoxOffice, TypePartner, TypePrivateLink:
	default:
		collector.Add("type", "invalid", "Use public, box_office, partner, or private_link.")
	}
	configuration, err := json.Marshal(input.Configuration)
	if err != nil || len(configuration) > 32768 {
		collector.Add("configuration", "invalid", "Configuration must be a JSON object no larger than 32 KiB.")
	}
	if err := collector.Error("The sales channel request is invalid."); err != nil {
		return CreateInput{}, err
	}
	return input, nil
}

func validateAllocation(input CreateAllocationInput) (CreateAllocationInput, error) {
	input.ScopeKey = strings.TrimSpace(input.ScopeKey)
	if input.ScopeKey == "" {
		input.ScopeKey = "all"
	}
	collector := validation.Collector{}
	if input.SessionID.IsZero() {
		collector.Add("session_id", "required", "A session identifier is required.")
	}
	if input.ChannelID.IsZero() {
		collector.Add("channel_id", "required", "A channel identifier is required.")
	}
	if !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:-]{0,127}$`).MatchString(input.ScopeKey) {
		collector.Add("scope_key", "invalid", "Use 1 to 128 letters, digits, dots, underscores, colons, or hyphens.")
	}
	if input.Mode != AllocationHardReserved && input.Mode != AllocationSoftPolicy {
		collector.Add("mode", "invalid", "Use hard_reserved or soft_policy.")
	}
	if input.Quantity < 1 {
		collector.Add("quantity", "out_of_range", "Allocation quantity must be greater than zero.")
	}
	if err := collector.Error("The allocation request is invalid."); err != nil {
		return CreateAllocationInput{}, err
	}
	return input, nil
}

func (service *Service) requireOrganization(ctx context.Context, organizationID identifier.ID, permission access.Permission) (access.Authorization, error) {
	authorization, err := access.Require(ctx, permission)
	if err != nil {
		if errors.Is(err, access.ErrUnauthenticated) {
			return access.Authorization{}, apperror.Wrap(err, apperror.CodeUnauthenticated, "Authentication is required.")
		}
		return access.Authorization{}, apperror.Wrap(err, apperror.CodePermissionDenied, "Sales channel access is not permitted.")
	}
	if organizationID.IsZero() || authorization.OrganizationID() != organizationID {
		return access.Authorization{}, apperror.Wrap(ErrNotFound, apperror.CodeNotFound, "The requested sales channel does not exist.")
	}
	return authorization, nil
}

func mapError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotFound):
		return apperror.Wrap(err, apperror.CodeNotFound, "The requested sales channel does not exist.")
	case errors.Is(err, ErrKeyConflict):
		return apperror.Wrap(err, apperror.CodeConflict, "A sales channel with this key already exists.")
	default:
		return err
	}
}

func mapAllocationError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotFound):
		return apperror.Wrap(err, apperror.CodeNotFound, "The requested session or sales channel does not exist.")
	case errors.Is(err, ErrAllocationConflict), errors.Is(err, ErrAllocationExists):
		return apperror.Wrap(err, apperror.CodeConflict, "The session channel allocation conflicts with existing configuration.")
	default:
		return err
	}
}

func EncodeCursor(cursor Cursor) (string, error) {
	if cursor.OrganizationID.IsZero() || cursor.ID.IsZero() || cursor.CreatedAt.IsZero() {
		return "", errors.New("cursor fields are required")
	}
	body, err := json.Marshal(struct {
		Version int    `json:"v"`
		OrgID   string `json:"organization_id"`
		Created string `json:"created_at"`
		ID      string `json:"id"`
	}{1, cursor.OrganizationID.String(), cursor.CreatedAt.UTC().Format(time.RFC3339Nano), cursor.ID.String()})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(body), nil
}

func DecodeCursor(value string) (Cursor, error) {
	if len(value) == 0 || len(value) > 512 {
		return Cursor{}, apperror.New(apperror.CodeInvalidCursor, "The sales channel cursor is invalid.")
	}
	var payload struct {
		Version int    `json:"v"`
		OrgID   string `json:"organization_id"`
		Created string `json:"created_at"`
		ID      string `json:"id"`
	}
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(body, &payload) != nil || payload.Version != 1 {
		return Cursor{}, apperror.New(apperror.CodeInvalidCursor, "The sales channel cursor is invalid.")
	}
	orgID, err := identifier.Parse(payload.OrgID)
	if err != nil {
		return Cursor{}, apperror.New(apperror.CodeInvalidCursor, "The sales channel cursor is invalid.")
	}
	id, err := identifier.Parse(payload.ID)
	if err != nil {
		return Cursor{}, apperror.New(apperror.CodeInvalidCursor, "The sales channel cursor is invalid.")
	}
	created, err := time.Parse(time.RFC3339Nano, payload.Created)
	if err != nil {
		return Cursor{}, apperror.New(apperror.CodeInvalidCursor, "The sales channel cursor is invalid.")
	}
	return Cursor{OrganizationID: orgID, CreatedAt: created.UTC(), ID: id}, nil
}

func EncodeAllocationCursor(cursor AllocationCursor) (string, error) {
	if cursor.OrganizationID.IsZero() || cursor.SessionID.IsZero() || cursor.ID.IsZero() || cursor.CreatedAt.IsZero() {
		return "", errors.New("allocation cursor fields are required")
	}
	body, err := json.Marshal(struct {
		Version int    `json:"v"`
		OrgID   string `json:"organization_id"`
		Session string `json:"session_id"`
		Created string `json:"created_at"`
		ID      string `json:"id"`
	}{1, cursor.OrganizationID.String(), cursor.SessionID.String(), cursor.CreatedAt.UTC().Format(time.RFC3339Nano), cursor.ID.String()})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(body), nil
}

func DecodeAllocationCursor(value string) (AllocationCursor, error) {
	if len(value) == 0 || len(value) > 512 {
		return AllocationCursor{}, apperror.New(apperror.CodeInvalidCursor, "The allocation cursor is invalid.")
	}
	var payload struct {
		Version int    `json:"v"`
		OrgID   string `json:"organization_id"`
		Session string `json:"session_id"`
		Created string `json:"created_at"`
		ID      string `json:"id"`
	}
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(body, &payload) != nil || payload.Version != 1 {
		return AllocationCursor{}, apperror.New(apperror.CodeInvalidCursor, "The allocation cursor is invalid.")
	}
	orgID, err := identifier.Parse(payload.OrgID)
	if err != nil {
		return AllocationCursor{}, apperror.New(apperror.CodeInvalidCursor, "The allocation cursor is invalid.")
	}
	sessionID, err := identifier.Parse(payload.Session)
	if err != nil {
		return AllocationCursor{}, apperror.New(apperror.CodeInvalidCursor, "The allocation cursor is invalid.")
	}
	id, err := identifier.Parse(payload.ID)
	if err != nil {
		return AllocationCursor{}, apperror.New(apperror.CodeInvalidCursor, "The allocation cursor is invalid.")
	}
	created, err := time.Parse(time.RFC3339Nano, payload.Created)
	if err != nil {
		return AllocationCursor{}, apperror.New(apperror.CodeInvalidCursor, "The allocation cursor is invalid.")
	}
	return AllocationCursor{OrganizationID: orgID, SessionID: sessionID, CreatedAt: created.UTC(), ID: id}, nil
}

func actorType(value access.PrincipalType) string {
	return string(value)
}

func newIDs3() (identifier.ID, identifier.ID, identifier.ID, error) {
	first, err := identifier.New()
	if err != nil {
		return identifier.ID{}, identifier.ID{}, identifier.ID{}, err
	}
	second, err := identifier.New()
	if err != nil {
		return identifier.ID{}, identifier.ID{}, identifier.ID{}, err
	}
	third, err := identifier.New()
	if err != nil {
		return identifier.ID{}, identifier.ID{}, identifier.ID{}, err
	}
	return first, second, third, nil
}
