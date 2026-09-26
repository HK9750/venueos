// Package venue owns organization-scoped venues and spaces.
package venue

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/HK9750/venueos/internal/access"
	"github.com/HK9750/venueos/internal/platform/apperror"
	"github.com/HK9750/venueos/internal/platform/clock"
	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/internal/platform/validation"
)

type Status string

const (
	StatusDraft    Status = "draft"
	StatusActive   Status = "active"
	StatusInactive Status = "inactive"
	StatusArchived Status = "archived"
)

type InventoryMode string

const (
	InventoryAssigned InventoryMode = "assigned_seating"
	InventoryGA       InventoryMode = "general_admission"
)

const (
	DefaultLimit = int32(50)
	MaxLimit     = int32(100)
)

var (
	ErrNotFound        = errors.New("venue resource not found")
	ErrSlugConflict    = errors.New("venue resource slug already exists")
	ErrVersionConflict = errors.New("venue resource version conflict")
	ErrInvalidState    = errors.New("venue resource state transition is invalid")
	slugPattern        = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
)

type Venue struct {
	ID             identifier.ID
	OrganizationID identifier.ID
	Slug           string
	DisplayName    string
	Status         Status
	Timezone       string
	Version        int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type Space struct {
	ID               identifier.ID
	OrganizationID   identifier.ID
	VenueID          identifier.ID
	Slug             string
	DisplayName      string
	Status           Status
	InventoryMode    InventoryMode
	PhysicalCapacity int64
	Version          int64
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type Gate struct {
	ID             identifier.ID
	OrganizationID identifier.ID
	VenueID        identifier.ID
	SpaceID        *identifier.ID
	Code           string
	DisplayName    string
	Status         Status
	Version        int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type GAPoolTemplate struct {
	ID               identifier.ID
	OrganizationID   identifier.ID
	SpaceID          identifier.ID
	Slug             string
	DisplayName      string
	Status           Status
	PhysicalCapacity int64
	SellableCapacity int64
	Version          int64
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type Cursor struct {
	OrganizationID identifier.ID
	ParentID       identifier.ID
	CreatedAt      time.Time
	ID             identifier.ID
}

type Page[T any] struct {
	Items      []T
	NextCursor *Cursor
}

type CreateVenueInput struct {
	OrganizationID identifier.ID
	Slug           string
	DisplayName    string
	Timezone       string
}

type UpdateVenueInput struct {
	OrganizationID identifier.ID
	VenueID        identifier.ID
	Slug           string
	DisplayName    string
	Timezone       string
	Version        int64
}

type ArchiveVenueInput struct {
	OrganizationID identifier.ID
	VenueID        identifier.ID
	Version        int64
}

type RestoreVenueInput struct {
	OrganizationID identifier.ID
	VenueID        identifier.ID
	Version        int64
}

type CreateSpaceInput struct {
	OrganizationID   identifier.ID
	VenueID          identifier.ID
	Slug             string
	DisplayName      string
	InventoryMode    InventoryMode
	PhysicalCapacity int64
}

type CreateGateInput struct {
	OrganizationID identifier.ID
	VenueID        identifier.ID
	SpaceID        identifier.ID
	Code           string
	DisplayName    string
}

type CreateGAPoolInput struct {
	OrganizationID   identifier.ID
	SpaceID          identifier.ID
	Slug             string
	DisplayName      string
	PhysicalCapacity int64
	SellableCapacity int64
}

type CreateVenueRecord struct {
	Venue     Venue
	AuditID   identifier.ID
	OutboxID  identifier.ID
	ActorType string
	ActorID   string
}

type UpdateVenueRecord struct {
	Venue      UpdateVenueInput
	AuditID    identifier.ID
	OutboxID   identifier.ID
	ActorType  string
	ActorID    string
	OccurredAt time.Time
}

type ArchiveVenueRecord struct {
	OrganizationID identifier.ID
	VenueID        identifier.ID
	Version        int64
	AuditID        identifier.ID
	OutboxID       identifier.ID
	ActorType      string
	ActorID        string
	OccurredAt     time.Time
}

type RestoreVenueRecord struct {
	OrganizationID identifier.ID
	VenueID        identifier.ID
	Version        int64
	AuditID        identifier.ID
	OutboxID       identifier.ID
	ActorType      string
	ActorID        string
	OccurredAt     time.Time
}

type CreateSpaceRecord struct {
	Space     Space
	AuditID   identifier.ID
	OutboxID  identifier.ID
	ActorType string
	ActorID   string
}

type CreateGateRecord struct {
	Gate      Gate
	AuditID   identifier.ID
	OutboxID  identifier.ID
	ActorType string
	ActorID   string
}

type CreateGAPoolRecord struct {
	Pool      GAPoolTemplate
	AuditID   identifier.ID
	OutboxID  identifier.ID
	ActorType string
	ActorID   string
}

type Repository interface {
	CreateVenue(context.Context, CreateVenueRecord) (Venue, error)
	GetVenue(context.Context, identifier.ID, identifier.ID) (Venue, error)
	ListVenues(context.Context, identifier.ID, int32, *Cursor) (Page[Venue], error)
	UpdateVenue(context.Context, UpdateVenueRecord) (Venue, error)
	ArchiveVenue(context.Context, ArchiveVenueRecord) (Venue, error)
	RestoreVenue(context.Context, RestoreVenueRecord) (Venue, error)
	CreateSpace(context.Context, CreateSpaceRecord) (Space, error)
	GetSpace(context.Context, identifier.ID, identifier.ID, identifier.ID) (Space, error)
	ListSpaces(context.Context, identifier.ID, identifier.ID, int32, *Cursor) (Page[Space], error)
	CreateGate(context.Context, CreateGateRecord) (Gate, error)
	GetGate(context.Context, identifier.ID, identifier.ID, identifier.ID) (Gate, error)
	ListGates(context.Context, identifier.ID, identifier.ID, int32, *Cursor) (Page[Gate], error)
	CreateGAPool(context.Context, CreateGAPoolRecord) (GAPoolTemplate, error)
	GetGAPool(context.Context, identifier.ID, identifier.ID, identifier.ID) (GAPoolTemplate, error)
	ListGAPools(context.Context, identifier.ID, identifier.ID, int32, *Cursor) (Page[GAPoolTemplate], error)
	CreateSeatMap(context.Context, CreateSeatMapRecord) (SeatMap, error)
	GetSeatMap(context.Context, identifier.ID, identifier.ID, identifier.ID) (SeatMap, error)
	ListSeatMaps(context.Context, identifier.ID, identifier.ID, int32, *Cursor) (Page[SeatMap], error)
	UpdateSeatMap(context.Context, UpdateSeatMapRecord) (SeatMap, error)
	PublishSeatMap(context.Context, PublishSeatMapRecord) (SeatMap, error)
	CloneSeatMap(context.Context, CloneSeatMapRecord) (SeatMap, error)
}

type Service struct {
	repository Repository
	clock      clock.Clock
}

func NewService(repository Repository, timeSource clock.Clock) *Service {
	return &Service{repository: repository, clock: timeSource}
}

func (service *Service) CreateVenue(ctx context.Context, input CreateVenueInput) (Venue, error) {
	authorization, err := service.requireOrganization(ctx, input.OrganizationID, access.PermissionVenueManage)
	if err != nil {
		return Venue{}, err
	}
	normalized, validationErr := validateVenueInput(input)
	if validationErr != nil {
		return Venue{}, validationErr
	}
	venueID, auditID, outboxID, err := newIDs3()
	if err != nil {
		return Venue{}, apperror.Wrap(err, apperror.CodeInternal, "A venue identifier could not be created.")
	}
	now := service.clock.Now().UTC()
	created, err := service.repository.CreateVenue(ctx, CreateVenueRecord{Venue: Venue{ID: venueID, OrganizationID: authorization.OrganizationID(), Slug: normalized.Slug, DisplayName: normalized.DisplayName, Status: StatusDraft, Timezone: normalized.Timezone, Version: 1, CreatedAt: now, UpdatedAt: now}, AuditID: auditID, OutboxID: outboxID, ActorType: actorType(authorization.Principal().Type()), ActorID: authorization.Principal().ID()})
	return created, mapMutationError(err)
}

func (service *Service) GetVenue(ctx context.Context, organizationID, venueID identifier.ID) (Venue, error) {
	authorization, err := service.requireOrganization(ctx, organizationID, access.PermissionVenueRead)
	if err != nil {
		return Venue{}, err
	}
	if venueID.IsZero() {
		return Venue{}, apperror.Wrap(ErrNotFound, apperror.CodeNotFound, "The requested venue does not exist.")
	}
	value, err := service.repository.GetVenue(ctx, authorization.OrganizationID(), venueID)
	if errors.Is(err, ErrNotFound) {
		return Venue{}, apperror.Wrap(err, apperror.CodeNotFound, "The requested venue does not exist.")
	}
	return value, err
}

func (service *Service) ListVenues(ctx context.Context, organizationID identifier.ID, limit int32, after *Cursor) (Page[Venue], error) {
	authorization, err := service.requireOrganization(ctx, organizationID, access.PermissionVenueRead)
	if err != nil {
		return Page[Venue]{}, err
	}
	limit, err = boundedLimit(limit)
	if err != nil {
		return Page[Venue]{}, err
	}
	if after != nil && (after.OrganizationID != authorization.OrganizationID() || !after.ParentID.IsZero()) {
		return Page[Venue]{}, apperror.New(apperror.CodeInvalidCursor, "The venue cursor is invalid.")
	}
	return service.repository.ListVenues(ctx, authorization.OrganizationID(), limit, after)
}

func (service *Service) UpdateVenue(ctx context.Context, input UpdateVenueInput) (Venue, error) {
	authorization, err := service.requireOrganization(ctx, input.OrganizationID, access.PermissionVenueManage)
	if err != nil {
		return Venue{}, err
	}
	if input.VenueID.IsZero() || input.Version < 1 {
		return Venue{}, apperror.New(apperror.CodeValidationFailed, "The venue update is invalid.")
	}
	normalized, validationErr := validateVenueInput(CreateVenueInput{Slug: input.Slug, DisplayName: input.DisplayName, Timezone: input.Timezone})
	if validationErr != nil {
		return Venue{}, validationErr
	}
	auditID, outboxID, err := newIDs2()
	if err != nil {
		return Venue{}, apperror.Wrap(err, apperror.CodeInternal, "A venue update identifier could not be created.")
	}
	input.Slug, input.DisplayName, input.Timezone = normalized.Slug, normalized.DisplayName, normalized.Timezone
	value, err := service.repository.UpdateVenue(ctx, UpdateVenueRecord{Venue: input, AuditID: auditID, OutboxID: outboxID, ActorType: actorType(authorization.Principal().Type()), ActorID: authorization.Principal().ID(), OccurredAt: service.clock.Now().UTC()})
	return value, mapMutationError(err)
}

func (service *Service) ArchiveVenue(ctx context.Context, input ArchiveVenueInput) (Venue, error) {
	authorization, err := service.requireOrganization(ctx, input.OrganizationID, access.PermissionVenueManage)
	if err != nil {
		return Venue{}, err
	}
	if input.VenueID.IsZero() || input.Version < 1 {
		return Venue{}, apperror.New(apperror.CodeValidationFailed, "The venue archive request is invalid.")
	}
	auditID, outboxID, err := newIDs2()
	if err != nil {
		return Venue{}, apperror.Wrap(err, apperror.CodeInternal, "A venue archive identifier could not be created.")
	}
	value, err := service.repository.ArchiveVenue(ctx, ArchiveVenueRecord{OrganizationID: authorization.OrganizationID(), VenueID: input.VenueID, Version: input.Version, AuditID: auditID, OutboxID: outboxID, ActorType: actorType(authorization.Principal().Type()), ActorID: authorization.Principal().ID(), OccurredAt: service.clock.Now().UTC()})
	return value, mapMutationError(err)
}

func (service *Service) RestoreVenue(ctx context.Context, input RestoreVenueInput) (Venue, error) {
	authorization, err := service.requireOrganization(ctx, input.OrganizationID, access.PermissionVenueManage)
	if err != nil {
		return Venue{}, err
	}
	if input.VenueID.IsZero() || input.Version < 1 {
		return Venue{}, apperror.New(apperror.CodeValidationFailed, "The venue restore request is invalid.")
	}
	auditID, outboxID, err := newIDs2()
	if err != nil {
		return Venue{}, apperror.Wrap(err, apperror.CodeInternal, "A venue restore identifier could not be created.")
	}
	value, err := service.repository.RestoreVenue(ctx, RestoreVenueRecord{OrganizationID: authorization.OrganizationID(), VenueID: input.VenueID, Version: input.Version, AuditID: auditID, OutboxID: outboxID, ActorType: actorType(authorization.Principal().Type()), ActorID: authorization.Principal().ID(), OccurredAt: service.clock.Now().UTC()})
	return value, mapMutationError(err)
}

func (service *Service) CreateSpace(ctx context.Context, input CreateSpaceInput) (Space, error) {
	authorization, err := service.requireOrganization(ctx, input.OrganizationID, access.PermissionVenueManage)
	if err != nil {
		return Space{}, err
	}
	normalized, validationErr := validateSpaceInput(input)
	if validationErr != nil {
		return Space{}, validationErr
	}
	spaceID, auditID, outboxID, err := newIDs3()
	if err != nil {
		return Space{}, apperror.Wrap(err, apperror.CodeInternal, "A space identifier could not be created.")
	}
	now := service.clock.Now().UTC()
	value, err := service.repository.CreateSpace(ctx, CreateSpaceRecord{Space: Space{ID: spaceID, OrganizationID: authorization.OrganizationID(), VenueID: input.VenueID, Slug: normalized.Slug, DisplayName: normalized.DisplayName, Status: StatusDraft, InventoryMode: normalized.InventoryMode, PhysicalCapacity: normalized.PhysicalCapacity, Version: 1, CreatedAt: now, UpdatedAt: now}, AuditID: auditID, OutboxID: outboxID, ActorType: actorType(authorization.Principal().Type()), ActorID: authorization.Principal().ID()})
	return value, mapMutationError(err)
}

func (service *Service) GetSpace(ctx context.Context, organizationID, venueID, spaceID identifier.ID) (Space, error) {
	authorization, err := service.requireOrganization(ctx, organizationID, access.PermissionVenueRead)
	if err != nil {
		return Space{}, err
	}
	value, err := service.repository.GetSpace(ctx, authorization.OrganizationID(), venueID, spaceID)
	if errors.Is(err, ErrNotFound) {
		return Space{}, apperror.Wrap(err, apperror.CodeNotFound, "The requested space does not exist.")
	}
	return value, err
}

func (service *Service) ListSpaces(ctx context.Context, organizationID, venueID identifier.ID, limit int32, after *Cursor) (Page[Space], error) {
	authorization, err := service.requireOrganization(ctx, organizationID, access.PermissionVenueRead)
	if err != nil {
		return Page[Space]{}, err
	}
	limit, err = boundedLimit(limit)
	if err != nil {
		return Page[Space]{}, err
	}
	if after != nil && (after.OrganizationID != authorization.OrganizationID() || after.ParentID != venueID) {
		return Page[Space]{}, apperror.New(apperror.CodeInvalidCursor, "The space cursor is invalid.")
	}
	return service.repository.ListSpaces(ctx, authorization.OrganizationID(), venueID, limit, after)
}

func (service *Service) CreateGate(ctx context.Context, input CreateGateInput) (Gate, error) {
	authorization, err := service.requireOrganization(ctx, input.OrganizationID, access.PermissionVenueManage)
	if err != nil {
		return Gate{}, err
	}
	input.Code = strings.ToLower(strings.TrimSpace(input.Code))
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	collector := validation.Collector{}
	if input.VenueID.IsZero() {
		collector.Add("venue_id", "required", "A venue identifier is required.")
	}
	if len(input.Code) < 1 || len(input.Code) > 32 || !slugPattern.MatchString(input.Code) {
		collector.Add("code", "invalid", "Use 1 to 32 lowercase letters, digits, or single hyphens.")
	}
	if input.DisplayName == "" || utf8.RuneCountInString(input.DisplayName) > 120 || strings.IndexFunc(input.DisplayName, unicode.IsControl) >= 0 {
		collector.Add("display_name", "invalid", "Use 1 to 120 visible characters.")
	}
	if err := collector.Error("The gate request is invalid."); err != nil {
		return Gate{}, err
	}
	gateID, auditID, outboxID, err := newIDs3()
	if err != nil {
		return Gate{}, apperror.Wrap(err, apperror.CodeInternal, "A gate identifier could not be created.")
	}
	now := service.clock.Now().UTC()
	value, err := service.repository.CreateGate(ctx, CreateGateRecord{Gate: Gate{ID: gateID, OrganizationID: authorization.OrganizationID(), VenueID: input.VenueID, SpaceID: optionalID(input.SpaceID), Code: input.Code, DisplayName: input.DisplayName, Status: StatusDraft, Version: 1, CreatedAt: now, UpdatedAt: now}, AuditID: auditID, OutboxID: outboxID, ActorType: actorType(authorization.Principal().Type()), ActorID: authorization.Principal().ID()})
	return value, mapMutationError(err)
}

func (service *Service) GetGate(ctx context.Context, organizationID, venueID, gateID identifier.ID) (Gate, error) {
	authorization, err := service.requireOrganization(ctx, organizationID, access.PermissionVenueRead)
	if err != nil {
		return Gate{}, err
	}
	value, err := service.repository.GetGate(ctx, authorization.OrganizationID(), venueID, gateID)
	if errors.Is(err, ErrNotFound) {
		return Gate{}, apperror.Wrap(err, apperror.CodeNotFound, "The requested gate does not exist.")
	}
	return value, err
}

func (service *Service) ListGates(ctx context.Context, organizationID, venueID identifier.ID, limit int32, after *Cursor) (Page[Gate], error) {
	authorization, err := service.requireOrganization(ctx, organizationID, access.PermissionVenueRead)
	if err != nil {
		return Page[Gate]{}, err
	}
	limit, err = boundedLimit(limit)
	if err != nil {
		return Page[Gate]{}, err
	}
	if after != nil && (after.OrganizationID != authorization.OrganizationID() || after.ParentID != venueID) {
		return Page[Gate]{}, apperror.New(apperror.CodeInvalidCursor, "The gate cursor is invalid.")
	}
	return service.repository.ListGates(ctx, authorization.OrganizationID(), venueID, limit, after)
}

func (service *Service) CreateGAPool(ctx context.Context, input CreateGAPoolInput) (GAPoolTemplate, error) {
	authorization, err := service.requireOrganization(ctx, input.OrganizationID, access.PermissionVenueManage)
	if err != nil {
		return GAPoolTemplate{}, err
	}
	input.Slug = strings.ToLower(strings.TrimSpace(input.Slug))
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	collector := validation.Collector{}
	if input.SpaceID.IsZero() {
		collector.Add("space_id", "required", "A space identifier is required.")
	}
	if len(input.Slug) < 2 || len(input.Slug) > 63 || !slugPattern.MatchString(input.Slug) {
		collector.Add("slug", "invalid", "Use 2 to 63 lowercase letters, digits, or single hyphens.")
	}
	if input.DisplayName == "" || utf8.RuneCountInString(input.DisplayName) > 160 || strings.IndexFunc(input.DisplayName, unicode.IsControl) >= 0 {
		collector.Add("display_name", "invalid", "Use 1 to 160 visible characters.")
	}
	if input.PhysicalCapacity < 1 || input.PhysicalCapacity > 1000000 {
		collector.Add("physical_capacity", "out_of_range", "Use a capacity between 1 and 1000000.")
	}
	if input.SellableCapacity < 1 || input.SellableCapacity > input.PhysicalCapacity {
		collector.Add("sellable_capacity", "out_of_range", "Sellable capacity must be positive and no greater than physical capacity.")
	}
	if err := collector.Error("The general-admission pool request is invalid."); err != nil {
		return GAPoolTemplate{}, err
	}
	poolID, auditID, outboxID, err := newIDs3()
	if err != nil {
		return GAPoolTemplate{}, apperror.Wrap(err, apperror.CodeInternal, "A general-admission pool identifier could not be created.")
	}
	now := service.clock.Now().UTC()
	value, err := service.repository.CreateGAPool(ctx, CreateGAPoolRecord{Pool: GAPoolTemplate{ID: poolID, OrganizationID: authorization.OrganizationID(), SpaceID: input.SpaceID, Slug: input.Slug, DisplayName: input.DisplayName, Status: StatusDraft, PhysicalCapacity: input.PhysicalCapacity, SellableCapacity: input.SellableCapacity, Version: 1, CreatedAt: now, UpdatedAt: now}, AuditID: auditID, OutboxID: outboxID, ActorType: actorType(authorization.Principal().Type()), ActorID: authorization.Principal().ID()})
	return value, mapMutationError(err)
}

func (service *Service) GetGAPool(ctx context.Context, organizationID, spaceID, poolID identifier.ID) (GAPoolTemplate, error) {
	authorization, err := service.requireOrganization(ctx, organizationID, access.PermissionVenueRead)
	if err != nil {
		return GAPoolTemplate{}, err
	}
	value, err := service.repository.GetGAPool(ctx, authorization.OrganizationID(), spaceID, poolID)
	if errors.Is(err, ErrNotFound) {
		return GAPoolTemplate{}, apperror.Wrap(err, apperror.CodeNotFound, "The requested general-admission pool does not exist.")
	}
	return value, err
}

func (service *Service) ListGAPools(ctx context.Context, organizationID, spaceID identifier.ID, limit int32, after *Cursor) (Page[GAPoolTemplate], error) {
	authorization, err := service.requireOrganization(ctx, organizationID, access.PermissionVenueRead)
	if err != nil {
		return Page[GAPoolTemplate]{}, err
	}
	limit, err = boundedLimit(limit)
	if err != nil {
		return Page[GAPoolTemplate]{}, err
	}
	if after != nil && (after.OrganizationID != authorization.OrganizationID() || after.ParentID != spaceID) {
		return Page[GAPoolTemplate]{}, apperror.New(apperror.CodeInvalidCursor, "The general-admission pool cursor is invalid.")
	}
	return service.repository.ListGAPools(ctx, authorization.OrganizationID(), spaceID, limit, after)
}

func (service *Service) requireOrganization(ctx context.Context, organizationID identifier.ID, permission access.Permission) (access.Authorization, error) {
	authorization, err := access.Require(ctx, permission)
	if err != nil {
		if errors.Is(err, access.ErrUnauthenticated) {
			return access.Authorization{}, apperror.Wrap(err, apperror.CodeUnauthenticated, "Authentication is required.")
		}
		return access.Authorization{}, apperror.Wrap(err, apperror.CodePermissionDenied, "Venue access is not permitted.")
	}
	if organizationID.IsZero() || authorization.OrganizationID() != organizationID {
		return access.Authorization{}, apperror.Wrap(ErrNotFound, apperror.CodeNotFound, "The requested venue resource does not exist.")
	}
	return authorization, nil
}

func validateVenueInput(input CreateVenueInput) (CreateVenueInput, error) {
	input.Slug = strings.ToLower(strings.TrimSpace(input.Slug))
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.Timezone = strings.TrimSpace(input.Timezone)
	collector := validation.Collector{}
	if len(input.Slug) < 2 || len(input.Slug) > 63 || !slugPattern.MatchString(input.Slug) {
		collector.Add("slug", "invalid", "Use 2 to 63 lowercase letters, digits, or single hyphens.")
	}
	if input.DisplayName == "" || utf8.RuneCountInString(input.DisplayName) > 160 || strings.IndexFunc(input.DisplayName, unicode.IsControl) >= 0 {
		collector.Add("display_name", "invalid", "Use 1 to 160 visible characters.")
	}
	if len(input.Timezone) > 64 {
		collector.Add("timezone", "invalid", "Use a valid IANA timezone identifier.")
	} else if _, err := time.LoadLocation(input.Timezone); err != nil {
		collector.Add("timezone", "invalid", "Use a valid IANA timezone identifier.")
	}
	return input, collector.Error("The venue request is invalid.")
}

func validateSpaceInput(input CreateSpaceInput) (CreateSpaceInput, error) {
	input.Slug = strings.ToLower(strings.TrimSpace(input.Slug))
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	collector := validation.Collector{}
	if input.VenueID.IsZero() {
		collector.Add("venue_id", "required", "A venue identifier is required.")
	}
	if len(input.Slug) < 2 || len(input.Slug) > 63 || !slugPattern.MatchString(input.Slug) {
		collector.Add("slug", "invalid", "Use 2 to 63 lowercase letters, digits, or single hyphens.")
	}
	if input.DisplayName == "" || utf8.RuneCountInString(input.DisplayName) > 160 || strings.IndexFunc(input.DisplayName, unicode.IsControl) >= 0 {
		collector.Add("display_name", "invalid", "Use 1 to 160 visible characters.")
	}
	if input.InventoryMode != InventoryAssigned && input.InventoryMode != InventoryGA {
		collector.Add("inventory_mode", "invalid", "Use assigned_seating or general_admission.")
	}
	if input.PhysicalCapacity < 1 || input.PhysicalCapacity > 1000000 {
		collector.Add("physical_capacity", "out_of_range", "Use a capacity between 1 and 1000000.")
	}
	return input, collector.Error("The space request is invalid.")
}

func boundedLimit(limit int32) (int32, error) {
	if limit == 0 {
		return DefaultLimit, nil
	}
	if limit < 1 || limit > MaxLimit {
		return 0, apperror.New(apperror.CodeValidationFailed, "The venue list request is invalid.", apperror.Detail{Field: "limit", Code: "out_of_range", Message: fmt.Sprintf("Use a limit between 1 and %d.", MaxLimit)})
	}
	return limit, nil
}

func mapMutationError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotFound):
		return apperror.Wrap(err, apperror.CodeNotFound, "The requested venue resource does not exist.")
	case errors.Is(err, ErrSlugConflict):
		return apperror.Wrap(err, apperror.CodeConflict, "A venue resource with this slug already exists.")
	case errors.Is(err, ErrVersionConflict):
		return apperror.Wrap(err, apperror.CodePreconditionFailed, "The venue resource changed; reload it and retry.")
	case errors.Is(err, ErrInvalidState):
		return apperror.Wrap(err, apperror.CodeConflict, "The venue resource cannot be changed in its current state.")
	default:
		return err
	}
}

func EncodeCursor(cursor Cursor) (string, error) {
	if cursor.OrganizationID.IsZero() || cursor.ID.IsZero() || cursor.CreatedAt.IsZero() {
		return "", errors.New("cursor fields are required")
	}
	payload := struct {
		Version int    `json:"v"`
		OrgID   string `json:"organization_id"`
		Parent  string `json:"parent_id,omitempty"`
		Created string `json:"created_at"`
		ID      string `json:"id"`
	}{Version: 1, OrgID: cursor.OrganizationID.String(), Created: cursor.CreatedAt.UTC().Format(time.RFC3339Nano), ID: cursor.ID.String()}
	if !cursor.ParentID.IsZero() {
		payload.Parent = cursor.ParentID.String()
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(body), nil
}

func DecodeCursor(value string) (Cursor, error) {
	if len(value) == 0 || len(value) > 512 {
		return Cursor{}, apperror.New(apperror.CodeInvalidCursor, "The venue cursor is invalid.")
	}
	var payload struct {
		Version int    `json:"v"`
		OrgID   string `json:"organization_id"`
		Parent  string `json:"parent_id"`
		Created string `json:"created_at"`
		ID      string `json:"id"`
	}
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(body, &payload) != nil || payload.Version != 1 {
		return Cursor{}, apperror.New(apperror.CodeInvalidCursor, "The venue cursor is invalid.")
	}
	orgID, err := identifier.Parse(payload.OrgID)
	if err != nil {
		return Cursor{}, apperror.New(apperror.CodeInvalidCursor, "The venue cursor is invalid.")
	}
	id, err := identifier.Parse(payload.ID)
	if err != nil {
		return Cursor{}, apperror.New(apperror.CodeInvalidCursor, "The venue cursor is invalid.")
	}
	createdAt, err := time.Parse(time.RFC3339Nano, payload.Created)
	if err != nil {
		return Cursor{}, apperror.New(apperror.CodeInvalidCursor, "The venue cursor is invalid.")
	}
	var parentID identifier.ID
	if payload.Parent != "" {
		parentID, err = identifier.Parse(payload.Parent)
		if err != nil {
			return Cursor{}, apperror.New(apperror.CodeInvalidCursor, "The venue cursor is invalid.")
		}
	}
	return Cursor{OrganizationID: orgID, ParentID: parentID, CreatedAt: createdAt.UTC(), ID: id}, nil
}

func actorType(value access.PrincipalType) string {
	if value == access.PrincipalPlatform {
		return "platform"
	}
	return string(value)
}

func optionalID(value identifier.ID) *identifier.ID {
	if value.IsZero() {
		return nil
	}
	return &value
}

func newIDs2() (identifier.ID, identifier.ID, error) {
	first, err := identifier.New()
	if err != nil {
		return identifier.ID{}, identifier.ID{}, err
	}
	second, err := identifier.New()
	if err != nil {
		return identifier.ID{}, identifier.ID{}, err
	}
	return first, second, nil
}

func newIDs3() (identifier.ID, identifier.ID, identifier.ID, error) {
	first, second, err := newIDs2()
	if err != nil {
		return identifier.ID{}, identifier.ID{}, identifier.ID{}, err
	}
	third, err := identifier.New()
	if err != nil {
		return identifier.ID{}, identifier.ID{}, identifier.ID{}, err
	}
	return first, second, third, nil
}
