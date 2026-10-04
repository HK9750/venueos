package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/HK9750/venueos/internal/access"
	"github.com/HK9750/venueos/internal/config"
	"github.com/HK9750/venueos/internal/observability"
	"github.com/HK9750/venueos/internal/platform/identifier"
)

type stubAPIKeys struct {
	page         access.APIKeyPage
	organization identifier.ID
	limit        int32
	after        *access.APIKeyCursor
	revoked      access.RevokeAPIKeyInput
	revokeResult access.APIKey
	created      access.CreateAPIKeyInput
	rotated      access.RotateAPIKeyInput
	issued       access.IssuedAPIKey
}

func (service *stubAPIKeys) List(_ context.Context, organizationID identifier.ID, limit int32, after *access.APIKeyCursor) (access.APIKeyPage, error) {
	service.organization = organizationID
	service.limit = limit
	service.after = after
	return service.page, nil
}

func (service *stubAPIKeys) Revoke(_ context.Context, input access.RevokeAPIKeyInput) (access.APIKey, error) {
	service.revoked = input
	return service.revokeResult, nil
}

func (service *stubAPIKeys) Create(_ context.Context, input access.CreateAPIKeyInput) (access.IssuedAPIKey, error) {
	service.created = input
	return service.issued, nil
}

func (service *stubAPIKeys) Rotate(_ context.Context, input access.RotateAPIKeyInput) (access.IssuedAPIKey, error) {
	service.rotated = input
	return service.issued, nil
}

func TestAPIKeyListRouteReturnsMetadataAndOpaqueCursor(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	organizationID := mustAuthID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	keyID := mustAuthID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6")
	service := &stubAPIKeys{page: access.APIKeyPage{
		Items:      []access.APIKey{{ID: keyID, OrganizationID: organizationID, Prefix: "vos_123456789abc", Name: "Reporting", Scopes: []access.Permission{access.PermissionReportRead}, Version: 1, CreatedAt: time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)}},
		NextCursor: &access.APIKeyCursor{OrganizationID: organizationID, ID: keyID, CreatedAt: time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)},
	}}
	server := NewServer(stubUsers{}, ready{}, logger).WithAPIKeys(service)
	handler := NewHandler(server, config.Telemetry{}, logger, observability.NewMetrics())
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/organizations/"+organizationID.String()+"/api-keys?limit=10", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"prefix":"vos_123456789abc"`) || !strings.Contains(response.Body.String(), `"next_cursor"`) {
		t.Fatalf("status/body = %d/%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "secret_hash") || strings.Contains(response.Body.String(), "token") {
		t.Fatalf("secret material leaked in response: %s", response.Body.String())
	}
	if service.organization != organizationID || service.limit != 10 || service.after != nil {
		t.Fatalf("service input = organization %s, limit %d, after %#v", service.organization, service.limit, service.after)
	}
}

func TestAPIKeyListRouteRejectsInvalidCursor(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	organizationID := mustAuthID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	server := NewServer(stubUsers{}, ready{}, logger).WithAPIKeys(&stubAPIKeys{})
	handler := NewHandler(server, config.Telemetry{}, logger, observability.NewMetrics())
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/organizations/"+organizationID.String()+"/api-keys?cursor=not-a-cursor", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"invalid_cursor"`) {
		t.Fatalf("status/body = %d/%s", response.Code, response.Body.String())
	}
}

func TestAPIKeyRevokeRouteRequiresVersionAndReturnsCurrentETag(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	organizationID := mustAuthID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	keyID := mustAuthID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6")
	service := &stubAPIKeys{revokeResult: access.APIKey{ID: keyID, OrganizationID: organizationID, Version: 2}}
	server := NewServer(stubUsers{}, ready{}, logger).WithAPIKeys(service)
	handler := NewHandler(server, config.Telemetry{}, logger, observability.NewMetrics())
	request := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/v1/organizations/"+organizationID.String()+"/api-keys/"+keyID.String(), nil)
	request.Header.Set("If-Match", `"1"`)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || response.Header().Get("ETag") != `"2"` {
		t.Fatalf("status/etag = %d/%q", response.Code, response.Header().Get("ETag"))
	}
	if service.revoked.OrganizationID != organizationID || service.revoked.APIKeyID != keyID || service.revoked.ExpectedVersion != 1 {
		t.Fatalf("service input = %#v", service.revoked)
	}
}

