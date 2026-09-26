// Package event owns tenant-scoped event identities and immutable content revisions.
package event

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
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
	StatusDraft       Status = "draft"
	StatusPublished   Status = "published"
	StatusUnpublished Status = "unpublished"
	StatusCancelled   Status = "cancelled"
	StatusArchived    Status = "archived"
	DefaultLimit             = int32(50)
	MaxLimit                 = int32(100)
)

var (
	ErrNotFound           = errors.New("event not found")
	ErrSlugConflict       = errors.New("event slug already exists")
	ErrVersionConflict    = errors.New("event version conflict")
	ErrInvalidState       = errors.New("event state transition is invalid")
	ErrPublicationBlocked = errors.New("event publication validation failed")
	eventSlugPattern      = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
)

type Event struct {
	ID               identifier.ID
	OrganizationID   identifier.ID
	Slug             string
	Title            string
	ShortDescription string
	LongDescription  string
	Status           Status
	CurrentRevision  int64
	Checksum         string
	Version          int64
	PublishedAt      *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type Cursor struct {
	OrganizationID identifier.ID
	CreatedAt      time.Time
	ID             identifier.ID
}

type Page struct {
	Items      []Event
	NextCursor *Cursor
}

type CreateInput struct {
	OrganizationID   identifier.ID
	Slug             string
	Title            string
	ShortDescription string
	LongDescription  string
}

type UpdateInput struct {
	OrganizationID   identifier.ID
	EventID          identifier.ID
	Slug             string
	Title            string
	ShortDescription string
	LongDescription  string
	Version          int64
}

type PublishInput struct {
	OrganizationID identifier.ID
	EventID        identifier.ID
	Version        int64
}

type CloneInput struct {
	OrganizationID identifier.ID
	EventID        identifier.ID
}

type CreateRecord struct {
	Event     Event
	Checksum  []byte
	AuditID   identifier.ID
	OutboxID  identifier.ID
	ActorType string
	ActorID   string
}

type UpdateRecord struct {
	Input      UpdateInput
	Checksum   []byte
	AuditID    identifier.ID
	OutboxID   identifier.ID
	ActorType  string
	ActorID    string
	OccurredAt time.Time
}

type PublishRecord struct {
	Input      PublishInput
	AuditID    identifier.ID
	OutboxID   identifier.ID
	ActorType  string
	ActorID    string
	OccurredAt time.Time
}

type CloneRecord struct {
	OrganizationID identifier.ID
	EventID        identifier.ID
	CloneID        identifier.ID
	AuditID        identifier.ID
	OutboxID       identifier.ID
	ActorType      string
	ActorID        string
	OccurredAt     time.Time
}

type Repository interface {
	Create(context.Context, CreateRecord) (Event, error)
	Get(context.Context, identifier.ID, identifier.ID) (Event, error)
	List(context.Context, identifier.ID, int32, *Cursor) (Page, error)
	Update(context.Context, UpdateRecord) (Event, error)
	Publish(context.Context, PublishRecord) (Event, error)
	Clone(context.Context, CloneRecord) (Event, error)
}

type Service struct {
	repository           Repository
	clock                clock.Clock
	publicationValidator PublicationValidator
}

type PublicationValidator interface {
	ValidateForPublish(context.Context, identifier.ID, identifier.ID) (bool, error)
}

func NewService(repository Repository, timeSource clock.Clock) *Service {
	return &Service{repository: repository, clock: timeSource}
}

func (service *Service) WithPublicationValidator(validator PublicationValidator) *Service {
	service.publicationValidator = validator
	return service
}

func (service *Service) Create(ctx context.Context, input CreateInput) (Event, error) {
	authorization, err := service.requireOrganization(ctx, input.OrganizationID, access.PermissionCatalogManage)
	if err != nil {
		return Event{}, err
	}
	normalized, checksum, err := normalize(input)
	if err != nil {
		return Event{}, err
	}
	eventID, auditID, outboxID, err := newIDs3()
	if err != nil {
		return Event{}, apperror.Wrap(err, apperror.CodeInternal, "An event identifier could not be created.")
	}
	now := service.clock.Now().UTC()
	created, err := service.repository.Create(ctx, CreateRecord{Event: Event{ID: eventID, OrganizationID: authorization.OrganizationID(), Slug: normalized.Slug, Title: normalized.Title, ShortDescription: normalized.ShortDescription, LongDescription: normalized.LongDescription, Status: StatusDraft, CurrentRevision: 0, Checksum: checksumString(checksum), Version: 1, CreatedAt: now, UpdatedAt: now}, Checksum: checksum, AuditID: auditID, OutboxID: outboxID, ActorType: actorType(authorization.Principal().Type()), ActorID: authorization.Principal().ID()})
	return created, mapError(err)
}

func (service *Service) Get(ctx context.Context, organizationID, eventID identifier.ID) (Event, error) {
	authorization, err := service.requireOrganization(ctx, organizationID, access.PermissionCatalogRead)
	if err != nil {
		return Event{}, err
	}
	found, err := service.repository.Get(ctx, authorization.OrganizationID(), eventID)
	if errors.Is(err, ErrNotFound) {
		return Event{}, apperror.Wrap(err, apperror.CodeNotFound, "The requested event does not exist.")
	}
	return found, err
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
		return Page{}, apperror.New(apperror.CodeValidationFailed, "The event list request is invalid.", apperror.Detail{Field: "limit", Code: "out_of_range", Message: fmt.Sprintf("Use a limit between 1 and %d.", MaxLimit)})
	}
	if after != nil && after.OrganizationID != authorization.OrganizationID() {
		return Page{}, apperror.New(apperror.CodeInvalidCursor, "The event cursor is invalid.")
	}
	return service.repository.List(ctx, authorization.OrganizationID(), limit, after)
}

