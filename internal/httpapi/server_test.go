package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/HK9750/venueos/internal/access"
	"github.com/HK9750/venueos/internal/channel"
	"github.com/HK9750/venueos/internal/config"
	"github.com/HK9750/venueos/internal/event"
	"github.com/HK9750/venueos/internal/inventory"
	"github.com/HK9750/venueos/internal/invitation"
	"github.com/HK9750/venueos/internal/membership"
	"github.com/HK9750/venueos/internal/observability"
	"github.com/HK9750/venueos/internal/organization"
	"github.com/HK9750/venueos/internal/platform/apperror"
	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/internal/platform/money"
	"github.com/HK9750/venueos/internal/pricing"
	"github.com/HK9750/venueos/internal/publication"
	"github.com/HK9750/venueos/internal/session"
	"github.com/HK9750/venueos/internal/user"
	"github.com/HK9750/venueos/internal/venue"
	"github.com/google/uuid"
)

type stubUsers struct{}

func (stubUsers) Create(_ context.Context, input user.CreateInput) (user.User, error) {
	now := time.Now().UTC()
	return user.User{ID: uuid.MustParse("11111111-1111-1111-1111-111111111111"), Email: input.Email, Name: input.Name, CreatedAt: now, UpdatedAt: now}, nil
}

func (stubUsers) Get(context.Context, uuid.UUID) (user.User, error) {
	return user.User{}, user.ErrNotFound
}

func (stubUsers) List(context.Context, int32, int32) ([]user.User, error) {
	return []user.User{}, nil
}

type ready struct{}

func (ready) Ping(context.Context) error { return nil }

type panicUsers struct{ stubUsers }

func (panicUsers) List(context.Context, int32, int32) ([]user.User, error) {
	panic("boom")
}

type invalidUsers struct{ stubUsers }

func (invalidUsers) Create(context.Context, user.CreateInput) (user.User, error) {
	return user.User{}, user.ErrInvalid
}

type failingUsers struct{ stubUsers }

func (failingUsers) List(context.Context, int32, int32) ([]user.User, error) {
	return nil, errors.New("database password must not escape")
}

type stubOrganizations struct {
	replayed bool
	err      error
	input    organization.CreateInput
}

type stubMemberships struct {
	page    membership.Page
	changed membership.ChangeRoleInput
	revoked membership.RevokeInput
	result  membership.Membership
	err     error
}

type stubInvitations struct {
	issued       invitation.IssueInput
	accepted     invitation.AcceptInput
	revoked      invitation.RevokeInput
	issueResult  invitation.IssueResult
	acceptResult invitation.Invitation
	revokeResult invitation.Invitation
	err          error
}

type stubEvents struct {
	result    event.Event
	page      event.Page
	created   event.CreateInput
	updated   event.UpdateInput
	published event.PublishInput
	cloned    event.CloneInput
	err       error
}

type stubPublication struct {
	report publication.Report
	err    error
}

func (service *stubPublication) Validate(context.Context, identifier.ID, identifier.ID) (publication.Report, error) {
	return service.report, service.err
}

type stubSessions struct {
	result  session.Session
	page    session.Page
	created session.CreateInput
	err     error
}

type stubInventory struct {
	snapshot inventory.AvailabilitySnapshot
	replay   inventory.AvailabilityReplayPage
	err      error
}

func (service *stubInventory) GetAvailabilitySnapshot(context.Context, identifier.ID, identifier.ID) (inventory.AvailabilitySnapshot, error) {
	return service.snapshot, service.err
}

func (service *stubInventory) ListAvailabilityEvents(context.Context, identifier.ID, identifier.ID, int64, int32) (inventory.AvailabilityReplayPage, error) {
	return service.replay, service.err
}

type stubPricing struct {
	result  pricing.PriceTier
	page    pricing.Page
	created pricing.CreateInput
	err     error
}

type stubSalesChannels struct {
	result            channel.SalesChannel
	page              channel.Page
	allocationResult  channel.Allocation
	allocationPage    channel.AllocationPage
	created           channel.CreateInput
	createdAllocation channel.CreateAllocationInput
	err               error
}

func (service *stubSalesChannels) Create(_ context.Context, input channel.CreateInput) (channel.SalesChannel, error) {
	service.created = input
	return service.result, service.err
}

func (service *stubSalesChannels) List(context.Context, identifier.ID, int32, *channel.Cursor) (channel.Page, error) {
	return service.page, service.err
}

func (service *stubSalesChannels) CreateAllocation(_ context.Context, input channel.CreateAllocationInput) (channel.Allocation, error) {
	service.createdAllocation = input
	return service.allocationResult, service.err
}

func (service *stubSalesChannels) ListAllocations(context.Context, identifier.ID, identifier.ID, int32, *channel.AllocationCursor) (channel.AllocationPage, error) {
	return service.allocationPage, service.err
}

func (service *stubPricing) Create(_ context.Context, input pricing.CreateInput) (pricing.PriceTier, error) {
	service.created = input
	return service.result, service.err
}

func (service *stubPricing) List(context.Context, identifier.ID, identifier.ID, int32, *pricing.Cursor) (pricing.Page, error) {
	return service.page, service.err
}

func (service *stubSessions) Create(_ context.Context, input session.CreateInput) (session.Session, error) {
	service.created = input
	return service.result, service.err
}
func (service *stubSessions) Get(context.Context, identifier.ID, identifier.ID, identifier.ID) (session.Session, error) {
	return service.result, service.err
}
func (service *stubSessions) List(context.Context, identifier.ID, identifier.ID, int32, *session.Cursor) (session.Page, error) {
	return service.page, service.err
}

func (service *stubEvents) Create(_ context.Context, input event.CreateInput) (event.Event, error) {
	service.created = input
	return service.result, service.err
}
func (service *stubEvents) Get(context.Context, identifier.ID, identifier.ID) (event.Event, error) {
	return service.result, service.err
}
func (service *stubEvents) List(context.Context, identifier.ID, int32, *event.Cursor) (event.Page, error) {
	return service.page, service.err
}
func (service *stubEvents) Update(_ context.Context, input event.UpdateInput) (event.Event, error) {
	service.updated = input
	return service.result, service.err
}
func (service *stubEvents) Publish(_ context.Context, input event.PublishInput) (event.Event, error) {
	service.published = input
	return service.result, service.err
}
func (service *stubEvents) Clone(_ context.Context, input event.CloneInput) (event.Event, error) {
	service.cloned = input
	return service.result, service.err
}

