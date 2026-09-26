// Package pricing owns session-scoped commercial price tiers.
package pricing

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
	"github.com/HK9750/venueos/internal/platform/money"
	"github.com/HK9750/venueos/internal/platform/validation"
)

type Status string

const (
	StatusDraft    Status = "draft"
	StatusActive   Status = "active"
	StatusInactive Status = "inactive"
	StatusArchived Status = "archived"
	DefaultLimit          = int32(50)
	MaxLimit              = int32(100)
)

var (
	ErrNotFound         = errors.New("price tier not found")
	ErrSlugConflict     = errors.New("price tier slug already exists")
	ErrCurrencyConflict = errors.New("price tier currency conflicts with session currency")
	ErrInvalidState     = errors.New("price tier session is not schedulable")
	priceSlugPattern    = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
)

type PriceTier struct {
	ID              identifier.ID
	OrganizationID  identifier.ID
	SessionID       identifier.ID
	Slug            string
	DisplayName     string
	Currency        string
	AmountMinor     int64
	MinimumQuantity int32
	MaximumQuantity int32
	SalesStartAt    *time.Time
	SalesEndAt      *time.Time
	Status          Status
	Version         int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type Cursor struct {
	OrganizationID identifier.ID
	SessionID      identifier.ID
	CreatedAt      time.Time
	ID             identifier.ID
}

type Page struct {
	Items      []PriceTier
	NextCursor *Cursor
}

type CreateInput struct {
	OrganizationID  identifier.ID
	EventID         identifier.ID
	SessionID       identifier.ID
	Slug            string
	DisplayName     string
	Currency        string
	AmountMinor     int64
	MinimumQuantity int32
	MaximumQuantity int32
	SalesStartAt    *time.Time
	SalesEndAt      *time.Time
}

type CreateRecord struct {
	Tier      PriceTier
	EventID   identifier.ID
	AuditID   identifier.ID
	OutboxID  identifier.ID
	ActorType string
	ActorID   string
}

type Repository interface {
	Create(context.Context, CreateRecord) (PriceTier, error)
	List(context.Context, identifier.ID, identifier.ID, int32, *Cursor) (Page, error)
}

type Service struct {
	repository Repository
	clock      clock.Clock
}

func NewService(repository Repository, timeSource clock.Clock) *Service {
	return &Service{repository: repository, clock: timeSource}
}

func (service *Service) Create(ctx context.Context, input CreateInput) (PriceTier, error) {
	authorization, err := service.requireOrganization(ctx, input.OrganizationID, access.PermissionCatalogManage)
	if err != nil {
		return PriceTier{}, err
	}
	normalized, err := validate(input)
	if err != nil {
		return PriceTier{}, err
	}
	tierID, auditID, outboxID, err := newIDs3()
	if err != nil {
		return PriceTier{}, apperror.Wrap(err, apperror.CodeInternal, "A price tier identifier could not be created.")
	}
	now := service.clock.Now().UTC()
	created, err := service.repository.Create(ctx, CreateRecord{Tier: PriceTier{ID: tierID, OrganizationID: authorization.OrganizationID(), SessionID: normalized.SessionID, Slug: normalized.Slug, DisplayName: normalized.DisplayName, Currency: normalized.Currency, AmountMinor: normalized.AmountMinor, MinimumQuantity: normalized.MinimumQuantity, MaximumQuantity: normalized.MaximumQuantity, SalesStartAt: normalized.SalesStartAt, SalesEndAt: normalized.SalesEndAt, Status: StatusDraft, Version: 1, CreatedAt: now, UpdatedAt: now}, EventID: normalized.EventID, AuditID: auditID, OutboxID: outboxID, ActorType: actorType(authorization.Principal().Type()), ActorID: authorization.Principal().ID()})
	return created, mapError(err)
}

func (service *Service) List(ctx context.Context, organizationID, sessionID identifier.ID, limit int32, after *Cursor) (Page, error) {
	authorization, err := service.requireOrganization(ctx, organizationID, access.PermissionCatalogRead)
	if err != nil {
		return Page{}, err
	}
	if limit == 0 {
		limit = DefaultLimit
	}
	if limit < 1 || limit > MaxLimit {
		return Page{}, apperror.New(apperror.CodeValidationFailed, "The price tier list request is invalid.", apperror.Detail{Field: "limit", Code: "out_of_range", Message: fmt.Sprintf("Use a limit between 1 and %d.", MaxLimit)})
	}
	if after != nil && (after.OrganizationID != authorization.OrganizationID() || after.SessionID != sessionID) {
		return Page{}, apperror.New(apperror.CodeInvalidCursor, "The price tier cursor is invalid.")
	}
	return service.repository.List(ctx, authorization.OrganizationID(), sessionID, limit, after)
}

func validate(input CreateInput) (CreateInput, error) {
	input.Slug = strings.ToLower(strings.TrimSpace(input.Slug))
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
	if input.MinimumQuantity == 0 {
		input.MinimumQuantity = 1
	}
	if input.MaximumQuantity == 0 {
		input.MaximumQuantity = 10
	}
	collector := validation.Collector{}
	if input.SessionID.IsZero() {
		collector.Add("session_id", "required", "A session identifier is required.")
	}
	if len(input.Slug) < 2 || len(input.Slug) > 63 || !priceSlugPattern.MatchString(input.Slug) {
		collector.Add("slug", "invalid", "Use 2 to 63 lowercase letters, digits, or single hyphens.")
	}
	if input.DisplayName == "" || len(input.DisplayName) > 160 {
		collector.Add("display_name", "invalid", "Use 1 to 160 visible characters.")
	}
	if _, err := money.NewCurrency(input.Currency); err != nil {
		collector.Add("currency", "invalid", "Use three uppercase ISO-4217 currency letters.")
	}
	if input.AmountMinor < 0 {
		collector.Add("amount_minor", "out_of_range", "Amount must be zero or greater in minor currency units.")
	}
	if input.MinimumQuantity < 1 || input.MaximumQuantity < input.MinimumQuantity || input.MaximumQuantity > 1000 {
		collector.Add("maximum_quantity", "out_of_range", "Quantity bounds must be positive, ordered, and no greater than 1000.")
	}
	if (input.SalesStartAt == nil) != (input.SalesEndAt == nil) {
		collector.Add("sales_start_at", "invalid", "Sales start and end must be supplied together.")
	} else if input.SalesStartAt != nil && !input.SalesEndAt.After(*input.SalesStartAt) {
		collector.Add("sales_end_at", "invalid", "The sales window must end after it starts.")
	}
	if err := collector.Error("The price tier request is invalid."); err != nil {
		return CreateInput{}, err
	}
	if input.SalesStartAt != nil {
		start := input.SalesStartAt.UTC()
		end := input.SalesEndAt.UTC()
		input.SalesStartAt = &start
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
		return access.Authorization{}, apperror.Wrap(err, apperror.CodePermissionDenied, "Pricing access is not permitted.")
	}
	if organizationID.IsZero() || authorization.OrganizationID() != organizationID {
		return access.Authorization{}, apperror.Wrap(ErrNotFound, apperror.CodeNotFound, "The requested price tier does not exist.")
	}
	return authorization, nil
}

func mapError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotFound):
		return apperror.Wrap(err, apperror.CodeNotFound, "The requested session or price tier does not exist.")
	case errors.Is(err, ErrSlugConflict), errors.Is(err, ErrCurrencyConflict):
		return apperror.Wrap(err, apperror.CodeConflict, "The price tier conflicts with an existing session price configuration.")
	case errors.Is(err, ErrInvalidState):
		return apperror.Wrap(err, apperror.CodeConflict, "The session cannot accept price tiers in its current state.")
	default:
		return err
	}
}

