package pricing

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
	result  PriceTier
	err     error
}

func (repository *fakeRepository) Create(_ context.Context, record CreateRecord) (PriceTier, error) {
	repository.created = record
	return repository.result, repository.err
}
func (repository *fakeRepository) List(context.Context, identifier.ID, identifier.ID, int32, *Cursor) (Page, error) {
	return Page{}, repository.err
}

func TestCreateUsesMinorUnitsAndDefaultsQuantity(t *testing.T) {
	organizationID := newID(t)
	repository := &fakeRepository{result: PriceTier{Status: StatusDraft}}
	service := NewService(repository, clock.System{})
	_, err := service.Create(tenantContext(t, organizationID), CreateInput{OrganizationID: organizationID, EventID: newID(t), SessionID: newID(t), Slug: "adult", DisplayName: "Adult", Currency: "pkr", AmountMinor: 125000})
	if err != nil {
		t.Fatal(err)
	}
	if repository.created.Tier.Currency != "PKR" || repository.created.Tier.AmountMinor != 125000 || repository.created.Tier.MinimumQuantity != 1 || repository.created.Tier.MaximumQuantity != 10 {
		t.Fatalf("price tier = %#v", repository.created.Tier)
	}
}

func TestCreateRejectsInvalidWindow(t *testing.T) {
	organizationID := newID(t)
	start := time.Now().UTC()
	end := start.Add(-time.Minute)
	_, err := NewService(&fakeRepository{}, clock.System{}).Create(tenantContext(t, organizationID), CreateInput{OrganizationID: organizationID, EventID: newID(t), SessionID: newID(t), Slug: "adult", DisplayName: "Adult", Currency: "USD", SalesStartAt: &start, SalesEndAt: &end})
	var classified *apperror.Error
	if err == nil || !errors.As(err, &classified) || classified.Code() != apperror.CodeValidationFailed {
		t.Fatalf("window validation error = %v", err)
	}
}

func tenantContext(t *testing.T, organizationID identifier.ID) context.Context {
	t.Helper()
	principal, err := access.NewPrincipal(access.PrincipalUser, "pricing-test-user")
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