type stubVenues struct {
	venueResult      venue.Venue
	spaceResult      venue.Space
	page             venue.Page[venue.Venue]
	spacePage        venue.Page[venue.Space]
	seatMapResult    venue.SeatMap
	seatMapPage      venue.Page[venue.SeatMap]
	created          venue.CreateVenueInput
	updated          venue.UpdateVenueInput
	archived         venue.ArchiveVenueInput
	createdSpace     venue.CreateSpaceInput
	createdSeatMap   venue.CreateSeatMapInput
	updatedSeatMap   venue.UpdateSeatMapInput
	publishedSeatMap venue.PublishSeatMapInput
	clonedSeatMap    venue.CloneSeatMapInput
	err              error
}

func (service *stubVenues) CreateVenue(_ context.Context, input venue.CreateVenueInput) (venue.Venue, error) {
	service.created = input
	return service.venueResult, service.err
}
func (service *stubVenues) GetVenue(context.Context, identifier.ID, identifier.ID) (venue.Venue, error) {
	return service.venueResult, service.err
}
func (service *stubVenues) ListVenues(context.Context, identifier.ID, int32, *venue.Cursor) (venue.Page[venue.Venue], error) {
	return service.page, service.err
}
func (service *stubVenues) UpdateVenue(_ context.Context, input venue.UpdateVenueInput) (venue.Venue, error) {
	service.updated = input
	return service.venueResult, service.err
}
func (service *stubVenues) ArchiveVenue(_ context.Context, input venue.ArchiveVenueInput) (venue.Venue, error) {
	service.archived = input
	return service.venueResult, service.err
}
func (service *stubVenues) RestoreVenue(_ context.Context, input venue.RestoreVenueInput) (venue.Venue, error) {
	return service.venueResult, service.err
}
func (service *stubVenues) CreateSpace(_ context.Context, input venue.CreateSpaceInput) (venue.Space, error) {
	service.createdSpace = input
	return service.spaceResult, service.err
}
func (service *stubVenues) GetSpace(context.Context, identifier.ID, identifier.ID, identifier.ID) (venue.Space, error) {
	return service.spaceResult, service.err
}
func (service *stubVenues) ListSpaces(context.Context, identifier.ID, identifier.ID, int32, *venue.Cursor) (venue.Page[venue.Space], error) {
	return service.spacePage, service.err
}
func (service *stubVenues) CreateGate(context.Context, venue.CreateGateInput) (venue.Gate, error) {
	return venue.Gate{}, service.err
}
func (service *stubVenues) GetGate(context.Context, identifier.ID, identifier.ID, identifier.ID) (venue.Gate, error) {
	return venue.Gate{}, service.err
}
func (service *stubVenues) ListGates(context.Context, identifier.ID, identifier.ID, int32, *venue.Cursor) (venue.Page[venue.Gate], error) {
	return venue.Page[venue.Gate]{}, service.err
}
func (service *stubVenues) CreateGAPool(context.Context, venue.CreateGAPoolInput) (venue.GAPoolTemplate, error) {
	return venue.GAPoolTemplate{}, service.err
}
func (service *stubVenues) GetGAPool(context.Context, identifier.ID, identifier.ID, identifier.ID) (venue.GAPoolTemplate, error) {
	return venue.GAPoolTemplate{}, service.err
}
func (service *stubVenues) ListGAPools(context.Context, identifier.ID, identifier.ID, int32, *venue.Cursor) (venue.Page[venue.GAPoolTemplate], error) {
	return venue.Page[venue.GAPoolTemplate]{}, service.err
}
func (service *stubVenues) CreateSeatMap(_ context.Context, input venue.CreateSeatMapInput) (venue.SeatMap, error) {
	service.createdSeatMap = input
	return service.seatMapResult, service.err
}
func (service *stubVenues) GetSeatMap(context.Context, identifier.ID, identifier.ID, identifier.ID) (venue.SeatMap, error) {
	return service.seatMapResult, service.err
}
func (service *stubVenues) ListSeatMaps(context.Context, identifier.ID, identifier.ID, int32, *venue.Cursor) (venue.Page[venue.SeatMap], error) {
	return service.seatMapPage, service.err
}
func (service *stubVenues) UpdateSeatMap(_ context.Context, input venue.UpdateSeatMapInput) (venue.SeatMap, error) {
	service.updatedSeatMap = input
	return service.seatMapResult, service.err
}
func (service *stubVenues) PublishSeatMap(_ context.Context, input venue.PublishSeatMapInput) (venue.SeatMap, error) {
	service.publishedSeatMap = input
	return service.seatMapResult, service.err
}
func (service *stubVenues) CloneSeatMap(_ context.Context, input venue.CloneSeatMapInput) (venue.SeatMap, error) {
	service.clonedSeatMap = input
	return service.seatMapResult, service.err
}

func (service *stubInvitations) Issue(_ context.Context, input invitation.IssueInput) (invitation.IssueResult, error) {
	service.issued = input
	return service.issueResult, service.err
}
func (service *stubInvitations) Accept(_ context.Context, input invitation.AcceptInput) (invitation.Invitation, error) {
	service.accepted = input
	return service.acceptResult, service.err
}
func (service *stubInvitations) Revoke(_ context.Context, input invitation.RevokeInput) (invitation.Invitation, error) {
	service.revoked = input
	return service.revokeResult, service.err
}

func (service *stubMemberships) List(context.Context, identifier.ID, int32, *membership.Cursor) (membership.Page, error) {
	return service.page, service.err
}
func (service *stubMemberships) ChangeRole(_ context.Context, input membership.ChangeRoleInput) (membership.Membership, error) {
	service.changed = input
	return service.result, service.err
}
func (service *stubMemberships) Revoke(_ context.Context, input membership.RevokeInput) (membership.Membership, error) {
	service.revoked = input
	return service.result, service.err
}

