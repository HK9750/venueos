package access

import (
	"context"
	"testing"
	"time"

	"github.com/HK9750/venueos/internal/platform/clock"
	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/stretchr/testify/require"
)

func TestAPIKeyServiceCreatesShowOnceCredentialAndAuthenticates(t *testing.T) {
	organizationID := newAccessTestID(t)
	principal, err := NewPrincipal(PrincipalUser, "user-1")
	require.NoError(t, err)
	authorization, err := NewAuthorizationForRole(principal, organizationID, RoleOwner)
	require.NoError(t, err)
	ctx := WithAuthorization(context.Background(), authorization)
	instant := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	repository := &apiKeyRepositoryFake{}
	service := NewAPIKeyService(repository, clock.NewFixed(instant))
	expiresAt := instant.Add(24 * time.Hour)

	issued, err := service.Create(ctx, CreateAPIKeyInput{
		OrganizationID: organizationID,
		Name:           "Reporting integration",
		Scopes:         []Permission{PermissionReportRead, PermissionInventoryRead},
		ExpiresAt:      &expiresAt,
	})
	require.NoError(t, err)
	require.NotEmpty(t, issued.Token)
	require.NotContains(t, issued.APIKey.Name, issued.Token)
	require.Equal(t, organizationID, issued.APIKey.OrganizationID)
	require.Equal(t, int64(1), issued.APIKey.Version)
	require.NotEqual(t, [32]byte{}, repository.created.SecretHash)
	require.NotContains(t, string(repository.created.SecretHash[:]), issued.Token)

	verified, key, err := service.Authenticate(context.Background(), issued.Token)
	require.NoError(t, err)
	require.Equal(t, issued.APIKey.ID, key.ID)
	require.Equal(t, organizationID, verified.OrganizationID())
	require.True(t, verified.Has(PermissionReportRead))
	require.False(t, verified.Has(PermissionOrderRefund))
	require.Equal(t, 1, repository.touchCount)
}

func TestAPIKeyServiceRejectsUnusableCredentialsAndScopes(t *testing.T) {
	organizationID := newAccessTestID(t)
	principal, err := NewPrincipal(PrincipalUser, "user-2")
	require.NoError(t, err)
	authorization, err := NewAuthorizationForRole(principal, organizationID, RoleReadOnly)
	require.NoError(t, err)
	ctx := WithAuthorization(context.Background(), authorization)
	service := NewAPIKeyService(&apiKeyRepositoryFake{}, clock.NewFixed(time.Now().UTC()))

	_, err = service.Create(ctx, CreateAPIKeyInput{
		OrganizationID: organizationID,
		Name:           "Not allowed",
		Scopes:         []Permission{PermissionReportRead},
	})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrUnauthorized)

	owner, err := NewPrincipal(PrincipalUser, "user-3")
	require.NoError(t, err)
	ownerAuthorization, err := NewAuthorizationForRole(owner, organizationID, RoleOwner)
	require.NoError(t, err)
	ownerContext := WithAuthorization(context.Background(), ownerAuthorization)
	_, err = service.Create(ownerContext, CreateAPIKeyInput{
		OrganizationID: organizationID,
		Name:           "Duplicate scopes",
		Scopes:         []Permission{PermissionReportRead, PermissionReportRead},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "validation_failed")
}

func TestAPIKeyServiceRevokeUsesVersionAndStopsAuthentication(t *testing.T) {
	organizationID := newAccessTestID(t)
	principal, err := NewPrincipal(PrincipalUser, "user-4")
	require.NoError(t, err)
	authorization, err := NewAuthorizationForRole(principal, organizationID, RoleOwner)
	require.NoError(t, err)
	ctx := WithAuthorization(context.Background(), authorization)
	instant := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	repository := &apiKeyRepositoryFake{}
	service := NewAPIKeyService(repository, clock.NewFixed(instant))
	issued, err := service.Create(ctx, CreateAPIKeyInput{OrganizationID: organizationID, Name: "Revocable", Scopes: []Permission{PermissionReportRead}})
	require.NoError(t, err)

	revoked, err := service.Revoke(ctx, RevokeAPIKeyInput{OrganizationID: organizationID, APIKeyID: issued.APIKey.ID, ExpectedVersion: 1})
	require.NoError(t, err)
	require.NotNil(t, revoked.RevokedAt)
	require.Equal(t, int64(2), revoked.Version)
	_, _, err = service.Authenticate(context.Background(), issued.Token)
	require.ErrorIs(t, err, ErrAPIKeyInactive)

	_, err = service.Revoke(ctx, RevokeAPIKeyInput{OrganizationID: organizationID, APIKeyID: issued.APIKey.ID, ExpectedVersion: 1})
	require.Error(t, err)
	require.Contains(t, err.Error(), "inactive")
}

