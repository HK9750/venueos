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
	"github.com/HK9750/venueos/internal/order"
	"github.com/HK9750/venueos/internal/platform/identifier"
)

type stubOrders struct {
	result     order.Order
	page       order.Page
	err        error
	listOrg    identifier.ID
	listLimit  int32
	listCursor *order.Cursor
}

func (service *stubOrders) GetStaff(_ context.Context, _ order.GetStaffServiceInput) (order.Order, error) {
	return service.result, service.err
}

func (service *stubOrders) ListStaff(_ context.Context, organizationID identifier.ID, limit int32, cursor *order.Cursor) (order.Page, error) {
	service.listOrg = organizationID
	service.listLimit = limit
	service.listCursor = cursor
	return service.page, service.err
}

func TestGetStaffOrderRouteOmitsOwnerTokenAndMapsLines(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	organizationID := mustAuthID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	orderID := mustAuthID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6")
	priceTierID := mustAuthID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef7")
	principal, err := access.NewPrincipal(access.PrincipalUser, "finance-user")
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := access.NewAuthorizationForRole(principal, organizationID, access.RoleFinance)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	service := &stubOrders{result: order.Order{ID: orderID, OrganizationID: organizationID, CartID: mustAuthID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef8"), SessionID: mustAuthID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef9"), HoldID: mustAuthID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394efa"), OrderNumber: "VO-ABC12345", Currency: "USD", Status: order.StatusPaymentPending, SubtotalMinor: 2500, TotalMinor: 2500, Version: 1, CreatedAt: now, UpdatedAt: now, OwnerTokenHash: [32]byte{1}, Lines: []order.Line{{LineNumber: 1, PriceTierID: priceTierID, Quantity: 1, UnitMinor: 2500, SubtotalMinor: 2500}}}}
	server := NewServer(stubUsers{}, ready{}, logger).WithOrders(service)
	handler := NewHandler(server, config.Telemetry{}, logger, observability.NewMetrics())
	request := httptest.NewRequestWithContext(access.WithAuthorization(context.Background(), authorization), http.MethodGet, "/v1/organizations/"+organizationID.String()+"/orders/"+orderID.String(), nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"order_number":"VO-ABC12345"`) || !strings.Contains(response.Body.String(), `"line_number":1`) || strings.Contains(response.Body.String(), "owner_token") {
		t.Fatalf("status/body = %d/%s", response.Code, response.Body.String())
	}
}

func TestListStaffOrdersRouteReturnsBoundedCursorPage(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	organizationID := mustAuthID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	orderID := mustAuthID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6")
	principal, err := access.NewPrincipal(access.PrincipalUser, "finance-user")
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := access.NewAuthorizationForRole(principal, organizationID, access.RoleFinance)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	service := &stubOrders{page: order.Page{Items: []order.Order{{ID: orderID, OrganizationID: organizationID, OrderNumber: "VO-ABC12345", Currency: "USD", Status: order.StatusPaymentPending, Version: 1, CreatedAt: now, UpdatedAt: now}}, NextCursor: &order.Cursor{OrganizationID: organizationID, ID: orderID, CreatedAt: now}}}
	server := NewServer(stubUsers{}, ready{}, logger).WithOrders(service)
	handler := NewHandler(server, config.Telemetry{}, logger, observability.NewMetrics())
	request := httptest.NewRequestWithContext(access.WithAuthorization(context.Background(), authorization), http.MethodGet, "/v1/organizations/"+organizationID.String()+"/orders?limit=1", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"has_more":true`) || !strings.Contains(response.Body.String(), `"order_number":"VO-ABC12345"`) {
		t.Fatalf("status/body = %d/%s", response.Code, response.Body.String())
	}
	if service.listOrg != organizationID || service.listLimit != 1 || service.listCursor != nil {
		t.Fatalf("list input = %#v/%d/%#v", service.listOrg, service.listLimit, service.listCursor)
	}
}

var _ OrderService = (*stubOrders)(nil)
