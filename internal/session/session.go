// Package session owns scheduled event occurrences and their immutable catalog references.
package session

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/HK9750/venueos/internal/access"
	"github.com/HK9750/venueos/internal/platform/apperror"
	"github.com/HK9750/venueos/internal/platform/clock"
	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/internal/platform/validation"
)

type Status string

const (
	StatusDraft       Status = "draft"
	StatusScheduled   Status = "scheduled"
	StatusOnSale      Status = "on_sale"
	StatusSalesClosed Status = "sales_closed"
	StatusInProgress  Status = "in_progress"
	StatusCompleted   Status = "completed"
	StatusCancelled   Status = "cancelled"
)

type InventoryMode string

const (
	InventoryAssigned InventoryMode = "assigned_seating"
	InventoryGA       InventoryMode = "general_admission"
	DefaultLimit                    = int32(50)
	MaxLimit                        = int32(100)
)

var (
	ErrNotFound        = errors.New("session not found")
	ErrVersionConflict = errors.New("session version conflict")
	ErrInvalidState    = errors.New("session state is invalid")
	ErrOverlap         = errors.New("session overlaps another scheduled session")
)

type Session struct {
	ID                identifier.ID
	OrganizationID    identifier.ID
	EventID           identifier.ID
	EventRevision     int64
	VenueID           identifier.ID
	SpaceID           identifier.ID
	SeatMapVersionID  *identifier.ID
	InventoryMode     InventoryMode
	DoorsAt           *time.Time
	StartsAt          time.Time
	EndsAt            time.Time
	SalesStartAt      *time.Time
	SalesEndAt        *time.Time
	Timezone          string
	Status            Status
	Version           int64
	InventoryRevision int64
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type Cursor struct {
	OrganizationID identifier.ID
	EventID        identifier.ID
	CreatedAt      time.Time
	ID             identifier.ID
}

type Page struct {
	Items      []Session
	NextCursor *Cursor
}

type CreateInput struct {
	OrganizationID   identifier.ID
	EventID          identifier.ID
	EventRevision    int64
	VenueID          identifier.ID
	SpaceID          identifier.ID
	SeatMapVersionID *identifier.ID
	DoorsAt          *time.Time
	StartsAt         time.Time
	EndsAt           time.Time
	SalesStartAt     *time.Time
	SalesEndAt       *time.Time
	Timezone         string
}

type CreateRecord struct {
	Session   Session
	AuditID   identifier.ID
	OutboxID  identifier.ID
	ActorType string
	ActorID   string
}

type Repository interface {
	Create(context.Context, CreateRecord) (Session, error)
	Get(context.Context, identifier.ID, identifier.ID, identifier.ID) (Session, error)
	List(context.Context, identifier.ID, identifier.ID, int32, *Cursor) (Page, error)
}

type Service struct {
	repository Repository
	clock      clock.Clock
}

func NewService(repository Repository, timeSource clock.Clock) *Service {
	return &Service{repository: repository, clock: timeSource}
}

func (service *Service) Create(ctx context.Context, input CreateInput) (Session, error) {
	authorization, err := service.requireOrganization(ctx, input.OrganizationID, access.PermissionCatalogManage)
	if err != nil {
		return Session{}, err
	}
	normalized, err := validate(input)
	if err != nil {
		return Session{}, err
	}
	sessionID, auditID, outboxID, err := newIDs3()
	if err != nil {
		return Session{}, apperror.Wrap(err, apperror.CodeInternal, "A session identifier could not be created.")
	}
	now := service.clock.Now().UTC()
	created, err := service.repository.Create(ctx, CreateRecord{Session: Session{ID: sessionID, OrganizationID: authorization.OrganizationID(), EventID: normalized.EventID, EventRevision: normalized.EventRevision, VenueID: normalized.VenueID, SpaceID: normalized.SpaceID, SeatMapVersionID: normalized.SeatMapVersionID, DoorsAt: normalized.DoorsAt, StartsAt: normalized.StartsAt, EndsAt: normalized.EndsAt, SalesStartAt: normalized.SalesStartAt, SalesEndAt: normalized.SalesEndAt, Timezone: normalized.Timezone, Status: StatusScheduled, Version: 1, CreatedAt: now, UpdatedAt: now}, AuditID: auditID, OutboxID: outboxID, ActorType: actorType(authorization.Principal().Type()), ActorID: authorization.Principal().ID()})
	return created, mapError(err)
}

func (service *Service) Get(ctx context.Context, organizationID, eventID, sessionID identifier.ID) (Session, error) {
	authorization, err := service.requireOrganization(ctx, organizationID, access.PermissionCatalogRead)
	if err != nil {
		return Session{}, err
	}
	found, err := service.repository.Get(ctx, authorization.OrganizationID(), eventID, sessionID)
	if errors.Is(err, ErrNotFound) {
		return Session{}, apperror.Wrap(err, apperror.CodeNotFound, "The requested session does not exist.")
	}
	return found, err
}

func (service *Service) List(ctx context.Context, organizationID, eventID identifier.ID, limit int32, after *Cursor) (Page, error) {
	authorization, err := service.requireOrganization(ctx, organizationID, access.PermissionCatalogRead)
	if err != nil {
		return Page{}, err
	}
	if limit == 0 {
		limit = DefaultLimit
	}
	if limit < 1 || limit > MaxLimit {
		return Page{}, apperror.New(apperror.CodeValidationFailed, "The session list request is invalid.", apperror.Detail{Field: "limit", Code: "out_of_range", Message: fmt.Sprintf("Use a limit between 1 and %d.", MaxLimit)})
	}
	if after != nil && (after.OrganizationID != authorization.OrganizationID() || after.EventID != eventID) {
		return Page{}, apperror.New(apperror.CodeInvalidCursor, "The session cursor is invalid.")
	}
	return service.repository.List(ctx, authorization.OrganizationID(), eventID, limit, after)
}

func validate(input CreateInput) (CreateInput, error) {
	input.Timezone = strings.TrimSpace(input.Timezone)
	collector := validation.Collector{}
	if input.EventID.IsZero() {
		collector.Add("event_id", "required", "An event identifier is required.")
	}
	if input.EventRevision < 1 {
		collector.Add("event_revision", "invalid", "A published event revision is required.")
	}
	if input.VenueID.IsZero() {
		collector.Add("venue_id", "required", "A venue identifier is required.")
	}
	if input.SpaceID.IsZero() {
		collector.Add("space_id", "required", "A space identifier is required.")
	}
	if input.StartsAt.IsZero() || input.EndsAt.IsZero() || !input.EndsAt.After(input.StartsAt) {
		collector.Add("starts_at", "invalid", "The session must end after it starts.")
	}
	if input.DoorsAt != nil && input.DoorsAt.After(input.StartsAt) {
		collector.Add("doors_at", "invalid", "Doors must open at or before the session start.")
	}
	if (input.SalesStartAt == nil) != (input.SalesEndAt == nil) {
		collector.Add("sales_start_at", "invalid", "Sales start and end must be supplied together.")
	} else if input.SalesStartAt != nil && (!input.SalesEndAt.After(*input.SalesStartAt) || input.SalesEndAt.After(input.StartsAt)) {
		collector.Add("sales_end_at", "invalid", "The sales window must end after it starts and no later than the session start.")
	}
	if len(input.Timezone) == 0 || len(input.Timezone) > 64 {
		collector.Add("timezone", "invalid", "Use a valid IANA timezone identifier.")
	} else if _, err := time.LoadLocation(input.Timezone); err != nil {
		collector.Add("timezone", "invalid", "Use a valid IANA timezone identifier.")
	}
	if err := collector.Error("The session request is invalid."); err != nil {
		return CreateInput{}, err
	}
	input.StartsAt = input.StartsAt.UTC()
	input.EndsAt = input.EndsAt.UTC()
	if input.DoorsAt != nil {
		value := input.DoorsAt.UTC()
		input.DoorsAt = &value
	}
	if input.SalesStartAt != nil {
		value := input.SalesStartAt.UTC()
		input.SalesStartAt = &value
		end := input.SalesEndAt.UTC()
		input.SalesEndAt = &end
	}
	return input, nil
}

func (service *Service) requireOrganization(ctx context.Context, organizationID identifier.ID, permission access.Permission) (access.Authorization, error) {
	authorization, err := access.Require(ctx, permission)
	if err != nil {
		if errors.Is(err, access.ErrUnauthenticated) {
			return access.Authorization{}, apperror.Wrap(err, apperror.CodeUnauthenticated, "Authentication is required.")
		}
		return access.Authorization{}, apperror.Wrap(err, apperror.CodePermissionDenied, "Session access is not permitted.")
	}
	if organizationID.IsZero() || authorization.OrganizationID() != organizationID {
		return access.Authorization{}, apperror.Wrap(ErrNotFound, apperror.CodeNotFound, "The requested session does not exist.")
	}
	return authorization, nil
}

func mapError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotFound):
		return apperror.Wrap(err, apperror.CodeNotFound, "The requested session does not exist.")
	case errors.Is(err, ErrOverlap):
		return apperror.Wrap(err, apperror.CodeConflict, "The session overlaps another scheduled session in this space.")
	case errors.Is(err, ErrInvalidState):
		return apperror.Wrap(err, apperror.CodeConflict, "The session references a resource that is not schedulable.")
	default:
		return err
	}
}

