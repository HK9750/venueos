package order

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/HK9750/venueos/internal/access"
	"github.com/HK9750/venueos/internal/platform/identifier"
)

type fakeOrderRepository struct {
	record CreateFromCartRecord
	staff  Order
	page   Page
}

func (repository *fakeOrderRepository) CreateFromCart(_ context.Context, record CreateFromCartRecord) (Order, error) {
	repository.record = record
	return record.Order, nil
}

func (repository *fakeOrderRepository) Get(_ context.Context, _ identifier.ID, _ identifier.ID, _ [32]byte) (Order, error) {
	return Order{}, nil
}

func (repository *fakeOrderRepository) GetStaff(_ context.Context, _, _ identifier.ID) (Order, error) {
	return repository.staff, nil
}

func (repository *fakeOrderRepository) ListStaff(context.Context, identifier.ID, int32, *Cursor) (Page, error) {
	return repository.page, nil
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

func TestServiceGetStaffRequiresTenantAuthorization(t *testing.T) {
	organizationID := mustOrderID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	orderID := mustOrderID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6")
	principal, err := access.NewPrincipal(access.PrincipalUser, "finance-user")
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := access.NewAuthorizationForRole(principal, organizationID, access.RoleFinance)
	if err != nil {
		t.Fatal(err)
	}
	repository := &fakeOrderRepository{staff: Order{ID: orderID, OrganizationID: organizationID, Status: StatusPaymentPending, Version: 1}}
	service := NewService(repository)
	value, err := service.GetStaff(access.WithAuthorization(context.Background(), authorization), GetStaffServiceInput{OrganizationID: organizationID, OrderID: orderID})
	if err != nil || value.ID != orderID {
		t.Fatalf("GetStaff() = %#v, %v", value, err)
	}
	_, err = service.GetStaff(access.WithAuthorization(context.Background(), authorization), GetStaffServiceInput{OrganizationID: mustOrderID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef7"), OrderID: orderID})
	if err == nil || !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant error = %v", err)
	}
}

func TestServiceListStaffValidatesBoundedCursorAndLimit(t *testing.T) {
	organizationID := mustOrderID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	principal, err := access.NewPrincipal(access.PrincipalUser, "finance-user")
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := access.NewAuthorizationForRole(principal, organizationID, access.RoleFinance)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(&fakeOrderRepository{})
	ctx := access.WithAuthorization(context.Background(), authorization)
	if _, err := service.ListStaff(ctx, organizationID, MaxLimit+1, nil); err == nil {
		t.Fatal("ListStaff() accepted an oversized limit")
	}
	_, err = service.ListStaff(ctx, organizationID, 10, &Cursor{OrganizationID: mustOrderID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6"), ID: organizationID, CreatedAt: time.Now()})
	if err == nil {
		t.Fatal("ListStaff() accepted a cross-tenant cursor")
	}
}
