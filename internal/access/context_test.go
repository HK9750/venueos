package access

import (
	"context"
	"errors"
	"testing"

	"github.com/HK9750/venueos/internal/platform/identifier"
)

func TestPrincipalAndAuthorizationContext(t *testing.T) {
	principal, err := NewPrincipal(PrincipalUser, "issuer|subject")
	if err != nil {
		t.Fatal(err)
	}
	organizationID, err := identifier.New()
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := NewAuthorization(principal, organizationID, PermissionOrganizationRead)
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithAuthorization(context.Background(), authorization)

	actual, err := Require(ctx, PermissionOrganizationRead)
	if err != nil {
		t.Fatal(err)
	}
	if actual.OrganizationID() != organizationID || actual.Principal().ID() != principal.ID() {
		t.Fatalf("authorization = %#v", actual)
	}
	if _, err := Require(ctx, PermissionOrganizationManage); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Require() error = %v, want unauthorized", err)
	}
}

func TestContextRejectsMissingOrInvalidValues(t *testing.T) {
	if _, err := PrincipalFromContext(context.Background()); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("PrincipalFromContext() error = %v", err)
	}
	if _, err := AuthorizationFromContext(context.Background()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("AuthorizationFromContext() error = %v", err)
	}
	if _, err := NewPrincipal("unknown", "subject"); err == nil {
		t.Fatal("NewPrincipal() accepted an unknown type")
	}
	principal, err := NewPrincipal(PrincipalUser, "subject")
	if err != nil {
		t.Fatal(err)
	}
	organizationID, err := identifier.New()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewAuthorization(principal, organizationID, "unknown.permission"); err == nil {
		t.Fatal("NewAuthorization() accepted an unknown permission")
	}
}

func TestPlatformAuthorization(t *testing.T) {
	principal, err := NewPrincipal(PrincipalPlatform, "onboarding-service")
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := NewPlatformAuthorization(principal, PermissionPlatformSupport)
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithPlatformAuthorization(context.Background(), authorization)
	if _, err := RequirePlatform(ctx, PermissionPlatformSupport); err != nil {
		t.Fatal(err)
	}
	if _, err := RequirePlatform(ctx, PermissionAuditRead); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("RequirePlatform() error = %v", err)
	}
}

func TestAuthorizationCannotBeReboundToAnotherPrincipal(t *testing.T) {
	organizationID, err := identifier.New()
	if err != nil {
		t.Fatal(err)
	}
	first, err := NewPrincipal(PrincipalUser, "issuer|first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewPrincipal(PrincipalUser, "issuer|second")
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := NewAuthorization(first, organizationID, PermissionOrganizationRead)
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithPrincipal(WithAuthorization(context.Background(), authorization), second)
	if _, err := Require(ctx, PermissionOrganizationRead); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Require() error = %v, want unauthorized", err)
	}

	platformPrincipal, err := NewPrincipal(PrincipalPlatform, "onboarding-service")
	if err != nil {
		t.Fatal(err)
	}
	otherPlatformPrincipal, err := NewPrincipal(PrincipalPlatform, "other-service")
	if err != nil {
		t.Fatal(err)
	}
	platformAuthorization, err := NewPlatformAuthorization(platformPrincipal, PermissionPlatformSupport)
	if err != nil {
		t.Fatal(err)
	}
	ctx = WithPrincipal(WithPlatformAuthorization(context.Background(), platformAuthorization), otherPlatformPrincipal)
	if _, err := RequirePlatform(ctx, PermissionPlatformSupport); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("RequirePlatform() error = %v, want unauthorized", err)
	}
}

func TestRolePermissionMatrix(t *testing.T) {
	organizationID, err := identifier.New()
	if err != nil {
		t.Fatal(err)
	}
	principal, err := NewPrincipal(PrincipalUser, "issuer|role-test")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		role  Role
		allow Permission
		deny  Permission
	}{
		{RoleOwner, PermissionMembershipManage, PermissionPlatformSupport},
		{RoleAdmin, PermissionCatalogPublish, PermissionPlatformSupport},
		{RoleBoxOffice, PermissionOrderCreate, PermissionOrderRefund},
		{RoleDoorManager, PermissionEntryOverride, PermissionOrderRefund},
		{RoleScanner, PermissionEntryScan, PermissionEntryOverride},
		{RoleFinance, PermissionOrderRefund, PermissionEntryScan},
		{RoleSupport, PermissionAuditRead, PermissionMembershipManage},
		{RoleReadOnly, PermissionReportRead, PermissionOrderCreate},
	}
	for _, test := range tests {
		t.Run(string(test.role), func(t *testing.T) {
			authorization, err := NewAuthorizationForRole(principal, organizationID, test.role)
			if err != nil {
				t.Fatal(err)
			}
			if !authorization.Has(test.allow) || authorization.Has(test.deny) {
				t.Fatalf("role %s permissions: allow=%v deny=%v", test.role, test.allow, test.deny)
			}
		})
	}
	if _, err := NewAuthorizationForRole(principal, organizationID, "unknown"); err == nil {
		t.Fatal("unknown role was accepted")
	}
}
