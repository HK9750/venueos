package device

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/HK9750/venueos/internal/access"
	"github.com/HK9750/venueos/internal/platform/clock"
	"github.com/HK9750/venueos/internal/platform/identifier"
)

type deviceRepositoryFake struct {
	created           CreateRecord
	device            Device
	changed           ChangeStateRecord
	list              Page
	changeResult      Device
	assignments       AssignmentPage
	createdAssignment CreateAssignmentRecord
	revokedAssignment RevokeAssignmentRecord
	credential        Device
	credentialErr     error
}

func (fake *deviceRepositoryFake) List(context.Context, identifier.ID, int32, *Cursor) (Page, error) {
	return fake.list, nil
}

func (fake *deviceRepositoryFake) Create(_ context.Context, record CreateRecord) (Device, error) {
	fake.created = record
	return record.Device, nil
}

func (fake *deviceRepositoryFake) Get(context.Context, identifier.ID, identifier.ID) (Device, error) {
	return fake.device, nil
}

func (fake *deviceRepositoryFake) ChangeState(_ context.Context, record ChangeStateRecord) (Device, error) {
	fake.changed = record
	if fake.changeResult.ID.IsZero() {
		fake.changeResult = Device{ID: record.DeviceID, OrganizationID: record.OrganizationID, State: record.State, Version: record.ExpectedVersion + 1}
	}
	return fake.changeResult, nil
}

func (fake *deviceRepositoryFake) ListAssignments(context.Context, identifier.ID, identifier.ID, int32, *AssignmentCursor) (AssignmentPage, error) {
	return fake.assignments, nil
}

func (fake *deviceRepositoryFake) CreateAssignment(_ context.Context, record CreateAssignmentRecord) (Assignment, error) {
	fake.createdAssignment = record
	return record.Assignment, nil
}

func (fake *deviceRepositoryFake) RevokeAssignment(_ context.Context, record RevokeAssignmentRecord) (Assignment, error) {
	fake.revokedAssignment = record
	return Assignment{ID: record.AssignmentID, OrganizationID: record.OrganizationID, DeviceID: record.DeviceID, State: "revoked", Version: record.ExpectedVersion + 1}, nil
}

func (fake *deviceRepositoryFake) LookupCredential(context.Context, [32]byte) (Device, error) {
	if fake.credentialErr != nil {
		return Device{}, fake.credentialErr
	}
	return fake.credential, nil
}

func deviceTestID(t *testing.T, value string) identifier.ID {
	t.Helper()
	id, err := identifier.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func deviceTestContext(t *testing.T, organizationID identifier.ID, role access.Role) context.Context {
	t.Helper()
	principal, err := access.NewPrincipal(access.PrincipalUser, "device-manager")
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := access.NewAuthorizationForRole(principal, organizationID, role)
	if err != nil {
		t.Fatal(err)
	}
	return access.WithAuthorization(context.Background(), authorization)
}

func TestServiceEnrollsCredentialExactlyOnceAndScopesTenant(t *testing.T) {
	organizationID := deviceTestID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	otherOrganizationID := deviceTestID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6")
	fake := &deviceRepositoryFake{}
	service := NewService(fake, clock.NewFixed(time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)))
	venueID := deviceTestID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef7")
	issued, err := service.Create(deviceTestContext(t, organizationID, access.RoleDoorManager), CreateInput{OrganizationID: organizationID, VenueID: venueID, Name: "North scanner", Platform: "android", AppVersion: "1.4.2"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if len(issued.Token) < 80 || issued.Device.State != StateActive || fake.created.CredentialHash == [32]byte{} {
		t.Fatalf("issued device = %#v, record = %#v", issued, fake.created)
	}
	if sha256.Sum256([]byte(issued.Token)) != fake.created.CredentialHash {
		t.Fatal("stored credential hash does not match the issued token")
	}
	if issued.Device.OrganizationID != organizationID || issued.Device.VenueID != venueID {
		t.Fatalf("issued scope = %#v", issued.Device)
	}
	_, err = service.Create(deviceTestContext(t, otherOrganizationID, access.RoleDoorManager), CreateInput{OrganizationID: organizationID, VenueID: venueID, Name: "cross-tenant", Platform: "android", AppVersion: "1"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant create error = %v", err)
	}
}

func TestServiceStateChangeRequiresVersionAndPermission(t *testing.T) {
	organizationID := deviceTestID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	deviceID := deviceTestID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6")
	fake := &deviceRepositoryFake{}
	service := NewService(fake, clock.NewFixed(time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)))
	_, err := service.ChangeState(deviceTestContext(t, organizationID, access.RoleScanner), ChangeStateInput{OrganizationID: organizationID, DeviceID: deviceID, State: StateSuspended, ExpectedVersion: 1})
	if err == nil {
		t.Fatal("scanner was allowed to change device state")
	}
	_, err = service.ChangeState(deviceTestContext(t, organizationID, access.RoleDoorManager), ChangeStateInput{OrganizationID: organizationID, DeviceID: deviceID, State: StateSuspended, ExpectedVersion: 0})
	if err == nil {
		t.Fatal("zero version was accepted")
	}
	updated, err := service.ChangeState(deviceTestContext(t, organizationID, access.RoleDoorManager), ChangeStateInput{OrganizationID: organizationID, DeviceID: deviceID, State: StateSuspended, ExpectedVersion: 1})
	if err != nil || updated.State != StateSuspended || fake.changed.ActorID == "" {
		t.Fatalf("ChangeState() = %#v, %v, record = %#v", updated, err, fake.changed)
	}
}

func TestCursorRoundTrip(t *testing.T) {
	cursor := Cursor{OrganizationID: deviceTestID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5"), ID: deviceTestID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6"), CreatedAt: time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)}
	encoded, err := EncodeCursor(cursor)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeCursor(encoded)
	if err != nil || decoded != cursor {
		t.Fatalf("cursor round trip = %#v, %v", decoded, err)
	}
}