func EncodeCursor(cursor Cursor) (string, error) {
	if cursor.OrganizationID.IsZero() || cursor.SessionID.IsZero() || cursor.ID.IsZero() || cursor.CreatedAt.IsZero() {
		return "", errors.New("cursor fields are required")
	}
	body, err := json.Marshal(struct {
		Version   int    `json:"v"`
		OrgID     string `json:"organization_id"`
		SessionID string `json:"session_id"`
		Created   string `json:"created_at"`
		ID        string `json:"id"`
	}{1, cursor.OrganizationID.String(), cursor.SessionID.String(), cursor.CreatedAt.UTC().Format(time.RFC3339Nano), cursor.ID.String()})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(body), nil
}

func DecodeCursor(value string) (Cursor, error) {
	if len(value) == 0 || len(value) > 512 {
		return Cursor{}, apperror.New(apperror.CodeInvalidCursor, "The price tier cursor is invalid.")
	}
	var payload struct {
		Version   int    `json:"v"`
		OrgID     string `json:"organization_id"`
		SessionID string `json:"session_id"`
		Created   string `json:"created_at"`
		ID        string `json:"id"`
	}
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(body, &payload) != nil || payload.Version != 1 {
		return Cursor{}, apperror.New(apperror.CodeInvalidCursor, "The price tier cursor is invalid.")
	}
	orgID, err := identifier.Parse(payload.OrgID)
	if err != nil {
		return Cursor{}, apperror.New(apperror.CodeInvalidCursor, "The price tier cursor is invalid.")
	}
	sessionID, err := identifier.Parse(payload.SessionID)
	if err != nil {
		return Cursor{}, apperror.New(apperror.CodeInvalidCursor, "The price tier cursor is invalid.")
	}
	id, err := identifier.Parse(payload.ID)
	if err != nil {
		return Cursor{}, apperror.New(apperror.CodeInvalidCursor, "The price tier cursor is invalid.")
	}
	createdAt, err := time.Parse(time.RFC3339Nano, payload.Created)
	if err != nil {
		return Cursor{}, apperror.New(apperror.CodeInvalidCursor, "The price tier cursor is invalid.")
	}
	return Cursor{OrganizationID: orgID, SessionID: sessionID, CreatedAt: createdAt.UTC(), ID: id}, nil
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