func EncodeCursor(cursor Cursor) (string, error) {
	if cursor.OrganizationID.IsZero() || cursor.EventID.IsZero() || cursor.ID.IsZero() || cursor.CreatedAt.IsZero() {
		return "", errors.New("cursor fields are required")
	}
	body, err := json.Marshal(struct {
		Version int    `json:"v"`
		OrgID   string `json:"organization_id"`
		EventID string `json:"event_id"`
		Created string `json:"created_at"`
		ID      string `json:"id"`
	}{1, cursor.OrganizationID.String(), cursor.EventID.String(), cursor.CreatedAt.UTC().Format(time.RFC3339Nano), cursor.ID.String()})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(body), nil
}

func DecodeCursor(value string) (Cursor, error) {
	if len(value) == 0 || len(value) > 512 {
		return Cursor{}, apperror.New(apperror.CodeInvalidCursor, "The session cursor is invalid.")
	}
	var payload struct {
		Version int    `json:"v"`
		OrgID   string `json:"organization_id"`
		EventID string `json:"event_id"`
		Created string `json:"created_at"`
		ID      string `json:"id"`
	}
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(body, &payload) != nil || payload.Version != 1 {
		return Cursor{}, apperror.New(apperror.CodeInvalidCursor, "The session cursor is invalid.")
	}
	orgID, err := identifier.Parse(payload.OrgID)
	if err != nil {
		return Cursor{}, apperror.New(apperror.CodeInvalidCursor, "The session cursor is invalid.")
	}
	eventID, err := identifier.Parse(payload.EventID)
	if err != nil {
		return Cursor{}, apperror.New(apperror.CodeInvalidCursor, "The session cursor is invalid.")
	}
	id, err := identifier.Parse(payload.ID)
	if err != nil {
		return Cursor{}, apperror.New(apperror.CodeInvalidCursor, "The session cursor is invalid.")
	}
	createdAt, err := time.Parse(time.RFC3339Nano, payload.Created)
	if err != nil {
		return Cursor{}, apperror.New(apperror.CodeInvalidCursor, "The session cursor is invalid.")
	}
	return Cursor{OrganizationID: orgID, EventID: eventID, CreatedAt: createdAt.UTC(), ID: id}, nil
}

func actorType(value access.PrincipalType) string {
	if value == access.PrincipalPlatform {
		return "platform"
	}
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
