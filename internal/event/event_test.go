package event

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
	publish PublishRecord
	result  Event
	err     error
}

func (repository *fakeRepository) Create(_ context.Context, record CreateRecord) (Event, error) {
	repository.created = record
	return repository.result, repository.err
}
func (repository *fakeRepository) Get(context.Context, identifier.ID, identifier.ID) (Event, error) {
	return repository.result, repository.err
}
func (repository *fakeRepository) List(context.Context, identifier.ID, int32, *Cursor) (Page, error) {
	return Page{}, repository.err
}
func (repository *fakeRepository) Update(context.Context, UpdateRecord) (Event, error) {
	return repository.result, repository.err
}

func (repository *fakeRepository) Publish(_ context.Context, record PublishRecord) (Event, error) {
	repository.publish = record
	return repository.result, repository.err
}
func (repository *fakeRepository) Clone(context.Context, CloneRecord) (Event, error) {
	return repository.result, repository.err
}

func TestCreateNormalizesAndHashesDraft(t *testing.T) {
	organizationID := newID(t)
	repository := &fakeRepository{result: Event{Status: StatusDraft}}
	service := NewService(repository, clock.NewFixed(time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)))
	created, err := service.Create(tenantContext(t, organizationID), CreateInput{OrganizationID: organizationID, Slug: " Summer-Show ", Title: " Summer Show ", ShortDescription: " Short "})
	if err != nil {
		t.Fatal(err)
	}
	if repository.created.Event.Slug != "summer-show" || repository.created.Event.Title != "Summer Show" || repository.created.Event.Version != 1 {
		t.Fatalf("create record = %#v", repository.created.Event)
	}
	if len(repository.created.Checksum) != 32 || created.Status != StatusDraft {
		t.Fatalf("checksum/status = %d/%s", len(repository.created.Checksum), created.Status)
	}
}

func TestCreateRejectsInvalidTitle(t *testing.T) {
	organizationID := newID(t)
	service := NewService(&fakeRepository{}, clock.System{})
	_, err := service.Create(tenantContext(t, organizationID), CreateInput{OrganizationID: organizationID, Slug: "summer-show", Title: "\n"})
	var classified *apperror.Error
	if err == nil || !errors.As(err, &classified) || classified.Code() != apperror.CodeValidationFailed {
		t.Fatalf("title validation error = %v", err)
	}
}

type fakePublicationValidator struct {
	valid bool
	err   error
}

func (validator fakePublicationValidator) ValidateForPublish(context.Context, identifier.ID, identifier.ID) (bool, error) {
	return validator.valid, validator.err
}

func TestPublishRejectsInvalidPublication(t *testing.T) {
	organizationID := newID(t)
	repository := &fakeRepository{result: Event{Status: StatusPublished}}
	service := NewService(repository, clock.System{}).WithPublicationValidator(fakePublicationValidator{})
	_, err := service.Publish(tenantContext(t, organizationID), PublishInput{OrganizationID: organizationID, EventID: newID(t), Version: 1})
	var classified *apperror.Error
	if err == nil || !errors.As(err, &classified) || classified.Code() != apperror.CodeConflict {
		t.Fatalf("publication validation error = %v", err)
	}
	if !repository.publish.Input.EventID.IsZero() {
		t.Fatal("publish repository was called after validation failed")
	}
}

func TestPublishFailsClosedWithoutValidator(t *testing.T) {
	organizationID := newID(t)
	repository := &fakeRepository{result: Event{Status: StatusPublished}}
	service := NewService(repository, clock.System{})
	_, err := service.Publish(tenantContext(t, organizationID), PublishInput{OrganizationID: organizationID, EventID: newID(t), Version: 1})
	var classified *apperror.Error
	if err == nil || !errors.As(err, &classified) || classified.Code() != apperror.CodeDependencyUnavailable {
		t.Fatalf("unconfigured publication validator error = %v", err)
	}
	if !repository.publish.Input.EventID.IsZero() {
		t.Fatal("publish repository was called without a validator")
	}
}

func tenantContext(t *testing.T, organizationID identifier.ID) context.Context {
	t.Helper()
	principal, err := access.NewPrincipal(access.PrincipalUser, "event-test-user")
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
