// Package device owns tenant-scoped scanner device enrollment and lifecycle.
package device

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
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

const (
	DefaultLimit = int32(50)
	MaxLimit     = int32(100)
	maxName      = 160
	maxPlatform  = 32
	maxApp       = 64
)

var (
	ErrNotFound           = errors.New("device not found")
	ErrVersionConflict    = errors.New("device version conflict")
	ErrInvalidState       = errors.New("device state transition is invalid")
	ErrInvalid            = errors.New("invalid device")
	ErrCredentialInvalid  = errors.New("device credential is invalid")
	ErrAssignmentNotFound = errors.New("device assignment not found")
	ErrAssignmentVersion  = errors.New("device assignment version conflict")
	ErrAssignmentConflict = errors.New("device assignment already exists")
	platformPattern       = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,31}$`)
)

type State string

const (
	StateActive    State = "active"
	StateSuspended State = "suspended"
	StateRevoked   State = "revoked"
)

func (state State) Valid() bool {
	return state == StateActive || state == StateSuspended || state == StateRevoked
}

type Device struct {
	ID             identifier.ID
	OrganizationID identifier.ID
	VenueID        identifier.ID
	Name           string
	Platform       string
	AppVersion     string
	State          State
	LastSeenAt     *time.Time
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
	Items      []Device
	NextCursor *Cursor
}

type Assignment struct {
	ID             identifier.ID
	OrganizationID identifier.ID
	DeviceID       identifier.ID
	SessionID      *identifier.ID
	GateID         *identifier.ID
	Capability     access.Permission
	State          string
	ValidFrom      time.Time
	ValidUntil     *time.Time
	Version        int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type AssignmentCursor struct {
	OrganizationID identifier.ID
	DeviceID       identifier.ID
	CreatedAt      time.Time
	ID             identifier.ID
}

type AssignmentPage struct {
	Items      []Assignment
	NextCursor *AssignmentCursor
}

type CreateAssignmentInput struct {
	OrganizationID identifier.ID
	DeviceID       identifier.ID
	SessionID      *identifier.ID
	GateID         *identifier.ID
	Capability     access.Permission
	ValidFrom      time.Time
	ValidUntil     *time.Time
}

type CreateAssignmentRecord struct {
	Assignment Assignment
	AuditID    identifier.ID
	OutboxID   identifier.ID
	ActorType  string
	ActorID    string
}

type RevokeAssignmentInput struct {
	OrganizationID  identifier.ID
	DeviceID        identifier.ID
	AssignmentID    identifier.ID
	ExpectedVersion int64
}

type RevokeAssignmentRecord struct {
	RevokeAssignmentInput
	OccurredAt time.Time
	AuditID    identifier.ID
	OutboxID   identifier.ID
	ActorType  string
	ActorID    string
}

type CreateInput struct {
	OrganizationID identifier.ID
	VenueID        identifier.ID
	Name           string
	Platform       string
	AppVersion     string
}

type CreateRecord struct {
	Device         Device
	CredentialHash [32]byte
	AuditID        identifier.ID
	OutboxID       identifier.ID
	ActorType      string
	ActorID        string
}

type ChangeStateInput struct {
	OrganizationID  identifier.ID
	DeviceID        identifier.ID
	State           State
	ExpectedVersion int64
}

type ChangeStateRecord struct {
	ChangeStateInput
	OccurredAt time.Time
	AuditID    identifier.ID
	OutboxID   identifier.ID
	ActorType  string
	ActorID    string
}

type IssuedDevice struct {
	Device Device
	Token  string
}

type Repository interface {
	List(context.Context, identifier.ID, int32, *Cursor) (Page, error)
	Create(context.Context, CreateRecord) (Device, error)
	Get(context.Context, identifier.ID, identifier.ID) (Device, error)
	ChangeState(context.Context, ChangeStateRecord) (Device, error)
}

type AssignmentRepository interface {
	ListAssignments(context.Context, identifier.ID, identifier.ID, int32, *AssignmentCursor) (AssignmentPage, error)
	CreateAssignment(context.Context, CreateAssignmentRecord) (Assignment, error)
	RevokeAssignment(context.Context, RevokeAssignmentRecord) (Assignment, error)
}

type CredentialRepository interface {
	LookupCredential(context.Context, [32]byte) (Device, error)
}

type Service struct {
	repository Repository
	clock      clock.Clock
}

func NewService(repository Repository, timeSource clock.Clock) *Service {
	return &Service{repository: repository, clock: timeSource}
}

func (service *Service) List(ctx context.Context, organizationID identifier.ID, limit int32, after *Cursor) (Page, error) {
	if err := requireDeviceManagement(ctx); err != nil {
		return Page{}, err
	}
	authorization, _ := access.Require(ctx, access.PermissionDeviceManage)
	if organizationID.IsZero() || authorization.OrganizationID() != organizationID {
		return Page{}, apperror.Wrap(ErrNotFound, apperror.CodeNotFound, "The requested device does not exist.")
	}
	if limit == 0 {
		limit = DefaultLimit
	}
	if limit < 1 || limit > MaxLimit {
		return Page{}, apperror.New(apperror.CodeValidationFailed, "The device list request is invalid.", apperror.Detail{Field: "limit", Code: "out_of_range", Message: fmt.Sprintf("Use a limit between 1 and %d.", MaxLimit)})
	}
	if after != nil && (after.OrganizationID != organizationID || after.ID.IsZero() || after.CreatedAt.IsZero()) {
		return Page{}, apperror.New(apperror.CodeInvalidCursor, "The device cursor is invalid.")
	}
	return service.repository.List(ctx, organizationID, limit, after)
}

func (service *Service) Create(ctx context.Context, input CreateInput) (IssuedDevice, error) {
	if err := requireDeviceManagement(ctx); err != nil {
		return IssuedDevice{}, err
	}
	authorization, _ := access.Require(ctx, access.PermissionDeviceManage)
	if input.OrganizationID.IsZero() || authorization.OrganizationID() != input.OrganizationID || input.VenueID.IsZero() {
		return IssuedDevice{}, apperror.Wrap(ErrNotFound, apperror.CodeNotFound, "The requested device does not exist.")
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Platform = strings.TrimSpace(strings.ToLower(input.Platform))
	input.AppVersion = strings.TrimSpace(input.AppVersion)
	collector := validation.Collector{}
	if len(input.Name) < 1 || len(input.Name) > maxName {
		collector.Add("name", "invalid", "Device name must be between 1 and 160 characters.")
	}
	if !platformPattern.MatchString(input.Platform) || len(input.Platform) > maxPlatform {
		collector.Add("platform", "invalid", "Platform must use lowercase letters, digits, dots, underscores, or hyphens.")
	}
	if len(input.AppVersion) < 1 || len(input.AppVersion) > maxApp {
		collector.Add("app_version", "invalid", "App version must be between 1 and 64 characters.")
	}
	if err := collector.Error("The device request is invalid."); err != nil {
		return IssuedDevice{}, err
	}
	now := service.clock.Now().UTC()
	id, err := identifier.New()
	if err != nil {
		return IssuedDevice{}, apperror.Wrap(err, apperror.CodeInternal, "A device identifier could not be created.")
	}
	token, hash, err := newCredential(id)
	if err != nil {
		return IssuedDevice{}, apperror.Wrap(err, apperror.CodeInternal, "Device credential material could not be created.")
	}
	auditID, err := identifier.New()
	if err != nil {
		return IssuedDevice{}, apperror.Wrap(err, apperror.CodeInternal, "Device mutation identifiers could not be created.")
	}
	outboxID, err := identifier.New()
	if err != nil {
		return IssuedDevice{}, apperror.Wrap(err, apperror.CodeInternal, "Device mutation identifiers could not be created.")
	}
	value := Device{ID: id, OrganizationID: input.OrganizationID, VenueID: input.VenueID, Name: input.Name, Platform: input.Platform, AppVersion: input.AppVersion, State: StateActive, Version: 1, CreatedAt: now, UpdatedAt: now}
	created, err := service.repository.Create(ctx, CreateRecord{Device: value, CredentialHash: hash, AuditID: auditID, OutboxID: outboxID, ActorType: string(authorization.Principal().Type()), ActorID: authorization.Principal().ID()})
	if err != nil {
		return IssuedDevice{}, mapMutationError(err)
	}
	return IssuedDevice{Device: created, Token: token}, nil
}

func (service *Service) Get(ctx context.Context, organizationID, deviceID identifier.ID) (Device, error) {
	if err := requireDeviceManagement(ctx); err != nil {
		return Device{}, err
	}
	authorization, _ := access.Require(ctx, access.PermissionDeviceManage)
	if organizationID.IsZero() || deviceID.IsZero() || authorization.OrganizationID() != organizationID {
		return Device{}, apperror.Wrap(ErrNotFound, apperror.CodeNotFound, "The requested device does not exist.")
	}
	value, err := service.repository.Get(ctx, organizationID, deviceID)
	if err != nil {
		return Device{}, mapMutationError(err)
	}
	return value, nil
}

func (service *Service) ChangeState(ctx context.Context, input ChangeStateInput) (Device, error) {
	if err := requireDeviceManagement(ctx); err != nil {
		return Device{}, err
	}
	authorization, _ := access.Require(ctx, access.PermissionDeviceManage)
	if input.OrganizationID.IsZero() || input.DeviceID.IsZero() || authorization.OrganizationID() != input.OrganizationID {
		return Device{}, apperror.Wrap(ErrNotFound, apperror.CodeNotFound, "The requested device does not exist.")
	}
	if !input.State.Valid() || input.ExpectedVersion < 1 {
		return Device{}, apperror.New(apperror.CodeValidationFailed, "The device state request is invalid.", apperror.Detail{Field: "state", Code: "invalid", Message: "State must be active, suspended, or revoked and version must be positive."})
	}
	now := service.clock.Now().UTC()
	auditID, err := identifier.New()
	if err != nil {
		return Device{}, apperror.Wrap(err, apperror.CodeInternal, "Device mutation identifiers could not be created.")
	}
	outboxID, err := identifier.New()
	if err != nil {
		return Device{}, apperror.Wrap(err, apperror.CodeInternal, "Device mutation identifiers could not be created.")
	}
	value, err := service.repository.ChangeState(ctx, ChangeStateRecord{ChangeStateInput: input, OccurredAt: now, AuditID: auditID, OutboxID: outboxID, ActorType: string(authorization.Principal().Type()), ActorID: authorization.Principal().ID()})
	if err != nil {
		return Device{}, mapMutationError(err)
	}
	return value, nil
}

// Authenticate verifies a device credential without exposing the stored
// verifier. It returns only active devices; suspended/revoked credentials fail
// closed with the same public error as an unknown credential.
func (service *Service) Authenticate(ctx context.Context, token string) (Device, error) {
	if len(token) < 80 || len(token) > 256 || !strings.HasPrefix(token, "vdev_") || strings.TrimSpace(token) != token {
		return Device{}, ErrCredentialInvalid
	}
	repository, ok := service.repository.(CredentialRepository)
	if !ok {
		return Device{}, errors.New("device repository does not support credential lookup")
	}
	value, err := repository.LookupCredential(ctx, sha256.Sum256([]byte(token)))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Device{}, ErrCredentialInvalid
		}
		return Device{}, err
	}
	if value.State != StateActive {
		return Device{}, ErrCredentialInvalid
	}
	return value, nil
}

func (service *Service) Authorization(ctx context.Context, token string) (access.Authorization, error) {
	value, err := service.Authenticate(ctx, token)
	if err != nil {
		return access.Authorization{}, err
	}
	principal, err := access.NewPrincipal(access.PrincipalDevice, value.ID.String())
	if err != nil {
		return access.Authorization{}, err
	}
	return access.NewAuthorization(principal, value.OrganizationID, access.PermissionEntryScan)
}

func (service *Service) ListAssignments(ctx context.Context, organizationID, deviceID identifier.ID, limit int32, after *AssignmentCursor) (AssignmentPage, error) {
	if err := requireDeviceManagement(ctx); err != nil {
		return AssignmentPage{}, err
	}
	authorization, _ := access.Require(ctx, access.PermissionDeviceManage)
	if organizationID.IsZero() || deviceID.IsZero() || authorization.OrganizationID() != organizationID {
		return AssignmentPage{}, apperror.Wrap(ErrNotFound, apperror.CodeNotFound, "The requested device does not exist.")
	}
	if limit == 0 {
		limit = DefaultLimit
	}
	if limit < 1 || limit > MaxLimit {
		return AssignmentPage{}, apperror.New(apperror.CodeValidationFailed, "The assignment list request is invalid.", apperror.Detail{Field: "limit", Code: "out_of_range", Message: fmt.Sprintf("Use a limit between 1 and %d.", MaxLimit)})
	}
	if after != nil && (after.OrganizationID != organizationID || after.DeviceID != deviceID || after.ID.IsZero() || after.CreatedAt.IsZero()) {
		return AssignmentPage{}, apperror.New(apperror.CodeInvalidCursor, "The assignment cursor is invalid.")
	}
	repository, ok := service.repository.(AssignmentRepository)
	if !ok {
		return AssignmentPage{}, errors.New("device repository does not support assignments")
	}
	return repository.ListAssignments(ctx, organizationID, deviceID, limit, after)
}

func (service *Service) CreateAssignment(ctx context.Context, input CreateAssignmentInput) (Assignment, error) {
	if err := requireDeviceManagement(ctx); err != nil {
		return Assignment{}, err
	}
	authorization, _ := access.Require(ctx, access.PermissionDeviceManage)
	if input.OrganizationID.IsZero() || input.DeviceID.IsZero() || authorization.OrganizationID() != input.OrganizationID {
		return Assignment{}, apperror.Wrap(ErrNotFound, apperror.CodeNotFound, "The requested device does not exist.")
	}
	if input.SessionID == nil && input.GateID == nil {
		return Assignment{}, apperror.New(apperror.CodeValidationFailed, "The device assignment request is invalid.", apperror.Detail{Field: "scope", Code: "required", Message: "A session or gate scope is required."})
	}
	if input.SessionID != nil && input.SessionID.IsZero() {
		return Assignment{}, apperror.New(apperror.CodeValidationFailed, "The device assignment request is invalid.", apperror.Detail{Field: "session_id", Code: "invalid", Message: "Session identifier is invalid."})
	}
	if input.GateID != nil && input.GateID.IsZero() {
		return Assignment{}, apperror.New(apperror.CodeValidationFailed, "The device assignment request is invalid.", apperror.Detail{Field: "gate_id", Code: "invalid", Message: "Gate identifier is invalid."})
	}
	if input.Capability == "" {
		input.Capability = access.PermissionEntryScan
	}
	if input.Capability != access.PermissionEntryScan && input.Capability != access.PermissionEntryOverride {
		return Assignment{}, apperror.New(apperror.CodeValidationFailed, "The device assignment request is invalid.", apperror.Detail{Field: "capability", Code: "invalid", Message: "Capability must be entry.scan or entry.override."})
	}
	if !authorization.Has(input.Capability) {
		return Assignment{}, apperror.Wrap(access.ErrUnauthorized, apperror.CodePermissionDenied, "The requested device capability is not granted to the caller.")
	}
	now := service.clock.Now().UTC()
	validFrom := input.ValidFrom.UTC()
	if validFrom.IsZero() {
		validFrom = now
	}
	if input.ValidUntil != nil {
		until := input.ValidUntil.UTC()
		if !until.After(validFrom) {
			return Assignment{}, apperror.New(apperror.CodeValidationFailed, "The device assignment request is invalid.", apperror.Detail{Field: "valid_until", Code: "invalid", Message: "Assignment expiry must be after its start."})
		}
		input.ValidUntil = &until
	}
	id, err := identifier.New()
	if err != nil {
		return Assignment{}, apperror.Wrap(err, apperror.CodeInternal, "An assignment identifier could not be created.")
	}
	auditID, err := identifier.New()
	if err != nil {
		return Assignment{}, apperror.Wrap(err, apperror.CodeInternal, "Assignment mutation identifiers could not be created.")
	}
	outboxID, err := identifier.New()
	if err != nil {
		return Assignment{}, apperror.Wrap(err, apperror.CodeInternal, "Assignment mutation identifiers could not be created.")
	}
	assignment := Assignment{ID: id, OrganizationID: input.OrganizationID, DeviceID: input.DeviceID, SessionID: copyID(input.SessionID), GateID: copyID(input.GateID), Capability: input.Capability, State: "active", ValidFrom: validFrom, ValidUntil: copyTime(input.ValidUntil), Version: 1, CreatedAt: now, UpdatedAt: now}
	repository, ok := service.repository.(AssignmentRepository)
	if !ok {
		return Assignment{}, errors.New("device repository does not support assignments")
	}
	created, err := repository.CreateAssignment(ctx, CreateAssignmentRecord{Assignment: assignment, AuditID: auditID, OutboxID: outboxID, ActorType: string(authorization.Principal().Type()), ActorID: authorization.Principal().ID()})
	if err != nil {
		return Assignment{}, mapAssignmentError(err)
	}
	return created, nil
}

func (service *Service) RevokeAssignment(ctx context.Context, input RevokeAssignmentInput) (Assignment, error) {
	if err := requireDeviceManagement(ctx); err != nil {
		return Assignment{}, err
	}
	authorization, _ := access.Require(ctx, access.PermissionDeviceManage)
	if input.OrganizationID.IsZero() || input.DeviceID.IsZero() || input.AssignmentID.IsZero() || authorization.OrganizationID() != input.OrganizationID {
		return Assignment{}, apperror.Wrap(ErrAssignmentNotFound, apperror.CodeNotFound, "The requested device assignment does not exist.")
	}
	if input.ExpectedVersion < 1 {
		return Assignment{}, apperror.New(apperror.CodeValidationFailed, "The assignment revocation request is invalid.", apperror.Detail{Field: "expected_version", Code: "required", Message: "A positive assignment version is required."})
	}
	auditID, err := identifier.New()
	if err != nil {
		return Assignment{}, apperror.Wrap(err, apperror.CodeInternal, "Assignment mutation identifiers could not be created.")
	}
	outboxID, err := identifier.New()
	if err != nil {
		return Assignment{}, apperror.Wrap(err, apperror.CodeInternal, "Assignment mutation identifiers could not be created.")
	}
	repository, ok := service.repository.(AssignmentRepository)
	if !ok {
		return Assignment{}, errors.New("device repository does not support assignments")
	}
	value, err := repository.RevokeAssignment(ctx, RevokeAssignmentRecord{RevokeAssignmentInput: input, OccurredAt: service.clock.Now().UTC(), AuditID: auditID, OutboxID: outboxID, ActorType: string(authorization.Principal().Type()), ActorID: authorization.Principal().ID()})
	if err != nil {
		return Assignment{}, mapAssignmentError(err)
	}
	return value, nil
}

func requireDeviceManagement(ctx context.Context) error {
	authorization, err := access.Require(ctx, access.PermissionDeviceManage)
	if err == nil && !authorization.OrganizationID().IsZero() {
		return nil
	}
	if errors.Is(err, access.ErrUnauthenticated) {
		return apperror.Wrap(err, apperror.CodeUnauthenticated, "Authentication is required.")
	}
	return apperror.Wrap(err, apperror.CodePermissionDenied, "Device management is not permitted.")
}

func newCredential(id identifier.ID) (string, [32]byte, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", [32]byte{}, err
	}
	token := "vdev_" + id.String() + "." + base64.RawURLEncoding.EncodeToString(secret)
	return token, sha256.Sum256([]byte(token)), nil
}

func mapMutationError(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return apperror.Wrap(err, apperror.CodeNotFound, "The requested device does not exist.")
	case errors.Is(err, ErrVersionConflict):
		return apperror.Wrap(err, apperror.CodePreconditionFailed, "The device changed; refresh and retry.")
	case errors.Is(err, ErrInvalidState):
		return apperror.Wrap(err, apperror.CodeConflict, "The device state transition is not allowed.")
	default:
		return err
	}
}

func mapAssignmentError(err error) error {
	switch {
	case errors.Is(err, ErrAssignmentNotFound):
		return apperror.Wrap(err, apperror.CodeNotFound, "The requested device assignment does not exist.")
	case errors.Is(err, ErrAssignmentVersion):
		return apperror.Wrap(err, apperror.CodePreconditionFailed, "The device assignment changed; refresh and retry.")
	case errors.Is(err, ErrInvalidState):
		return apperror.Wrap(err, apperror.CodeConflict, "The device cannot accept this assignment.")
	case errors.Is(err, ErrAssignmentConflict):
		return apperror.Wrap(err, apperror.CodeConflict, "The device already has this assignment.")
	default:
		return err
	}
}

func copyID(value *identifier.ID) *identifier.ID {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func copyTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copyValue := value.UTC()
	return &copyValue
}

func EncodeCursor(cursor Cursor) (string, error) {
	if cursor.OrganizationID.IsZero() || cursor.ID.IsZero() || cursor.CreatedAt.IsZero() {
		return "", fmt.Errorf("invalid device cursor")
	}
	payload, err := json.Marshal(cursor)
	if err != nil {
		return "", fmt.Errorf("encode device cursor: %w", err)
	}
	value := base64.RawURLEncoding.EncodeToString(payload)
	if len(value) > 512 {
		return "", fmt.Errorf("device cursor is too long")
	}
	return value, nil
}

func DecodeCursor(value string) (Cursor, error) {
	if len(value) < 1 || len(value) > 512 {
		return Cursor{}, fmt.Errorf("invalid device cursor")
	}
	payload, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return Cursor{}, fmt.Errorf("invalid device cursor: %w", err)
	}
	var cursor Cursor
	if err := json.Unmarshal(payload, &cursor); err != nil || cursor.OrganizationID.IsZero() || cursor.ID.IsZero() || cursor.CreatedAt.IsZero() {
		return Cursor{}, fmt.Errorf("invalid device cursor")
	}
	return cursor, nil
}

func EncodeAssignmentCursor(cursor AssignmentCursor) (string, error) {
	if cursor.OrganizationID.IsZero() || cursor.DeviceID.IsZero() || cursor.ID.IsZero() || cursor.CreatedAt.IsZero() {
		return "", fmt.Errorf("invalid assignment cursor")
	}
	payload, err := json.Marshal(cursor)
	if err != nil {
		return "", fmt.Errorf("encode assignment cursor: %w", err)
	}
	value := base64.RawURLEncoding.EncodeToString(payload)
	if len(value) > 512 {
		return "", fmt.Errorf("assignment cursor is too long")
	}
	return value, nil
}

func DecodeAssignmentCursor(value string) (AssignmentCursor, error) {
	if len(value) < 1 || len(value) > 512 {
		return AssignmentCursor{}, fmt.Errorf("invalid assignment cursor")
	}
	payload, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return AssignmentCursor{}, fmt.Errorf("invalid assignment cursor: %w", err)
	}
	var cursor AssignmentCursor
	if err := json.Unmarshal(payload, &cursor); err != nil || cursor.OrganizationID.IsZero() || cursor.DeviceID.IsZero() || cursor.ID.IsZero() || cursor.CreatedAt.IsZero() {
		return AssignmentCursor{}, fmt.Errorf("invalid assignment cursor")
	}
	return cursor, nil
}
