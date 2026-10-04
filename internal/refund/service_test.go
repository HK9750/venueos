package refund

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/HK9750/venueos/internal/access"
	"github.com/HK9750/venueos/internal/platform/clock"
	"github.com/HK9750/venueos/internal/platform/identifier"
)

type requestRepositoryFake struct {
	record CreateRecord
	value  Refund
	err    error
}

func (repository *requestRepositoryFake) Create(_ context.Context, record CreateRecord) (Refund, error) {
	repository.record = record
	if repository.err != nil {
		return Refund{}, repository.err
	}
	if repository.value.ID.IsZero() {
		return record.Refund, nil
	}
	return repository.value, nil
}

func (repository *requestRepositoryFake) Get(context.Context, identifier.ID, identifier.ID) (Refund, error) {
	return repository.value, repository.err
}

func TestServiceRequestDerivesTenantAndActorFromAuthorization(t *testing.T) {
	organizationID := testRefundID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	orderID := testRefundID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6")
	paymentAttemptID := testRefundID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef7")
	principal, err := access.NewPrincipal(access.PrincipalUser, "user-123")
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := access.NewAuthorizationForRole(principal, organizationID, access.RoleFinance)
	if err != nil {
		t.Fatal(err)
	}
	repository := &requestRepositoryFake{}
	service := NewService(repository, clock.NewFixed(time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)))
	created, err := service.Request(access.WithAuthorization(context.Background(), authorization), RequestInput{
		OrganizationID: organizationID, OrderID: orderID, PaymentAttemptID: paymentAttemptID,
		AmountMinor: 1250, Currency: "usd", IdempotencyKey: "refund-request-0001", Reason: "Customer request",
	})
	if err != nil {
		t.Fatalf("Request() error = %v", err)
	}
	if created.OrganizationID != organizationID || created.ActorType != string(access.PrincipalUser) || created.ActorID != "user-123" || created.Currency != "USD" || created.Status != StatusRequested {
		t.Fatalf("created refund = %#v", created)
	}
	if repository.record.Refund.OrganizationID != organizationID || repository.record.Refund.ActorID != "user-123" || repository.record.AuditID.IsZero() || repository.record.OutboxID.IsZero() {
		t.Fatalf("create record = %#v", repository.record)
	}
}

func TestServiceRequestRejectsCrossTenantTarget(t *testing.T) {
	organizationID := testRefundID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	otherOrganizationID := testRefundID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6")
	principal, err := access.NewPrincipal(access.PrincipalUser, "user-123")
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := access.NewAuthorizationForRole(principal, organizationID, access.RoleFinance)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(&requestRepositoryFake{}, clock.NewFixed(time.Now()))
	_, err = service.Request(access.WithAuthorization(context.Background(), authorization), RequestInput{OrganizationID: otherOrganizationID})
	if err == nil || !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant error = %v", err)
	}
}

func TestServiceRequestMapsIdempotencyConflict(t *testing.T) {
	organizationID := testRefundID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	principal, err := access.NewPrincipal(access.PrincipalUser, "user-123")
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := access.NewAuthorizationForRole(principal, organizationID, access.RoleFinance)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(&requestRepositoryFake{err: ErrExists}, clock.NewFixed(time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)))
	_, err = service.Request(access.WithAuthorization(context.Background(), authorization), RequestInput{
		OrganizationID: organizationID, OrderID: testRefundID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6"), PaymentAttemptID: testRefundID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef7"), AmountMinor: 1, Currency: "USD", IdempotencyKey: "refund-request-0001", Reason: "reason",
	})
	if err == nil || !errors.Is(err, ErrExists) {
		t.Fatalf("idempotency error = %v", err)
	}
}

func TestServiceGetRequiresTheRefundToBelongToTheOrder(t *testing.T) {
	organizationID := testRefundID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	orderID := testRefundID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6")
	refundID := testRefundID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef7")
	principal, err := access.NewPrincipal(access.PrincipalUser, "user-123")
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := access.NewAuthorizationForRole(principal, organizationID, access.RoleFinance)
	if err != nil {
		t.Fatal(err)
	}
	repository := &requestRepositoryFake{value: Refund{ID: refundID, OrganizationID: organizationID, OrderID: orderID}}
	service := NewService(repository, clock.NewFixed(time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)))
	value, err := service.Get(access.WithAuthorization(context.Background(), authorization), GetInput{OrganizationID: organizationID, OrderID: orderID, RefundID: refundID})
	if err != nil || value.ID != refundID {
		t.Fatalf("Get() = %#v, %v", value, err)
	}
	_, err = service.Get(access.WithAuthorization(context.Background(), authorization), GetInput{OrganizationID: organizationID, OrderID: testRefundID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef8"), RefundID: refundID})
	if err == nil || !errors.Is(err, ErrNotFound) {
		t.Fatalf("mismatched order error = %v", err)
	}
}

func testRefundID(t *testing.T, value string) identifier.ID {
	t.Helper()
	id, err := identifier.Parse(value)
	if err != nil {
		t.Fatalf("Parse(%q) error = %v", value, err)
	}
	return id
}
