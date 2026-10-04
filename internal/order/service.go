package order

import (
	"context"
	"errors"
	"strings"

	"github.com/HK9750/venueos/internal/access"
	"github.com/HK9750/venueos/internal/platform/apperror"
	"github.com/HK9750/venueos/internal/platform/identifier"
)

type Service struct {
	repository Repository
}

type CreateFromCartServiceInput struct {
	OrganizationID identifier.ID
	CartID         identifier.ID
	OwnerTokenHash [32]byte
}

type GetServiceInput struct {
	OrganizationID identifier.ID
	OrderID        identifier.ID
	OwnerTokenHash [32]byte
}

type GetStaffServiceInput struct {
	OrganizationID identifier.ID
	OrderID        identifier.ID
}

func NewService(repository Repository) *Service { return &Service{repository: repository} }

// Create is the verified-principal checkout foundation. It persists the
// payment_pending order and checks out the cart atomically; provider calls and
// guest identity remain outside this boundary until their policies are chosen.
func (service *Service) Create(ctx context.Context, input CreateFromCartServiceInput) (Order, error) {
	authorization, err := service.requireOrganization(ctx, input.OrganizationID, access.PermissionOrderCreate)
	if err != nil {
		return Order{}, err
	}
	if input.CartID.IsZero() || input.OwnerTokenHash == [32]byte{} {
		return Order{}, apperror.New(apperror.CodeValidationFailed, "The checkout request is invalid.", apperror.Detail{Field: "cart_id", Code: "invalid", Message: "A cart and owner token are required."})
	}
	orderID, err := identifier.New()
	if err != nil {
		return Order{}, apperror.Wrap(err, apperror.CodeInternal, "An order identifier could not be created.")
	}
	auditID, err := identifier.New()
	if err != nil {
		return Order{}, apperror.Wrap(err, apperror.CodeInternal, "An order audit identifier could not be created.")
	}
	outboxID, err := identifier.New()
	if err != nil {
		return Order{}, apperror.Wrap(err, apperror.CodeInternal, "An order event identifier could not be created.")
	}
	cartAuditID, err := identifier.New()
	if err != nil {
		return Order{}, apperror.Wrap(err, apperror.CodeInternal, "A cart audit identifier could not be created.")
	}
	cartOutboxID, err := identifier.New()
	if err != nil {
		return Order{}, apperror.Wrap(err, apperror.CodeInternal, "A cart event identifier could not be created.")
	}
	value, err := service.repository.CreateFromCart(ctx, CreateFromCartRecord{Order: Order{ID: orderID, OrganizationID: authorization.OrganizationID(), CartID: input.CartID, OrderNumber: makeOrderNumber(orderID), OwnerTokenHash: input.OwnerTokenHash}, AuditID: auditID, OutboxID: outboxID, CartAuditID: cartAuditID, CartOutboxID: cartOutboxID, ActorType: string(authorization.Principal().Type()), ActorID: authorization.Principal().ID()})
	return value, mapError(err)
}

func (service *Service) Get(ctx context.Context, input GetServiceInput) (Order, error) {
	authorization, err := service.requireOrganization(ctx, input.OrganizationID, access.PermissionOrderRead)
	if err != nil {
		return Order{}, err
	}
	if input.OrderID.IsZero() || input.OwnerTokenHash == [32]byte{} {
		return Order{}, apperror.Wrap(ErrNotFound, apperror.CodeNotFound, "The requested order does not exist.")
	}
	value, err := service.repository.Get(ctx, authorization.OrganizationID(), input.OrderID, input.OwnerTokenHash)
	return value, mapError(err)
}

func (service *Service) GetStaff(ctx context.Context, input GetStaffServiceInput) (Order, error) {
	authorization, err := service.requireOrganization(ctx, input.OrganizationID, access.PermissionOrderRead)
	if err != nil {
		return Order{}, err
	}
	if input.OrderID.IsZero() {
		return Order{}, apperror.Wrap(ErrNotFound, apperror.CodeNotFound, "The requested order does not exist.")
	}
	value, err := service.repository.GetStaff(ctx, authorization.OrganizationID(), input.OrderID)
	return value, mapError(err)
}

func (service *Service) ListStaff(ctx context.Context, organizationID identifier.ID, limit int32, after *Cursor) (Page, error) {
	authorization, err := service.requireOrganization(ctx, organizationID, access.PermissionOrderRead)
	if err != nil {
		return Page{}, err
	}
	if limit == 0 {
		limit = DefaultLimit
	}
	if limit < 1 || limit > MaxLimit {
		return Page{}, apperror.New(apperror.CodeValidationFailed, "The order list request is invalid.", apperror.Detail{Field: "limit", Code: "out_of_range", Message: "Use a limit between 1 and 100."})
	}
	if after != nil && (after.OrganizationID != authorization.OrganizationID() || after.ID.IsZero() || after.CreatedAt.IsZero()) {
		return Page{}, apperror.New(apperror.CodeInvalidCursor, "The order cursor is invalid.")
	}
	return service.repository.ListStaff(ctx, authorization.OrganizationID(), limit, after)
}

func (service *Service) requireOrganization(ctx context.Context, organizationID identifier.ID, permission access.Permission) (access.Authorization, error) {
	authorization, err := access.Require(ctx, permission)
	if err != nil {
		if errors.Is(err, access.ErrUnauthenticated) {
			return access.Authorization{}, apperror.Wrap(err, apperror.CodeUnauthenticated, "Authentication is required.")
		}
		return access.Authorization{}, apperror.Wrap(err, apperror.CodePermissionDenied, "Order access is not permitted.")
	}
	if organizationID.IsZero() || authorization.OrganizationID() != organizationID {
		return access.Authorization{}, apperror.Wrap(ErrNotFound, apperror.CodeNotFound, "The requested order does not exist.")
	}
	return authorization, nil
}

func makeOrderNumber(id identifier.ID) string {
	compact := strings.ReplaceAll(id.String(), "-", "")
	return "VO-" + strings.ToUpper(compact[len(compact)-20:])
}

func mapError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotFound):
		return apperror.Wrap(err, apperror.CodeNotFound, "The requested order does not exist.")
	case errors.Is(err, ErrCartNotActive), errors.Is(err, ErrHoldNotActive), errors.Is(err, ErrOrderExists):
		return apperror.Wrap(err, apperror.CodeConflict, "The checkout cannot proceed in the current cart or hold state.")
	case errors.Is(err, ErrVersionConflict):
		return apperror.Wrap(err, apperror.CodePreconditionFailed, "The cart changed; reload it and retry.")
	case errors.Is(err, ErrInvalidOrder):
		return apperror.Wrap(err, apperror.CodeValidationFailed, "The order request is invalid.")
	default:
		return err
	}
}