func TestAPIKeyServiceRotatesImmediatelyWithoutOverlap(t *testing.T) {
	organizationID := newAccessTestID(t)
	principal, err := NewPrincipal(PrincipalUser, "user-5")
	require.NoError(t, err)
	authorization, err := NewAuthorizationForRole(principal, organizationID, RoleOwner)
	require.NoError(t, err)
	ctx := WithAuthorization(context.Background(), authorization)
	instant := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	repository := &apiKeyRepositoryFake{}
	service := NewAPIKeyService(repository, clock.NewFixed(instant))
	issued, err := service.Create(ctx, CreateAPIKeyInput{OrganizationID: organizationID, Name: "Rotating", Scopes: []Permission{PermissionReportRead}})
	require.NoError(t, err)

	rotated, err := service.Rotate(ctx, RotateAPIKeyInput{OrganizationID: organizationID, APIKeyID: issued.APIKey.ID, ExpectedVersion: 1})
	require.NoError(t, err)
	require.NotEqual(t, issued.APIKey.ID, rotated.APIKey.ID)
	require.NotEqual(t, issued.Token, rotated.Token)
	_, _, err = service.Authenticate(context.Background(), issued.Token)
	require.ErrorIs(t, err, ErrAPIKeyInactive)
	verified, key, err := service.Authenticate(context.Background(), rotated.Token)
	require.NoError(t, err)
	require.Equal(t, rotated.APIKey.ID, key.ID)
	require.Equal(t, organizationID, verified.OrganizationID())
}

type apiKeyRepositoryFake struct {
	created    CreateAPIKeyRecord
	stored     StoredAPIKey
	keys       map[string]StoredAPIKey
	touchCount int
}

func (fake *apiKeyRepositoryFake) CreateAPIKey(_ context.Context, record CreateAPIKeyRecord) (APIKey, error) {
	fake.created = record
	fake.stored = StoredAPIKey{APIKey: record.APIKey, SecretHash: record.SecretHash}
	if fake.keys == nil {
		fake.keys = make(map[string]StoredAPIKey)
	}
	fake.keys[record.APIKey.Prefix] = fake.stored
	return record.APIKey, nil
}

func (fake *apiKeyRepositoryFake) RevokeAPIKey(_ context.Context, record RevokeAPIKeyRecord) (APIKey, error) {
	current, found := fake.keyByID(record.APIKeyID)
	if !found {
		return APIKey{}, ErrAPIKeyNotFound
	}
	if current.RevokedAt != nil {
		return APIKey{}, ErrAPIKeyInactive
	}
	if current.Version != record.ExpectedVersion {
		return APIKey{}, ErrAPIKeyVersion
	}
	revokedAt := record.RevokedAt
	current.RevokedAt = &revokedAt
	current.Version++
	current.UpdatedAt = record.RevokedAt
	fake.keys[current.Prefix] = current
	fake.stored = current
	return current.APIKey, nil
}

func (fake *apiKeyRepositoryFake) GetAPIKey(_ context.Context, organizationID, keyID identifier.ID) (APIKey, error) {
	current, found := fake.keyByID(keyID)
	if !found || current.OrganizationID != organizationID {
		return APIKey{}, ErrAPIKeyNotFound
	}
	return current.APIKey, nil
}

func (fake *apiKeyRepositoryFake) RotateAPIKey(_ context.Context, record RotateAPIKeyRecord) (APIKey, error) {
	current, found := fake.keyByID(record.APIKeyID)
	if !found {
		return APIKey{}, ErrAPIKeyNotFound
	}
	if current.RevokedAt != nil {
		return APIKey{}, ErrAPIKeyInactive
	}
	if current.Version != record.ExpectedVersion {
		return APIKey{}, ErrAPIKeyVersion
	}
	revokedAt := record.RotatedAt
	current.RevokedAt = &revokedAt
	current.Version++
	current.UpdatedAt = record.RotatedAt
	fake.keys[current.Prefix] = current
	replacement := StoredAPIKey{APIKey: record.Replacement, SecretHash: record.SecretHash}
	fake.keys[replacement.Prefix] = replacement
	fake.stored = replacement
	return replacement.APIKey, nil
}

func (fake *apiKeyRepositoryFake) LookupAPIKey(_ context.Context, prefix string) (StoredAPIKey, error) {
	stored, found := fake.keys[prefix]
	if !found {
		return StoredAPIKey{}, ErrAPIKeyNotFound
	}
	return stored, nil
}

func (fake *apiKeyRepositoryFake) TouchAPIKey(_ context.Context, id identifier.ID, at time.Time) error {
	stored, found := fake.keyByID(id)
	if !found || stored.RevokedAt != nil {
		return ErrAPIKeyInactive
	}
	fake.touchCount++
	stored.LastUsedAt = &at
	fake.keys[stored.Prefix] = stored
	fake.stored = stored
	return nil
}

func (fake *apiKeyRepositoryFake) keyByID(id identifier.ID) (StoredAPIKey, bool) {
	for _, stored := range fake.keys {
		if stored.ID == id {
			return stored, true
		}
	}
	return StoredAPIKey{}, false
}

func newAccessTestID(t *testing.T) identifier.ID {
	t.Helper()
	id, err := identifier.New()
	require.NoError(t, err)
	return id
}
