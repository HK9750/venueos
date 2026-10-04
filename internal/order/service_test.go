package order

import (
	"context"
	"testing"

	"github.com/HK9750/venueos/internal/access"
	"github.com/HK9750/venueos/internal/platform/identifier"
)

type fakeOrderRepository struct {
	record CreateFromCartRecord
}

func (repository *fakeOrderRepository) CreateFromCart(_ context.Context, record CreateFromCartRecord) (Order, error) {
	repository.record = record
	return record.Order, nil
}

func (repository *fakeOrderRepository) Get(_ context.Context, _ identifier.ID, _ identifier.ID, _ [32]byte) (Order, error) {
	return Order{}, nil
}

func TestServiceCreateDerivesTenantAndActorFromAuthorization(t *testing.T) {
	organizationID := mustOrderID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	userID := mustOrderID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6")
	principal, err := access.NewPrincipal(access.PrincipalUser, userID.String())
	if err != nil {
		t.Fatalf("NewPrincipal() error = %v", err)
	}
	authorization, err := access.NewAuthorizationForRole(principal, organizationID, access.RoleBoxOffice)
	if err != nil {
		t.Fatalf("NewAuthorizationForRole() error = %v", err)
	}
	repository := &fakeOrderRepository{}
	service := NewService(repository)
	ctx := access.WithAuthorization(context.Background(), authorization)
	created, err := service.Create(ctx, CreateFromCartServiceInput{OrganizationID: organizationID, CartID: mustOrderID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef7"), OwnerTokenHash: [32]byte{1}})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.OrganizationID != organizationID || repository.record.ActorID != userID.String() || repository.record.Order.OrderNumber == "" {
		t.Fatalf("created/record = %#v/%#v", created, repository.record)
	}
	if repository.record.Order.OrderNumber[:3] != "VO-" {
		t.Fatalf("order number = %q", repository.record.Order.OrderNumber)
	}
}