func TestServiceCreatesAndRevokesScopedAssignment(t *testing.T) {
	organizationID := deviceTestID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	deviceID := deviceTestID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6")
	sessionID := deviceTestID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef7")
	fake := &deviceRepositoryFake{}
	service := NewService(fake, clock.NewFixed(time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)))
	created, err := service.CreateAssignment(deviceTestContext(t, organizationID, access.RoleDoorManager), CreateAssignmentInput{OrganizationID: organizationID, DeviceID: deviceID, SessionID: &sessionID, Capability: access.PermissionEntryScan})
	if err != nil || created.SessionID == nil || created.Capability != access.PermissionEntryScan || fake.createdAssignment.AuditID.IsZero() {
		t.Fatalf("CreateAssignment() = %#v, %v, record = %#v", created, err, fake.createdAssignment)
	}
	revoked, err := service.RevokeAssignment(deviceTestContext(t, organizationID, access.RoleDoorManager), RevokeAssignmentInput{OrganizationID: organizationID, DeviceID: deviceID, AssignmentID: created.ID, ExpectedVersion: 1})
	if err != nil || revoked.State != "revoked" || fake.revokedAssignment.OutboxID.IsZero() {
		t.Fatalf("RevokeAssignment() = %#v, %v, record = %#v", revoked, err, fake.revokedAssignment)
	}
	_, err = service.CreateAssignment(deviceTestContext(t, organizationID, access.RoleDoorManager), CreateAssignmentInput{OrganizationID: organizationID, DeviceID: deviceID})
	if err == nil {
		t.Fatal("assignment without session or gate scope was accepted")
	}
}

func TestServiceAuthenticatesOnlyActiveDeviceCredentials(t *testing.T) {
	organizationID := deviceTestID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	deviceID := deviceTestID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6")
	fake := &deviceRepositoryFake{credential: Device{ID: deviceID, OrganizationID: organizationID, State: StateActive}}
	service := NewService(fake, clock.NewFixed(time.Now().UTC()))
	token := "vdev_" + deviceID.String() + "." + "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-"
	value, err := service.Authenticate(context.Background(), token)
	if err != nil || value.ID != deviceID {
		t.Fatalf("Authenticate() = %#v, %v", value, err)
	}
	authorization, err := service.Authorization(context.Background(), token)
	if err != nil || authorization.OrganizationID() != organizationID || !authorization.Has(access.PermissionEntryScan) || authorization.Principal().Type() != access.PrincipalDevice {
		t.Fatalf("Authorization() = %#v, %v", authorization, err)
	}
	fake.credential.State = StateRevoked
	if _, err := service.Authenticate(context.Background(), token); !errors.Is(err, ErrCredentialInvalid) {
		t.Fatalf("revoked credential error = %v", err)
	}
}
