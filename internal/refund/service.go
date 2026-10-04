package refund

import (
	"context"
	"errors"
	"fmt"

	"github.com/HK9750/venueos/internal/access"
	"github.com/HK9750/venueos/internal/platform/apperror"
	"github.com/HK9750/venueos/internal/platform/clock"
	"github.com/HK9750/venueos/internal/platform/identifier"
)

// RequestInput is the authenticated staff command for creating a durable
// refund request. Organization and actor identity are derived from context;
// callers cannot use the request body to widen either scope.
type RequestInput struct {
	OrganizationID   identifier.ID
	OrderID          identifier.ID
	PaymentAttemptID identifier.ID
	AmountMinor      int64
	Currency         string
	IdempotencyKey   string
	Reason           string
}

type GetInput struct {
	OrganizationID identifier.ID
	OrderID        identifier.ID
	RefundID       identifier.ID
}

type Service struct {
	repository Repository
	clock      clock.Clock
}

func NewService(repository Repository, timeSource clock.Clock) *Service {
	if timeSource == nil {
		timeSource = clock.System{}
	}
	return &Service{repository: repository, clock: timeSource}
}

// Request records the requested state and its audit/outbox entries atomically.
// Provider execution is deliberately left to the refund worker.
func (service *Service) Request(ctx context.Context, input RequestInput) (Refund, error) {
	authorization, err := service.requireOrganization(ctx, input.OrganizationID)
	if err != nil {
		return Refund{}, err
	}
	refundID, err := identifier.New()
	if err != nil {
		return Refund{}, apperror.Wrap(err, apperror.CodeInternal, "A refund identifier could not be created.")
	}
	auditID, err := identifier.New()
	if err != nil {
		return Refund{}, apperror.Wrap(err, apperror.CodeInternal, "A refund audit identifier could not be created.")
	}
	outboxID, err := identifier.New()
	if err != nil {
		return Refund{}, apperror.Wrap(err, apperror.CodeInternal, "A refund event identifier could not be created.")
	}
	value, err := Build(CreateInput{
		ID:               refundID,
		OrganizationID:   authorization.OrganizationID(),
		OrderID:          input.OrderID,
		PaymentAttemptID: input.PaymentAttemptID,
		AmountMinor:      input.AmountMinor,
		Currency:         input.Currency,
		IdempotencyKey:   input.IdempotencyKey,
		Reason:           input.Reason,
		ActorType:        string(authorization.Principal().Type()),
		ActorID:          authorization.Principal().ID(),
		Now:              service.clock.Now().UTC(),
	})
	if err != nil {
		return Refund{}, mapError(err)
	}
	created, err := service.repository.Create(ctx, CreateRecord{Refund: value, AuditID: auditID, OutboxID: outboxID})
	return created, mapError(err)
}

func (service *Service) Get(ctx context.Context, input GetInput) (Refund, error) {
	authorization, err := service.requireOrganization(ctx, input.OrganizationID)
	if err != nil {
		return Refund{}, err
	}
	if input.OrderID.IsZero() || input.RefundID.IsZero() {
		return Refund{}, mapError(ErrNotFound)
	}
	value, err := service.repository.Get(ctx, authorization.OrganizationID(), input.RefundID)
	if err != nil {
		return Refund{}, mapError(err)
	}
	if value.OrderID != input.OrderID {
		return Refund{}, mapError(ErrNotFound)
	}
	return value, nil
}

func (service *Service) requireOrganization(ctx context.Context, organizationID identifier.ID) (access.Authorization, error) {
	authorization, err := access.Require(ctx, access.PermissionOrderRefund)
	if err != nil {
		if errors.Is(err, access.ErrUnauthenticated) {
			return access.Authorization{}, apperror.Wrap(err, apperror.CodeUnauthenticated, "Authentication is required.")
		}
		return access.Authorization{}, apperror.Wrap(err, apperror.CodePermissionDenied, "Refund access is not permitted.")
	}
	if organizationID.IsZero() || authorization.OrganizationID() != organizationID {
		return access.Authorization{}, apperror.Wrap(ErrNotFound, apperror.CodeNotFound, "The requested refund target does not exist.")
	}
	return authorization, nil
}

func mapError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotFound):
		return apperror.Wrap(err, apperror.CodeNotFound, "The requested refund target does not exist.")
	case errors.Is(err, ErrExists):
		return apperror.Wrap(err, apperror.CodeIdempotencyKeyReused, "The idempotency key was already used with different refund details.")
	case errors.Is(err, ErrExceedsCaptured), errors.Is(err, ErrInvalidTransition):
		return apperror.Wrap(err, apperror.CodeConflict, "The refund cannot be created in the current payment state.")
	case errors.Is(err, ErrInvalidAmount), errors.Is(err, ErrInvalidRefund):
		return apperror.Wrap(err, apperror.CodeValidationFailed, "The refund request is invalid.")
	default:
		return fmt.Errorf("refund request: %w", err)
	}
}

var _ interface {
	Request(context.Context, RequestInput) (Refund, error)
} = (*Service)(nil)
