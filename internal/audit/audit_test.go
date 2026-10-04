package audit

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/HK9750/venueos/internal/access"
	"github.com/HK9750/venueos/internal/platform/identifier"
)

type fakeRepository struct {
	input ListInput
	page  Page
}

func (repository *fakeRepository) ListAuditEntries(_ context.Context, input ListInput) (Page, error) {
	repository.input = input
	return repository.page, nil
}

func testAuthorizationContext(t *testing.T, organizationID identifier.ID, role access.Role) context.Context {
	t.Helper()
	principal, err := access.NewPrincipal(access.PrincipalUser, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := access.NewAuthorizationForRole(principal, organizationID, role)
	if err != nil {
		t.Fatal(err)
	}
	return access.WithAuthorization(context.Background(), authorization)
}

func testID(t *testing.T, value string) identifier.ID {
	t.Helper()
	id, err := identifier.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestListRequiresAuditPermissionAndTenantScope(t *testing.T) {
	organizationID := testID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	repository := &fakeRepository{}
	service := NewService(repository)

	if _, err := service.List(context.Background(), ListInput{OrganizationID: organizationID}); err == nil {
		t.Fatal("List() succeeded without authorization")
	}
	if _, err := service.List(testAuthorizationContext(t, organizationID, access.RoleScanner), ListInput{OrganizationID: organizationID}); err == nil {
		t.Fatal("List() succeeded without audit permission")
	}
	otherOrganization := testID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6")
	if _, err := service.List(testAuthorizationContext(t, organizationID, access.RoleAdmin), ListInput{OrganizationID: otherOrganization}); err == nil {
		t.Fatal("List() succeeded across tenant scope")
	}
}

func TestListBindsCursorToFilters(t *testing.T) {
	organizationID := testID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	service := NewService(&fakeRepository{})
	ctx := testAuthorizationContext(t, organizationID, access.RoleAdmin)
	input := ListInput{OrganizationID: organizationID, Action: "api_key.revoked", Limit: 10}
	cursor := Cursor{OrganizationID: organizationID, ID: testID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6"), OccurredAt: time.Now().UTC(), FilterHash: FilterHash(input)}
	encoded, err := EncodeCursor(cursor)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeCursor(encoded)
	if err != nil {
		t.Fatal(err)
	}
	input.After = &decoded
	if _, err := service.List(ctx, input); err != nil {
		t.Fatalf("List() rejected matching cursor: %v", err)
	}
	input.Action = "organization.suspended"
	if _, err := service.List(ctx, input); err == nil || !strings.Contains(err.Error(), "cursor") {
		t.Fatalf("List() accepted cursor for another filter: %v", err)
	}
}

func TestRedactJSONRemovesCredentialMaterialRecursively(t *testing.T) {
	input := json.RawMessage(`{"api_key":"safe-prefix","secret_hash":"do-not-show","nested":{"access_token":"do-not-show"},"version":2}`)
	output := string(RedactJSON(input))
	if strings.Contains(output, "do-not-show") || !strings.Contains(output, "[REDACTED]") || !strings.Contains(output, "safe-prefix") {
		t.Fatalf("redacted output = %s", output)
	}
}
