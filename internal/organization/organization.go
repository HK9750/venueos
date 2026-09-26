// Package organization contains organization lifecycle rules and use cases.
package organization

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/HK9750/venueos/internal/access"
	"github.com/HK9750/venueos/internal/platform/apperror"
	"github.com/HK9750/venueos/internal/platform/clock"
	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/internal/platform/money"
	"github.com/HK9750/venueos/internal/platform/validation"
)

var (
	ErrNotFound        = errors.New("organization not found")
	ErrSlugConflict    = errors.New("organization slug already exists")
	ErrKeyReused       = errors.New("idempotency key reused")
	slugPattern        = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	localePattern      = regexp.MustCompile(`^[A-Za-z]{2,8}(?:-[A-Za-z0-9]{1,8})*$`)
	idempotencyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]+$`)
)

type Organization struct {
	ID              identifier.ID
	Slug            string
	DisplayName     string
	Status          string
	DefaultLocale   string
	DefaultTimezone string
	DefaultCurrency money.Currency
	SettingsVersion int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type CreateInput struct {
	Slug            string
	DisplayName     string
	DefaultLocale   string
	DefaultTimezone string
	DefaultCurrency string
	IdempotencyKey  string
}

type CreateRecord struct {
	Organization      Organization
	IdempotencyID     identifier.ID
	AuditID           identifier.ID
	OutboxID          identifier.ID
	PrincipalType     string
	PrincipalID       string
	IdempotencyKey    string
	Fingerprint       [32]byte
	IdempotencyExpiry time.Time
}

type Repository interface {
	Create(context.Context, CreateRecord) (Organization, bool, error)
	Get(context.Context, identifier.ID) (Organization, error)
}

type Service struct {
	repository Repository
	clock      clock.Clock
}

func NewService(repository Repository, timeSource clock.Clock) *Service {
	return &Service{repository: repository, clock: timeSource}
}

func (service *Service) Create(ctx context.Context, input CreateInput) (Organization, bool, error) {
	authorization, err := access.RequirePlatform(ctx, access.PermissionPlatformSupport)
	if err != nil {
		return Organization{}, false, accessError(err, "Platform onboarding permission is required.")
	}
	normalized, currency, validationErr := validateCreate(input)
	if validationErr != nil {
		return Organization{}, false, validationErr
	}
	organizationID, idempotencyID, auditID, outboxID, err := newIDs()
	if err != nil {
		return Organization{}, false, apperror.Wrap(err, apperror.CodeInternal, "An organization identifier could not be created.")
	}
	now := service.clock.Now().UTC()
	fingerprintBody, _ := json.Marshal(normalized)
	created, replayed, err := service.repository.Create(ctx, CreateRecord{
		Organization: Organization{
			ID: organizationID, Slug: normalized.Slug, DisplayName: normalized.DisplayName,
			Status: "active", DefaultLocale: normalized.DefaultLocale,
			DefaultTimezone: normalized.DefaultTimezone, DefaultCurrency: currency,
			SettingsVersion: 1, CreatedAt: now, UpdatedAt: now,
		},
		IdempotencyID: idempotencyID, AuditID: auditID, OutboxID: outboxID,
		PrincipalType: string(authorization.Principal().Type()), PrincipalID: authorization.Principal().ID(),
		IdempotencyKey: normalized.IdempotencyKey, Fingerprint: sha256.Sum256(fingerprintBody),
		IdempotencyExpiry: now.Add(72 * time.Hour),
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrSlugConflict):
			return Organization{}, false, apperror.Wrap(err, apperror.CodeConflict, "An organization with this slug already exists.")
		case errors.Is(err, ErrKeyReused):
			return Organization{}, false, apperror.Wrap(err, apperror.CodeIdempotencyKeyReused, "The idempotency key was already used for a different request.")
		default:
			return Organization{}, false, err
		}
	}
	return created, replayed, nil
}

func (service *Service) Get(ctx context.Context, id identifier.ID) (Organization, error) {
	authorization, err := access.Require(ctx, access.PermissionOrganizationRead)
	if err != nil {
		return Organization{}, accessError(err, "Organization read permission is required.")
	}
	if id.IsZero() || authorization.OrganizationID() != id {
		return Organization{}, apperror.Wrap(ErrNotFound, apperror.CodeNotFound, "The requested organization does not exist.")
	}
	found, err := service.repository.Get(ctx, authorization.OrganizationID())
	if errors.Is(err, ErrNotFound) {
		return Organization{}, apperror.Wrap(err, apperror.CodeNotFound, "The requested organization does not exist.")
	}
	return found, err
}

func accessError(err error, deniedMessage string) error {
	if errors.Is(err, access.ErrUnauthenticated) {
		return apperror.Wrap(err, apperror.CodeUnauthenticated, "Authentication is required.")
	}
	return apperror.Wrap(err, apperror.CodePermissionDenied, deniedMessage)
}

func validateCreate(input CreateInput) (CreateInput, money.Currency, error) {
	input.Slug = strings.ToLower(strings.TrimSpace(input.Slug))
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.DefaultLocale = strings.TrimSpace(input.DefaultLocale)
	input.DefaultTimezone = strings.TrimSpace(input.DefaultTimezone)
	input.DefaultCurrency = strings.TrimSpace(input.DefaultCurrency)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	var collector validation.Collector
	if len(input.Slug) < 3 || len(input.Slug) > 63 || !slugPattern.MatchString(input.Slug) {
		collector.Add("slug", "invalid", "Use 3 to 63 lowercase letters, digits, or single hyphens.")
	}
	if input.DisplayName == "" || utf8.RuneCountInString(input.DisplayName) > 120 || strings.IndexFunc(input.DisplayName, unicode.IsControl) >= 0 {
		collector.Add("display_name", "invalid", "Use 1 to 120 visible characters.")
	}
	if len(input.DefaultLocale) > 35 || !localePattern.MatchString(input.DefaultLocale) {
		collector.Add("default_locale", "invalid", "Use a valid language tag.")
	}
	if len(input.DefaultTimezone) > 64 {
		collector.Add("default_timezone", "invalid", "Use a valid IANA timezone identifier.")
	} else if _, err := time.LoadLocation(input.DefaultTimezone); err != nil {
		collector.Add("default_timezone", "invalid", "Use a valid IANA timezone identifier.")
	}
	currency, err := money.NewCurrency(input.DefaultCurrency)
	if err != nil {
		collector.Add("default_currency", "invalid", "Use a three-letter uppercase currency code.")
	}
	if len(input.IdempotencyKey) < 16 || len(input.IdempotencyKey) > 128 || !idempotencyPattern.MatchString(input.IdempotencyKey) {
		collector.Add("idempotency_key", "invalid", "Use 16 to 128 letters, digits, dots, underscores, colons, or hyphens.")
	}
	if err := collector.Error("The organization request is invalid."); err != nil {
		return CreateInput{}, "", err
	}
	return input, currency, nil
}

func newIDs() (identifier.ID, identifier.ID, identifier.ID, identifier.ID, error) {
	values := make([]identifier.ID, 4)
	for index := range values {
		value, err := identifier.New()
		if err != nil {
			return identifier.ID{}, identifier.ID{}, identifier.ID{}, identifier.ID{}, err
		}
		values[index] = value
	}
	return values[0], values[1], values[2], values[3], nil
}
