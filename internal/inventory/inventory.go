// Package inventory owns durable session inventory and holds.
package inventory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/HK9750/venueos/internal/access"
	"github.com/HK9750/venueos/internal/platform/apperror"
	"github.com/HK9750/venueos/internal/platform/clock"
	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/internal/platform/money"
	"github.com/HK9750/venueos/internal/platform/validation"
)

type HoldStatus string

const (
	HoldActive          HoldStatus = "active"
	HoldConfirmed       HoldStatus = "confirmed"
	HoldReleased        HoldStatus = "released"
	HoldExpired         HoldStatus = "expired"
	HoldCancelled       HoldStatus = "cancelled"
	DefaultHoldDuration            = 10 * time.Minute
	MaxHoldQuantity                = int64(100)
	MaxHoldRenewals                = int32(1)
)

var (
	ErrNotFound          = errors.New("inventory resource not found")
	ErrInsufficient      = errors.New("insufficient inventory")
	ErrHoldConflict      = errors.New("hold conflicts with existing state")
	ErrInvalidState      = errors.New("hold state is invalid")
	ErrVersionConflict   = errors.New("hold version conflict")
	ErrReplayUnavailable = errors.New("availability replay is unavailable")
)

type Hold struct {
	ID             identifier.ID
	OrganizationID identifier.ID
	SessionID      identifier.ID
	OwnerTokenHash [32]byte
	OwnerUserID    *identifier.ID
	ChannelID      *identifier.ID
	Currency       string
	State          HoldStatus
	ExpiresAt      time.Time
	RenewalCount   int32
	Version        int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type HoldItem struct {
	ID             identifier.ID
	OrganizationID identifier.ID
	HoldID         identifier.ID
	SessionID      identifier.ID
	PoolID         identifier.ID
	SeatID         *identifier.ID
	Quantity       int64
	CreatedAt      time.Time
}

type CreateGAHoldInput struct {
	OrganizationID identifier.ID
	SessionID      identifier.ID
	PoolID         identifier.ID
	ChannelID      *identifier.ID
	OwnerUserID    *identifier.ID
	OwnerTokenHash [32]byte
	Currency       string
	Quantity       int64
}

type CreateGAHoldRecord struct {
	Hold      Hold
	Item      HoldItem
	AuditID   identifier.ID
	OutboxID  identifier.ID
	ActorType string
	ActorID   string
}

type CreateReservedSeatHoldInput struct {
	OrganizationID identifier.ID
	SessionID      identifier.ID
	SeatIDs        []identifier.ID
	ChannelID      *identifier.ID
	OwnerUserID    *identifier.ID
	OwnerTokenHash [32]byte
	Currency       string
}

type CreateReservedSeatHoldRecord struct {
	Hold      Hold
	Items     []HoldItem
	AuditID   identifier.ID
	OutboxID  identifier.ID
	ActorType string
	ActorID   string
}

type ReleaseHoldInput struct {
	OrganizationID identifier.ID
	HoldID         identifier.ID
	OwnerTokenHash [32]byte
}

type ReleaseHoldRecord struct {
	OrganizationID identifier.ID
	HoldID         identifier.ID
	OwnerTokenHash [32]byte
	AuditID        identifier.ID
	OutboxID       identifier.ID
	ActorType      string
	ActorID        string
}

type RenewHoldInput struct {
	OrganizationID  identifier.ID
	HoldID          identifier.ID
	OwnerTokenHash  [32]byte
	ExpectedVersion int64
}

type RenewHoldRecord struct {
	OrganizationID  identifier.ID
	HoldID          identifier.ID
	OwnerTokenHash  [32]byte
	ExpectedVersion int64
	AuditID         identifier.ID
	OutboxID        identifier.ID
	ActorType       string
	ActorID         string
}

type ModifyHoldInput struct {
	OrganizationID  identifier.ID
	HoldID          identifier.ID
	OwnerTokenHash  [32]byte
	ExpectedVersion int64
	PoolID          *identifier.ID
	Quantity        int64
	SeatIDs         []identifier.ID
}

type ModifyHoldRecord struct {
	OrganizationID  identifier.ID
	HoldID          identifier.ID
	OwnerTokenHash  [32]byte
	ExpectedVersion int64
	Items           []HoldItem
	AuditID         identifier.ID
	OutboxID        identifier.ID
	ActorType       string
	ActorID         string
}

type ExpireDueRecord struct {
	Now       time.Time
	Limit     int32
	ActorType string
	ActorID   string
}

type Repository interface {
	CreateGAHold(context.Context, CreateGAHoldRecord) (Hold, error)
	CreateReservedSeatHold(context.Context, CreateReservedSeatHoldRecord) (Hold, error)
	ReleaseHold(context.Context, ReleaseHoldRecord) (Hold, error)
	RenewHold(context.Context, RenewHoldRecord) (Hold, error)
	ModifyHold(context.Context, ModifyHoldRecord) (Hold, error)
}

type ExpirationRepository interface {
	ExpireDue(context.Context, ExpireDueRecord) (int64, error)
}

type SeatAvailability struct {
	ID         identifier.ID
	SeatKey    string
	Label      string
	Category   string
	Sellable   bool
	Wheelchair bool
	State      string
	Version    int64
}

type GAPoolAvailability struct {
	ID               identifier.ID
	Slug             string
	SellableCapacity int64
	SoldQuantity     int64
	HeldQuantity     int64
	KilledQuantity   int64
	CompedQuantity   int64
	Available        int64
	Version          int64
}

type AvailabilitySnapshot struct {
	OrganizationID identifier.ID
	SessionID      identifier.ID
	InventoryMode  string
	SessionStatus  string
	Revision       int64
	ServerTime     time.Time
	Seats          []SeatAvailability
	GAPools        []GAPoolAvailability
}

type AvailabilityEvent struct {
	ID            identifier.ID
	SessionID     identifier.ID
	Sequence      int64
	EventType     string
	SchemaVersion int32
	Payload       []byte
	OccurredAt    time.Time
}

type AvailabilityReplayPage struct {
	Events          []AvailabilityEvent
	CurrentRevision int64
	HasMore         bool
}

type SnapshotRepository interface {
	GetAvailabilitySnapshot(context.Context, identifier.ID, identifier.ID) (AvailabilitySnapshot, error)
}

type ReplayRepository interface {
	ListAvailabilityEvents(context.Context, identifier.ID, identifier.ID, int64, int32) (AvailabilityReplayPage, error)
}

type Service struct {
	repository Repository
	clock      clock.Clock
}

func NewService(repository Repository, timeSource clock.Clock) *Service {
	return &Service{repository: repository, clock: timeSource}
}

func (service *Service) CreateGAHold(ctx context.Context, input CreateGAHoldInput) (Hold, error) {
	authorization, err := access.Require(ctx, access.PermissionOrderCreate)
	if err != nil {
		if errors.Is(err, access.ErrUnauthenticated) {
			return Hold{}, apperror.Wrap(err, apperror.CodeUnauthenticated, "Authentication is required.")
		}
		return Hold{}, apperror.Wrap(err, apperror.CodePermissionDenied, "Inventory holds are not permitted.")
	}
	if input.OrganizationID.IsZero() || authorization.OrganizationID() != input.OrganizationID {
		return Hold{}, apperror.Wrap(ErrNotFound, apperror.CodeNotFound, "The requested inventory does not exist.")
	}
	normalized, err := validate(input)
	if err != nil {
		return Hold{}, err
	}
	holdID, itemID, auditID, outboxID, err := newIDs4()
	if err != nil {
		return Hold{}, apperror.Wrap(err, apperror.CodeInternal, "A hold identifier could not be created.")
	}
	now := service.clock.Now().UTC()
	hold := Hold{ID: holdID, OrganizationID: input.OrganizationID, SessionID: normalized.SessionID, OwnerTokenHash: normalized.OwnerTokenHash, OwnerUserID: normalized.OwnerUserID, ChannelID: normalized.ChannelID, Currency: normalized.Currency, State: HoldActive, ExpiresAt: now.Add(DefaultHoldDuration), Version: 1, CreatedAt: now, UpdatedAt: now}
	item := HoldItem{ID: itemID, OrganizationID: input.OrganizationID, HoldID: holdID, SessionID: normalized.SessionID, PoolID: normalized.PoolID, Quantity: normalized.Quantity, CreatedAt: now}
	created, err := service.repository.CreateGAHold(ctx, CreateGAHoldRecord{Hold: hold, Item: item, AuditID: auditID, OutboxID: outboxID, ActorType: string(authorization.Principal().Type()), ActorID: authorization.Principal().ID()})
	return created, mapError(err)
}

func (service *Service) CreateReservedSeatHold(ctx context.Context, input CreateReservedSeatHoldInput) (Hold, error) {
	authorization, err := access.Require(ctx, access.PermissionOrderCreate)
	if err != nil {
		if errors.Is(err, access.ErrUnauthenticated) {
			return Hold{}, apperror.Wrap(err, apperror.CodeUnauthenticated, "Authentication is required.")
		}
		return Hold{}, apperror.Wrap(err, apperror.CodePermissionDenied, "Inventory holds are not permitted.")
	}
	if input.OrganizationID.IsZero() || authorization.OrganizationID() != input.OrganizationID {
		return Hold{}, apperror.Wrap(ErrNotFound, apperror.CodeNotFound, "The requested inventory does not exist.")
	}
	if err := validateReservedSeatHold(input); err != nil {
		return Hold{}, err
	}
	ids, err := newIDsN(len(input.SeatIDs) + 3)
	if err != nil {
		return Hold{}, apperror.Wrap(err, apperror.CodeInternal, "A reserved-seat hold identifier could not be created.")
	}
	now := service.clock.Now().UTC()
	hold := Hold{ID: ids[0], OrganizationID: input.OrganizationID, SessionID: input.SessionID, OwnerTokenHash: input.OwnerTokenHash, OwnerUserID: input.OwnerUserID, ChannelID: input.ChannelID, Currency: strings.ToUpper(strings.TrimSpace(input.Currency)), State: HoldActive, ExpiresAt: now.Add(DefaultHoldDuration), Version: 1, CreatedAt: now, UpdatedAt: now}
	items := make([]HoldItem, 0, len(input.SeatIDs))
	for index, seatID := range input.SeatIDs {
		seatIDCopy := seatID
		items = append(items, HoldItem{ID: ids[index+3], OrganizationID: input.OrganizationID, HoldID: hold.ID, SessionID: input.SessionID, SeatID: &seatIDCopy, Quantity: 1, CreatedAt: now})
	}
	created, err := service.repository.CreateReservedSeatHold(ctx, CreateReservedSeatHoldRecord{Hold: hold, Items: items, AuditID: ids[1], OutboxID: ids[2], ActorType: string(authorization.Principal().Type()), ActorID: authorization.Principal().ID()})
	return created, mapError(err)
}

func (service *Service) ReleaseHold(ctx context.Context, input ReleaseHoldInput) (Hold, error) {
	authorization, err := access.Require(ctx, access.PermissionOrderCreate)
	if err != nil {
		if errors.Is(err, access.ErrUnauthenticated) {
			return Hold{}, apperror.Wrap(err, apperror.CodeUnauthenticated, "Authentication is required.")
		}
		return Hold{}, apperror.Wrap(err, apperror.CodePermissionDenied, "Inventory holds are not permitted.")
	}
	if input.OrganizationID.IsZero() || input.HoldID.IsZero() || authorization.OrganizationID() != input.OrganizationID || input.OwnerTokenHash == [32]byte{} {
		return Hold{}, apperror.Wrap(ErrNotFound, apperror.CodeNotFound, "The requested hold does not exist.")
	}
	auditID, outboxID, err := newIDs2()
	if err != nil {
		return Hold{}, apperror.Wrap(err, apperror.CodeInternal, "A hold release identifier could not be created.")
	}
	released, err := service.repository.ReleaseHold(ctx, ReleaseHoldRecord{OrganizationID: input.OrganizationID, HoldID: input.HoldID, OwnerTokenHash: input.OwnerTokenHash, AuditID: auditID, OutboxID: outboxID, ActorType: string(authorization.Principal().Type()), ActorID: authorization.Principal().ID()})
	return released, mapError(err)
}

func (service *Service) RenewHold(ctx context.Context, input RenewHoldInput) (Hold, error) {
	authorization, err := access.Require(ctx, access.PermissionOrderCreate)
	if err != nil {
		if errors.Is(err, access.ErrUnauthenticated) {
			return Hold{}, apperror.Wrap(err, apperror.CodeUnauthenticated, "Authentication is required.")
		}
		return Hold{}, apperror.Wrap(err, apperror.CodePermissionDenied, "Inventory holds are not permitted.")
	}
	if input.OrganizationID.IsZero() || input.HoldID.IsZero() || authorization.OrganizationID() != input.OrganizationID || input.OwnerTokenHash == [32]byte{} {
		return Hold{}, apperror.Wrap(ErrNotFound, apperror.CodeNotFound, "The requested hold does not exist.")
	}
	if input.ExpectedVersion < 1 {
		return Hold{}, apperror.New(apperror.CodeValidationFailed, "The hold renewal request is invalid.", apperror.Detail{Field: "expected_version", Code: "required", Message: "A positive hold version is required."})
	}
	auditID, outboxID, err := newIDs2()
	if err != nil {
		return Hold{}, apperror.Wrap(err, apperror.CodeInternal, "A hold renewal identifier could not be created.")
	}
	renewed, err := service.repository.RenewHold(ctx, RenewHoldRecord{OrganizationID: input.OrganizationID, HoldID: input.HoldID, OwnerTokenHash: input.OwnerTokenHash, ExpectedVersion: input.ExpectedVersion, AuditID: auditID, OutboxID: outboxID, ActorType: string(authorization.Principal().Type()), ActorID: authorization.Principal().ID()})
	return renewed, mapError(err)
}

func (service *Service) ModifyHold(ctx context.Context, input ModifyHoldInput) (Hold, error) {
	authorization, err := access.Require(ctx, access.PermissionOrderCreate)
	if err != nil {
		if errors.Is(err, access.ErrUnauthenticated) {
			return Hold{}, apperror.Wrap(err, apperror.CodeUnauthenticated, "Authentication is required.")
		}
		return Hold{}, apperror.Wrap(err, apperror.CodePermissionDenied, "Inventory holds are not permitted.")
	}
	if input.OrganizationID.IsZero() || input.HoldID.IsZero() || authorization.OrganizationID() != input.OrganizationID || input.OwnerTokenHash == [32]byte{} {
		return Hold{}, apperror.Wrap(ErrNotFound, apperror.CodeNotFound, "The requested hold does not exist.")
	}
	if input.ExpectedVersion < 1 {
		return Hold{}, apperror.New(apperror.CodeValidationFailed, "The hold modification request is invalid.", apperror.Detail{Field: "expected_version", Code: "required", Message: "A positive hold version is required."})
	}
	if (input.PoolID == nil) == (len(input.SeatIDs) == 0) {
		return Hold{}, apperror.New(apperror.CodeValidationFailed, "The hold modification request is invalid.", apperror.Detail{Field: "inventory", Code: "one_mode_required", Message: "Specify one general-admission pool or one or more seats."})
	}
	items := make([]HoldItem, 0, len(input.SeatIDs)+1)
	if input.PoolID != nil {
		if input.PoolID.IsZero() || input.Quantity < 1 || input.Quantity > MaxHoldQuantity {
			return Hold{}, apperror.New(apperror.CodeValidationFailed, "The hold modification request is invalid.", apperror.Detail{Field: "quantity", Code: "out_of_range", Message: fmt.Sprintf("Hold quantity must be between 1 and %d.", MaxHoldQuantity)})
		}
		itemID, err := identifier.New()
		if err != nil {
			return Hold{}, apperror.Wrap(err, apperror.CodeInternal, "A hold modification identifier could not be created.")
		}
		poolID := *input.PoolID
		items = append(items, HoldItem{ID: itemID, OrganizationID: input.OrganizationID, HoldID: input.HoldID, PoolID: poolID, Quantity: input.Quantity})
	} else {
		if len(input.SeatIDs) > int(MaxHoldQuantity) {
			return Hold{}, apperror.New(apperror.CodeValidationFailed, "The hold modification request is invalid.", apperror.Detail{Field: "seat_ids", Code: "out_of_range", Message: fmt.Sprintf("A hold may contain at most %d seats.", MaxHoldQuantity)})
		}
		seen := make(map[identifier.ID]struct{}, len(input.SeatIDs))
		for _, seatID := range input.SeatIDs {
			if seatID.IsZero() {
				return Hold{}, apperror.New(apperror.CodeValidationFailed, "The hold modification request is invalid.", apperror.Detail{Field: "seat_ids", Code: "invalid", Message: "Every seat identifier must be valid."})
			}
			if _, exists := seen[seatID]; exists {
				return Hold{}, apperror.New(apperror.CodeValidationFailed, "The hold modification request is invalid.", apperror.Detail{Field: "seat_ids", Code: "duplicate", Message: "A seat may appear only once in a hold."})
			}
			seen[seatID] = struct{}{}
			itemID, err := identifier.New()
			if err != nil {
				return Hold{}, apperror.Wrap(err, apperror.CodeInternal, "A hold modification identifier could not be created.")
			}
			seatIDCopy := seatID
			items = append(items, HoldItem{ID: itemID, OrganizationID: input.OrganizationID, HoldID: input.HoldID, SeatID: &seatIDCopy, Quantity: 1})
		}
	}
	auditID, outboxID, err := newIDs2()
	if err != nil {
		return Hold{}, apperror.Wrap(err, apperror.CodeInternal, "A hold modification identifier could not be created.")
	}
	modified, err := service.repository.ModifyHold(ctx, ModifyHoldRecord{OrganizationID: input.OrganizationID, HoldID: input.HoldID, OwnerTokenHash: input.OwnerTokenHash, ExpectedVersion: input.ExpectedVersion, Items: items, AuditID: auditID, OutboxID: outboxID, ActorType: string(authorization.Principal().Type()), ActorID: authorization.Principal().ID()})
	return modified, mapError(err)
}

func (service *Service) GetAvailabilitySnapshot(ctx context.Context, organizationID, sessionID identifier.ID) (AvailabilitySnapshot, error) {
	authorization, err := service.requireReadOrganization(ctx, organizationID)
	if err != nil {
		return AvailabilitySnapshot{}, err
	}
	repository, ok := service.repository.(SnapshotRepository)
	if !ok {
		return AvailabilitySnapshot{}, errors.New("inventory repository does not support availability snapshots")
	}
	snapshot, err := repository.GetAvailabilitySnapshot(ctx, authorization.OrganizationID(), sessionID)
	if errors.Is(err, ErrNotFound) {
		return AvailabilitySnapshot{}, apperror.Wrap(err, apperror.CodeNotFound, "The requested session inventory does not exist.")
	}
	return snapshot, err
}

func (service *Service) ListAvailabilityEvents(ctx context.Context, organizationID, sessionID identifier.ID, after int64, limit int32) (AvailabilityReplayPage, error) {
	authorization, err := service.requireReadOrganization(ctx, organizationID)
	if err != nil {
		return AvailabilityReplayPage{}, err
	}
	if after < 0 {
		return AvailabilityReplayPage{}, apperror.New(apperror.CodeInvalidCursor, "The availability replay cursor is invalid.")
	}
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 100 {
		return AvailabilityReplayPage{}, apperror.New(apperror.CodeValidationFailed, "The availability replay request is invalid.", apperror.Detail{Field: "limit", Code: "out_of_range", Message: "Use a limit between 1 and 100."})
	}
	repository, ok := service.repository.(ReplayRepository)
	if !ok {
		return AvailabilityReplayPage{}, errors.New("inventory repository does not support availability replay")
	}
	page, err := repository.ListAvailabilityEvents(ctx, authorization.OrganizationID(), sessionID, after, limit)
	if errors.Is(err, ErrNotFound) {
		return AvailabilityReplayPage{}, apperror.Wrap(err, apperror.CodeNotFound, "The requested session inventory does not exist.")
	}
	if errors.Is(err, ErrReplayUnavailable) {
		return AvailabilityReplayPage{}, apperror.Wrap(err, apperror.CodeConflict, "Availability replay is no longer available; fetch a fresh snapshot.")
	}
	return page, err
}

func (service *Service) ExpireDue(ctx context.Context, limit int32) (int64, error) {
	repository, ok := service.repository.(ExpirationRepository)
	if !ok {
		return 0, errors.New("inventory repository does not support hold expiration")
	}
	if limit < 1 || limit > 100 {
		return 0, fmt.Errorf("hold expiration limit must be between 1 and 100")
	}
	return repository.ExpireDue(ctx, ExpireDueRecord{
		Now:       service.clock.Now().UTC(),
		Limit:     limit,
		ActorType: "system",
		ActorID:   "hold-expiry-worker",
	})
}

func (service *Service) requireReadOrganization(ctx context.Context, organizationID identifier.ID) (access.Authorization, error) {
	authorization, err := access.Require(ctx, access.PermissionCatalogRead)
	if err != nil {
		if errors.Is(err, access.ErrUnauthenticated) {
			return access.Authorization{}, apperror.Wrap(err, apperror.CodeUnauthenticated, "Authentication is required.")
		}
		return access.Authorization{}, apperror.Wrap(err, apperror.CodePermissionDenied, "Inventory access is not permitted.")
	}
	if organizationID.IsZero() || authorization.OrganizationID() != organizationID {
		return access.Authorization{}, apperror.Wrap(ErrNotFound, apperror.CodeNotFound, "The requested session inventory does not exist.")
	}
	return authorization, nil
}

func validate(input CreateGAHoldInput) (CreateGAHoldInput, error) {
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
	collector := validation.Collector{}
	if input.SessionID.IsZero() {
		collector.Add("session_id", "required", "A session identifier is required.")
	}
	if input.PoolID.IsZero() {
		collector.Add("pool_id", "required", "A general-admission pool identifier is required.")
	}
	if input.OwnerTokenHash == [32]byte{} {
		collector.Add("owner_token_hash", "required", "A hashed hold owner token is required.")
	}
	if _, err := money.NewCurrency(input.Currency); err != nil {
		collector.Add("currency", "invalid", "Use three uppercase ISO-4217 currency letters.")
	}
	if input.Quantity < 1 || input.Quantity > MaxHoldQuantity {
		collector.Add("quantity", "out_of_range", fmt.Sprintf("Hold quantity must be between 1 and %d.", MaxHoldQuantity))
	}
	if err := collector.Error("The hold request is invalid."); err != nil {
		return CreateGAHoldInput{}, err
	}
	return input, nil
}

func validateReservedSeatHold(input CreateReservedSeatHoldInput) error {
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
	collector := validation.Collector{}
	if input.SessionID.IsZero() {
		collector.Add("session_id", "required", "A session identifier is required.")
	}
	if len(input.SeatIDs) < 1 || len(input.SeatIDs) > int(MaxHoldQuantity) {
		collector.Add("seat_ids", "out_of_range", fmt.Sprintf("A reserved-seat hold must contain between 1 and %d seats.", MaxHoldQuantity))
	}
	if input.OwnerTokenHash == [32]byte{} {
		collector.Add("owner_token_hash", "required", "A hashed hold owner token is required.")
	}
	if _, err := money.NewCurrency(input.Currency); err != nil {
		collector.Add("currency", "invalid", "Use three uppercase ISO-4217 currency letters.")
	}
	seen := make(map[identifier.ID]struct{}, len(input.SeatIDs))
	for _, seatID := range input.SeatIDs {
		if seatID.IsZero() {
			collector.Add("seat_ids", "invalid", "Every seat identifier must be valid.")
			continue
		}
		if _, exists := seen[seatID]; exists {
			collector.Add("seat_ids", "duplicate", "A seat may appear only once in a hold.")
			continue
		}
		seen[seatID] = struct{}{}
	}
	return collector.Error("The reserved-seat hold request is invalid.")
}

func mapError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotFound):
		return apperror.Wrap(err, apperror.CodeNotFound, "The requested inventory does not exist.")
	case errors.Is(err, ErrInsufficient):
		return apperror.Wrap(err, apperror.CodeConflict, "The requested inventory is no longer available.")
	case errors.Is(err, ErrHoldConflict), errors.Is(err, ErrInvalidState):
		return apperror.Wrap(err, apperror.CodeConflict, "The hold cannot be created in the current inventory state.")
	case errors.Is(err, ErrVersionConflict):
		return apperror.Wrap(err, apperror.CodePreconditionFailed, "The hold changed; reload it and retry.")
	default:
		return err
	}
}

func newIDs4() (identifier.ID, identifier.ID, identifier.ID, identifier.ID, error) {
	first, err := identifier.New()
	if err != nil {
		return identifier.ID{}, identifier.ID{}, identifier.ID{}, identifier.ID{}, err
	}
	second, err := identifier.New()
	if err != nil {
		return identifier.ID{}, identifier.ID{}, identifier.ID{}, identifier.ID{}, err
	}
	third, err := identifier.New()
	if err != nil {
		return identifier.ID{}, identifier.ID{}, identifier.ID{}, identifier.ID{}, err
	}
	fourth, err := identifier.New()
	if err != nil {
		return identifier.ID{}, identifier.ID{}, identifier.ID{}, identifier.ID{}, err
	}
	return first, second, third, fourth, nil
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

func newIDsN(count int) ([]identifier.ID, error) {
	if count < 1 {
		return nil, errors.New("a positive identifier count is required")
	}
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