func (service *stubOrganizations) Create(_ context.Context, input organization.CreateInput) (organization.Organization, bool, error) {
	service.input = input
	if service.err != nil {
		return organization.Organization{}, false, service.err
	}
	id, _ := identifier.Parse("22222222-2222-4222-8222-222222222222")
	currency, _ := money.NewCurrency("PKR")
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	return organization.Organization{ID: id, Slug: input.Slug, DisplayName: input.DisplayName,
		Status: "active", DefaultLocale: input.DefaultLocale, DefaultTimezone: input.DefaultTimezone,
		DefaultCurrency: currency, SettingsVersion: 1, CreatedAt: now, UpdatedAt: now}, service.replayed, nil
}
func (*stubOrganizations) Get(context.Context, identifier.ID) (organization.Organization, error) {
	return organization.Organization{}, apperror.New(apperror.CodeNotFound, "The requested organization does not exist.")
}

func TestCreateUser(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewServer(stubUsers{}, ready{}, logger)
	handler := NewHandler(server, config.Telemetry{}, logger, observability.NewMetrics())
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/users", strings.NewReader(`{"email":"ada@example.com","name":"Ada"}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusCreated, recorder.Body.String())
	}
	var response User
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Email != "ada@example.com" {
		t.Fatalf("email = %q, want ada@example.com", response.Email)
	}
}

func TestGeneratedParameterErrorsUseStableEnvelope(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewServer(stubUsers{}, ready{}, logger)
	handler := NewHandler(server, config.Telemetry{}, logger, observability.NewMetrics())
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/users?limit=not-a-number", nil)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	if contentType := recorder.Header().Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("Content-Type = %q", contentType)
	}
	var response ErrorResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Error.Code != "invalid_request" {
		t.Fatalf("error code = %q, want invalid_request", response.Error.Code)
	}
	if response.Error.RequestId == "" || response.Error.RequestId != recorder.Header().Get("X-Request-ID") {
		t.Fatalf("request ID body/header mismatch: body=%q header=%q", response.Error.RequestId, recorder.Header().Get("X-Request-ID"))
	}
}

func TestListUsersRejectsInt32Overflow(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewServer(stubUsers{}, ready{}, logger)
	handler := NewHandler(server, config.Telemetry{}, logger, observability.NewMetrics())
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/users?limit=4294967297", nil)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
}

func TestCreateUserRejectsOversizedBody(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewServer(stubUsers{}, ready{}, logger)
	handler := NewHandler(server, config.Telemetry{}, logger, observability.NewMetrics())
	body := `{"email":"ada@example.com","name":"` + strings.Repeat("a", maxRequestBodyBytes) + `"}`
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/users", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusRequestEntityTooLarge, recorder.Body.String())
	}
}

func TestCreateUserUsesValidationErrorCode(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewServer(invalidUsers{}, ready{}, logger)
	handler := NewHandler(server, config.Telemetry{}, logger, observability.NewMetrics())
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/users", strings.NewReader(`{"email":"ada@example.com","name":"Ada"}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnprocessableEntity)
	}
	var response ErrorResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Error.Code != "validation_failed" {
		t.Fatalf("error code = %q, want validation_failed", response.Error.Code)
	}
}

func TestInternalErrorDoesNotExposeCause(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewServer(failingUsers{}, ready{}, logger)
	handler := NewHandler(server, config.Telemetry{}, logger, observability.NewMetrics())
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/users", nil)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
	if strings.Contains(recorder.Body.String(), "database password") {
		t.Fatalf("response exposed internal cause: %s", recorder.Body.String())
	}
}

func TestRecoveredPanicIsLoggedAndMeasured(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	metrics := observability.NewMetrics()
	server := NewServer(panicUsers{}, ready{}, logger)
	handler := NewHandler(server, config.Telemetry{MetricsEnabled: true}, logger, metrics)
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/users", nil)
	request.Header.Set("X-Request-ID", "panic-request")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
	logOutput := logs.String()
	if !strings.Contains(logOutput, `"request_id":"panic-request"`) || !strings.Contains(logOutput, `"status":500`) {
		t.Fatalf("logs do not contain correlated recovered request: %s", logOutput)
	}

	adminHandler := NewAdminHandler(config.Telemetry{MetricsEnabled: true}, logger, metrics)
	metricsRequest := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", nil)
	metricsRecorder := httptest.NewRecorder()
	adminHandler.ServeHTTP(metricsRecorder, metricsRequest)
	if !strings.Contains(metricsRecorder.Body.String(), `service_http_requests_total{method="GET",route="GET /v1/users",status="500"} 1`) {
		t.Fatalf("metrics do not contain recovered 500: %s", metricsRecorder.Body.String())
	}
}

func TestCreateOrganizationContract(t *testing.T) {
	for _, test := range []struct {
		name, key string
		replayed  bool
		want      int
	}{
		{name: "created", key: "0123456789abcdef", want: http.StatusCreated},
		{name: "replayed", key: "0123456789abcdef", replayed: true, want: http.StatusOK},
		{name: "missing key", want: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			organizations := &stubOrganizations{replayed: test.replayed}
			server := NewServer(stubUsers{}, ready{}, logger).WithOrganizations(organizations)
			handler := NewHandler(server, config.Telemetry{}, logger, observability.NewMetrics())
			request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/organizations",
				strings.NewReader(`{"slug":"example-venue","display_name":"Example Venue","default_locale":"en-US","default_timezone":"Asia/Karachi","default_currency":"PKR"}`))
			request.Header.Set("Content-Type", "application/json")
			if test.key != "" {
				request.Header.Set("Idempotency-Key", test.key)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != test.want {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, test.want, recorder.Body.String())
			}
			if test.want == http.StatusCreated && recorder.Header().Get("Location") == "" {
				t.Fatal("created response has no Location header")
			}
		})
	}
}

