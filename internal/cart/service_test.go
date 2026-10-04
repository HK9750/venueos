package cart

import (
	"context"
	"testing"
	"time"

	"github.com/HK9750/venueos/internal/access"
	"github.com/HK9750/venueos/internal/platform/clock"
	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/internal/platform/money"
	"github.com/HK9750/venueos/internal/pricing"
)

type fakeRepository struct {
	created CreateRecord
	value   Cart
}

func (repository *fakeRepository) Create(_ context.Context, record CreateRecord) (Cart, error) {
	repository.created = record
	return record.Cart, nil
}

func (repository *fakeRepository) Get(_ context.Context, _ identifier.ID, _ identifier.ID, _ [32]byte) (Cart, error) {
	return repository.value, nil
}

func (repository *fakeRepository) UpdateQuote(_ context.Context, record UpdateQuoteRecord) (Cart, error) {
	return repository.value, nil
}

func TestServiceCreateDerivesTenantAndUserFromVerifiedAuthorization(t *testing.T) {
	organizationID := mustCartID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	userID := mustCartID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6")
	principal, err := access.NewPrincipal(access.PrincipalUser, userID.String())
	if err != nil {
		t.Fatalf("NewPrincipal() error = %v", err)
	}
	authorization, err := access.NewAuthorizationForRole(principal, organizationID, access.RoleBoxOffice)
	if err != nil {
		t.Fatalf("NewAuthorizationForRole() error = %v", err)
	}
	usd, _ := money.NewCurrency("USD")
	unit, _ := money.New(2500, usd)
	quote, err := pricing.CalculateQuote(pricing.QuoteInput{Lines: []pricing.LineInput{{PriceTierID: mustCartID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef7"), Quantity: 1, UnitPrice: unit}}})
	if err != nil {
		t.Fatalf("CalculateQuote() error = %v", err)
	}
	repository := &fakeRepository{}
	service := NewService(repository, clock.NewFixed(time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)))
	ctx := access.WithAuthorization(context.Background(), authorization)
	created, err := service.Create(ctx, CreateCartInput{OrganizationID: organizationID, SessionID: mustCartID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef8"), HoldID: mustCartID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef9"), OwnerTokenHash: [32]byte{1}, Currency: "USD", Quote: quote})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.OrganizationID != organizationID || repository.created.Cart.OrganizationID != organizationID || repository.created.ActorID != userID.String() {
		t.Fatalf("created/record = %#v/%#v", created, repository.created)
	}
	if repository.created.Cart.OwnerUserID == nil || *repository.created.Cart.OwnerUserID != userID {
		t.Fatalf("owner user = %#v, want %s", repository.created.Cart.OwnerUserID, userID)
	}
}
