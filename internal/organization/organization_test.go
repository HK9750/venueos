package organization

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
	created  CreateRecord
	result   Organization
	replayed bool
	err      error
	getCalls int
}

func (repository *fakeRepository) Create(_ context.Context, record CreateRecord) (Organization, bool, error) {
	repository.created = record
	if repository.result.ID.IsZero() {
		repository.result = record.Organization
	}
	return repository.result, repository.replayed, repository.err
}
func (repository *fakeRepository) Get(context.Context, identifier.ID) (Organization, error) {
	repository.getCalls++
	return repository.result, repository.err
}

func TestCreateValidatesAuthorizationAndInput(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	repository := &fakeRepository{}
	service := NewService(repository, clock.NewFixed(now))
	input := validCreateInput()

	_, _, err := service.Create(context.Background(), input)
	requireCode(t, err, apperror.CodeUnauthenticated)

	ctx := platformContext(t)
	input.DefaultTimezone = "Not/AZone"
	_, _, err = service.Create(ctx, input)
	requireCode(t, err, apperror.CodeValidationFailed)
	if !repository.created.Organization.ID.IsZero() {
		t.Fatal("repository was called for invalid input")
	}
}

func TestCreateBuildsAtomicRecordAndPreservesReplay(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	repository := &fakeRepository{replayed: true}
	service := NewService(repository, clock.NewFixed(now))

	created, replayed, err := service.Create(platformContext(t), validCreateInput())
	if err != nil {
		t.Fatal(err)
	}
	if !replayed || created.ID.IsZero() || repository.created.AuditID.IsZero() || repository.created.OutboxID.IsZero() || repository.created.IdempotencyID.IsZero() {
		t.Fatalf("create result/record = replayed %v, %#v", replayed, repository.created)
	}
	if repository.created.Organization.CreatedAt != now || repository.created.IdempotencyExpiry != now.Add(72*time.Hour) {
		t.Fatalf("record times = %#v", repository.created)
	}
}

func TestGetHidesCrossTenantOrganization(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository, clock.System{})
	requested := newID(t)
	other := newID(t)
	principal, _ := access.NewPrincipal(access.PrincipalUser, "issuer|subject")
	authorization, _ := access.NewAuthorization(principal, other, access.PermissionOrganizationRead)

	_, err := service.Get(access.WithAuthorization(context.Background(), authorization), requested)
	requireCode(t, err, apperror.CodeNotFound)
	if repository.getCalls != 0 {
		t.Fatal("repository was called for cross-tenant ID")
	}
}

func validCreateInput() CreateInput {
	return CreateInput{Slug: "example-venue", DisplayName: "Example Venue", DefaultLocale: "en-US",
		DefaultTimezone: "Asia/Karachi", DefaultCurrency: "PKR", IdempotencyKey: "0123456789abcdef"}
}

func platformContext(t *testing.T) context.Context {
	t.Helper()
	principal, err := access.NewPrincipal(access.PrincipalPlatform, "onboarding-service")
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := access.NewPlatformAuthorization(principal, access.PermissionPlatformSupport)
	if err != nil {
		t.Fatal(err)
	}
	return access.WithPlatformAuthorization(context.Background(), authorization)
}

func newID(t *testing.T) identifier.ID {
	t.Helper()
	id, err := identifier.New()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func requireCode(t *testing.T, err error, expected apperror.Code) {
	t.Helper()
	var classified *apperror.Error
	if !errors.As(err, &classified) || classified.Code() != expected {
		t.Fatalf("error = %v, want code %s", err, expected)
	}
}