func TestOrganizationErrorUsesStableEnvelope(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	organizations := &stubOrganizations{err: apperror.New(apperror.CodeUnauthenticated, "Authentication is required.")}
	server := NewServer(stubUsers{}, ready{}, logger).WithOrganizations(organizations)
	handler := NewHandler(server, config.Telemetry{}, logger, observability.NewMetrics())
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/organizations",
		strings.NewReader(`{"slug":"example-venue","display_name":"Example Venue","default_locale":"en-US","default_timezone":"UTC","default_currency":"USD"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "0123456789abcdef")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized || !strings.Contains(recorder.Body.String(), `"code":"unauthenticated"`) {
		t.Fatalf("status/body = %d/%s", recorder.Code, recorder.Body.String())
	}
}

func TestAPIErrorMetricsUseStableCodeAndRoute(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	metrics := observability.NewMetrics()
	server := NewServer(stubUsers{}, ready{}, logger)
	handler := NewHandler(server, config.Telemetry{MetricsEnabled: true}, logger, metrics)
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/organizations/not-a-uuid", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}

	admin := NewAdminHandler(config.Telemetry{MetricsEnabled: true}, logger, metrics)
	metricsRequest := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", nil)
	metricsRecorder := httptest.NewRecorder()
	admin.ServeHTTP(metricsRecorder, metricsRequest)
	if !strings.Contains(metricsRecorder.Body.String(), `service_http_errors_total{code="invalid_request",method="GET",route="GET /v1/organizations/{organization_id}"} 1`) {
		t.Fatalf("stable API error metric missing: %s", metricsRecorder.Body.String())
	}
}

func TestMembershipRoutesContract(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	organizationID := uuid.MustParse("33333333-3333-4333-8333-333333333333")
	userID := uuid.MustParse("44444444-4444-4444-8444-444444444444")
	membershipID := uuid.MustParse("55555555-5555-4555-8555-555555555555")
	service := &stubMemberships{result: membership.Membership{
		ID: identifierFromUUID(t, membershipID), OrganizationID: identifierFromUUID(t, organizationID),
		UserID: identifierFromUUID(t, userID), Role: access.RoleFinance, Status: membership.StatusActive,
		Version: 2, CreatedAt: time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 9, 26, 12, 0, 1, 0, time.UTC),
	}}
	server := NewServer(stubUsers{}, ready{}, logger).WithMemberships(service)
	handler := NewHandler(server, config.Telemetry{}, logger, observability.NewMetrics())

	patchRequest := httptest.NewRequestWithContext(context.Background(), http.MethodPatch,
		"/v1/organizations/"+organizationID.String()+"/memberships/"+membershipID.String(),
		strings.NewReader(`{"role":"finance"}`))
	patchRequest.Header.Set("Content-Type", "application/json")
	patchRequest.Header.Set("If-Match", `W/"1"`)
	patchResponse := httptest.NewRecorder()
	handler.ServeHTTP(patchResponse, patchRequest)
	if patchResponse.Code != http.StatusOK || patchResponse.Header().Get("ETag") != `"2"` {
		t.Fatalf("patch status/etag = %d/%q; body=%s", patchResponse.Code, patchResponse.Header().Get("ETag"), patchResponse.Body.String())
	}
	if service.changed.Version != 1 || service.changed.Role != access.RoleFinance {
		t.Fatalf("change input = %#v", service.changed)
	}

	listRequest := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/v1/organizations/"+organizationID.String()+"/memberships?limit=10", nil)
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK || !strings.Contains(listResponse.Body.String(), `"has_more":false`) {
		t.Fatalf("list status/body = %d/%s", listResponse.Code, listResponse.Body.String())
	}

	deleteRequest := httptest.NewRequestWithContext(context.Background(), http.MethodDelete,
		"/v1/organizations/"+organizationID.String()+"/memberships/"+membershipID.String(), nil)
	deleteRequest.Header.Set("If-Match", `"2"`)
	deleteResponse := httptest.NewRecorder()
	handler.ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusNoContent || service.revoked.Version != 2 {
		t.Fatalf("delete status/input = %d/%#v", deleteResponse.Code, service.revoked)
	}
}

func TestMembershipRouteRequiresIfMatch(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewServer(stubUsers{}, ready{}, logger).WithMemberships(&stubMemberships{})
	handler := NewHandler(server, config.Telemetry{}, logger, observability.NewMetrics())
	request := httptest.NewRequestWithContext(context.Background(), http.MethodDelete,
		"/v1/organizations/33333333-3333-4333-8333-333333333333/memberships/55555555-5555-4555-8555-555555555555", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), `"code":"invalid_request"`) {
		t.Fatalf("status/body = %d/%s", recorder.Code, recorder.Body.String())
	}
}

func TestInvitationRoutesContract(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	organizationID := uuid.MustParse("66666666-6666-4666-8666-666666666666")
	invitationID := uuid.MustParse("77777777-7777-4777-8777-777777777777")
	service := &stubInvitations{issueResult: invitation.IssueResult{Invitation: invitation.Invitation{
		ID: identifierFromUUID(t, invitationID), OrganizationID: identifierFromUUID(t, organizationID), Email: "staff@example.com",
		Role: access.RoleFinance, Status: invitation.StatusInvited, Version: 1,
		ExpiresAt: time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC), CreatedAt: time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC),
	}, Token: "abcdefghijklmnopqrstuvwxyz0123456789-_"}}
	server := NewServer(stubUsers{}, ready{}, logger).WithInvitations(service)
	handler := NewHandler(server, config.Telemetry{}, logger, observability.NewMetrics())

	createRequest := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		"/v1/organizations/"+organizationID.String()+"/invitations", strings.NewReader(`{"email":"staff@example.com","role":"finance"}`))
	createRequest.Header.Set("Content-Type", "application/json")
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, createRequest)
	if createResponse.Code != http.StatusCreated || !strings.Contains(createResponse.Body.String(), `"token":"abcdefghijklmnopqrstuvwxyz0123456789-_"`) {
		t.Fatalf("create status/body = %d/%s", createResponse.Code, createResponse.Body.String())
	}
	if service.issued.Role != access.RoleFinance || service.issued.OrganizationID != identifierFromUUID(t, organizationID) {
		t.Fatalf("issue input = %#v", service.issued)
	}

	acceptRequest := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/invitations/accept", strings.NewReader(`{"token":"token-value"}`))
	acceptRequest.Header.Set("Content-Type", "application/json")
	acceptResponse := httptest.NewRecorder()
	handler.ServeHTTP(acceptResponse, acceptRequest)
	if acceptResponse.Code != http.StatusOK || service.accepted.Token != "token-value" {
		t.Fatalf("accept status/input = %d/%#v", acceptResponse.Code, service.accepted)
	}

	revokeRequest := httptest.NewRequestWithContext(context.Background(), http.MethodDelete,
		"/v1/organizations/"+organizationID.String()+"/invitations/"+invitationID.String(), nil)
	revokeRequest.Header.Set("If-Match", `"1"`)
	revokeResponse := httptest.NewRecorder()
	handler.ServeHTTP(revokeResponse, revokeRequest)
	if revokeResponse.Code != http.StatusNoContent || service.revoked.Version != 1 {
		t.Fatalf("revoke status/input = %d/%#v", revokeResponse.Code, service.revoked)
	}
}

func TestVenueAndSpaceRoutesContract(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	organizationID := uuid.MustParse("88888888-8888-4888-8888-888888888888")
	venueID := uuid.MustParse("99999999-9999-4999-8999-999999999999")
	spaceID := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	service := &stubVenues{venueResult: venue.Venue{ID: identifierFromUUID(t, venueID), OrganizationID: identifierFromUUID(t, organizationID), Slug: "main-hall", DisplayName: "Main Hall", Status: venue.StatusDraft, Timezone: "UTC", Version: 2}, spaceResult: venue.Space{ID: identifierFromUUID(t, spaceID), OrganizationID: identifierFromUUID(t, organizationID), VenueID: identifierFromUUID(t, venueID), Slug: "floor", DisplayName: "Floor", Status: venue.StatusDraft, InventoryMode: venue.InventoryGA, PhysicalCapacity: 500, Version: 1}}
	server := NewServer(stubUsers{}, ready{}, logger).WithVenues(service)
	handler := NewHandler(server, config.Telemetry{}, logger, observability.NewMetrics())

	createVenue := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/organizations/"+organizationID.String()+"/venues", strings.NewReader(`{"slug":"main-hall","display_name":"Main Hall","timezone":"UTC"}`))
	createVenue.Header.Set("Content-Type", "application/json")
	venueResponseRecorder := httptest.NewRecorder()
	handler.ServeHTTP(venueResponseRecorder, createVenue)
	if venueResponseRecorder.Code != http.StatusCreated || service.created.Slug != "main-hall" {
		t.Fatalf("create venue status/input = %d/%#v", venueResponseRecorder.Code, service.created)
	}

	listVenues := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/organizations/"+organizationID.String()+"/venues?limit=5", nil)
	listVenuesRecorder := httptest.NewRecorder()
	handler.ServeHTTP(listVenuesRecorder, listVenues)
	if listVenuesRecorder.Code != http.StatusOK {
		t.Fatalf("list venues status/body = %d/%s", listVenuesRecorder.Code, listVenuesRecorder.Body.String())
	}

	updateVenue := httptest.NewRequestWithContext(context.Background(), http.MethodPatch, "/v1/organizations/"+organizationID.String()+"/venues/"+venueID.String(), strings.NewReader(`{"slug":"main-hall","display_name":"Updated Hall","timezone":"UTC"}`))
	updateVenue.Header.Set("Content-Type", "application/json")
	updateVenue.Header.Set("If-Match", `W/"1"`)
	updateVenueRecorder := httptest.NewRecorder()
	handler.ServeHTTP(updateVenueRecorder, updateVenue)
	if updateVenueRecorder.Code != http.StatusOK || updateVenueRecorder.Header().Get("ETag") != `"2"` || service.updated.Version != 1 {
		t.Fatalf("update venue status/etag/input = %d/%q/%#v", updateVenueRecorder.Code, updateVenueRecorder.Header().Get("ETag"), service.updated)
	}

	archiveVenue := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/v1/organizations/"+organizationID.String()+"/venues/"+venueID.String(), nil)
	archiveVenue.Header.Set("If-Match", `"2"`)
	archiveVenueRecorder := httptest.NewRecorder()
	handler.ServeHTTP(archiveVenueRecorder, archiveVenue)
	if archiveVenueRecorder.Code != http.StatusNoContent || service.archived.Version != 2 {
		t.Fatalf("archive venue status/input = %d/%#v", archiveVenueRecorder.Code, service.archived)
	}

	createSpace := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/organizations/"+organizationID.String()+"/venues/"+venueID.String()+"/spaces", strings.NewReader(`{"slug":"floor","display_name":"Floor","inventory_mode":"general_admission","physical_capacity":500}`))
	createSpace.Header.Set("Content-Type", "application/json")
	createSpaceRecorder := httptest.NewRecorder()
	handler.ServeHTTP(createSpaceRecorder, createSpace)
	if createSpaceRecorder.Code != http.StatusCreated || service.createdSpace.VenueID != identifierFromUUID(t, venueID) {
		t.Fatalf("create space status/input = %d/%#v", createSpaceRecorder.Code, service.createdSpace)
	}
}

func TestSeatMapRoutesContract(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	organizationID := uuid.MustParse("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	venueID := uuid.MustParse("cccccccc-cccc-4ccc-8ccc-cccccccccccc")
	spaceID := uuid.MustParse("dddddddd-dddd-4ddd-8ddd-dddddddddddd")
	seatMapID := uuid.MustParse("eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee")
	mapValue := venue.SeatMap{ID: identifierFromUUID(t, seatMapID), OrganizationID: identifierFromUUID(t, organizationID), SpaceID: identifierFromUUID(t, spaceID), Name: "Main Map", Status: venue.SeatMapDraft, Revision: 0, Checksum: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", SeatCount: 1, SellableCount: 1, Version: 1, Content: venue.SeatMapContent{Sections: []venue.SeatMapSection{{ID: "main", Label: "Main", Rows: []venue.SeatMapRow{{ID: "a", Label: "A", Seats: []venue.SeatMapSeat{{ID: "a1", Label: "1", Sellable: true}}}}}}}}
	service := &stubVenues{spaceResult: venue.Space{ID: identifierFromUUID(t, spaceID), OrganizationID: identifierFromUUID(t, organizationID), VenueID: identifierFromUUID(t, venueID), InventoryMode: venue.InventoryAssigned}, seatMapResult: mapValue}
	server := NewServer(stubUsers{}, ready{}, logger).WithVenues(service)
	handler := NewHandler(server, config.Telemetry{}, logger, observability.NewMetrics())
	base := "/v1/organizations/" + organizationID.String() + "/venues/" + venueID.String() + "/spaces/" + spaceID.String() + "/seat-maps"

	create := httptest.NewRequestWithContext(context.Background(), http.MethodPost, base, strings.NewReader(`{"name":"Main Map","content":{"sections":[{"id":"main","label":"Main","rows":[{"id":"a","label":"A","seats":[{"id":"a1","label":"1","sellable":true}]}]}]}}`))
	create.Header.Set("Content-Type", "application/json")
	createRecorder := httptest.NewRecorder()
	handler.ServeHTTP(createRecorder, create)
	if createRecorder.Code != http.StatusCreated || service.createdSeatMap.Name != "Main Map" || service.createdSeatMap.SpaceID != identifierFromUUID(t, spaceID) {
		t.Fatalf("create seat map status/input = %d/%#v", createRecorder.Code, service.createdSeatMap)
	}

	list := httptest.NewRequestWithContext(context.Background(), http.MethodGet, base+"?limit=10", nil)
	listRecorder := httptest.NewRecorder()
	handler.ServeHTTP(listRecorder, list)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list seat maps status/body = %d/%s", listRecorder.Code, listRecorder.Body.String())
	}

	update := httptest.NewRequestWithContext(context.Background(), http.MethodPatch, base+"/"+seatMapID.String(), strings.NewReader(`{"name":"Updated Map","content":{"sections":[{"id":"main","label":"Main","rows":[{"id":"a","label":"A","seats":[{"id":"a1","label":"1","sellable":true}]}]}]}}`))
	update.Header.Set("Content-Type", "application/json")
	update.Header.Set("If-Match", `"1"`)
	updateRecorder := httptest.NewRecorder()
	handler.ServeHTTP(updateRecorder, update)
	if updateRecorder.Code != http.StatusOK || service.updatedSeatMap.Version != 1 {
		t.Fatalf("update seat map status/input = %d/%#v", updateRecorder.Code, service.updatedSeatMap)
	}

	publish := httptest.NewRequestWithContext(context.Background(), http.MethodPost, base+"/"+seatMapID.String()+"/publish", nil)
	publish.Header.Set("If-Match", `"1"`)
	publishRecorder := httptest.NewRecorder()
	handler.ServeHTTP(publishRecorder, publish)
	if publishRecorder.Code != http.StatusOK || service.publishedSeatMap.Version != 1 {
		t.Fatalf("publish seat map status/input = %d/%#v", publishRecorder.Code, service.publishedSeatMap)
	}

	clone := httptest.NewRequestWithContext(context.Background(), http.MethodPost, base+"/"+seatMapID.String(), nil)
	cloneRecorder := httptest.NewRecorder()
	handler.ServeHTTP(cloneRecorder, clone)
	if cloneRecorder.Code != http.StatusCreated || service.clonedSeatMap.SeatMapID != identifierFromUUID(t, seatMapID) {
		t.Fatalf("clone seat map status/input = %d/%#v", cloneRecorder.Code, service.clonedSeatMap)
	}
}

func TestEventRoutesContract(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	organizationID := uuid.MustParse("12121212-1212-4121-8121-121212121212")
	eventID := uuid.MustParse("13131313-1313-4131-8131-131313131313")
	service := &stubEvents{result: event.Event{ID: identifierFromUUID(t, eventID), OrganizationID: identifierFromUUID(t, organizationID), Slug: "summer-show", Title: "Summer Show", Status: event.StatusDraft, Version: 1}}
	server := NewServer(stubUsers{}, ready{}, logger).WithEvents(service)
	handler := NewHandler(server, config.Telemetry{}, logger, observability.NewMetrics())
	base := "/v1/organizations/" + organizationID.String() + "/events"

	create := httptest.NewRequestWithContext(context.Background(), http.MethodPost, base, strings.NewReader(`{"slug":"summer-show","title":"Summer Show","short_description":"Short","long_description":"Long"}`))
	create.Header.Set("Content-Type", "application/json")
	createRecorder := httptest.NewRecorder()
	handler.ServeHTTP(createRecorder, create)
	if createRecorder.Code != http.StatusCreated || service.created.Slug != "summer-show" {
		t.Fatalf("create event status/input = %d/%#v", createRecorder.Code, service.created)
	}

	list := httptest.NewRequestWithContext(context.Background(), http.MethodGet, base+"?limit=10", nil)
	listRecorder := httptest.NewRecorder()
	handler.ServeHTTP(listRecorder, list)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list event status/body = %d/%s", listRecorder.Code, listRecorder.Body.String())
	}

	update := httptest.NewRequestWithContext(context.Background(), http.MethodPatch, base+"/"+eventID.String(), strings.NewReader(`{"slug":"summer-show","title":"Updated Show"}`))
	update.Header.Set("Content-Type", "application/json")
	update.Header.Set("If-Match", `"1"`)
	updateRecorder := httptest.NewRecorder()
	handler.ServeHTTP(updateRecorder, update)
	if updateRecorder.Code != http.StatusOK || service.updated.Version != 1 {
		t.Fatalf("update event status/input = %d/%#v", updateRecorder.Code, service.updated)
	}

	publish := httptest.NewRequestWithContext(context.Background(), http.MethodPost, base+"/"+eventID.String()+"/publish", nil)
	publish.Header.Set("If-Match", `"1"`)
	publishRecorder := httptest.NewRecorder()
	handler.ServeHTTP(publishRecorder, publish)
	if publishRecorder.Code != http.StatusOK || service.published.Version != 1 {
		t.Fatalf("publish event status/input = %d/%#v", publishRecorder.Code, service.published)
	}

	clone := httptest.NewRequestWithContext(context.Background(), http.MethodPost, base+"/"+eventID.String(), nil)
	cloneRecorder := httptest.NewRecorder()
	handler.ServeHTTP(cloneRecorder, clone)
	if cloneRecorder.Code != http.StatusCreated || service.cloned.EventID != identifierFromUUID(t, eventID) {
		t.Fatalf("clone event status/input = %d/%#v", cloneRecorder.Code, service.cloned)
	}
}

func TestPublicationValidationRouteContract(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	organizationID := uuid.MustParse("28282828-2828-4282-8282-282828282828")
	eventID := uuid.MustParse("29292929-2929-4292-8292-292929292929")
	service := &stubPublication{report: publication.Report{EventID: identifierFromUUID(t, eventID), Valid: false, Blockers: []publication.Issue{{Code: "missing_price_tier", Message: "Add a price tier."}}, CheckedAt: time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)}}
	server := NewServer(stubUsers{}, ready{}, logger).WithPublication(service)
	handler := NewHandler(server, config.Telemetry{}, logger, observability.NewMetrics())
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/organizations/"+organizationID.String()+"/events/"+eventID.String()+"/validate-publication", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"missing_price_tier"`) {
		t.Fatalf("publication validation status/body = %d/%s", recorder.Code, recorder.Body.String())
	}
}

