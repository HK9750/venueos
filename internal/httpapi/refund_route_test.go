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
	"github.com/HK9750/venueos/internal/refund"
)

type stubRefunds struct {
	input  refund.RequestInput
	result refund.Refund
	err    error
}

func (service *stubRefunds) Request(_ context.Context, input refund.RequestInput) (refund.Refund, error) {
	service.input = input
	return service.result, service.err
}

func (service *stubRefunds) Get(context.Context, refund.GetInput) (refund.Refund, error) {
	return service.result, service.err
}

func TestCreateRefundRouteBindsTenantOrderAndIdempotencyKey(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	organizationID := mustAuthID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	orderID := mustAuthID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6")
	paymentAttemptID := mustAuthID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef7")
	refundID := mustAuthID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef8")
	principal, err := access.NewPrincipal(access.PrincipalUser, "user-123")
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := access.NewAuthorizationForRole(principal, organizationID, access.RoleFinance)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	service := &stubRefunds{result: refund.Refund{ID: refundID, OrganizationID: organizationID, OrderID: orderID, PaymentAttemptID: paymentAttemptID, AmountMinor: 1250, Currency: "USD", Status: refund.StatusRequested, IdempotencyKey: "refund-request-0001", Reason: "Customer request", Version: 1, CreatedAt: now, UpdatedAt: now}}
	server := NewServer(stubUsers{}, ready{}, logger).WithRefunds(service)
	handler := NewHandler(server, config.Telemetry{}, logger, observability.NewMetrics())
	request := httptest.NewRequestWithContext(access.WithAuthorization(context.Background(), authorization), http.MethodPost, "/v1/organizations/"+organizationID.String()+"/orders/"+orderID.String()+"/refunds", strings.NewReader(`{"payment_attempt_id":"01890f3e-7b4c-7cc6-9c52-6d6f83394ef7","amount_minor":1250,"currency":"USD","reason":"Customer request"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "refund-request-0001")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), `"status":"requested"`) {
		t.Fatalf("status/body = %d/%s", response.Code, response.Body.String())
	}
	if service.input.OrganizationID != organizationID || service.input.OrderID != orderID || service.input.PaymentAttemptID != paymentAttemptID || service.input.IdempotencyKey != "refund-request-0001" || service.input.AmountMinor != 1250 {
		t.Fatalf("service input = %#v", service.input)
	}
}

func TestCreateRefundRouteRequiresIdempotencyKey(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	organizationID := mustAuthID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	orderID := mustAuthID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6")
	principal, err := access.NewPrincipal(access.PrincipalUser, "user-123")
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := access.NewAuthorizationForRole(principal, organizationID, access.RoleFinance)
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(stubUsers{}, ready{}, logger).WithRefunds(&stubRefunds{})
	handler := NewHandler(server, config.Telemetry{}, logger, observability.NewMetrics())
	request := httptest.NewRequestWithContext(access.WithAuthorization(context.Background(), authorization), http.MethodPost, "/v1/organizations/"+organizationID.String()+"/orders/"+orderID.String()+"/refunds", strings.NewReader(`{"payment_attempt_id":"01890f3e-7b4c-7cc6-9c52-6d6f83394ef7","amount_minor":1250,"currency":"USD","reason":"Customer request"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"invalid_request"`) {
		t.Fatalf("status/body = %d/%s", response.Code, response.Body.String())
	}
}

func TestGetRefundRouteReturnsTenantScopedState(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	organizationID := mustAuthID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	orderID := mustAuthID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6")
	refundID := mustAuthID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef7")
	principal, err := access.NewPrincipal(access.PrincipalUser, "user-123")
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := access.NewAuthorizationForRole(principal, organizationID, access.RoleFinance)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	service := &stubRefunds{result: refund.Refund{ID: refundID, OrganizationID: organizationID, OrderID: orderID, Status: refund.StatusProcessing, Version: 2, CreatedAt: now, UpdatedAt: now}}
	server := NewServer(stubUsers{}, ready{}, logger).WithRefunds(service)
	handler := NewHandler(server, config.Telemetry{}, logger, observability.NewMetrics())
	request := httptest.NewRequestWithContext(access.WithAuthorization(context.Background(), authorization), http.MethodGet, "/v1/organizations/"+organizationID.String()+"/orders/"+orderID.String()+"/refunds/"+refundID.String(), nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"processing"`) {
		t.Fatalf("status/body = %d/%s", response.Code, response.Body.String())
	}
}

var _ RefundService = (*stubRefunds)(nil)
