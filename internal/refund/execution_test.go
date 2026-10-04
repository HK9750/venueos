package refund

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/HK9750/venueos/internal/payment"
	"github.com/HK9750/venueos/internal/platform/clock"
	"github.com/HK9750/venueos/internal/platform/identifier"
)

type executionRepositoryFake struct {
	prepared  PrepareExecutionResult
	prepare   int
	finalized []FinalizeExecutionRecord
	final     Refund
}

func (repository *executionRepositoryFake) PrepareExecution(context.Context, PrepareExecutionRecord) (PrepareExecutionResult, error) {
	repository.prepare++
	return repository.prepared, nil
}

func (repository *executionRepositoryFake) FinalizeExecution(_ context.Context, record FinalizeExecutionRecord) (Refund, error) {
	repository.finalized = append(repository.finalized, record)
	if repository.final.ID.IsZero() {
		repository.final = repository.prepared.Execution.Refund
	}
	repository.final.Status = record.Status
	repository.final.ProviderRefundID = record.ProviderRefundID
	repository.final.Version++
	return repository.final, nil
}

type executionAdapterFake struct {
	result payment.RefundResult
	err    error
	seen   payment.RefundRequest
	calls  int
}

func (adapter *executionAdapterFake) Name() string { return "stripe" }
func (*executionAdapterFake) CreateIntent(context.Context, payment.CreateIntentRequest) (payment.Intent, error) {
	return payment.Intent{}, errors.New("not used")
}
func (*executionAdapterFake) RetrieveIntent(context.Context, string) (payment.Intent, error) {
	return payment.Intent{}, errors.New("not used")
}
func (*executionAdapterFake) CancelIntent(context.Context, string) (payment.Intent, error) {
	return payment.Intent{}, errors.New("not used")
}
func (adapter *executionAdapterFake) CreateRefund(_ context.Context, request payment.RefundRequest) (payment.RefundResult, error) {
	adapter.calls++
	adapter.seen = request
	return adapter.result, adapter.err
}
func (*executionAdapterFake) VerifyWebhook(context.Context, []byte, map[string]string) (payment.WebhookEvent, error) {
	return payment.WebhookEvent{}, errors.New("not used")
}

func TestExecutionServiceFinalizesSuccessfulProviderResult(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	value := executionRefund(t, now)
	repository := &executionRepositoryFake{prepared: PrepareExecutionResult{Execution: Execution{Refund: value, Provider: "stripe", PaymentReference: "pi_123"}, Call: true}, final: value}
	adapter := &executionAdapterFake{result: payment.RefundResult{Provider: "stripe", ProviderRefundID: "re_123", State: payment.RefundStateSucceeded, AmountMinor: 500, Currency: "USD"}}
	service := NewExecutionService(repository, adapter, clock.NewFixed(now))
	result, err := service.Execute(context.Background(), ExecuteInput{OrganizationID: value.OrganizationID, RefundID: value.ID})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Refund.Status != StatusSucceeded || result.Refund.ProviderRefundID != "re_123" || len(repository.finalized) != 1 || repository.finalized[0].Status != StatusSucceeded || adapter.calls != 1 || adapter.seen.IdempotencyKey != value.IdempotencyKey {
		t.Fatalf("execution result/repository/adapter = %#v/%#v/%#v", result, repository, adapter)
	}
}

func TestExecutionServiceLeavesTransientProviderFailureRetryable(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	value := executionRefund(t, now)
	repository := &executionRepositoryFake{prepared: PrepareExecutionResult{Execution: Execution{Refund: value, Provider: "stripe", PaymentReference: "pi_123"}, Call: true}}
	adapter := &executionAdapterFake{err: errors.New("provider timeout")}
	service := NewExecutionService(repository, adapter, clock.NewFixed(now))
	_, err := service.Execute(context.Background(), ExecuteInput{OrganizationID: value.OrganizationID, RefundID: value.ID})
	if err == nil || len(repository.finalized) != 0 || adapter.calls != 1 {
		t.Fatalf("transient execution = err %v finalized %#v calls %d", err, repository.finalized, adapter.calls)
	}
}

func TestExecutionServiceDoesNotCallProviderForTerminalReplay(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	value := executionRefund(t, now)
	value.Status = StatusSucceeded
	repository := &executionRepositoryFake{prepared: PrepareExecutionResult{Execution: Execution{Refund: value, Provider: "stripe", PaymentReference: "pi_123"}, Call: false}}
	adapter := &executionAdapterFake{}
	service := NewExecutionService(repository, adapter, clock.NewFixed(now))
	result, err := service.Execute(context.Background(), ExecuteInput{OrganizationID: value.OrganizationID, RefundID: value.ID})
	if err != nil || !result.Replay || adapter.calls != 0 || len(repository.finalized) != 0 {
		t.Fatalf("terminal replay = %#v err %v adapter %#v repo %#v", result, err, adapter, repository)
	}
}

func executionRefund(t *testing.T, now time.Time) Refund {
	t.Helper()
	return Refund{ID: executionID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5"), OrganizationID: executionID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6"), OrderID: executionID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef7"), PaymentAttemptID: executionID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef8"), AmountMinor: 500, Currency: "USD", Status: StatusProcessing, IdempotencyKey: "refund-execution-0001", ActorType: "user", ActorID: "user-1", Version: 2, CreatedAt: now, UpdatedAt: now}
}

func executionID(t *testing.T, value string) identifier.ID {
	t.Helper()
	id, err := identifier.Parse(value)
	if err != nil {
		t.Fatalf("Parse(%q) error = %v", value, err)
	}
	return id
}