func TestSessionRoutesContract(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	organizationID := uuid.MustParse("14141414-1414-4141-8141-141414141414")
	eventID := uuid.MustParse("15151515-1515-4151-8151-151515151515")
	venueID := uuid.MustParse("16161616-1616-4161-8161-161616161616")
	spaceID := uuid.MustParse("17171717-1717-4171-8171-171717171717")
	sessionID := uuid.MustParse("18181818-1818-4181-8181-181818181818")
	service := &stubSessions{result: session.Session{ID: identifierFromUUID(t, sessionID), OrganizationID: identifierFromUUID(t, organizationID), EventID: identifierFromUUID(t, eventID), VenueID: identifierFromUUID(t, venueID), SpaceID: identifierFromUUID(t, spaceID), EventRevision: 1, InventoryMode: session.InventoryGA, Status: session.StatusScheduled, Version: 1}}
	server := NewServer(stubUsers{}, ready{}, logger).WithSessions(service)
	handler := NewHandler(server, config.Telemetry{}, logger, observability.NewMetrics())
	base := "/v1/organizations/" + organizationID.String() + "/events/" + eventID.String() + "/sessions"
	create := httptest.NewRequestWithContext(context.Background(), http.MethodPost, base, strings.NewReader(`{"event_revision":1,"venue_id":"16161616-1616-4161-8161-161616161616","space_id":"17171717-1717-4171-8171-171717171717","starts_at":"2026-10-01T18:00:00Z","ends_at":"2026-10-01T20:00:00Z","timezone":"UTC"}`))
	create.Header.Set("Content-Type", "application/json")
	createRecorder := httptest.NewRecorder()
	handler.ServeHTTP(createRecorder, create)
	if createRecorder.Code != http.StatusCreated || service.created.EventRevision != 1 || service.created.Timezone != "UTC" {
		t.Fatalf("create session status/input = %d/%#v", createRecorder.Code, service.created)
	}
	list := httptest.NewRequestWithContext(context.Background(), http.MethodGet, base+"?limit=10", nil)
	listRecorder := httptest.NewRecorder()
	handler.ServeHTTP(listRecorder, list)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list session status/body = %d/%s", listRecorder.Code, listRecorder.Body.String())
	}
	get := httptest.NewRequestWithContext(context.Background(), http.MethodGet, base+"/"+sessionID.String(), nil)
	getRecorder := httptest.NewRecorder()
	handler.ServeHTTP(getRecorder, get)
	if getRecorder.Code != http.StatusOK || !strings.Contains(getRecorder.Body.String(), `"status":"scheduled"`) {
		t.Fatalf("get session status/body = %d/%s", getRecorder.Code, getRecorder.Body.String())
	}
}

