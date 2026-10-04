package access

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/HK9750/venueos/internal/platform/apperror"
	"github.com/HK9750/venueos/internal/platform/clock"
	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/internal/platform/validation"
)

const (
	maxAPIKeyScopes = 32
	maxAPIKeyName   = 120
)

var (
	ErrAPIKeyNotFound     = errors.New("api key not found")
	ErrAPIKeyVersion      = errors.New("api key version conflict")
	ErrAPIKeyInactive     = errors.New("api key is inactive")
	ErrAPIKeyInvalid      = errors.New("api key is invalid")
	ErrAPIKeySecretFormat = errors.New("api key secret format is invalid")
)

type APIKey struct {
	ID             identifier.ID
	OrganizationID identifier.ID
	Prefix         string
	Name           string
	Scopes         []Permission
	CreatedByType  PrincipalType
	CreatedByID    string
	ExpiresAt      *time.Time
	RevokedAt      *time.Time
	LastUsedAt     *time.Time
	Version        int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

const (
	DefaultAPIKeyLimit = int32(50)
	MaxAPIKeyLimit     = int32(100)
)

type APIKeyCursor struct {
	OrganizationID identifier.ID
	CreatedAt      time.Time
	ID             identifier.ID
}

type APIKeyPage struct {
	Items      []APIKey
	NextCursor *APIKeyCursor
}

type StoredAPIKey struct {
	APIKey
	SecretHash [32]byte
}

type IssuedAPIKey struct {
	APIKey APIKey
	Token  string
}

type CreateAPIKeyInput struct {
	OrganizationID identifier.ID
	Name           string
	Scopes         []Permission
	ExpiresAt      *time.Time
}

type CreateAPIKeyRecord struct {
	APIKey     APIKey
	SecretHash [32]byte
	AuditID    identifier.ID
	OutboxID   identifier.ID
	ActorType  string
	ActorID    string
}

type RevokeAPIKeyInput struct {
	OrganizationID  identifier.ID
	APIKeyID        identifier.ID
	ExpectedVersion int64
}

type RevokeAPIKeyRecord struct {
	OrganizationID  identifier.ID
	APIKeyID        identifier.ID
	ExpectedVersion int64
	RevokedAt       time.Time
	AuditID         identifier.ID
	OutboxID        identifier.ID
	ActorType       string
	ActorID         string
}

type RotateAPIKeyInput struct {
	OrganizationID  identifier.ID
	APIKeyID        identifier.ID
	ExpectedVersion int64
	ExpiresAt       *time.Time
}

type RotateAPIKeyRecord struct {
	OrganizationID  identifier.ID
	APIKeyID        identifier.ID
	ExpectedVersion int64
	Replacement     APIKey
	SecretHash      [32]byte
	RotatedAt       time.Time
	AuditID         identifier.ID
	OutboxID        identifier.ID
	ActorType       string
	ActorID         string
}

type APIKeyRepository interface {
	ListAPIKeys(context.Context, identifier.ID, int32, *APIKeyCursor) (APIKeyPage, error)
	CreateAPIKey(context.Context, CreateAPIKeyRecord) (APIKey, error)
	RevokeAPIKey(context.Context, RevokeAPIKeyRecord) (APIKey, error)
	GetAPIKey(context.Context, identifier.ID, identifier.ID) (APIKey, error)
	RotateAPIKey(context.Context, RotateAPIKeyRecord) (APIKey, error)
	LookupAPIKey(context.Context, string) (StoredAPIKey, error)
	TouchAPIKey(context.Context, identifier.ID, time.Time) error
}

type APIKeyService struct {
	repository APIKeyRepository
	clock      clock.Clock
}

func NewAPIKeyService(repository APIKeyRepository, timeSource clock.Clock) *APIKeyService {
	return &APIKeyService{repository: repository, clock: timeSource}
}

func (service *APIKeyService) List(ctx context.Context, organizationID identifier.ID, limit int32, after *APIKeyCursor) (APIKeyPage, error) {
	authorization, err := Require(ctx, PermissionIntegrationManage)
	if err != nil {
		if errors.Is(err, ErrUnauthenticated) {
			return APIKeyPage{}, apperror.Wrap(err, apperror.CodeUnauthenticated, "Authentication is required.")
		}
		return APIKeyPage{}, apperror.Wrap(err, apperror.CodePermissionDenied, "API key management is not permitted.")
	}
	if organizationID.IsZero() || authorization.OrganizationID() != organizationID {
		return APIKeyPage{}, apperror.Wrap(ErrAPIKeyNotFound, apperror.CodeNotFound, "The requested API key does not exist.")
	}
	if limit == 0 {
		limit = DefaultAPIKeyLimit
	}
	if limit < 1 || limit > MaxAPIKeyLimit {
		return APIKeyPage{}, apperror.New(apperror.CodeValidationFailed, "The API key list request is invalid.", apperror.Detail{Field: "limit", Code: "out_of_range", Message: fmt.Sprintf("Use a limit between 1 and %d.", MaxAPIKeyLimit)})
	}
	if after != nil && (after.OrganizationID != authorization.OrganizationID() || after.ID.IsZero() || after.CreatedAt.IsZero()) {
		return APIKeyPage{}, apperror.New(apperror.CodeInvalidCursor, "The API key cursor is invalid.")
	}
	return service.repository.ListAPIKeys(ctx, authorization.OrganizationID(), limit, after)
}

func (service *APIKeyService) Create(ctx context.Context, input CreateAPIKeyInput) (IssuedAPIKey, error) {
	authorization, err := Require(ctx, PermissionIntegrationManage)
	if err != nil {
		if errors.Is(err, ErrUnauthenticated) {
			return IssuedAPIKey{}, apperror.Wrap(err, apperror.CodeUnauthenticated, "Authentication is required.")
		}
		return IssuedAPIKey{}, apperror.Wrap(err, apperror.CodePermissionDenied, "API key management is not permitted.")
	}
	if input.OrganizationID.IsZero() || authorization.OrganizationID() != input.OrganizationID {
		return IssuedAPIKey{}, apperror.Wrap(ErrAPIKeyNotFound, apperror.CodeNotFound, "The requested API key does not exist.")
	}
	now := service.clock.Now().UTC()
	name := strings.TrimSpace(input.Name)
	collector := validation.Collector{}
	if name == "" || len(name) > maxAPIKeyName {
		collector.Add("name", "invalid", "API key name must be between 1 and 120 characters.")
	}
	if len(input.Scopes) == 0 || len(input.Scopes) > maxAPIKeyScopes {
		collector.Add("scopes", "out_of_range", "Choose between 1 and 32 API key scopes.")
	}
	seen := make(map[Permission]struct{}, len(input.Scopes))
	for _, scope := range input.Scopes {
		if _, exists := seen[scope]; exists {
			collector.Add("scopes", "duplicate", "An API key scope may appear only once.")
			continue
		}
		seen[scope] = struct{}{}
		if !authorization.Has(scope) {
			collector.Add("scopes", "not_grantable", "Every API key scope must be granted to the creator.")
		}
	}
	if input.ExpiresAt != nil && !input.ExpiresAt.UTC().After(now) {
		collector.Add("expires_at", "invalid", "API key expiry must be in the future.")
	}
	if err := collector.Error("The API key request is invalid."); err != nil {
		return IssuedAPIKey{}, err
	}
	id, err := identifier.New()
	if err != nil {
		return IssuedAPIKey{}, apperror.Wrap(err, apperror.CodeInternal, "An API key identifier could not be created.")
	}
	prefix, secret, secretHash, err := generateAPIKeyMaterial()
	if err != nil {
		return IssuedAPIKey{}, apperror.Wrap(err, apperror.CodeInternal, "API key material could not be created.")
	}
	auditID, outboxID, err := newAPIKeyIDs()
	if err != nil {
		return IssuedAPIKey{}, apperror.Wrap(err, apperror.CodeInternal, "API key mutation identifiers could not be created.")
	}
	key := APIKey{
		ID: id, OrganizationID: input.OrganizationID, Prefix: prefix, Name: name,
		Scopes:        append([]Permission(nil), input.Scopes...),
		CreatedByType: authorization.Principal().Type(), CreatedByID: authorization.Principal().ID(),
		ExpiresAt: copyTime(input.ExpiresAt), Version: 1, CreatedAt: now, UpdatedAt: now,
	}
	created, err := service.repository.CreateAPIKey(ctx, CreateAPIKeyRecord{
		APIKey: key, SecretHash: secretHash, AuditID: auditID, OutboxID: outboxID,
		ActorType: string(authorization.Principal().Type()), ActorID: authorization.Principal().ID(),
	})
	if err != nil {
		return IssuedAPIKey{}, mapAPIKeyMutationError(err)
	}
	return IssuedAPIKey{APIKey: created, Token: prefix + "." + secret}, nil
}

func (service *APIKeyService) Revoke(ctx context.Context, input RevokeAPIKeyInput) (APIKey, error) {
	authorization, err := Require(ctx, PermissionIntegrationManage)
	if err != nil {
		if errors.Is(err, ErrUnauthenticated) {
			return APIKey{}, apperror.Wrap(err, apperror.CodeUnauthenticated, "Authentication is required.")
		}
		return APIKey{}, apperror.Wrap(err, apperror.CodePermissionDenied, "API key management is not permitted.")
	}
	if input.OrganizationID.IsZero() || authorization.OrganizationID() != input.OrganizationID || input.APIKeyID.IsZero() {
		return APIKey{}, apperror.Wrap(ErrAPIKeyNotFound, apperror.CodeNotFound, "The requested API key does not exist.")
	}
	if input.ExpectedVersion < 1 {
		return APIKey{}, apperror.New(apperror.CodeValidationFailed, "The API key revocation request is invalid.", apperror.Detail{Field: "expected_version", Code: "required", Message: "A positive API key version is required."})
	}
	revokedAt := service.clock.Now().UTC()
	auditID, outboxID, err := newAPIKeyIDs()
	if err != nil {
		return APIKey{}, apperror.Wrap(err, apperror.CodeInternal, "API key mutation identifiers could not be created.")
	}
	revoked, err := service.repository.RevokeAPIKey(ctx, RevokeAPIKeyRecord{
		OrganizationID: input.OrganizationID, APIKeyID: input.APIKeyID, ExpectedVersion: input.ExpectedVersion,
		RevokedAt: revokedAt, AuditID: auditID, OutboxID: outboxID,
		ActorType: string(authorization.Principal().Type()), ActorID: authorization.Principal().ID(),
	})
	if err != nil {
		return APIKey{}, mapAPIKeyMutationError(err)
	}
	return revoked, nil
}

func (service *APIKeyService) Rotate(ctx context.Context, input RotateAPIKeyInput) (IssuedAPIKey, error) {
	authorization, err := Require(ctx, PermissionIntegrationManage)
	if err != nil {
		if errors.Is(err, ErrUnauthenticated) {
			return IssuedAPIKey{}, apperror.Wrap(err, apperror.CodeUnauthenticated, "Authentication is required.")
		}
		return IssuedAPIKey{}, apperror.Wrap(err, apperror.CodePermissionDenied, "API key management is not permitted.")
	}
	if input.OrganizationID.IsZero() || authorization.OrganizationID() != input.OrganizationID || input.APIKeyID.IsZero() {
		return IssuedAPIKey{}, apperror.Wrap(ErrAPIKeyNotFound, apperror.CodeNotFound, "The requested API key does not exist.")
	}
	if input.ExpectedVersion < 1 {
		return IssuedAPIKey{}, apperror.New(apperror.CodeValidationFailed, "The API key rotation request is invalid.", apperror.Detail{Field: "expected_version", Code: "required", Message: "A positive API key version is required."})
	}
	current, err := service.repository.GetAPIKey(ctx, input.OrganizationID, input.APIKeyID)
	if err != nil {
		return IssuedAPIKey{}, mapAPIKeyMutationError(err)
	}
	now := service.clock.Now().UTC()
	if current.RevokedAt != nil || (current.ExpiresAt != nil && !current.ExpiresAt.After(now)) {
		return IssuedAPIKey{}, mapAPIKeyMutationError(ErrAPIKeyInactive)
	}
	expiresAt := current.ExpiresAt
	if input.ExpiresAt != nil {
		expiresAt = copyTime(input.ExpiresAt)
	}
	if expiresAt != nil && !expiresAt.After(now) {
		return IssuedAPIKey{}, apperror.New(apperror.CodeValidationFailed, "The API key rotation request is invalid.", apperror.Detail{Field: "expires_at", Code: "invalid", Message: "API key expiry must be in the future."})
	}
	id, err := identifier.New()
	if err != nil {
		return IssuedAPIKey{}, apperror.Wrap(err, apperror.CodeInternal, "An API key identifier could not be created.")
	}
	prefix, secret, secretHash, err := generateAPIKeyMaterial()
	if err != nil {
		return IssuedAPIKey{}, apperror.Wrap(err, apperror.CodeInternal, "API key material could not be created.")
	}
	auditID, outboxID, err := newAPIKeyIDs()
	if err != nil {
		return IssuedAPIKey{}, apperror.Wrap(err, apperror.CodeInternal, "API key mutation identifiers could not be created.")
	}
	replacement := APIKey{ID: id, OrganizationID: input.OrganizationID, Prefix: prefix, Name: current.Name,
		Scopes: append([]Permission(nil), current.Scopes...), CreatedByType: authorization.Principal().Type(),
		CreatedByID: authorization.Principal().ID(), ExpiresAt: expiresAt, Version: 1, CreatedAt: now, UpdatedAt: now}
	rotated, err := service.repository.RotateAPIKey(ctx, RotateAPIKeyRecord{
		OrganizationID: input.OrganizationID, APIKeyID: input.APIKeyID, ExpectedVersion: input.ExpectedVersion,
		Replacement: replacement, SecretHash: secretHash, RotatedAt: now, AuditID: auditID, OutboxID: outboxID,
		ActorType: string(authorization.Principal().Type()), ActorID: authorization.Principal().ID(),
	})
	if err != nil {
		return IssuedAPIKey{}, mapAPIKeyMutationError(err)
	}
	return IssuedAPIKey{APIKey: rotated, Token: prefix + "." + secret}, nil
}

func (service *APIKeyService) Authenticate(ctx context.Context, token string) (Authorization, APIKey, error) {
	prefix, secret, err := parseAPIKeyToken(token)
	if err != nil {
		return Authorization{}, APIKey{}, err
	}
	stored, err := service.repository.LookupAPIKey(ctx, prefix)
	if errors.Is(err, ErrAPIKeyNotFound) {
		return Authorization{}, APIKey{}, ErrAPIKeyInvalid
	}
	if err != nil {
		return Authorization{}, APIKey{}, err
	}
	now := service.clock.Now().UTC()
	if stored.RevokedAt != nil || (stored.ExpiresAt != nil && !stored.ExpiresAt.After(now)) {
		return Authorization{}, APIKey{}, ErrAPIKeyInactive
	}
	hash := sha256.Sum256([]byte(secret))
	if !hmac.Equal(hash[:], stored.SecretHash[:]) {
		return Authorization{}, APIKey{}, ErrAPIKeyInvalid
	}
	principal, err := NewPrincipal(PrincipalAPIKey, stored.ID.String())
	if err != nil {
		return Authorization{}, APIKey{}, fmt.Errorf("create API key principal: %w", err)
	}
	authorization, err := NewAuthorization(principal, stored.OrganizationID, stored.Scopes...)
	if err != nil {
		return Authorization{}, APIKey{}, fmt.Errorf("create API key authorization: %w", err)
	}
	if err := service.repository.TouchAPIKey(ctx, stored.ID, now); err != nil {
		return Authorization{}, APIKey{}, fmt.Errorf("record API key use: %w", err)
	}
	return authorization, stored.APIKey, nil
}

type apiKeyCursorPayload struct {
	Version        int    `json:"v"`
	OrganizationID string `json:"organization_id"`
	CreatedAt      string `json:"created_at"`
	ID             string `json:"id"`
}

// EncodeAPIKeyCursor binds the opaque cursor to one organization. A keyed
// signer can be added at the transport configuration boundary without changing
// repository ordering or tenant predicates.
func EncodeAPIKeyCursor(cursor APIKeyCursor) (string, error) {
	if cursor.OrganizationID.IsZero() || cursor.ID.IsZero() || cursor.CreatedAt.IsZero() {
		return "", errors.New("API key cursor fields are required")
	}
	body, err := json.Marshal(apiKeyCursorPayload{Version: 1, OrganizationID: cursor.OrganizationID.String(), CreatedAt: cursor.CreatedAt.UTC().Format(time.RFC3339Nano), ID: cursor.ID.String()})
	if err != nil {
		return "", fmt.Errorf("encode API key cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(body), nil
}

func DecodeAPIKeyCursor(value string) (APIKeyCursor, error) {
	if len(value) == 0 || len(value) > 512 {
		return APIKeyCursor{}, apperror.New(apperror.CodeInvalidCursor, "The API key cursor is invalid.")
	}
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return APIKeyCursor{}, apperror.Wrap(err, apperror.CodeInvalidCursor, "The API key cursor is invalid.")
	}
	var payload apiKeyCursorPayload
	if err := json.Unmarshal(body, &payload); err != nil || payload.Version != 1 {
		return APIKeyCursor{}, apperror.New(apperror.CodeInvalidCursor, "The API key cursor is invalid.")
	}
	organizationID, err := identifier.Parse(payload.OrganizationID)
	if err != nil {
		return APIKeyCursor{}, apperror.New(apperror.CodeInvalidCursor, "The API key cursor is invalid.")
	}
	id, err := identifier.Parse(payload.ID)
	if err != nil {
		return APIKeyCursor{}, apperror.New(apperror.CodeInvalidCursor, "The API key cursor is invalid.")
	}
	createdAt, err := time.Parse(time.RFC3339Nano, payload.CreatedAt)
	if err != nil {
		return APIKeyCursor{}, apperror.New(apperror.CodeInvalidCursor, "The API key cursor is invalid.")
	}
	return APIKeyCursor{OrganizationID: organizationID, CreatedAt: createdAt.UTC(), ID: id}, nil
}

func generateAPIKeyMaterial() (string, string, [32]byte, error) {
	var prefixBytes [9]byte
	if _, err := rand.Read(prefixBytes[:]); err != nil {
		return "", "", [32]byte{}, fmt.Errorf("generate API key prefix: %w", err)
	}
	var secretBytes [32]byte
	if _, err := rand.Read(secretBytes[:]); err != nil {
		return "", "", [32]byte{}, fmt.Errorf("generate API key secret: %w", err)
	}
	prefix := "vos_" + base64.RawURLEncoding.EncodeToString(prefixBytes[:])
	secret := base64.RawURLEncoding.EncodeToString(secretBytes[:])
	return prefix, secret, sha256.Sum256([]byte(secret)), nil
}

func parseAPIKeyToken(token string) (string, string, error) {
	if len(token) < 32 || len(token) > 256 {
		return "", "", ErrAPIKeySecretFormat
	}
	prefix, secret, ok := strings.Cut(token, ".")
	if !ok || len(prefix) != 16 || !strings.HasPrefix(prefix, "vos_") || len(secret) < 40 {
		return "", "", ErrAPIKeySecretFormat
	}
	return prefix, secret, nil
}

func mapAPIKeyMutationError(err error) error {
	switch {
	case errors.Is(err, ErrAPIKeyNotFound):
		return apperror.Wrap(err, apperror.CodeNotFound, "The requested API key does not exist.")
	case errors.Is(err, ErrAPIKeyVersion):
		return apperror.Wrap(err, apperror.CodePreconditionFailed, "The API key changed; reload it and retry.")
	case errors.Is(err, ErrAPIKeyInactive):
		return apperror.Wrap(err, apperror.CodeConflict, "The API key is already inactive.")
	default:
		return err
	}
}

func copyTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := value.UTC()
	return &copy
}

func newAPIKeyIDs() (identifier.ID, identifier.ID, error) {
	auditID, err := identifier.New()
	if err != nil {
		return identifier.ID{}, identifier.ID{}, err
	}
	outboxID, err := identifier.New()
	if err != nil {
		return identifier.ID{}, identifier.ID{}, err
	}
	return auditID, outboxID, nil
}
