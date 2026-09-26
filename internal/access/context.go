// Package access defines provider-neutral authenticated principals and verified
// organization authorization carried through application contexts.
package access

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/HK9750/venueos/internal/platform/identifier"
)

type PrincipalType string

const (
	PrincipalUser     PrincipalType = "user"
	PrincipalAPIKey   PrincipalType = "api_key"
	PrincipalDevice   PrincipalType = "device"
	PrincipalGuest    PrincipalType = "guest"
	PrincipalPlatform PrincipalType = "platform"
)

type Permission string

// Role is one of the fixed launch roles. Custom role builders are deliberately
// outside Phase 1; permissions remain explicit at authorization boundaries.
type Role string

const (
	RoleOwner       Role = "owner"
	RoleAdmin       Role = "admin"
	RoleBoxOffice   Role = "box_office"
	RoleDoorManager Role = "door_manager"
	RoleScanner     Role = "scanner"
	RoleFinance     Role = "finance"
	RoleSupport     Role = "support"
	RoleReadOnly    Role = "read_only"
)

const (
	PermissionOrganizationRead   Permission = "organization.read"
	PermissionOrganizationManage Permission = "organization.manage"
	PermissionMembershipRead     Permission = "membership.read"
	PermissionMembershipManage   Permission = "membership.manage"
	PermissionVenueRead          Permission = "venue.read"
	PermissionVenueManage        Permission = "venue.manage"
	PermissionCatalogRead        Permission = "catalog.read"
	PermissionCatalogManage      Permission = "catalog.manage"
	PermissionCatalogPublish     Permission = "catalog.publish"
	PermissionInventoryRead      Permission = "inventory.read"
	PermissionInventoryManage    Permission = "inventory.manage"
	PermissionOrderRead          Permission = "order.read"
	PermissionOrderCreate        Permission = "order.create"
	PermissionOrderRefund        Permission = "order.refund"
	PermissionOrderComp          Permission = "order.comp"
	PermissionTicketRead         Permission = "ticket.read"
	PermissionTicketResend       Permission = "ticket.resend"
	PermissionTicketVoid         Permission = "ticket.void"
	PermissionEntryScan          Permission = "entry.scan"
	PermissionEntryRead          Permission = "entry.read"
	PermissionEntryOverride      Permission = "entry.override"
	PermissionDeviceManage       Permission = "device.manage"
	PermissionFinanceRead        Permission = "finance.read"
	PermissionReconciliation     Permission = "reconciliation.manage"
	PermissionReportRead         Permission = "report.read"
	PermissionExportCreate       Permission = "export.create"
	PermissionPromotionManage    Permission = "promotion.manage"
	PermissionWaitlistManage     Permission = "waitlist.manage"
	PermissionIntegrationManage  Permission = "integration.manage"
	PermissionWebhookManage      Permission = "webhook.manage"
	PermissionAuditRead          Permission = "audit.read"
	PermissionSupportImpersonate Permission = "support.impersonate"
	PermissionPlatformSupport    Permission = "platform.support"
)