func (service *Service) Update(ctx context.Context, input UpdateInput) (Event, error) {
	authorization, err := service.requireOrganization(ctx, input.OrganizationID, access.PermissionCatalogManage)
	if err != nil {
		return Event{}, err
	}
	if input.EventID.IsZero() || input.Version < 1 {
		return Event{}, apperror.New(apperror.CodeValidationFailed, "The event update request is invalid.")
	}
	input.OrganizationID = authorization.OrganizationID()
	normalized, checksum, err := normalize(CreateInput{OrganizationID: input.OrganizationID, Slug: input.Slug, Title: input.Title, ShortDescription: input.ShortDescription, LongDescription: input.LongDescription})
	if err != nil {
		return Event{}, err
	}
	auditID, outboxID, err := newIDs2()
	if err != nil {
		return Event{}, apperror.Wrap(err, apperror.CodeInternal, "An event update identifier could not be created.")
	}
	updated, err := service.repository.Update(ctx, UpdateRecord{Input: UpdateInput{OrganizationID: input.OrganizationID, EventID: input.EventID, Slug: normalized.Slug, Title: normalized.Title, ShortDescription: normalized.ShortDescription, LongDescription: normalized.LongDescription, Version: input.Version}, Checksum: checksum, AuditID: auditID, OutboxID: outboxID, ActorType: actorType(authorization.Principal().Type()), ActorID: authorization.Principal().ID(), OccurredAt: service.clock.Now().UTC()})
	return updated, mapError(err)
}

func (service *Service) Publish(ctx context.Context, input PublishInput) (Event, error) {
	authorization, err := service.requireOrganization(ctx, input.OrganizationID, access.PermissionCatalogPublish)
	if err != nil {
		return Event{}, err
	}
	if input.EventID.IsZero() || input.Version < 1 {
		return Event{}, apperror.New(apperror.CodeValidationFailed, "The event publish request is invalid.")
	}
	if service.publicationValidator == nil {
		return Event{}, apperror.New(apperror.CodeDependencyUnavailable, "Event publication validation is not configured.")
	}
	valid, err := service.publicationValidator.ValidateForPublish(ctx, authorization.OrganizationID(), input.EventID)
	if err != nil {
		return Event{}, err
	}
	if !valid {
		return Event{}, apperror.Wrap(ErrPublicationBlocked, apperror.CodeConflict, "The event is not ready to publish.")
	}
	auditID, outboxID, err := newIDs2()
	if err != nil {
		return Event{}, apperror.Wrap(err, apperror.CodeInternal, "An event publication identifier could not be created.")
	}
	published, err := service.repository.Publish(ctx, PublishRecord{Input: PublishInput{OrganizationID: authorization.OrganizationID(), EventID: input.EventID, Version: input.Version}, AuditID: auditID, OutboxID: outboxID, ActorType: actorType(authorization.Principal().Type()), ActorID: authorization.Principal().ID(), OccurredAt: service.clock.Now().UTC()})
	return published, mapError(err)
}

func (service *Service) Clone(ctx context.Context, input CloneInput) (Event, error) {
	authorization, err := service.requireOrganization(ctx, input.OrganizationID, access.PermissionCatalogManage)
	if err != nil {
		return Event{}, err
	}
	if input.EventID.IsZero() {
		return Event{}, apperror.New(apperror.CodeValidationFailed, "The event clone request is invalid.")
	}
	cloneID, auditID, outboxID, err := newIDs3()
	if err != nil {
		return Event{}, apperror.Wrap(err, apperror.CodeInternal, "An event clone identifier could not be created.")
	}
	cloned, err := service.repository.Clone(ctx, CloneRecord{OrganizationID: authorization.OrganizationID(), EventID: input.EventID, CloneID: cloneID, AuditID: auditID, OutboxID: outboxID, ActorType: actorType(authorization.Principal().Type()), ActorID: authorization.Principal().ID(), OccurredAt: service.clock.Now().UTC()})
	return cloned, mapError(err)
}