func TestAvailabilityRoutesContract(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	organizationID := uuid.MustParse("23232323-2323-4232-8232-232323232323")
	sessionID := uuid.MustParse("24242424-2424-4242-8242-242424242424")
	seatID := uuid.MustParse("25252525-2525-4252-8252-252525252525")
	poolID := uuid.MustParse("26262626-2626-4262-8262-262626262626")
	service := &stubInventory{snapshot: inventory.AvailabilitySnapshot{OrganizationID: identifierFromUUID(t, organizationID), SessionID: identifierFromUUID(t, sessionID), InventoryMode: "general_admission", SessionStatus: "on_sale", Revision: 3, ServerTime: time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC), Seats: []inventory.SeatAvailability{{ID: identifierFromUUID(t, seatID), SeatKey: "a-1", Label: "1", State: "available", Version: 1}}, GAPools: []inventory.GAPoolAvailability{{ID: identifierFromUUID(t, poolID), Slug: "floor", SellableCapacity: 100, Available: 100, Version: 1}}}, replay: inventory.AvailabilityReplayPage{CurrentRevision: 3, Events: []inventory.AvailabilityEvent{{ID: identifierFromUUID(t, uuid.MustParse("27272727-2727-4272-8272-272727272727")), SessionID: identifierFromUUID(t, sessionID), Sequence: 3, EventType: "availability.changed", SchemaVersion: 1, Payload: []byte(`{"revision":3}`), OccurredAt: time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)}}}}
	server := NewServer(stubUsers{}, ready{}, logger).WithInventory(service)
	handler := NewHandler(server, config.Telemetry{}, logger, observability.NewMetrics())
	base := "/v1/organizations/" + organizationID.String() + "/sessions/" + sessionID.String() + "/availability"
	snapshotRequest := httptest.NewRequestWithContext(context.Background(), http.MethodGet, base, nil)
	snapshotRecorder := httptest.NewRecorder()
	handler.ServeHTTP(snapshotRecorder, snapshotRequest)
	if snapshotRecorder.Code != http.StatusOK || !strings.Contains(snapshotRecorder.Body.String(), `"revision":3`) || !strings.Contains(snapshotRecorder.Body.String(), `"slug":"floor"`) {
		t.Fatalf("availability snapshot status/body = %d/%s", snapshotRecorder.Code, snapshotRecorder.Body.String())
	}
	if snapshotRecorder.Header().Get("ETag") != `"24242424-2424-4242-8242-242424242424:3"` {
		t.Fatalf("availability snapshot etag = %q", snapshotRecorder.Header().Get("ETag"))
	}
	conditionalRequest := httptest.NewRequestWithContext(context.Background(), http.MethodGet, base, nil)
	conditionalRequest.Header.Set("If-None-Match", snapshotRecorder.Header().Get("ETag"))
	conditionalRecorder := httptest.NewRecorder()
	handler.ServeHTTP(conditionalRecorder, conditionalRequest)
	if conditionalRecorder.Code != http.StatusNotModified || conditionalRecorder.Body.Len() != 0 || conditionalRecorder.Header().Get("ETag") != snapshotRecorder.Header().Get("ETag") {
		t.Fatalf("conditional snapshot status/body/etag = %d/%s/%q", conditionalRecorder.Code, conditionalRecorder.Body.String(), conditionalRecorder.Header().Get("ETag"))
	}
	replayRequest := httptest.NewRequestWithContext(context.Background(), http.MethodGet, base+"/changes?after=2&limit=10", nil)
	replayRecorder := httptest.NewRecorder()
	handler.ServeHTTP(replayRecorder, replayRequest)
	if replayRecorder.Code != http.StatusOK || !strings.Contains(replayRecorder.Body.String(), `"sequence":3`) || !strings.Contains(replayRecorder.Body.String(), `"revision":3`) {
		t.Fatalf("availability replay status/body = %d/%s", replayRecorder.Code, replayRecorder.Body.String())
	}
}