var rolePermissions = map[Role][]Permission{
	RoleOwner: {
		PermissionOrganizationRead, PermissionOrganizationManage,
		PermissionMembershipRead, PermissionMembershipManage,
		PermissionVenueRead, PermissionVenueManage, PermissionCatalogRead,
		PermissionCatalogManage, PermissionCatalogPublish, PermissionInventoryRead,
		PermissionInventoryManage, PermissionOrderRead, PermissionOrderCreate,
		PermissionOrderRefund, PermissionOrderComp, PermissionTicketRead,
		PermissionTicketResend, PermissionTicketVoid, PermissionEntryScan,
		PermissionEntryRead, PermissionEntryOverride, PermissionDeviceManage,
		PermissionFinanceRead, PermissionReconciliation, PermissionReportRead,
		PermissionExportCreate, PermissionPromotionManage, PermissionWaitlistManage,
		PermissionIntegrationManage, PermissionWebhookManage, PermissionAuditRead,
	},
	RoleAdmin: {
		PermissionOrganizationRead, PermissionOrganizationManage,
		PermissionMembershipRead, PermissionMembershipManage,
		PermissionVenueRead, PermissionVenueManage, PermissionCatalogRead,
		PermissionCatalogManage, PermissionCatalogPublish, PermissionInventoryRead,
		PermissionInventoryManage, PermissionOrderRead, PermissionOrderCreate,
		PermissionOrderRefund, PermissionOrderComp, PermissionTicketRead,
		PermissionTicketResend, PermissionTicketVoid, PermissionEntryScan,
		PermissionEntryRead, PermissionEntryOverride, PermissionDeviceManage,
		PermissionFinanceRead, PermissionReconciliation, PermissionReportRead,
		PermissionExportCreate, PermissionPromotionManage, PermissionWaitlistManage,
		PermissionIntegrationManage, PermissionWebhookManage, PermissionAuditRead,
	},
	RoleBoxOffice: {
		PermissionOrganizationRead, PermissionVenueRead, PermissionCatalogRead,
		PermissionInventoryRead, PermissionOrderRead, PermissionOrderCreate,
		PermissionOrderComp, PermissionTicketRead, PermissionTicketResend,
		PermissionEntryRead,
	},
	RoleDoorManager: {
		PermissionOrganizationRead, PermissionVenueRead, PermissionCatalogRead,
		PermissionInventoryRead, PermissionTicketRead, PermissionEntryScan,
		PermissionEntryRead, PermissionEntryOverride, PermissionDeviceManage,
	},
	RoleScanner: {
		PermissionOrganizationRead, PermissionVenueRead, PermissionCatalogRead,
		PermissionTicketRead, PermissionEntryScan,
	},
	RoleFinance: {
		PermissionOrganizationRead, PermissionOrderRead, PermissionOrderRefund,
		PermissionFinanceRead, PermissionReconciliation, PermissionReportRead,
		PermissionExportCreate,
	},
	RoleSupport: {
		PermissionOrganizationRead, PermissionVenueRead, PermissionCatalogRead,
		PermissionInventoryRead, PermissionOrderRead, PermissionTicketRead,
		PermissionEntryRead, PermissionAuditRead,
	},
	RoleReadOnly: {
		PermissionOrganizationRead, PermissionVenueRead, PermissionCatalogRead,
		PermissionInventoryRead, PermissionOrderRead, PermissionTicketRead,
		PermissionEntryRead, PermissionReportRead, PermissionAuditRead,
	},
}

func (role Role) Valid() bool {
	_, ok := rolePermissions[role]
	return ok
}

// PermissionsForRole returns a defensive copy so callers cannot mutate the
// centralized role policy.
func PermissionsForRole(role Role) []Permission {
	return append([]Permission(nil), rolePermissions[role]...)
}

// NewAuthorizationForRole builds the verified tenant scope produced by a
// membership resolver. Authentication and membership adapters own the decision
// to call this function; request headers must never supply the role.
func NewAuthorizationForRole(principal Principal, organizationID identifier.ID, role Role) (Authorization, error) {
	if !role.Valid() {
		return Authorization{}, fmt.Errorf("unknown role %q", role)
	}
	return NewAuthorization(principal, organizationID, PermissionsForRole(role)...)
}

var (
	ErrUnauthenticated = errors.New("authenticated principal is required")
	ErrUnauthorized    = errors.New("verified organization authorization is required")
)

type Principal struct {
	typeName PrincipalType
	id       string
}

func NewPrincipal(typeName PrincipalType, id string) (Principal, error) {
	id = strings.TrimSpace(id)
	if !validPrincipalType(typeName) || len(id) == 0 || len(id) > 200 || !utf8.ValidString(id) || containsControl(id) {
		return Principal{}, errors.New("valid principal type and bounded identifier are required")
	}
	return Principal{typeName: typeName, id: id}, nil
}

func (principal Principal) Type() PrincipalType { return principal.typeName }
func (principal Principal) ID() string          { return principal.id }

func (principal Principal) IsZero() bool { return principal.typeName == "" || principal.id == "" }

type Authorization struct {
	principal      Principal
	organizationID identifier.ID
	permissions    map[Permission]struct{}
}

type PlatformAuthorization struct {
	principal   Principal
	permissions map[Permission]struct{}
}

func NewAuthorization(principal Principal, organizationID identifier.ID, permissions ...Permission) (Authorization, error) {
	if principal.IsZero() || organizationID.IsZero() {
		return Authorization{}, errors.New("principal and organization ID are required")
	}
	grants := make(map[Permission]struct{}, len(permissions))
	for _, permission := range permissions {
		if !validPermission(permission) {
			return Authorization{}, fmt.Errorf("unknown permission %q", permission)
		}
		grants[permission] = struct{}{}
	}
	return Authorization{principal: principal, organizationID: organizationID, permissions: grants}, nil
}

func (authorization Authorization) Principal() Principal { return authorization.principal }
func (authorization Authorization) OrganizationID() identifier.ID {
	return authorization.organizationID
}
func (authorization Authorization) Has(permission Permission) bool {
	_, ok := authorization.permissions[permission]
	return ok
}

