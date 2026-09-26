package session

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/HK9750/venueos/internal/access"
	"github.com/HK9750/venueos/internal/platform/apperror"
	"github.com/HK9750/venueos/internal/platform/clock"
	"github.com/HK9750/venueos/internal/platform/identifier"
)

type fakeRepository struct {
	created CreateRecord
	result  Session
	err     error
}

func (repository *fakeRepository) Create(_ context.Context, record CreateRecord) (Session, error) {
	repository.created = record
	return repository.result, repository.err
}
func (repository *fakeRepository) Get(context.Context, identifier.ID, identifier.ID, identifier.ID) (Session, error) {
	return repository.result, repository.err
}
func (repository *fakeRepository) List(context.Context, identifier.ID, identifier.ID, int32, *Cursor) (Page, error) {
	return Page{}, repository.err
}

func TestCreateNormalizesTimesToUTC(t *testing.T) {
	organizationID := newID(t)
	starts := time.Date(2026, 10, 1, 18, 0, 0, 0, time.FixedZone("PKT", 5*60*60))
	ends := starts.Add(2 * time.Hour)
	repository := &fakeRepository{result: Session{Status: StatusScheduled}}
	service := NewService(repository, clock.System{})
	_, err := service.Create(tenantContext(t, organizationID), CreateInput{OrganizationID: organizationID, EventID: newID(t), EventRevision: 1, VenueID: newID(t), SpaceID: newID(t), StartsAt: starts, EndsAt: ends, Timezone: "Asia/Karachi"})
	if err != nil {
		t.Fatal(err)
	}
	if repository.created.Session.StartsAt.Location() != time.UTC || repository.created.Session.EndsAt.Location() != time.UTC {
		t.Fatalf("times were not normalized: %v / %v", repository.created.Session.StartsAt, repository.created.Session.EndsAt)
	}
}

func TestCreateRejectsInvalidSalesWindow(t *testing.T) {
	organizationID := newID(t)
	starts := time.Date(2026, 10, 1, 18, 0, 0, 0, time.UTC)
	salesStart := starts.Add(-time.Hour)
	_, err := NewService(&fakeRepository{}, clock.System{}).Create(tenantContext(t, organizationID), CreateInput{OrganizationID: organizationID, EventID: newID(t), EventRevision: 1, VenueID: newID(t), SpaceID: newID(t), StartsAt: starts, EndsAt: starts.Add(2 * time.Hour), SalesStartAt: &salesStart, Timezone: "UTC"})
	var classified *apperror.Error
	if err == nil || !errors.As(err, &classified) || classified.Code() != apperror.CodeValidationFailed {
		t.Fatalf("sales validation error = %v", err)
	}
}

func tenantContext(t *testing.T, organizationID identifier.ID) context.Context {
	t.Helper()
	principal, err := access.NewPrincipal(access.PrincipalUser, "session-test-user")
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := access.NewAuthorizationForRole(principal, organizationID, access.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	return access.WithAuthorization(context.Background(), authorization)
}

func newID(t *testing.T) identifier.ID {
	t.Helper()
	id, err := identifier.New()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