func TestAPIKeyRevokeRouteRejectsMalformedIfMatch(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	organizationID := mustAuthID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	keyID := mustAuthID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6")
	server := NewServer(stubUsers{}, ready{}, logger).WithAPIKeys(&stubAPIKeys{})
	handler := NewHandler(server, config.Telemetry{}, logger, observability.NewMetrics())
	request := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/v1/organizations/"+organizationID.String()+"/api-keys/"+keyID.String(), nil)
	request.Header.Set("If-Match", "not-a-version")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"invalid_request"`) {
		t.Fatalf("status/body = %d/%s", response.Code, response.Body.String())
	}
}

func TestAPIKeyCreateRouteReturnsSecretExactlyOnce(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	organizationID := mustAuthID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	keyID := mustAuthID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6")
	expiry := time.Date(2026, 12, 3, 12, 0, 0, 0, time.UTC)
	service := &stubAPIKeys{issued: access.IssuedAPIKey{APIKey: access.APIKey{ID: keyID, OrganizationID: organizationID, Prefix: "vos_123456789abc", Name: "Reporting", Scopes: []access.Permission{access.PermissionReportRead}, Version: 1}, Token: "vos_123456789abc.secret"}}
	server := NewServer(stubUsers{}, ready{}, logger).WithAPIKeys(service)
	handler := NewHandler(server, config.Telemetry{}, logger, observability.NewMetrics())
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/organizations/"+organizationID.String()+"/api-keys", strings.NewReader(`{"name":"Reporting","scopes":["report.read"],"expires_at":"2026-12-03T12:00:00Z"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || response.Header().Get("Location") == "" || response.Header().Get("ETag") != `"1"` || !strings.Contains(response.Body.String(), `"token":"vos_123456789abc.secret"`) {
		t.Fatalf("status/headers/body = %d/%v/%s", response.Code, response.Header(), response.Body.String())
	}
	if service.created.OrganizationID != organizationID || service.created.Name != "Reporting" || len(service.created.Scopes) != 1 || service.created.Scopes[0] != access.PermissionReportRead || service.created.ExpiresAt == nil || !service.created.ExpiresAt.Equal(expiry) {
		t.Fatalf("create input = %#v", service.created)
	}
}

func TestAPIKeyRotateRouteUsesIfMatchAndAllowsEmptyBody(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	organizationID := mustAuthID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	keyID := mustAuthID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6")
	service := &stubAPIKeys{issued: access.IssuedAPIKey{APIKey: access.APIKey{ID: keyID, OrganizationID: organizationID, Version: 1}, Token: "vos_123456789abc.replacement"}}
	server := NewServer(stubUsers{}, ready{}, logger).WithAPIKeys(service)
	handler := NewHandler(server, config.Telemetry{}, logger, observability.NewMetrics())
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/organizations/"+organizationID.String()+"/api-keys/"+keyID.String()+"/rotate", nil)
	request.Header.Set("If-Match", `"4"`)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"token":"vos_123456789abc.replacement"`) {
		t.Fatalf("status/body = %d/%s", response.Code, response.Body.String())
	}
	if service.rotated.OrganizationID != organizationID || service.rotated.APIKeyID != keyID || service.rotated.ExpectedVersion != 4 {
		t.Fatalf("rotate input = %#v", service.rotated)
	}
}

var _ APIKeyCommandService = (*stubAPIKeys)(nil)
