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
	"github.com/HK9750/venueos/internal/device"
	"github.com/HK9750/venueos/internal/observability"
	"github.com/HK9750/venueos/internal/platform/identifier"
)

type stubDevices struct {
	page       device.Page
	issued     device.IssuedDevice
	found      device.Device
	changed    device.Device
	listOrg    identifier.ID
	createdOrg identifier.ID
	changeIn   device.ChangeStateInput
	assignIn   device.CreateAssignmentInput
	revokeIn   device.RevokeAssignmentInput
	assignment device.Assignment
	revoked    device.Assignment
}

func (stub *stubDevices) List(_ context.Context, organizationID identifier.ID, _ int32, _ *device.Cursor) (device.Page, error) {
	stub.listOrg = organizationID
	return stub.page, nil
}

func (stub *stubDevices) Create(_ context.Context, input device.CreateInput) (device.IssuedDevice, error) {
	stub.createdOrg = input.OrganizationID
	return stub.issued, nil
}

func (stub *stubDevices) Get(context.Context, identifier.ID, identifier.ID) (device.Device, error) {
	return stub.found, nil
}

func (stub *stubDevices) ChangeState(_ context.Context, input device.ChangeStateInput) (device.Device, error) {
	stub.changeIn = input
	return stub.changed, nil
}

func (stub *stubDevices) ListAssignments(context.Context, identifier.ID, identifier.ID, int32, *device.AssignmentCursor) (device.AssignmentPage, error) {
	return device.AssignmentPage{}, nil
}

func (stub *stubDevices) CreateAssignment(_ context.Context, input device.CreateAssignmentInput) (device.Assignment, error) {
	stub.assignIn = input
	return stub.assignment, nil
}

func (stub *stubDevices) RevokeAssignment(_ context.Context, input device.RevokeAssignmentInput) (device.Assignment, error) {
	stub.revokeIn = input
	return stub.revoked, nil
}

func TestDeviceRoutesNeverReturnCredentialOnReads(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	organizationID := mustAuthID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	deviceID := mustAuthID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6")
	venueID := mustAuthID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef7")
	sessionID := mustAuthID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef8")
	assignmentID := mustAuthID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef9")
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	stub := &stubDevices{
		page:       device.Page{Items: []device.Device{{ID: deviceID, OrganizationID: organizationID, VenueID: venueID, Name: "North", Platform: "android", AppVersion: "1.0", State: device.StateActive, Version: 1, CreatedAt: now, UpdatedAt: now}}},
		issued:     device.IssuedDevice{Device: device.Device{ID: deviceID, OrganizationID: organizationID, VenueID: venueID, Name: "North", Platform: "android", AppVersion: "1.0", State: device.StateActive, Version: 1, CreatedAt: now, UpdatedAt: now}, Token: "vdev_one_time_secret"},
		found:      device.Device{ID: deviceID, OrganizationID: organizationID, VenueID: venueID, Name: "North", Platform: "android", AppVersion: "1.0", State: device.StateActive, Version: 1, CreatedAt: now, UpdatedAt: now},
		changed:    device.Device{ID: deviceID, OrganizationID: organizationID, VenueID: venueID, Name: "North", Platform: "android", AppVersion: "1.0", State: device.StateSuspended, Version: 2, CreatedAt: now, UpdatedAt: now},
		assignment: device.Assignment{ID: assignmentID, OrganizationID: organizationID, DeviceID: deviceID, SessionID: &sessionID, Capability: "entry.scan", State: "active", ValidFrom: now, Version: 1, CreatedAt: now, UpdatedAt: now},
		revoked:    device.Assignment{ID: assignmentID, OrganizationID: organizationID, DeviceID: deviceID, SessionID: &sessionID, Capability: "entry.scan", State: "revoked", ValidFrom: now, Version: 2, CreatedAt: now, UpdatedAt: now},
	}
	server := NewServer(stubUsers{}, ready{}, logger).WithDevices(stub)
	handler := NewHandler(server, config.Telemetry{}, logger, observability.NewMetrics())
	base := "/v1/organizations/" + organizationID.String()

	list := httptest.NewRecorder()
	handler.ServeHTTP(list, httptest.NewRequestWithContext(context.Background(), http.MethodGet, base+"/devices", nil))
	if list.Code != http.StatusOK || strings.Contains(list.Body.String(), "token") || stub.listOrg != organizationID {
		t.Fatalf("list status/body = %d/%s", list.Code, list.Body.String())
	}

	create := httptest.NewRecorder()
	createRequest := httptest.NewRequestWithContext(context.Background(), http.MethodPost, base+"/devices", strings.NewReader(`{"venue_id":"`+venueID.String()+`","name":"North","platform":"android","app_version":"1.0"}`))
	createRequest.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(create, createRequest)
	if create.Code != http.StatusCreated || !strings.Contains(create.Body.String(), "vdev_one_time_secret") || stub.createdOrg != organizationID {
		t.Fatalf("create status/body = %d/%s", create.Code, create.Body.String())
	}

	change := httptest.NewRecorder()
	changeRequest := httptest.NewRequestWithContext(context.Background(), http.MethodPatch, base+"/devices/"+deviceID.String()+"/state", strings.NewReader(`{"state":"suspended"}`))
	changeRequest.Header.Set("Content-Type", "application/json")
	changeRequest.Header.Set("If-Match", `"1"`)
	handler.ServeHTTP(change, changeRequest)
	if change.Code != http.StatusOK || !strings.Contains(change.Body.String(), `"state":"suspended"`) || stub.changeIn.ExpectedVersion != 1 {
		t.Fatalf("change status/body = %d/%s input=%#v", change.Code, change.Body.String(), stub.changeIn)
	}

	assignment := httptest.NewRecorder()
	assignmentRequest := httptest.NewRequestWithContext(context.Background(), http.MethodPost, base+"/devices/"+deviceID.String()+"/assignments", strings.NewReader(`{"session_id":"`+sessionID.String()+`","capability":"entry.scan"}`))
	assignmentRequest.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(assignment, assignmentRequest)
	if assignment.Code != http.StatusCreated || !strings.Contains(assignment.Body.String(), assignmentID.String()) || stub.assignIn.SessionID == nil || *stub.assignIn.SessionID != sessionID {
		t.Fatalf("assignment status/body = %d/%s input=%#v", assignment.Code, assignment.Body.String(), stub.assignIn)
	}

	revoke := httptest.NewRecorder()
	revokeRequest := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, base+"/devices/"+deviceID.String()+"/assignments/"+assignmentID.String(), nil)
	revokeRequest.Header.Set("If-Match", `"1"`)
	handler.ServeHTTP(revoke, revokeRequest)
	if revoke.Code != http.StatusOK || !strings.Contains(revoke.Body.String(), `"state":"revoked"`) || stub.revokeIn.ExpectedVersion != 1 {
		t.Fatalf("revoke status/body = %d/%s input=%#v", revoke.Code, revoke.Body.String(), stub.revokeIn)
	}
}

var _ DeviceService = (*stubDevices)(nil)