func normalize(input CreateInput) (CreateInput, []byte, error) {
	input.Slug = strings.ToLower(strings.TrimSpace(input.Slug))
	input.Title = strings.TrimSpace(input.Title)
	input.ShortDescription = strings.TrimSpace(input.ShortDescription)
	input.LongDescription = strings.TrimSpace(input.LongDescription)
	collector := validation.Collector{}
	if len(input.Slug) < 2 || len(input.Slug) > 120 || !eventSlugPattern.MatchString(input.Slug) {
		collector.Add("slug", "invalid", "Use 2 to 120 lowercase letters, digits, or single hyphens.")
	}
	if !validText(input.Title, 200) {
		collector.Add("title", "invalid", "Use 1 to 200 visible characters.")
	}
	if !validOptionalText(input.ShortDescription, 500) {
		collector.Add("short_description", "invalid", "Use at most 500 visible characters.")
	}
	if !validOptionalText(input.LongDescription, 10000) {
		collector.Add("long_description", "invalid", "Use at most 10000 visible characters.")
	}
	if err := collector.Error("The event request is invalid."); err != nil {
		return CreateInput{}, nil, err
	}
	canonical, err := json.Marshal(struct {
		Slug  string `json:"slug"`
		Title string `json:"title"`
		Short string `json:"short_description"`
		Long  string `json:"long_description"`
	}{input.Slug, input.Title, input.ShortDescription, input.LongDescription})
	if err != nil {
		return CreateInput{}, nil, apperror.Wrap(err, apperror.CodeInternal, "Event content could not be canonicalized.")
	}
	digest := sha256.Sum256(canonical)
	return input, digest[:], nil
}

func validText(value string, max int) bool {
	return value != "" && utf8.RuneCountInString(value) <= max && strings.IndexFunc(value, unicode.IsControl) < 0
}

func validOptionalText(value string, max int) bool {
	return utf8.RuneCountInString(value) <= max && strings.IndexFunc(value, unicode.IsControl) < 0
}

func checksumString(value []byte) string { return hex.EncodeToString(value) }

func (service *Service) requireOrganization(ctx context.Context, organizationID identifier.ID, permission access.Permission) (access.Authorization, error) {
	authorization, err := access.Require(ctx, permission)
	if err != nil {
		if errors.Is(err, access.ErrUnauthenticated) {
			return access.Authorization{}, apperror.Wrap(err, apperror.CodeUnauthenticated, "Authentication is required.")
		}
		return access.Authorization{}, apperror.Wrap(err, apperror.CodePermissionDenied, "Event access is not permitted.")
	}
	if organizationID.IsZero() || authorization.OrganizationID() != organizationID {
		return access.Authorization{}, apperror.Wrap(ErrNotFound, apperror.CodeNotFound, "The requested event does not exist.")
	}
	return authorization, nil
}

func mapError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotFound):
		return apperror.Wrap(err, apperror.CodeNotFound, "The requested event does not exist.")
	case errors.Is(err, ErrSlugConflict):
		return apperror.Wrap(err, apperror.CodeConflict, "An event with this slug already exists.")
	case errors.Is(err, ErrVersionConflict):
		return apperror.Wrap(err, apperror.CodePreconditionFailed, "The event changed; reload it and retry.")
	case errors.Is(err, ErrInvalidState):
		return apperror.Wrap(err, apperror.CodeConflict, "The event cannot be changed in its current state.")
	case errors.Is(err, ErrPublicationBlocked):
		return apperror.Wrap(err, apperror.CodeConflict, "The event is not ready to publish.")
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
		return Cursor{}, apperror.New(apperror.CodeInvalidCursor, "The event cursor is invalid.")
	}
	var payload struct {
		Version int    `json:"v"`
		OrgID   string `json:"organization_id"`
		Created string `json:"created_at"`
		ID      string `json:"id"`
	}
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(body, &payload) != nil || payload.Version != 1 {
		return Cursor{}, apperror.New(apperror.CodeInvalidCursor, "The event cursor is invalid.")
	}
	orgID, err := identifier.Parse(payload.OrgID)
	if err != nil {
		return Cursor{}, apperror.New(apperror.CodeInvalidCursor, "The event cursor is invalid.")
	}
	id, err := identifier.Parse(payload.ID)
	if err != nil {
		return Cursor{}, apperror.New(apperror.CodeInvalidCursor, "The event cursor is invalid.")
	}
	createdAt, err := time.Parse(time.RFC3339Nano, payload.Created)
	if err != nil {
		return Cursor{}, apperror.New(apperror.CodeInvalidCursor, "The event cursor is invalid.")
	}
	return Cursor{OrganizationID: orgID, CreatedAt: createdAt.UTC(), ID: id}, nil
}

func actorType(value access.PrincipalType) string {
	if value == access.PrincipalPlatform {
		return "platform"
	}
	return string(value)
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
