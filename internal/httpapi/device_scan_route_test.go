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
	"github.com/HK9750/venueos/internal/entry"
	"github.com/HK9750/venueos/internal/observability"
)

type stubEntryScan struct {
	input  entry.ScanInput
	result entry.ScanResult
}

func (service *stubEntryScan) Summary(context.Context, entry.SummaryInput) (entry.Summary, error) {
	return entry.Summary{}, nil
}

func (service *stubEntryScan) Scan(_ context.Context, input entry.ScanInput) (entry.ScanResult, error) {
	service.input = input
	return service.result, nil
}

func TestRecordDeviceScanRouteUsesAuthenticatedOrganizationAndMapsResult(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	organizationID := mustAuthID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	deviceID := mustAuthID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6")
	ticketID := mustAuthID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef7")
	admissionID := mustAuthID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef8")
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	principal, err := access.NewPrincipal(access.PrincipalDevice, deviceID.String())
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := access.NewAuthorization(principal, organizationID, access.PermissionEntryScan)
	if err != nil {
		t.Fatal(err)
	}
	service := &stubEntryScan{result: entry.ScanResult{ScanAttemptID: mustAuthID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef9"), AdmissionID: &admissionID, Decision: entry.Decision{Code: entry.ResultAdmitted, Admittable: true, TicketID: ticketID}, Replay: true}}
	server := NewServer(stubUsers{}, ready{}, logger).WithEntry(service)
	handler := NewHandler(server, config.Telemetry{}, logger, observability.NewMetrics())
	request := httptest.NewRequestWithContext(access.WithAuthorization(context.Background(), authorization), http.MethodPost, "/v1/device/scans", strings.NewReader(`{"device_scan_id":"scan-0001","credential":"vos-ticket.v1.opaque","scanned_at":"2026-10-03T11:59:59Z","metadata":{"source":"door"}}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"decision":"admitted"`) || !strings.Contains(response.Body.String(), `"replay":true`) {
		t.Fatalf("status/body = %d/%s", response.Code, response.Body.String())
	}
	if service.input.OrganizationID != organizationID || service.input.DeviceScanID != "scan-0001" || service.input.Credential != "vos-ticket.v1.opaque" || !service.input.ScannedAt.Equal(now.Add(-time.Second)) || string(service.input.Metadata) != `{"source":"door"}` {
		t.Fatalf("service input = %#v", service.input)
	}
}

func TestRecordDeviceScanRouteRejectsMissingCredential(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	organizationID := mustAuthID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	deviceID := mustAuthID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6")
	principal, err := access.NewPrincipal(access.PrincipalDevice, deviceID.String())
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := access.NewAuthorization(principal, organizationID, access.PermissionEntryScan)
	if err != nil {
		t.Fatal(err)
	}
	service := &stubEntryScan{}
	server := NewServer(stubUsers{}, ready{}, logger).WithEntry(service)
	handler := NewHandler(server, config.Telemetry{}, logger, observability.NewMetrics())
	request := httptest.NewRequestWithContext(access.WithAuthorization(context.Background(), authorization), http.MethodPost, "/v1/device/scans", strings.NewReader(`{"device_scan_id":"scan-0001","scanned_at":"2026-10-03T11:59:59Z"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"malformed_request"`) {
		t.Fatalf("status/body = %d/%s", response.Code, response.Body.String())
	}
}

var _ EntryScanService = (*stubEntryScan)(nil)