func NewPlatformAuthorization(principal Principal, permissions ...Permission) (PlatformAuthorization, error) {
	if principal.IsZero() || principal.Type() != PrincipalPlatform {
		return PlatformAuthorization{}, errors.New("platform principal is required")
	}
	grants := make(map[Permission]struct{}, len(permissions))
	for _, permission := range permissions {
		if !validPermission(permission) {
			return PlatformAuthorization{}, fmt.Errorf("unknown permission %q", permission)
		}
		grants[permission] = struct{}{}
	}
	return PlatformAuthorization{principal: principal, permissions: grants}, nil
}

func (authorization PlatformAuthorization) Principal() Principal { return authorization.principal }
func (authorization PlatformAuthorization) Has(permission Permission) bool {
	_, ok := authorization.permissions[permission]
	return ok
}

type principalContextKey struct{}
type authorizationContextKey struct{}
type platformAuthorizationContextKey struct{}

func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	if principal.IsZero() {
		return ctx
	}
	return context.WithValue(ctx, principalContextKey{}, principal)
}

func PrincipalFromContext(ctx context.Context) (Principal, error) {
	principal, ok := ctx.Value(principalContextKey{}).(Principal)
	if !ok || principal.IsZero() {
		return Principal{}, ErrUnauthenticated
	}
	return principal, nil
}

func WithAuthorization(ctx context.Context, authorization Authorization) context.Context {
	if authorization.principal.IsZero() || authorization.organizationID.IsZero() {
		return ctx
	}
	ctx = WithPrincipal(ctx, authorization.principal)
	return context.WithValue(ctx, authorizationContextKey{}, authorization)
}

func AuthorizationFromContext(ctx context.Context) (Authorization, error) {
	authorization, ok := ctx.Value(authorizationContextKey{}).(Authorization)
	if !ok || authorization.principal.IsZero() || authorization.organizationID.IsZero() {
		return Authorization{}, ErrUnauthorized
	}
	return authorization, nil
}

func Require(ctx context.Context, permission Permission) (Authorization, error) {
	principal, err := PrincipalFromContext(ctx)
	if err != nil {
		return Authorization{}, err
	}
	authorization, err := AuthorizationFromContext(ctx)
	if err != nil {
		return Authorization{}, err
	}
	if authorization.Principal() != principal {
		return Authorization{}, fmt.Errorf("%w: principal context does not match authorization", ErrUnauthorized)
	}
	if !authorization.Has(permission) {
		return Authorization{}, fmt.Errorf("%w: %s", ErrUnauthorized, permission)
	}
	return authorization, nil
}

func WithPlatformAuthorization(ctx context.Context, authorization PlatformAuthorization) context.Context {
	if authorization.principal.IsZero() {
		return ctx
	}
	ctx = WithPrincipal(ctx, authorization.principal)
	return context.WithValue(ctx, platformAuthorizationContextKey{}, authorization)
}

func RequirePlatform(ctx context.Context, permission Permission) (PlatformAuthorization, error) {
	principal, err := PrincipalFromContext(ctx)
	if err != nil {
		return PlatformAuthorization{}, err
	}
	authorization, ok := ctx.Value(platformAuthorizationContextKey{}).(PlatformAuthorization)
	if !ok || authorization.principal.IsZero() || authorization.Principal() != principal || !authorization.Has(permission) {
		return PlatformAuthorization{}, fmt.Errorf("%w: %s", ErrUnauthorized, permission)
	}
	return authorization, nil
}

func validPrincipalType(value PrincipalType) bool {
	return value == PrincipalUser || value == PrincipalAPIKey || value == PrincipalDevice ||
		value == PrincipalGuest || value == PrincipalPlatform
}

func validPermission(value Permission) bool {
	switch value {
	case PermissionOrganizationRead, PermissionOrganizationManage,
		PermissionMembershipRead, PermissionMembershipManage,
		PermissionVenueRead, PermissionVenueManage, PermissionCatalogRead,
		PermissionCatalogManage, PermissionCatalogPublish, PermissionInventoryRead,
		PermissionInventoryManage, PermissionOrderRead, PermissionOrderCreate,
		PermissionOrderRefund, PermissionOrderComp, PermissionTicketRead,
		PermissionTicketResend, PermissionTicketVoid, PermissionEntryScan,
		PermissionEntryRead, PermissionEntryOverride, PermissionDeviceManage,
		PermissionFinanceRead, PermissionReconciliation, PermissionReportRead,
		PermissionExportCreate, PermissionPromotionManage, PermissionWaitlistManage,
		PermissionIntegrationManage, PermissionWebhookManage, PermissionAuditRead,
		PermissionSupportImpersonate, PermissionPlatformSupport:
		return true
	default:
		return false
	}
}

func containsControl(value string) bool {
	return strings.IndexFunc(value, unicode.IsControl) >= 0
}
