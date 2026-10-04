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

	"github.com/HK9750/venueos/internal/audit"
	"github.com/HK9750/venueos/internal/config"
	"github.com/HK9750/venueos/internal/observability"
)

type stubAudit struct {
	input audit.ListInput
	page  audit.Page
}

func (service *stubAudit) List(_ context.Context, input audit.ListInput) (audit.Page, error) {
	service.input = input
	return service.page, nil
}

func TestAuditListRouteReturnsRedactedBoundedEntries(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	organizationID := mustAuthID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	entryID := mustAuthID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6")
	service := &stubAudit{}
	service.page = audit.Page{
		Items: []audit.Entry{{
			ID: entryID, OrganizationID: organizationID, ActorType: "user", ActorID: "user-1",
			Action: "api_key.revoked", SubjectType: "api_key", SubjectID: "key-1", Result: "success",
			BeforeData: []byte(`{"secret_hash":"must-not-escape","version":1}`),
			AfterData:  []byte(`{"revoked":true}`), OccurredAt: time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC),
		}},
		NextCursor: &audit.Cursor{OrganizationID: organizationID, ID: entryID, OccurredAt: time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC), FilterHash: audit.FilterHash(audit.ListInput{OrganizationID: organizationID, Action: "api_key.revoked", Limit: 10})},
	}
	server := NewServer(stubUsers{}, ready{}, logger).WithAudit(service)
	handler := NewHandler(server, config.Telemetry{}, logger, observability.NewMetrics())
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/organizations/"+organizationID.String()+"/audit-entries?limit=10&action=api_key.revoked", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(body, `"action":"api_key.revoked"`) || !strings.Contains(body, `"next_cursor"`) {
		t.Fatalf("status/body = %d/%s", response.Code, body)
	}
	if strings.Contains(body, "must-not-escape") || !strings.Contains(body, "[REDACTED]") {
		t.Fatalf("audit secret was not redacted: %s", body)
	}
	if service.input.OrganizationID != organizationID || service.input.Limit != 10 || service.input.Action != "api_key.revoked" {
		t.Fatalf("service input = %#v", service.input)
	}
}

func TestAuditListRouteRejectsInvalidCursor(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	organizationID := mustAuthID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	server := NewServer(stubUsers{}, ready{}, logger).WithAudit(&stubAudit{})
	handler := NewHandler(server, config.Telemetry{}, logger, observability.NewMetrics())
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/organizations/"+organizationID.String()+"/audit-entries?cursor=not-a-cursor", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"invalid_cursor"`) {
		t.Fatalf("status/body = %d/%s", response.Code, response.Body.String())
	}
}

var _ AuditService = (*stubAudit)(nil)
