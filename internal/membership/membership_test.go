package membership

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/HK9750/venueos/internal/access"
	"github.com/HK9750/venueos/internal/platform/apperror"
	"github.com/HK9750/venueos/internal/platform/clock"
	"github.com/HK9750/venueos/internal/platform/identifier"
)

type fakeRepository struct {
	page         Page
	changed      ChangeRoleRecord
	revoked      RevokeRecord
	changeResult Membership
	revokeResult Membership
	err          error
}

func (repository *fakeRepository) List(context.Context, identifier.ID, int32, *Cursor) (Page, error) {
	return repository.page, repository.err
}
func (repository *fakeRepository) ChangeRole(_ context.Context, record ChangeRoleRecord) (Membership, error) {
	repository.changed = record
	return repository.changeResult, repository.err
}
func (repository *fakeRepository) Revoke(_ context.Context, record RevokeRecord) (Membership, error) {
	repository.revoked = record
	return repository.revokeResult, repository.err
}

func TestRoleChangeRequiresTenantPermissionAndVersion(t *testing.T) {
	organizationID := newID(t)
	repository := &fakeRepository{}
	service := NewService(repository, clock.NewFixed(time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)))
	ctx := tenantContext(t, organizationID, access.RoleAdmin)
	membershipID := newID(t)

	_, err := service.ChangeRole(ctx, ChangeRoleInput{OrganizationID: organizationID, MembershipID: membershipID, Role: access.RoleFinance})
	if err == nil {
		t.Fatal("ChangeRole accepted a missing version")
	}

	updated, err := service.ChangeRole(ctx, ChangeRoleInput{OrganizationID: organizationID, MembershipID: membershipID, Role: access.RoleFinance, Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	if repository.changed.MembershipID != membershipID || repository.changed.Role != access.RoleFinance || updated.ID != repository.changeResult.ID {
		t.Fatalf("change record/result = %#v / %#v", repository.changed, updated)
	}

	otherOrganization := newID(t)
	_, err = service.ChangeRole(ctx, ChangeRoleInput{OrganizationID: otherOrganization, MembershipID: membershipID, Role: access.RoleFinance, Version: 1})
	var classified *apperror.Error
	if err == nil || !errors.As(err, &classified) || classified.Code() != apperror.CodeNotFound {
		t.Fatalf("cross-tenant error = %v", err)
	}
}

func TestLastOwnerAndStaleVersionAreMapped(t *testing.T) {
	organizationID := newID(t)
	repository := &fakeRepository{err: ErrLastOwner}
	service := NewService(repository, clock.System{})
	_, err := service.Revoke(tenantContext(t, organizationID, access.RoleOwner), RevokeInput{
		OrganizationID: organizationID, MembershipID: newID(t), Version: 1,
	})
	if err == nil || !errors.Is(err, ErrLastOwner) {
		t.Fatalf("last owner error = %v", err)
	}

	repository.err = ErrVersionConflict
	_, err = service.ChangeRole(tenantContext(t, organizationID, access.RoleOwner), ChangeRoleInput{
		OrganizationID: organizationID, MembershipID: newID(t), Role: access.RoleAdmin, Version: 1,
	})
	if err == nil || !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale version error = %v", err)
	}
}

func TestCursorRoundTripBindsOrganization(t *testing.T) {
	cursor := Cursor{OrganizationID: newID(t), CreatedAt: time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC), ID: newID(t)}
	encoded, err := EncodeCursor(cursor)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeCursor(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.OrganizationID != cursor.OrganizationID || decoded.ID != cursor.ID || !decoded.CreatedAt.Equal(cursor.CreatedAt) {
		t.Fatalf("decoded cursor = %#v", decoded)
	}
	if _, err := DecodeCursor("not-a-cursor"); err == nil {
		t.Fatal("invalid cursor accepted")
	}
}

func tenantContext(t *testing.T, organizationID identifier.ID, role access.Role) context.Context {
	t.Helper()
	principal, err := access.NewPrincipal(access.PrincipalUser, "issuer|membership-test")
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := access.NewAuthorizationForRole(principal, organizationID, role)
	if err != nil {
		t.Fatal(err)
	}
	return access.WithAuthorization(context.Background(), authorization)
}

func newID(t *testing.T) identifier.ID {
	t.Helper()
	id, err := identifier.New()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
