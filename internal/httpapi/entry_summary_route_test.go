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

	"github.com/HK9750/venueos/internal/config"
	"github.com/HK9750/venueos/internal/entry"
	"github.com/HK9750/venueos/internal/observability"
)

type stubEntrySummary struct {
	input  entry.SummaryInput
	result entry.Summary
}

func (service *stubEntrySummary) Summary(_ context.Context, input entry.SummaryInput) (entry.Summary, error) {
	service.input = input
	return service.result, nil
}

func TestEntrySummaryRouteMapsBoundedAggregates(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	organizationID := mustAuthID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	sessionID := mustAuthID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6")
	gateID := mustAuthID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef7")
	priceTierID := mustAuthID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef8")
	dataAsOf := time.Date(2026, 10, 3, 12, 59, 12, 0, time.UTC)
	service := &stubEntrySummary{result: entry.Summary{OrganizationID: organizationID, SessionID: sessionID, GeneratedAt: dataAsOf.Add(2 * time.Second), DataAsOf: &dataAsOf, DataDelay: 2 * time.Second, TotalScans: 3, ByResult: []entry.ResultCount{{Result: "admitted", Count: 2}}, ByGate: []entry.GateCount{{GateID: &gateID, Count: 3}}, ByMinute: []entry.MinuteCount{{Minute: dataAsOf.Truncate(time.Minute), Count: 3}}, ByTicketType: []entry.TicketTypeCount{{PriceTierID: priceTierID, Count: 3}}}}
	server := NewServer(stubUsers{}, ready{}, logger).WithEntry(service)
	handler := NewHandler(server, config.Telemetry{}, logger, observability.NewMetrics())
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/organizations/"+organizationID.String()+"/sessions/"+sessionID.String()+"/entry-summary?from=2026-10-03T12:00:00Z&until=2026-10-03T13:00:00Z", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"total_scans":3`) || !strings.Contains(response.Body.String(), gateID.String()) {
		t.Fatalf("status/body = %d/%s", response.Code, response.Body.String())
	}
	if service.input.OrganizationID != organizationID || service.input.SessionID != sessionID || service.input.Until.Sub(service.input.From) != time.Hour {
		t.Fatalf("service input = %#v", service.input)
	}
}

var _ EntryService = (*stubEntrySummary)(nil)
