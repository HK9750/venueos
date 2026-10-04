package refund

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/HK9750/venueos/internal/payment"
	"github.com/HK9750/venueos/internal/platform/apperror"
	"github.com/HK9750/venueos/internal/platform/clock"
	"github.com/HK9750/venueos/internal/platform/identifier"
)

const refundProviderTimeout = 10 * time.Second

// Execution contains only the provider facts required after the database
// transaction has marked a refund as processing. The provider call happens
// outside the transaction and uses the refund idempotency key.
type Execution struct {
	Refund
	Provider         string
	PaymentReference string
}

type PrepareExecutionRecord struct {
	OrganizationID identifier.ID
	RefundID       identifier.ID
	AuditID        identifier.ID
	OutboxID       identifier.ID
	OccurredAt     time.Time
}

type PrepareExecutionResult struct {
	Execution Execution
	Call      bool
}

type FinalizeExecutionRecord struct {
	OrganizationID   identifier.ID
	RefundID         identifier.ID
	ExpectedVersion  int64
	Status           Status
	ProviderRefundID string
	AuditID          identifier.ID
	OutboxID         identifier.ID
	OccurredAt       time.Time
}

// ExecutionRepository is separate from request creation so callers cannot
// accidentally use provider execution methods to bypass refund ceilings.
type ExecutionRepository interface {
	PrepareExecution(context.Context, PrepareExecutionRecord) (PrepareExecutionResult, error)
	FinalizeExecution(context.Context, FinalizeExecutionRecord) (Refund, error)
}

type ExecutionService struct {
	repository ExecutionRepository
	adapter    payment.Adapter
	clock      clock.Clock
}

type ExecuteInput struct {
	OrganizationID identifier.ID
	RefundID       identifier.ID
}

type ExecuteResult struct {
	Refund Refund
	Replay bool
}

func NewExecutionService(repository ExecutionRepository, adapter payment.Adapter, timeSource clock.Clock) *ExecutionService {
	if timeSource == nil {
		timeSource = clock.System{}
	}
	return &ExecutionService{repository: repository, adapter: adapter, clock: timeSource}
}

