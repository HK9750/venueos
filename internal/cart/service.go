package cart

import (
	"context"
	"errors"

	"github.com/HK9750/venueos/internal/access"
	"github.com/HK9750/venueos/internal/platform/apperror"
	"github.com/HK9750/venueos/internal/platform/clock"
	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/internal/platform/money"
	"github.com/HK9750/venueos/internal/pricing"
)

type Service struct {
	repository Repository
	clock      clock.Clock
}

type CreateCartInput struct {
	OrganizationID identifier.ID
	SessionID      identifier.ID
	HoldID         identifier.ID
	OwnerTokenHash [32]byte
	Currency       string
	Quote          pricing.Quote
}

type GetCartInput struct {
	OrganizationID identifier.ID
	CartID         identifier.ID
	OwnerTokenHash [32]byte
}

type UpdateCartQuoteInput struct {
	OrganizationID  identifier.ID
	CartID          identifier.ID
	OwnerTokenHash  [32]byte
	ExpectedVersion int64
	Quote           pricing.Quote
}

func NewService(repository Repository, timeSource clock.Clock) *Service {
	return &Service{repository: repository, clock: timeSource}
}

// Create is the staff/verified-principal path. Guest cart authorization remains
// intentionally deferred until Q-P02 chooses guest checkout versus accounts.
func (service *Service) Create(ctx context.Context, input CreateCartInput) (Cart, error) {
	authorization, err := service.requireOrganization(ctx, input.OrganizationID, access.PermissionOrderCreate)
	if err != nil {
		return Cart{}, err
	}
	cartID, err := identifier.New()
	if err != nil {
		return Cart{}, apperror.Wrap(err, apperror.CodeInternal, "A cart identifier could not be created.")
	}
	auditID, err := identifier.New()
	if err != nil {
		return Cart{}, apperror.Wrap(err, apperror.CodeInternal, "A cart audit identifier could not be created.")
	}
	outboxID, err := identifier.New()
	if err != nil {
		return Cart{}, apperror.Wrap(err, apperror.CodeInternal, "A cart event identifier could not be created.")
	}
	ownerUserID := principalUserID(authorization.Principal())
	value, err := Build(CreateInput{ID: cartID, OrganizationID: authorization.OrganizationID(), SessionID: input.SessionID, HoldID: input.HoldID, OwnerTokenHash: input.OwnerTokenHash, OwnerUserID: ownerUserID, Currency: input.Currency, Quote: input.Quote, Now: service.clock.Now().UTC()})
	if err != nil {
		return Cart{}, mapError(err)
	}
	created, err := service.repository.Create(ctx, CreateRecord{Cart: value, AuditID: auditID, OutboxID: outboxID, ActorType: string(authorization.Principal().Type()), ActorID: authorization.Principal().ID()})
	return created, mapError(err)
}

func (service *Service) Get(ctx context.Context, input GetCartInput) (Cart, error) {
	authorization, err := service.requireOrganization(ctx, input.OrganizationID, access.PermissionOrderRead)
	if err != nil {
		return Cart{}, err
	}
	if input.CartID.IsZero() || input.OwnerTokenHash == [32]byte{} {
		return Cart{}, apperror.Wrap(ErrNotFound, apperror.CodeNotFound, "The requested cart does not exist.")
	}
	value, err := service.repository.Get(ctx, authorization.OrganizationID(), input.CartID, input.OwnerTokenHash)
	return value, mapError(err)
}

func (service *Service) UpdateQuote(ctx context.Context, input UpdateCartQuoteInput) (Cart, error) {
	authorization, err := service.requireOrganization(ctx, input.OrganizationID, access.PermissionOrderCreate)
	if err != nil {
		return Cart{}, err
	}
	if input.CartID.IsZero() || input.OwnerTokenHash == [32]byte{} || input.ExpectedVersion < 1 {
		return Cart{}, apperror.New(apperror.CodeValidationFailed, "The cart update request is invalid.", apperror.Detail{Field: "cart_id", Code: "invalid", Message: "A cart, owner token, and positive version are required."})
	}
	auditID, err := identifier.New()
	if err != nil {
		return Cart{}, apperror.Wrap(err, apperror.CodeInternal, "A cart audit identifier could not be created.")
	}
	outboxID, err := identifier.New()
	if err != nil {
		return Cart{}, apperror.Wrap(err, apperror.CodeInternal, "A cart event identifier could not be created.")
	}
	updated, err := service.repository.UpdateQuote(ctx, UpdateQuoteRecord{OrganizationID: authorization.OrganizationID(), CartID: input.CartID, OwnerTokenHash: input.OwnerTokenHash, ExpectedVersion: input.ExpectedVersion, Quote: input.Quote, AuditID: auditID, OutboxID: outboxID, ActorType: string(authorization.Principal().Type()), ActorID: authorization.Principal().ID(), OccurredAt: service.clock.Now().UTC()})
	return updated, mapError(err)
}

func (service *Service) requireOrganization(ctx context.Context, organizationID identifier.ID, permission access.Permission) (access.Authorization, error) {
	authorization, err := access.Require(ctx, permission)
	if err != nil {
		if errors.Is(err, access.ErrUnauthenticated) {
			return access.Authorization{}, apperror.Wrap(err, apperror.CodeUnauthenticated, "Authentication is required.")
		}
		return access.Authorization{}, apperror.Wrap(err, apperror.CodePermissionDenied, "Cart access is not permitted.")
	}
	if organizationID.IsZero() || authorization.OrganizationID() != organizationID {
		return access.Authorization{}, apperror.Wrap(ErrNotFound, apperror.CodeNotFound, "The requested cart does not exist.")
	}
	return authorization, nil
}

func principalUserID(principal access.Principal) *identifier.ID {
	if principal.Type() != access.PrincipalUser {
		return nil
	}
	value, err := identifier.Parse(principal.ID())
	if err != nil {
		return nil
	}
	return &value
}

func mapError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotFound):
		return apperror.Wrap(err, apperror.CodeNotFound, "The requested cart does not exist.")
	case errors.Is(err, ErrActiveCartExists), errors.Is(err, ErrHoldNotActive):
		return apperror.Wrap(err, apperror.CodeConflict, "The cart cannot be changed in the current hold state.")
	case errors.Is(err, ErrVersionConflict):
		return apperror.Wrap(err, apperror.CodePreconditionFailed, "The cart changed; reload it and retry.")
	case errors.Is(err, ErrInvalidCart):
		return apperror.Wrap(err, apperror.CodeValidationFailed, "The cart request is invalid.")
	case errors.Is(err, money.ErrCurrencyMismatch):
		return apperror.Wrap(err, apperror.CodeConflict, "The cart currency does not match the hold.")
	default:
		return err
	}
}