func TestPriceTierRoutesContract(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	organizationID := uuid.MustParse("19191919-1919-4191-8191-191919191919")
	eventID := uuid.MustParse("20202020-2020-4202-8202-202020202020")
	sessionID := uuid.MustParse("21212121-2121-4212-8212-212121212121")
	priceTierID := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	service := &stubPricing{result: pricing.PriceTier{
		ID: identifierFromUUID(t, priceTierID), OrganizationID: identifierFromUUID(t, organizationID), SessionID: identifierFromUUID(t, sessionID),
		Slug: "general", DisplayName: "General Admission", Currency: "PKR", AmountMinor: 2500,
		MinimumQuantity: 1, MaximumQuantity: 10, Status: pricing.StatusDraft, Version: 1,
	}}
	server := NewServer(stubUsers{}, ready{}, logger).WithPricing(service)
	handler := NewHandler(server, config.Telemetry{}, logger, observability.NewMetrics())
	base := "/v1/organizations/" + organizationID.String() + "/events/" + eventID.String() + "/sessions/" + sessionID.String() + "/price-tiers"

	create := httptest.NewRequestWithContext(context.Background(), http.MethodPost, base, strings.NewReader(`{"slug":"general","display_name":"General Admission","currency":"PKR","amount_minor":2500}`))
	create.Header.Set("Content-Type", "application/json")
	createRecorder := httptest.NewRecorder()
	handler.ServeHTTP(createRecorder, create)
	if createRecorder.Code != http.StatusCreated || service.created.EventID != identifierFromUUID(t, eventID) || service.created.AmountMinor != 2500 {
		t.Fatalf("create price tier status/input = %d/%#v", createRecorder.Code, service.created)
	}

	list := httptest.NewRequestWithContext(context.Background(), http.MethodGet, base+"?limit=10", nil)
	listRecorder := httptest.NewRecorder()
	handler.ServeHTTP(listRecorder, list)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list price tiers status/body = %d/%s", listRecorder.Code, listRecorder.Body.String())
	}
}