// Execute runs one bounded provider attempt. The database marks the request
// processing first; no network call is made while a database lock is held. A
// transient or unknown provider failure leaves the processing request for a
// later idempotent retry/reconciliation. Permanent failure is finalized.
func (service *ExecutionService) Execute(ctx context.Context, input ExecuteInput) (ExecuteResult, error) {
	if service == nil || service.repository == nil || service.adapter == nil {
		return ExecuteResult{}, apperror.New(apperror.CodeDependencyUnavailable, "Refund execution is not configured.")
	}
	if input.OrganizationID.IsZero() || input.RefundID.IsZero() {
		return ExecuteResult{}, apperror.New(apperror.CodeValidationFailed, "The refund execution request is invalid.")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now := service.clock.Now().UTC()
	auditID, err := identifier.New()
	if err != nil {
		return ExecuteResult{}, apperror.Wrap(err, apperror.CodeInternal, "A refund execution audit identifier could not be created.")
	}
	outboxID, err := identifier.New()
	if err != nil {
		return ExecuteResult{}, apperror.Wrap(err, apperror.CodeInternal, "A refund execution event identifier could not be created.")
	}
	prepared, err := service.repository.PrepareExecution(ctx, PrepareExecutionRecord{OrganizationID: input.OrganizationID, RefundID: input.RefundID, AuditID: auditID, OutboxID: outboxID, OccurredAt: now})
	if errors.Is(err, ErrNotFound) {
		return ExecuteResult{}, apperror.Wrap(err, apperror.CodeNotFound, "The refund does not exist.")
	}
	if err != nil {
		return ExecuteResult{}, err
	}
	if !prepared.Call {
		return ExecuteResult{Refund: prepared.Execution.Refund, Replay: true}, nil
	}
	request := payment.RefundRequest{OrganizationID: prepared.Execution.OrganizationID, OrderID: prepared.Execution.OrderID, PaymentReference: prepared.Execution.PaymentReference, AmountMinor: prepared.Execution.AmountMinor, Currency: prepared.Execution.Currency, IdempotencyKey: prepared.Execution.IdempotencyKey}
	if _, err := request.Normalize(); err != nil {
		return ExecuteResult{}, apperror.Wrap(err, apperror.CodeInternal, "The stored refund execution facts are invalid.")
	}
	providerCtx, cancel := context.WithTimeout(ctx, refundProviderTimeout)
	result, err := service.adapter.CreateRefund(providerCtx, request)
	cancel()
	if err != nil {
		failure := payment.Classify(err)
		if failure.Class != payment.FailurePermanent {
			return ExecuteResult{}, failure
		}
		return service.finalize(ctx, prepared.Execution.Refund, StatusFailed, "")
	}
	if err := validateRefundResult(result, request, service.adapter.Name()); err != nil {
		return ExecuteResult{}, err
	}
	switch result.State {
	case payment.RefundStateSucceeded:
		return service.finalize(ctx, prepared.Execution.Refund, StatusSucceeded, result.ProviderRefundID)
	case payment.RefundStateFailed:
		return service.finalize(ctx, prepared.Execution.Refund, StatusFailed, result.ProviderRefundID)
	case payment.RefundStateProcessing:
		return ExecuteResult{}, &payment.Failure{Class: payment.FailureTransient, Code: "refund_processing", SafeMessage: "The refund provider is still processing the request."}
	default:
		return ExecuteResult{}, payment.Failure{Class: payment.FailureUnknown, Code: "refund_state", SafeMessage: "The refund provider returned an unsupported state."}
	}
}

func (service *ExecutionService) finalize(ctx context.Context, current Refund, status Status, providerRefundID string) (ExecuteResult, error) {
	auditID, err := identifier.New()
	if err != nil {
		return ExecuteResult{}, apperror.Wrap(err, apperror.CodeInternal, "A refund finalization audit identifier could not be created.")
	}
	outboxID, err := identifier.New()
	if err != nil {
		return ExecuteResult{}, apperror.Wrap(err, apperror.CodeInternal, "A refund finalization event identifier could not be created.")
	}
	updated, err := service.repository.FinalizeExecution(ctx, FinalizeExecutionRecord{OrganizationID: current.OrganizationID, RefundID: current.ID, ExpectedVersion: current.Version, Status: status, ProviderRefundID: providerRefundID, AuditID: auditID, OutboxID: outboxID, OccurredAt: service.clock.Now().UTC()})
	if err != nil {
		return ExecuteResult{}, err
	}
	return ExecuteResult{Refund: updated}, nil
}

func validateRefundResult(result payment.RefundResult, request payment.RefundRequest, adapterName string) error {
	if !strings.EqualFold(strings.TrimSpace(result.Provider), strings.TrimSpace(adapterName)) {
		return fmt.Errorf("%w: refund provider does not match adapter", ErrInvalidRefund)
	}
	if result.AmountMinor != request.AmountMinor || !strings.EqualFold(strings.TrimSpace(result.Currency), strings.TrimSpace(request.Currency)) {
		return fmt.Errorf("%w: refund provider amount or currency does not match", payment.ErrAmountMismatch)
	}
	if result.State != payment.RefundStateSucceeded && result.State != payment.RefundStateProcessing && result.State != payment.RefundStateFailed {
		return fmt.Errorf("%w: provider refund state is invalid", payment.ErrInvalidEvent)
	}
	if result.State != payment.RefundStateFailed && !validReference(result.ProviderRefundID, 1, 255) {
		return fmt.Errorf("%w: provider refund reference is invalid", payment.ErrInvalidEvent)
	}
	if result.ProviderRefundID != "" && !validReference(result.ProviderRefundID, 1, 255) {
		return fmt.Errorf("%w: provider refund reference is invalid", payment.ErrInvalidEvent)
	}
	return nil
}