func TestSalesChannelRoutesContract(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	organizationID := uuid.MustParse("23232323-2323-4232-8232-232323232323")
	channelID := uuid.MustParse("24242424-2424-4242-8242-242424242424")
	service := &stubSalesChannels{result: channel.SalesChannel{ID: identifierFromUUID(t, channelID), OrganizationID: identifierFromUUID(t, organizationID), Key: "public", DisplayName: "Public", Type: channel.TypePublic, Status: channel.StatusActive, Version: 1, Configuration: map[string]any{}}}
	server := NewServer(stubUsers{}, ready{}, logger).WithSalesChannels(service)
	handler := NewHandler(server, config.Telemetry{}, logger, observability.NewMetrics())
	base := "/v1/organizations/" + organizationID.String() + "/sales-channels"
	create := httptest.NewRequestWithContext(context.Background(), http.MethodPost, base, strings.NewReader(`{"key":"public","display_name":"Public","type":"public","configuration":{"audience":"customer"}}`))
	create.Header.Set("Content-Type", "application/json")
	createRecorder := httptest.NewRecorder()
	handler.ServeHTTP(createRecorder, create)
	if createRecorder.Code != http.StatusCreated || service.created.Key != "public" || service.created.Type != channel.TypePublic {
		t.Fatalf("create sales channel status/input = %d/%#v", createRecorder.Code, service.created)
	}
	list := httptest.NewRequestWithContext(context.Background(), http.MethodGet, base+"?limit=10", nil)
	listRecorder := httptest.NewRecorder()
	handler.ServeHTTP(listRecorder, list)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list sales channels status/body = %d/%s", listRecorder.Code, listRecorder.Body.String())
	}
	allocationID := uuid.MustParse("25252525-2525-4252-8252-252525252525")
	service.allocationResult = channel.Allocation{ID: identifierFromUUID(t, allocationID), OrganizationID: identifierFromUUID(t, organizationID), SessionID: identifierFromUUID(t, uuid.MustParse("26262626-2626-4262-8262-262626262626")), ChannelID: identifierFromUUID(t, channelID), ScopeKey: "all", Mode: channel.AllocationHardReserved, Quantity: 50, Version: 1}
	sessionID := service.allocationResult.SessionID.UUID()
	eventID := uuid.MustParse("27272727-2727-4272-8272-272727272727")
	allocationBase := "/v1/organizations/" + organizationID.String() + "/events/" + eventID.String() + "/sessions/" + sessionID.String() + "/channel-allocations"
	allocationCreate := httptest.NewRequestWithContext(context.Background(), http.MethodPost, allocationBase, strings.NewReader(`{"channel_id":"24242424-2424-4242-8242-242424242424","mode":"hard_reserved","quantity":50}`))
	allocationCreate.Header.Set("Content-Type", "application/json")
	allocationRecorder := httptest.NewRecorder()
	handler.ServeHTTP(allocationRecorder, allocationCreate)
	if allocationRecorder.Code != http.StatusCreated || service.createdAllocation.Quantity != 50 || service.createdAllocation.Mode != channel.AllocationHardReserved {
		t.Fatalf("create allocation status/input = %d/%#v", allocationRecorder.Code, service.createdAllocation)
	}
	allocationList := httptest.NewRequestWithContext(context.Background(), http.MethodGet, allocationBase+"?limit=10", nil)
	allocationListRecorder := httptest.NewRecorder()
	handler.ServeHTTP(allocationListRecorder, allocationList)
	if allocationListRecorder.Code != http.StatusOK {
		t.Fatalf("list allocations status/body = %d/%s", allocationListRecorder.Code, allocationListRecorder.Body.String())
	}
}

func identifierFromUUID(t *testing.T, value uuid.UUID) identifier.ID {
	t.Helper()
	id, err := identifier.FromUUID(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
