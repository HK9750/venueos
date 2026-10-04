package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/HK9750/venueos/internal/access"
	"github.com/HK9750/venueos/internal/audit"
	"github.com/HK9750/venueos/internal/channel"
	"github.com/HK9750/venueos/internal/device"
	"github.com/HK9750/venueos/internal/entry"
	"github.com/HK9750/venueos/internal/event"
	"github.com/HK9750/venueos/internal/inventory"
	"github.com/HK9750/venueos/internal/invitation"
	"github.com/HK9750/venueos/internal/membership"
	"github.com/HK9750/venueos/internal/order"
	"github.com/HK9750/venueos/internal/organization"
	"github.com/HK9750/venueos/internal/platform/apperror"
	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/internal/pricing"
	"github.com/HK9750/venueos/internal/publication"
	"github.com/HK9750/venueos/internal/refund"
	"github.com/HK9750/venueos/internal/session"
	"github.com/HK9750/venueos/internal/user"
	"github.com/HK9750/venueos/internal/venue"
	"github.com/HK9750/venueos/pkg/requestid"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

const maxRequestBodyBytes = 1 << 20

type UserService interface {
	Create(context.Context, user.CreateInput) (user.User, error)
	Get(context.Context, uuid.UUID) (user.User, error)
	List(context.Context, int32, int32) ([]user.User, error)
}

type Readiness interface {
	Ping(context.Context) error
}

type OrganizationService interface {
	Create(context.Context, organization.CreateInput) (organization.Organization, bool, error)
	Get(context.Context, identifier.ID) (organization.Organization, error)
}

type APIKeyService interface {
	List(context.Context, identifier.ID, int32, *access.APIKeyCursor) (access.APIKeyPage, error)
	Revoke(context.Context, access.RevokeAPIKeyInput) (access.APIKey, error)
}

type APIKeyCommandService interface {
	Create(context.Context, access.CreateAPIKeyInput) (access.IssuedAPIKey, error)
	Rotate(context.Context, access.RotateAPIKeyInput) (access.IssuedAPIKey, error)
}

type AuditService interface {
	List(context.Context, audit.ListInput) (audit.Page, error)
}

type MembershipService interface {
	List(context.Context, identifier.ID, int32, *membership.Cursor) (membership.Page, error)
	ChangeRole(context.Context, membership.ChangeRoleInput) (membership.Membership, error)
	Revoke(context.Context, membership.RevokeInput) (membership.Membership, error)
}

type InvitationService interface {
	Issue(context.Context, invitation.IssueInput) (invitation.IssueResult, error)
	Accept(context.Context, invitation.AcceptInput) (invitation.Invitation, error)
	Revoke(context.Context, invitation.RevokeInput) (invitation.Invitation, error)
}

type EventService interface {
	Create(context.Context, event.CreateInput) (event.Event, error)
	Get(context.Context, identifier.ID, identifier.ID) (event.Event, error)
	List(context.Context, identifier.ID, int32, *event.Cursor) (event.Page, error)
	Update(context.Context, event.UpdateInput) (event.Event, error)
	Publish(context.Context, event.PublishInput) (event.Event, error)
	Clone(context.Context, event.CloneInput) (event.Event, error)
}

type PublicationService interface {
	Validate(context.Context, identifier.ID, identifier.ID) (publication.Report, error)
}

type SessionService interface {
	Create(context.Context, session.CreateInput) (session.Session, error)
	Get(context.Context, identifier.ID, identifier.ID, identifier.ID) (session.Session, error)
	List(context.Context, identifier.ID, identifier.ID, int32, *session.Cursor) (session.Page, error)
}

type InventoryService interface {
	GetAvailabilitySnapshot(context.Context, identifier.ID, identifier.ID) (inventory.AvailabilitySnapshot, error)
	ListAvailabilityEvents(context.Context, identifier.ID, identifier.ID, int64, int32) (inventory.AvailabilityReplayPage, error)
}

type EntryService interface {
	Summary(context.Context, entry.SummaryInput) (entry.Summary, error)
}

type EntryScanService interface {
	Scan(context.Context, entry.ScanInput) (entry.ScanResult, error)
}

type DeviceService interface {
	List(context.Context, identifier.ID, int32, *device.Cursor) (device.Page, error)
	Create(context.Context, device.CreateInput) (device.IssuedDevice, error)
	Get(context.Context, identifier.ID, identifier.ID) (device.Device, error)
	ChangeState(context.Context, device.ChangeStateInput) (device.Device, error)
	ListAssignments(context.Context, identifier.ID, identifier.ID, int32, *device.AssignmentCursor) (device.AssignmentPage, error)
	CreateAssignment(context.Context, device.CreateAssignmentInput) (device.Assignment, error)
	RevokeAssignment(context.Context, device.RevokeAssignmentInput) (device.Assignment, error)
}

type PricingService interface {
	Create(context.Context, pricing.CreateInput) (pricing.PriceTier, error)
	List(context.Context, identifier.ID, identifier.ID, int32, *pricing.Cursor) (pricing.Page, error)
}

type SalesChannelService interface {
	Create(context.Context, channel.CreateInput) (channel.SalesChannel, error)
	List(context.Context, identifier.ID, int32, *channel.Cursor) (channel.Page, error)
	CreateAllocation(context.Context, channel.CreateAllocationInput) (channel.Allocation, error)
	ListAllocations(context.Context, identifier.ID, identifier.ID, int32, *channel.AllocationCursor) (channel.AllocationPage, error)
}

type VenueService interface {
	CreateVenue(context.Context, venue.CreateVenueInput) (venue.Venue, error)
	GetVenue(context.Context, identifier.ID, identifier.ID) (venue.Venue, error)
	ListVenues(context.Context, identifier.ID, int32, *venue.Cursor) (venue.Page[venue.Venue], error)
	UpdateVenue(context.Context, venue.UpdateVenueInput) (venue.Venue, error)
	ArchiveVenue(context.Context, venue.ArchiveVenueInput) (venue.Venue, error)
	RestoreVenue(context.Context, venue.RestoreVenueInput) (venue.Venue, error)
	CreateSpace(context.Context, venue.CreateSpaceInput) (venue.Space, error)
	GetSpace(context.Context, identifier.ID, identifier.ID, identifier.ID) (venue.Space, error)
	ListSpaces(context.Context, identifier.ID, identifier.ID, int32, *venue.Cursor) (venue.Page[venue.Space], error)
	CreateGate(context.Context, venue.CreateGateInput) (venue.Gate, error)
	GetGate(context.Context, identifier.ID, identifier.ID, identifier.ID) (venue.Gate, error)
	ListGates(context.Context, identifier.ID, identifier.ID, int32, *venue.Cursor) (venue.Page[venue.Gate], error)
	CreateGAPool(context.Context, venue.CreateGAPoolInput) (venue.GAPoolTemplate, error)
	GetGAPool(context.Context, identifier.ID, identifier.ID, identifier.ID) (venue.GAPoolTemplate, error)
	ListGAPools(context.Context, identifier.ID, identifier.ID, int32, *venue.Cursor) (venue.Page[venue.GAPoolTemplate], error)
	CreateSeatMap(context.Context, venue.CreateSeatMapInput) (venue.SeatMap, error)
	GetSeatMap(context.Context, identifier.ID, identifier.ID, identifier.ID) (venue.SeatMap, error)
	ListSeatMaps(context.Context, identifier.ID, identifier.ID, int32, *venue.Cursor) (venue.Page[venue.SeatMap], error)
	UpdateSeatMap(context.Context, venue.UpdateSeatMapInput) (venue.SeatMap, error)
	PublishSeatMap(context.Context, venue.PublishSeatMapInput) (venue.SeatMap, error)
	CloneSeatMap(context.Context, venue.CloneSeatMapInput) (venue.SeatMap, error)
}

type OrderService interface {
	GetStaff(context.Context, order.GetStaffServiceInput) (order.Order, error)
	ListStaff(context.Context, identifier.ID, int32, *order.Cursor) (order.Page, error)
}

type RefundService interface {
	Request(context.Context, refund.RequestInput) (refund.Refund, error)
	Get(context.Context, refund.GetInput) (refund.Refund, error)
}

type Server struct {
	users         UserService
	organizations OrganizationService
	apiKeys       APIKeyService
	audits        AuditService
	memberships   MembershipService
	invitations   InvitationService
	events        EventService
	sessions      SessionService
	inventory     InventoryService
	entries       EntryService
	devices       DeviceService
	pricing       PricingService
	channels      SalesChannelService
	publication   PublicationService
	venues        VenueService
	refunds       RefundService
	orders        OrderService
	readiness     Readiness
	logger        *slog.Logger
}

func NewServer(users UserService, readiness Readiness, logger *slog.Logger) *Server {
	return &Server{users: users, readiness: readiness, logger: logger}
}

func (s *Server) WithOrganizations(service OrganizationService) *Server {
	s.organizations = service
	return s
}

func (s *Server) WithAPIKeys(service APIKeyService) *Server {
	s.apiKeys = service
	return s
}

func (s *Server) WithAudit(service AuditService) *Server {
	s.audits = service
	return s
}

func (s *Server) WithMemberships(service MembershipService) *Server {
	s.memberships = service
	return s
}

func (s *Server) WithInvitations(service InvitationService) *Server {
	s.invitations = service
	return s
}

func (s *Server) WithEvents(service EventService) *Server {
	s.events = service
	return s
}

func (s *Server) WithSessions(service SessionService) *Server {
	s.sessions = service
	return s
}

func (s *Server) WithInventory(service InventoryService) *Server {
	s.inventory = service
	return s
}

func (s *Server) WithEntry(service EntryService) *Server {
	s.entries = service
	return s
}

func (s *Server) WithDevices(service DeviceService) *Server {
	s.devices = service
	return s
}

func (s *Server) WithPricing(service PricingService) *Server {
	s.pricing = service
	return s
}

func (s *Server) WithSalesChannels(service SalesChannelService) *Server {
	s.channels = service
	return s
}

func (s *Server) WithPublication(service PublicationService) *Server {
	s.publication = service
	return s
}

func (s *Server) WithVenues(service VenueService) *Server {
	s.venues = service
	return s
}

func (s *Server) WithRefunds(service RefundService) *Server {
	s.refunds = service
	return s
}

func (s *Server) WithOrders(service OrderService) *Server {
	s.orders = service
	return s
}

func (s *Server) Healthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) Readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.readiness.Ping(ctx); err != nil {
		s.logger.WarnContext(r.Context(), "readiness check failed", slog.Any("error", err), slog.Group("request", requestLogAttrs(r.Context())...))
		writeAPIError(w, r, http.StatusServiceUnavailable, apperror.CodeDependencyUnavailable, "A required dependency is unavailable.", nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) CreateRefund(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, orderID uuid.UUID, params CreateRefundParams) {
	if s.refunds == nil {
		s.writeApplicationError(w, r, errors.New("refund service is not configured"))
		return
	}
	orgID, err := identifier.FromUUID(organizationID)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "The organization identifier is invalid.", nil)
		return
	}
	targetOrderID, err := identifier.FromUUID(orderID)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "The order identifier is invalid.", nil)
		return
	}
	var body CreateRefund
	if !s.decodeJSONBody(w, r, &body) {
		return
	}
	paymentAttemptID, err := identifier.FromUUID(body.PaymentAttemptId)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "The payment attempt identifier is invalid.", nil)
		return
	}
	created, err := s.refunds.Request(r.Context(), refund.RequestInput{
		OrganizationID:   orgID,
		OrderID:          targetOrderID,
		PaymentAttemptID: paymentAttemptID,
		AmountMinor:      body.AmountMinor,
		Currency:         body.Currency,
		IdempotencyKey:   params.IdempotencyKey,
		Reason:           body.Reason,
	})
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	s.writeRefund(w, http.StatusCreated, created)
}

func (s *Server) GetRefund(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, orderID uuid.UUID, refundID uuid.UUID) {
	if s.refunds == nil {
		s.writeApplicationError(w, r, errors.New("refund service is not configured"))
		return
	}
	orgID, err := identifier.FromUUID(organizationID)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "The organization identifier is invalid.", nil)
		return
	}
	targetOrderID, err := identifier.FromUUID(orderID)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "The order identifier is invalid.", nil)
		return
	}
	targetRefundID, err := identifier.FromUUID(refundID)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "The refund identifier is invalid.", nil)
		return
	}
	value, err := s.refunds.Get(r.Context(), refund.GetInput{OrganizationID: orgID, OrderID: targetOrderID, RefundID: targetRefundID})
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	s.writeRefund(w, http.StatusOK, value)
}

func (s *Server) GetStaffOrder(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, orderID uuid.UUID) {
	if s.orders == nil {
		s.writeApplicationError(w, r, errors.New("order service is not configured"))
		return
	}
	orgID, err := identifier.FromUUID(organizationID)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "The organization identifier is invalid.", nil)
		return
	}
	targetOrderID, err := identifier.FromUUID(orderID)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "The order identifier is invalid.", nil)
		return
	}
	value, err := s.orders.GetStaff(r.Context(), order.GetStaffServiceInput{OrganizationID: orgID, OrderID: targetOrderID})
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, orderResponse(value))
}

func (s *Server) ListStaffOrders(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, params ListStaffOrdersParams) {
	if s.orders == nil {
		s.writeApplicationError(w, r, errors.New("order service is not configured"))
		return
	}
	orgID, err := identifier.FromUUID(organizationID)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "The organization identifier is invalid.", nil)
		return
	}
	limit := int32(50)
	if params.Limit != nil {
		limit = *params.Limit
	}
	var after *order.Cursor
	if params.Cursor != nil {
		decoded, decodeErr := order.DecodeCursor(*params.Cursor)
		if decodeErr != nil {
			s.writeApplicationError(w, r, decodeErr)
			return
		}
		after = &decoded
	}
	page, err := s.orders.ListStaff(r.Context(), orgID, limit, after)
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	response := OrderPage{Items: make([]Order, 0, len(page.Items)), Page: CursorPage{HasMore: page.NextCursor != nil}}
	for _, value := range page.Items {
		response.Items = append(response.Items, orderResponse(value))
	}
	if page.NextCursor != nil {
		cursor, encodeErr := order.EncodeCursor(*page.NextCursor)
		if encodeErr != nil {
			s.writeApplicationError(w, r, encodeErr)
			return
		}
		response.Page.NextCursor = &cursor
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) ListUsers(w http.ResponseWriter, r *http.Request, params ListUsersParams) {
	limit, offset := int32(20), int32(0)
	if params.Limit != nil {
		limit = *params.Limit
	}
	if params.Offset != nil {
		offset = *params.Offset
	}

	items, err := s.users.List(r.Context(), limit, offset)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	response := UserList{Items: make([]User, 0, len(items)), Limit: int(limit), Offset: int(offset)}
	for _, item := range items {
		response.Items = append(response.Items, userResponse(item))
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) CreateUser(w http.ResponseWriter, r *http.Request) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeAPIError(w, r, http.StatusUnsupportedMediaType, apperror.CodeUnsupportedMediaType, "Content-Type must be application/json.", nil)
		return
	}

	var body CreateUser
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	if err := decodeJSON(r.Body, &body); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeAPIError(w, r, http.StatusRequestEntityTooLarge, apperror.CodePayloadTooLarge, "Request body must not exceed 1 MiB.", nil)
			return
		}
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeMalformedRequest, "Request body must be one valid JSON object.", nil)
		return
	}

	created, err := s.users.Create(r.Context(), user.CreateInput{Email: string(body.Email), Name: body.Name})
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	w.Header().Set("Location", "/v1/users/"+created.ID.String())
	writeJSON(w, http.StatusCreated, userResponse(created))
}

func (s *Server) GetUser(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	found, err := s.users.Get(r.Context(), id)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, userResponse(found))
}

func (s *Server) CreateOrganization(w http.ResponseWriter, r *http.Request, params CreateOrganizationParams) {
	if s.organizations == nil {
		s.writeApplicationError(w, r, errors.New("organization service is not configured"))
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeAPIError(w, r, http.StatusUnsupportedMediaType, apperror.CodeUnsupportedMediaType, "Content-Type must be application/json.", nil)
		return
	}
	var body CreateOrganization
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	if err := decodeJSON(r.Body, &body); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeAPIError(w, r, http.StatusRequestEntityTooLarge, apperror.CodePayloadTooLarge, "Request body must not exceed 1 MiB.", nil)
			return
		}
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeMalformedRequest, "Request body must be one valid JSON object.", nil)
		return
	}
	created, replayed, err := s.organizations.Create(r.Context(), organization.CreateInput{
		Slug: body.Slug, DisplayName: body.DisplayName, DefaultLocale: body.DefaultLocale,
		DefaultTimezone: body.DefaultTimezone, DefaultCurrency: body.DefaultCurrency,
		IdempotencyKey: params.IdempotencyKey,
	})
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
	} else {
		w.Header().Set("Location", "/v1/organizations/"+created.ID.String())
	}
	writeJSON(w, status, organizationResponse(created))
}

func (s *Server) GetOrganization(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID) {
	if s.organizations == nil {
		s.writeApplicationError(w, r, errors.New("organization service is not configured"))
		return
	}
	id, err := identifier.FromUUID(organizationID)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "The organization identifier is invalid.", nil)
		return
	}
	found, err := s.organizations.Get(r.Context(), id)
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, organizationResponse(found))
}

func (s *Server) ListAPIKeys(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, params ListAPIKeysParams) {
	if s.apiKeys == nil {
		s.writeApplicationError(w, r, errors.New("API key service is not configured"))
		return
	}
	orgID, ok := s.parseOrganizationID(w, r, organizationID)
	if !ok {
		return
	}
	limit := int32(0)
	if params.Limit != nil {
		limit = *params.Limit
	}
	var after *access.APIKeyCursor
	if params.Cursor != nil {
		decoded, decodeErr := access.DecodeAPIKeyCursor(*params.Cursor)
		if decodeErr != nil {
			s.writeApplicationError(w, r, decodeErr)
			return
		}
		after = &decoded
	}
	page, err := s.apiKeys.List(r.Context(), orgID, limit, after)
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	response := APIKeyPage{Items: make([]APIKey, 0, len(page.Items))}
	response.Page.HasMore = page.NextCursor != nil
	if page.NextCursor != nil {
		cursor, encodeErr := access.EncodeAPIKeyCursor(*page.NextCursor)
		if encodeErr != nil {
			s.writeApplicationError(w, r, encodeErr)
			return
		}
		response.Page.NextCursor = &cursor
	}
	for _, item := range page.Items {
		response.Items = append(response.Items, apiKeyResponse(item))
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) CreateAPIKey(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID) {
	commandService, ok := s.apiKeys.(APIKeyCommandService)
	if !ok {
		s.writeApplicationError(w, r, errors.New("API key command service is not configured"))
		return
	}
	orgID, ok := s.parseOrganizationID(w, r, organizationID)
	if !ok {
		return
	}
	var body CreateAPIKey
	if !s.decodeJSONBody(w, r, &body) {
		return
	}
	scopes := make([]access.Permission, 0, len(body.Scopes))
	for _, scope := range body.Scopes {
		scopes = append(scopes, access.Permission(scope))
	}
	issued, err := commandService.Create(r.Context(), access.CreateAPIKeyInput{OrganizationID: orgID, Name: body.Name, Scopes: scopes, ExpiresAt: body.ExpiresAt})
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	token := issued.Token
	w.Header().Set("Location", "/v1/organizations/"+organizationID.String()+"/api-keys/"+issued.APIKey.ID.String())
	w.Header().Set("ETag", fmt.Sprintf("\"%d\"", issued.APIKey.Version))
	writeJSON(w, http.StatusCreated, IssuedAPIKey{ApiKey: apiKeyResponse(issued.APIKey), Token: &token})
}

func (s *Server) RevokeAPIKey(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, keyID uuid.UUID, params RevokeAPIKeyParams) {
	if s.apiKeys == nil {
		s.writeApplicationError(w, r, errors.New("API key service is not configured"))
		return
	}
	orgID, ok := s.parseOrganizationID(w, r, organizationID)
	if !ok {
		return
	}
	targetID, err := identifier.FromUUID(keyID)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "The API key identifier is invalid.", nil)
		return
	}
	version, err := parseIfMatch(params.IfMatch)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "If-Match must contain a positive API key version.", nil)
		return
	}
	revoked, err := s.apiKeys.Revoke(r.Context(), access.RevokeAPIKeyInput{
		OrganizationID: orgID, APIKeyID: targetID, ExpectedVersion: version,
	})
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf("\"%d\"", revoked.Version))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) RotateAPIKey(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, keyID uuid.UUID, params RotateAPIKeyParams) {
	commandService, ok := s.apiKeys.(APIKeyCommandService)
	if !ok {
		s.writeApplicationError(w, r, errors.New("API key command service is not configured"))
		return
	}
	orgID, ok := s.parseOrganizationID(w, r, organizationID)
	if !ok {
		return
	}
	targetID, err := identifier.FromUUID(keyID)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "The API key identifier is invalid.", nil)
		return
	}
	version, err := parseIfMatch(params.IfMatch)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "If-Match must contain a positive API key version.", nil)
		return
	}
	var body RotateAPIKey
	if r.ContentLength != 0 {
		if !s.decodeJSONBody(w, r, &body) {
			return
		}
	}
	issued, err := commandService.Rotate(r.Context(), access.RotateAPIKeyInput{OrganizationID: orgID, APIKeyID: targetID, ExpectedVersion: version, ExpiresAt: body.ExpiresAt})
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	token := issued.Token
	w.Header().Set("ETag", fmt.Sprintf("\"%d\"", issued.APIKey.Version))
	writeJSON(w, http.StatusOK, IssuedAPIKey{ApiKey: apiKeyResponse(issued.APIKey), Token: &token})
}

func (s *Server) ListDevices(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, params ListDevicesParams) {
	if s.devices == nil {
		s.writeApplicationError(w, r, errors.New("device service is not configured"))
		return
	}
	orgID, ok := s.parseOrganizationID(w, r, organizationID)
	if !ok {
		return
	}
	limit := int32(0)
	if params.Limit != nil {
		limit = *params.Limit
	}
	var after *device.Cursor
	if params.Cursor != nil {
		decoded, decodeErr := device.DecodeCursor(*params.Cursor)
		if decodeErr != nil {
			s.writeApplicationError(w, r, apperror.Wrap(decodeErr, apperror.CodeInvalidCursor, "The device cursor is invalid."))
			return
		}
		after = &decoded
	}
	page, err := s.devices.List(r.Context(), orgID, limit, after)
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	response := DevicePage{Items: make([]Device, 0, len(page.Items))}
	response.Page.HasMore = page.NextCursor != nil
	if page.NextCursor != nil {
		cursor, encodeErr := device.EncodeCursor(*page.NextCursor)
		if encodeErr != nil {
			s.writeApplicationError(w, r, encodeErr)
			return
		}
		response.Page.NextCursor = &cursor
	}
	for _, item := range page.Items {
		response.Items = append(response.Items, deviceResponse(item))
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) CreateDevice(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID) {
	if s.devices == nil {
		s.writeApplicationError(w, r, errors.New("device service is not configured"))
		return
	}
	orgID, ok := s.parseOrganizationID(w, r, organizationID)
	if !ok {
		return
	}
	var body CreateDevice
	if !s.decodeJSONBody(w, r, &body) {
		return
	}
	venueID, err := identifier.FromUUID(body.VenueId)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "The venue identifier is invalid.", nil)
		return
	}
	issued, err := s.devices.Create(r.Context(), device.CreateInput{OrganizationID: orgID, VenueID: venueID, Name: body.Name, Platform: body.Platform, AppVersion: body.AppVersion})
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	w.Header().Set("Location", "/v1/organizations/"+organizationID.String()+"/devices/"+issued.Device.ID.String())
	w.Header().Set("ETag", fmt.Sprintf("\"%d\"", issued.Device.Version))
	writeJSON(w, http.StatusCreated, issuedDeviceResponse(issued))
}

func (s *Server) GetDevice(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, deviceID uuid.UUID) {
	if s.devices == nil {
		s.writeApplicationError(w, r, errors.New("device service is not configured"))
		return
	}
	orgID, targetID, ok := s.resourceIDs(w, r, organizationID, deviceID, "device")
	if !ok {
		return
	}
	found, err := s.devices.Get(r.Context(), orgID, targetID)
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf("\"%d\"", found.Version))
	writeJSON(w, http.StatusOK, deviceResponse(found))
}

func (s *Server) ChangeDeviceState(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, deviceID uuid.UUID, params ChangeDeviceStateParams) {
	if s.devices == nil {
		s.writeApplicationError(w, r, errors.New("device service is not configured"))
		return
	}
	orgID, targetID, version, ok := s.resourceCommandInputs(w, r, organizationID, deviceID, params.IfMatch, "device")
	if !ok {
		return
	}
	var body ChangeDeviceState
	if !s.decodeJSONBody(w, r, &body) {
		return
	}
	updated, err := s.devices.ChangeState(r.Context(), device.ChangeStateInput{OrganizationID: orgID, DeviceID: targetID, State: device.State(body.State), ExpectedVersion: version})
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf("\"%d\"", updated.Version))
	writeJSON(w, http.StatusOK, deviceResponse(updated))
}

func (s *Server) ListDeviceAssignments(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, deviceID uuid.UUID, params ListDeviceAssignmentsParams) {
	if s.devices == nil {
		s.writeApplicationError(w, r, errors.New("device service is not configured"))
		return
	}
	orgID, targetID, ok := s.resourceIDs(w, r, organizationID, deviceID, "device")
	if !ok {
		return
	}
	limit := int32(0)
	if params.Limit != nil {
		limit = *params.Limit
	}
	var after *device.AssignmentCursor
	if params.Cursor != nil {
		decoded, decodeErr := device.DecodeAssignmentCursor(*params.Cursor)
		if decodeErr != nil {
			s.writeApplicationError(w, r, apperror.Wrap(decodeErr, apperror.CodeInvalidCursor, "The assignment cursor is invalid."))
			return
		}
		after = &decoded
	}
	page, err := s.devices.ListAssignments(r.Context(), orgID, targetID, limit, after)
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	response := DeviceAssignmentPage{Items: make([]DeviceAssignment, 0, len(page.Items))}
	response.Page.HasMore = page.NextCursor != nil
	if page.NextCursor != nil {
		cursor, encodeErr := device.EncodeAssignmentCursor(*page.NextCursor)
		if encodeErr != nil {
			s.writeApplicationError(w, r, encodeErr)
			return
		}
		response.Page.NextCursor = &cursor
	}
	for _, item := range page.Items {
		response.Items = append(response.Items, deviceAssignmentResponse(item))
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) CreateDeviceAssignment(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, deviceID uuid.UUID) {
	if s.devices == nil {
		s.writeApplicationError(w, r, errors.New("device service is not configured"))
		return
	}
	orgID, targetID, ok := s.resourceIDs(w, r, organizationID, deviceID, "device")
	if !ok {
		return
	}
	var body CreateDeviceAssignment
	if !s.decodeJSONBody(w, r, &body) {
		return
	}
	var sessionID, gateID *identifier.ID
	if body.SessionId != nil {
		value, err := identifier.FromUUID(*body.SessionId)
		if err != nil {
			writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "The session identifier is invalid.", nil)
			return
		}
		sessionID = &value
	}
	if body.GateId != nil {
		value, err := identifier.FromUUID(*body.GateId)
		if err != nil {
			writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "The gate identifier is invalid.", nil)
			return
		}
		gateID = &value
	}
	capability := access.PermissionEntryScan
	if body.Capability != nil {
		capability = access.Permission(*body.Capability)
	}
	created, err := s.devices.CreateAssignment(r.Context(), device.CreateAssignmentInput{OrganizationID: orgID, DeviceID: targetID, SessionID: sessionID, GateID: gateID, Capability: capability, ValidFrom: timeValue(body.ValidFrom), ValidUntil: body.ValidUntil})
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	w.Header().Set("Location", "/v1/organizations/"+organizationID.String()+"/devices/"+deviceID.String()+"/assignments/"+created.ID.String())
	w.Header().Set("ETag", fmt.Sprintf("\"%d\"", created.Version))
	writeJSON(w, http.StatusCreated, deviceAssignmentResponse(created))
}

func (s *Server) RevokeDeviceAssignment(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, deviceID uuid.UUID, assignmentID uuid.UUID, params RevokeDeviceAssignmentParams) {
	if s.devices == nil {
		s.writeApplicationError(w, r, errors.New("device service is not configured"))
		return
	}
	orgID, targetID, ok := s.resourceIDs(w, r, organizationID, deviceID, "device")
	if !ok {
		return
	}
	assignment, err := identifier.FromUUID(assignmentID)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "The assignment identifier is invalid.", nil)
		return
	}
	version, err := parseIfMatch(params.IfMatch)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "If-Match must contain a positive assignment version.", nil)
		return
	}
	revoked, err := s.devices.RevokeAssignment(r.Context(), device.RevokeAssignmentInput{OrganizationID: orgID, DeviceID: targetID, AssignmentID: assignment, ExpectedVersion: version})
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf("\"%d\"", revoked.Version))
	writeJSON(w, http.StatusOK, deviceAssignmentResponse(revoked))
}

func (s *Server) ListAuditEntries(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, params ListAuditEntriesParams) {
	if s.audits == nil {
		s.writeApplicationError(w, r, errors.New("audit service is not configured"))
		return
	}
	orgID, ok := s.parseOrganizationID(w, r, organizationID)
	if !ok {
		return
	}
	input := audit.ListInput{OrganizationID: orgID}
	if params.Limit != nil {
		input.Limit = *params.Limit
	}
	if params.ActorType != nil {
		input.ActorType = *params.ActorType
	}
	if params.ActorId != nil {
		input.ActorID = *params.ActorId
	}
	if params.Action != nil {
		input.Action = *params.Action
	}
	if params.SubjectType != nil {
		input.SubjectType = *params.SubjectType
	}
	if params.SubjectId != nil {
		input.SubjectID = *params.SubjectId
	}
	if params.Result != nil {
		input.Result = string(*params.Result)
	}
	if params.RequestId != nil {
		input.RequestID = *params.RequestId
	}
	input.OccurredFrom = params.OccurredFrom
	input.OccurredUntil = params.OccurredUntil
	if params.Cursor != nil {
		cursor, decodeErr := audit.DecodeCursor(*params.Cursor)
		if decodeErr != nil {
			s.writeApplicationError(w, r, decodeErr)
			return
		}
		input.After = &cursor
	}
	page, err := s.audits.List(r.Context(), input)
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	response := AuditEntryPage{Items: make([]AuditEntry, 0, len(page.Items))}
	response.Page.HasMore = page.NextCursor != nil
	if page.NextCursor != nil {
		cursor, encodeErr := audit.EncodeCursor(*page.NextCursor)
		if encodeErr != nil {
			s.writeApplicationError(w, r, encodeErr)
			return
		}
		response.Page.NextCursor = &cursor
	}
	for _, item := range page.Items {
		response.Items = append(response.Items, auditEntryResponse(item))
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) CreateInvitation(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID) {
	if s.invitations == nil {
		s.writeApplicationError(w, r, errors.New("invitation service is not configured"))
		return
	}
	orgID, err := identifier.FromUUID(organizationID)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "The organization identifier is invalid.", nil)
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeAPIError(w, r, http.StatusUnsupportedMediaType, apperror.CodeUnsupportedMediaType, "Content-Type must be application/json.", nil)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	var body CreateInvitation
	if err := decodeJSON(r.Body, &body); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeAPIError(w, r, http.StatusRequestEntityTooLarge, apperror.CodePayloadTooLarge, "Request body must not exceed 1 MiB.", nil)
			return
		}
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeMalformedRequest, "Request body must be one valid JSON object.", nil)
		return
	}
	issued, err := s.invitations.Issue(r.Context(), invitation.IssueInput{OrganizationID: orgID, Email: string(body.Email), Role: access.Role(body.Role)})
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, issuedInvitationResponse(issued))
}

func (s *Server) AcceptInvitation(w http.ResponseWriter, r *http.Request) {
	if s.invitations == nil {
		s.writeApplicationError(w, r, errors.New("invitation service is not configured"))
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeAPIError(w, r, http.StatusUnsupportedMediaType, apperror.CodeUnsupportedMediaType, "Content-Type must be application/json.", nil)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	var body AcceptInvitation
	if err := decodeJSON(r.Body, &body); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeAPIError(w, r, http.StatusRequestEntityTooLarge, apperror.CodePayloadTooLarge, "Request body must not exceed 1 MiB.", nil)
			return
		}
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeMalformedRequest, "Request body must be one valid JSON object.", nil)
		return
	}
	accepted, err := s.invitations.Accept(r.Context(), invitation.AcceptInput{Token: body.Token})
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, invitationResponse(accepted))
}

func (s *Server) RevokeInvitation(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, invitationID uuid.UUID, params RevokeInvitationParams) {
	if s.invitations == nil {
		s.writeApplicationError(w, r, errors.New("invitation service is not configured"))
		return
	}
	orgID, targetID, version, ok := s.invitationCommandInputs(w, r, organizationID, invitationID, params.IfMatch)
	if !ok {
		return
	}
	if _, err := s.invitations.Revoke(r.Context(), invitation.RevokeInput{OrganizationID: orgID, InvitationID: targetID, Version: version}); err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) ListEvents(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, params ListEventsParams) {
	if s.events == nil {
		s.writeApplicationError(w, r, errors.New("event service is not configured"))
		return
	}
	orgID, ok := s.parseOrganizationID(w, r, organizationID)
	if !ok {
		return
	}
	limit := int32(0)
	if params.Limit != nil {
		limit = *params.Limit
	}
	var after *event.Cursor
	if params.Cursor != nil {
		decoded, decodeErr := event.DecodeCursor(*params.Cursor)
		if decodeErr != nil {
			s.writeApplicationError(w, r, decodeErr)
			return
		}
		after = &decoded
	}
	page, err := s.events.List(r.Context(), orgID, limit, after)
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	response := EventPage{Items: make([]Event, 0, len(page.Items))}
	response.Page.HasMore = page.NextCursor != nil
	if page.NextCursor != nil {
		cursor, encodeErr := event.EncodeCursor(*page.NextCursor)
		if encodeErr != nil {
			s.writeApplicationError(w, r, encodeErr)
			return
		}
		response.Page.NextCursor = &cursor
	}
	for _, item := range page.Items {
		response.Items = append(response.Items, eventResponse(item))
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) CreateEvent(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID) {
	if s.events == nil {
		s.writeApplicationError(w, r, errors.New("event service is not configured"))
		return
	}
	orgID, ok := s.parseOrganizationID(w, r, organizationID)
	if !ok {
		return
	}
	var body CreateEvent
	if !s.decodeJSONBody(w, r, &body) {
		return
	}
	created, err := s.events.Create(r.Context(), event.CreateInput{OrganizationID: orgID, Slug: body.Slug, Title: body.Title, ShortDescription: optionalString(body.ShortDescription), LongDescription: optionalString(body.LongDescription)})
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf("\"%d\"", created.Version))
	writeJSON(w, http.StatusCreated, eventResponse(created))
}

func (s *Server) GetEvent(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, eventID uuid.UUID) {
	if s.events == nil {
		s.writeApplicationError(w, r, errors.New("event service is not configured"))
		return
	}
	orgID, targetID, ok := s.resourceIDs(w, r, organizationID, eventID, "event")
	if !ok {
		return
	}
	found, err := s.events.Get(r.Context(), orgID, targetID)
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf("\"%d\"", found.Version))
	writeJSON(w, http.StatusOK, eventResponse(found))
}

func (s *Server) UpdateEvent(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, eventID uuid.UUID, params UpdateEventParams) {
	if s.events == nil {
		s.writeApplicationError(w, r, errors.New("event service is not configured"))
		return
	}
	orgID, targetID, version, ok := s.resourceCommandInputs(w, r, organizationID, eventID, params.IfMatch, "event")
	if !ok {
		return
	}
	var body UpdateEvent
	if !s.decodeJSONBody(w, r, &body) {
		return
	}
	updated, err := s.events.Update(r.Context(), event.UpdateInput{OrganizationID: orgID, EventID: targetID, Slug: body.Slug, Title: body.Title, ShortDescription: optionalString(body.ShortDescription), LongDescription: optionalString(body.LongDescription), Version: version})
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf("\"%d\"", updated.Version))
	writeJSON(w, http.StatusOK, eventResponse(updated))
}

func (s *Server) CloneEvent(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, eventID uuid.UUID) {
	if s.events == nil {
		s.writeApplicationError(w, r, errors.New("event service is not configured"))
		return
	}
	orgID, targetID, ok := s.resourceIDs(w, r, organizationID, eventID, "event")
	if !ok {
		return
	}
	cloned, err := s.events.Clone(r.Context(), event.CloneInput{OrganizationID: orgID, EventID: targetID})
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf("\"%d\"", cloned.Version))
	writeJSON(w, http.StatusCreated, eventResponse(cloned))
}

func (s *Server) PublishEvent(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, eventID uuid.UUID, params PublishEventParams) {
	if s.events == nil {
		s.writeApplicationError(w, r, errors.New("event service is not configured"))
		return
	}
	orgID, targetID, version, ok := s.resourceCommandInputs(w, r, organizationID, eventID, params.IfMatch, "event")
	if !ok {
		return
	}
	published, err := s.events.Publish(r.Context(), event.PublishInput{OrganizationID: orgID, EventID: targetID, Version: version})
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf("\"%d\"", published.Version))
	writeJSON(w, http.StatusOK, eventResponse(published))
}

func (s *Server) ValidateEventPublication(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, eventID uuid.UUID) {
	if s.publication == nil {
		s.writeApplicationError(w, r, errors.New("publication service is not configured"))
		return
	}
	orgID, targetID, ok := s.resourceIDs(w, r, organizationID, eventID, "event")
	if !ok {
		return
	}
	report, err := s.publication.Validate(r.Context(), orgID, targetID)
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, publicationReportResponse(report))
}

func (s *Server) ListSessions(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, eventID uuid.UUID, params ListSessionsParams) {
	if s.sessions == nil {
		s.writeApplicationError(w, r, errors.New("session service is not configured"))
		return
	}
	orgID, targetEventID, ok := s.resourceIDs(w, r, organizationID, eventID, "event")
	if !ok {
		return
	}
	limit := int32(0)
	if params.Limit != nil {
		limit = *params.Limit
	}
	var after *session.Cursor
	if params.Cursor != nil {
		decoded, decodeErr := session.DecodeCursor(*params.Cursor)
		if decodeErr != nil {
			s.writeApplicationError(w, r, decodeErr)
			return
		}
		after = &decoded
	}
	page, err := s.sessions.List(r.Context(), orgID, targetEventID, limit, after)
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	response := SessionPage{Items: make([]Session, 0, len(page.Items))}
	response.Page.HasMore = page.NextCursor != nil
	if page.NextCursor != nil {
		cursor, encodeErr := session.EncodeCursor(*page.NextCursor)
		if encodeErr != nil {
			s.writeApplicationError(w, r, encodeErr)
			return
		}
		response.Page.NextCursor = &cursor
	}
	for _, item := range page.Items {
		response.Items = append(response.Items, sessionResponse(item))
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) CreateSession(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, eventID uuid.UUID) {
	if s.sessions == nil {
		s.writeApplicationError(w, r, errors.New("session service is not configured"))
		return
	}
	orgID, targetEventID, ok := s.resourceIDs(w, r, organizationID, eventID, "event")
	if !ok {
		return
	}
	var body CreateSession
	if !s.decodeJSONBody(w, r, &body) {
		return
	}
	venueID, err := identifier.FromUUID(body.VenueId)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "The venue identifier is invalid.", nil)
		return
	}
	spaceID, err := identifier.FromUUID(body.SpaceId)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "The space identifier is invalid.", nil)
		return
	}
	var seatMapID *identifier.ID
	if body.SeatMapVersionId != nil {
		parsed, parseErr := identifier.FromUUID(*body.SeatMapVersionId)
		if parseErr != nil {
			writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "The seat-map identifier is invalid.", nil)
			return
		}
		seatMapID = &parsed
	}
	created, err := s.sessions.Create(r.Context(), session.CreateInput{OrganizationID: orgID, EventID: targetEventID, EventRevision: body.EventRevision, VenueID: venueID, SpaceID: spaceID, SeatMapVersionID: seatMapID, DoorsAt: body.DoorsAt, StartsAt: body.StartsAt, EndsAt: body.EndsAt, SalesStartAt: body.SalesStartAt, SalesEndAt: body.SalesEndAt, Timezone: body.Timezone})
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf("\"%d\"", created.Version))
	writeJSON(w, http.StatusCreated, sessionResponse(created))
}

func (s *Server) GetSession(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, eventID uuid.UUID, sessionID uuid.UUID) {
	if s.sessions == nil {
		s.writeApplicationError(w, r, errors.New("session service is not configured"))
		return
	}
	orgID, targetEventID, targetSessionID, ok := s.sessionResourceIDs(w, r, organizationID, eventID, sessionID)
	if !ok {
		return
	}
	found, err := s.sessions.Get(r.Context(), orgID, targetEventID, targetSessionID)
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf("\"%d\"", found.Version))
	writeJSON(w, http.StatusOK, sessionResponse(found))
}

func (s *Server) GetSessionAvailability(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, sessionID uuid.UUID, params GetSessionAvailabilityParams) {
	if s.inventory == nil {
		s.writeApplicationError(w, r, errors.New("inventory service is not configured"))
		return
	}
	orgID, ok := s.parseOrganizationID(w, r, organizationID)
	if !ok {
		return
	}
	targetSessionID, err := identifier.FromUUID(sessionID)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "The session identifier is invalid.", nil)
		return
	}
	snapshot, err := s.inventory.GetAvailabilitySnapshot(r.Context(), orgID, targetSessionID)
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	etag := fmt.Sprintf("\"%s:%d\"", snapshot.SessionID.String(), snapshot.Revision)
	w.Header().Set("ETag", etag)
	if params.IfNoneMatch != nil && etagMatches(*params.IfNoneMatch, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	response := availabilitySnapshotResponse(snapshot)
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) ListSessionAvailabilityChanges(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, sessionID uuid.UUID, params ListSessionAvailabilityChangesParams) {
	if s.inventory == nil {
		s.writeApplicationError(w, r, errors.New("inventory service is not configured"))
		return
	}
	orgID, ok := s.parseOrganizationID(w, r, organizationID)
	if !ok {
		return
	}
	targetSessionID, err := identifier.FromUUID(sessionID)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "The session identifier is invalid.", nil)
		return
	}
	after := int64(0)
	if params.After != nil {
		after = *params.After
	}
	limit := int32(0)
	if params.Limit != nil {
		limit = *params.Limit
	}
	page, err := s.inventory.ListAvailabilityEvents(r.Context(), orgID, targetSessionID, after, limit)
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	response := AvailabilityReplayPage{CurrentRevision: page.CurrentRevision, Events: make([]AvailabilityEvent, 0, len(page.Events)), HasMore: page.HasMore}
	for _, item := range page.Events {
		payload := map[string]interface{}{}
		if len(item.Payload) > 0 {
			if err := json.Unmarshal(item.Payload, &payload); err != nil {
				s.writeApplicationError(w, r, fmt.Errorf("decode availability event payload: %w", err))
				return
			}
		}
		response.Events = append(response.Events, AvailabilityEvent{Id: item.ID.UUID(), SessionId: item.SessionID.UUID(), Sequence: item.Sequence, EventType: item.EventType, SchemaVersion: item.SchemaVersion, Payload: payload, OccurredAt: item.OccurredAt})
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) GetEntrySummary(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, sessionID uuid.UUID, params GetEntrySummaryParams) {
	if s.entries == nil {
		s.writeApplicationError(w, r, errors.New("entry service is not configured"))
		return
	}
	orgID, ok := s.parseOrganizationID(w, r, organizationID)
	if !ok {
		return
	}
	targetSessionID, err := identifier.FromUUID(sessionID)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "The session identifier is invalid.", nil)
		return
	}
	value, err := s.entries.Summary(r.Context(), entry.SummaryInput{OrganizationID: orgID, SessionID: targetSessionID, From: params.From, Until: params.Until})
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, entrySummaryResponse(value))
}

func (s *Server) RecordDeviceScan(w http.ResponseWriter, r *http.Request) {
	scanService, ok := s.entries.(EntryScanService)
	if !ok {
		s.writeApplicationError(w, r, errors.New("entry scan service is not configured"))
		return
	}
	authorization, err := access.AuthorizationFromContext(r.Context())
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	var body RecordDeviceScan
	if !s.decodeJSONBody(w, r, &body) {
		return
	}
	if body.Credential == nil {
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeMalformedRequest, "The ticket credential is required.", nil)
		return
	}
	var gateID *identifier.ID
	if body.GateId != nil {
		parsed, parseErr := identifier.FromUUID(*body.GateId)
		if parseErr != nil {
			writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "The gate identifier is invalid.", nil)
			return
		}
		gateID = &parsed
	}
	metadata := []byte(nil)
	if body.Metadata != nil {
		metadata, err = json.Marshal(*body.Metadata)
		if err != nil {
			writeAPIError(w, r, http.StatusBadRequest, apperror.CodeMalformedRequest, "The scan metadata is invalid.", nil)
			return
		}
	}
	result, err := scanService.Scan(r.Context(), entry.ScanInput{
		OrganizationID: authorization.OrganizationID(), GateID: gateID, DeviceScanID: body.DeviceScanId,
		Credential: *body.Credential, ScannedAt: body.ScannedAt, Metadata: metadata,
	})
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	var admissionID *openapi_types.UUID
	if result.AdmissionID != nil {
		parsed := result.AdmissionID.UUID()
		admissionID = &parsed
	}
	writeJSON(w, http.StatusOK, DeviceScanResult{
		ScanAttemptId: result.ScanAttemptID.UUID(), AdmissionId: admissionID,
		TicketId: result.Decision.TicketID.UUID(), Decision: DeviceScanResultDecision(result.Decision.Code),
		Admitted: result.Decision.Admittable, Replay: result.Replay,
	})
}

func (s *Server) sessionResourceIDs(w http.ResponseWriter, r *http.Request, organizationID, eventID, sessionID uuid.UUID) (identifier.ID, identifier.ID, identifier.ID, bool) {
	orgID, targetEventID, ok := s.resourceIDs(w, r, organizationID, eventID, "event")
	if !ok {
		return identifier.ID{}, identifier.ID{}, identifier.ID{}, false
	}
	targetSessionID, err := identifier.FromUUID(sessionID)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "The session identifier is invalid.", nil)
		return identifier.ID{}, identifier.ID{}, identifier.ID{}, false
	}
	return orgID, targetEventID, targetSessionID, true
}

func (s *Server) ListPriceTiers(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, eventID uuid.UUID, sessionID uuid.UUID, params ListPriceTiersParams) {
	if s.pricing == nil {
		s.writeApplicationError(w, r, errors.New("pricing service is not configured"))
		return
	}
	orgID, _, targetSessionID, ok := s.sessionResourceIDs(w, r, organizationID, eventID, sessionID)
	if !ok {
		return
	}
	limit := int32(0)
	if params.Limit != nil {
		limit = *params.Limit
	}
	var after *pricing.Cursor
	if params.Cursor != nil {
		decoded, decodeErr := pricing.DecodeCursor(*params.Cursor)
		if decodeErr != nil {
			s.writeApplicationError(w, r, decodeErr)
			return
		}
		after = &decoded
	}
	page, err := s.pricing.List(r.Context(), orgID, targetSessionID, limit, after)
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	response := PriceTierPage{Items: make([]PriceTier, 0, len(page.Items))}
	response.Page.HasMore = page.NextCursor != nil
	if page.NextCursor != nil {
		cursor, encodeErr := pricing.EncodeCursor(*page.NextCursor)
		if encodeErr != nil {
			s.writeApplicationError(w, r, encodeErr)
			return
		}
		response.Page.NextCursor = &cursor
	}
	for _, item := range page.Items {
		response.Items = append(response.Items, priceTierResponse(item))
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) CreatePriceTier(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, eventID uuid.UUID, sessionID uuid.UUID) {
	if s.pricing == nil {
		s.writeApplicationError(w, r, errors.New("pricing service is not configured"))
		return
	}
	orgID, targetEventID, targetSessionID, ok := s.sessionResourceIDs(w, r, organizationID, eventID, sessionID)
	if !ok {
		return
	}
	var body CreatePriceTier
	if !s.decodeJSONBody(w, r, &body) {
		return
	}
	created, err := s.pricing.Create(r.Context(), pricing.CreateInput{OrganizationID: orgID, EventID: targetEventID, SessionID: targetSessionID, Slug: body.Slug, DisplayName: body.DisplayName, Currency: body.Currency, AmountMinor: body.AmountMinor, MinimumQuantity: optionalInt32(body.MinimumQuantity), MaximumQuantity: optionalInt32(body.MaximumQuantity), SalesStartAt: body.SalesStartAt, SalesEndAt: body.SalesEndAt})
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf("\"%d\"", created.Version))
	writeJSON(w, http.StatusCreated, priceTierResponse(created))
}

func (s *Server) ListSessionChannelAllocations(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, eventID uuid.UUID, sessionID uuid.UUID, params ListSessionChannelAllocationsParams) {
	if s.channels == nil {
		s.writeApplicationError(w, r, errors.New("sales channel service is not configured"))
		return
	}
	orgID, _, targetSessionID, ok := s.sessionResourceIDs(w, r, organizationID, eventID, sessionID)
	if !ok {
		return
	}
	limit := int32(0)
	if params.Limit != nil {
		limit = *params.Limit
	}
	var after *channel.AllocationCursor
	if params.Cursor != nil {
		decoded, decodeErr := channel.DecodeAllocationCursor(*params.Cursor)
		if decodeErr != nil {
			s.writeApplicationError(w, r, decodeErr)
			return
		}
		after = &decoded
	}
	page, err := s.channels.ListAllocations(r.Context(), orgID, targetSessionID, limit, after)
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	response := SessionChannelAllocationPage{Items: make([]SessionChannelAllocation, 0, len(page.Items))}
	response.Page.HasMore = page.NextCursor != nil
	if page.NextCursor != nil {
		cursor, encodeErr := channel.EncodeAllocationCursor(*page.NextCursor)
		if encodeErr != nil {
			s.writeApplicationError(w, r, encodeErr)
			return
		}
		response.Page.NextCursor = &cursor
	}
	for _, item := range page.Items {
		response.Items = append(response.Items, allocationResponse(item))
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) CreateSessionChannelAllocation(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, eventID uuid.UUID, sessionID uuid.UUID) {
	if s.channels == nil {
		s.writeApplicationError(w, r, errors.New("sales channel service is not configured"))
		return
	}
	orgID, targetEventID, targetSessionID, ok := s.sessionResourceIDs(w, r, organizationID, eventID, sessionID)
	if !ok {
		return
	}
	var body CreateSessionChannelAllocation
	if !s.decodeJSONBody(w, r, &body) {
		return
	}
	channelID, err := identifier.FromUUID(body.ChannelId)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "The channel identifier is invalid.", nil)
		return
	}
	created, err := s.channels.CreateAllocation(r.Context(), channel.CreateAllocationInput{OrganizationID: orgID, EventID: targetEventID, SessionID: targetSessionID, ChannelID: channelID, ScopeKey: optionalString(body.ScopeKey), Mode: channel.AllocationMode(body.Mode), Quantity: body.Quantity})
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf("\"%d\"", created.Version))
	writeJSON(w, http.StatusCreated, allocationResponse(created))
}

func (s *Server) ListSalesChannels(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, params ListSalesChannelsParams) {
	if s.channels == nil {
		s.writeApplicationError(w, r, errors.New("sales channel service is not configured"))
		return
	}
	orgID, err := identifier.FromUUID(organizationID)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "The organization identifier is invalid.", nil)
		return
	}
	limit := int32(0)
	if params.Limit != nil {
		limit = *params.Limit
	}
	var after *channel.Cursor
	if params.Cursor != nil {
		decoded, decodeErr := channel.DecodeCursor(*params.Cursor)
		if decodeErr != nil {
			s.writeApplicationError(w, r, decodeErr)
			return
		}
		after = &decoded
	}
	page, err := s.channels.List(r.Context(), orgID, limit, after)
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	response := SalesChannelPage{Items: make([]SalesChannel, 0, len(page.Items))}
	response.Page.HasMore = page.NextCursor != nil
	if page.NextCursor != nil {
		cursor, encodeErr := channel.EncodeCursor(*page.NextCursor)
		if encodeErr != nil {
			s.writeApplicationError(w, r, encodeErr)
			return
		}
		response.Page.NextCursor = &cursor
	}
	for _, item := range page.Items {
		response.Items = append(response.Items, salesChannelResponse(item))
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) CreateSalesChannel(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID) {
	if s.channels == nil {
		s.writeApplicationError(w, r, errors.New("sales channel service is not configured"))
		return
	}
	orgID, ok := s.parseOrganizationID(w, r, organizationID)
	if !ok {
		return
	}
	var body CreateSalesChannel
	if !s.decodeJSONBody(w, r, &body) {
		return
	}
	var configuration map[string]any
	if body.Configuration != nil {
		configuration = *body.Configuration
	}
	created, err := s.channels.Create(r.Context(), channel.CreateInput{OrganizationID: orgID, Key: body.Key, DisplayName: body.DisplayName, Type: channel.Type(body.Type), Configuration: configuration})
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf("\"%d\"", created.Version))
	writeJSON(w, http.StatusCreated, salesChannelResponse(created))
}

func (s *Server) ListVenues(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, params ListVenuesParams) {
	if s.venues == nil {
		s.writeApplicationError(w, r, errors.New("venue service is not configured"))
		return
	}
	orgID, err := identifier.FromUUID(organizationID)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "The organization identifier is invalid.", nil)
		return
	}
	limit := int32(0)
	if params.Limit != nil {
		limit = *params.Limit
	}
	var after *venue.Cursor
	if params.Cursor != nil {
		decoded, decodeErr := venue.DecodeCursor(*params.Cursor)
		if decodeErr != nil {
			s.writeApplicationError(w, r, decodeErr)
			return
		}
		after = &decoded
	}
	page, err := s.venues.ListVenues(r.Context(), orgID, limit, after)
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	response := VenuePage{Items: make([]Venue, 0, len(page.Items))}
	response.Page.HasMore = page.NextCursor != nil
	if page.NextCursor != nil {
		cursor, encodeErr := venue.EncodeCursor(*page.NextCursor)
		if encodeErr != nil {
			s.writeApplicationError(w, r, encodeErr)
			return
		}
		response.Page.NextCursor = &cursor
	}
	for _, item := range page.Items {
		response.Items = append(response.Items, venueResponse(item))
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) CreateVenue(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID) {
	if s.venues == nil {
		s.writeApplicationError(w, r, errors.New("venue service is not configured"))
		return
	}
	orgID, ok := s.parseOrganizationID(w, r, organizationID)
	if !ok {
		return
	}
	var body CreateVenue
	if !s.decodeJSONBody(w, r, &body) {
		return
	}
	created, err := s.venues.CreateVenue(r.Context(), venue.CreateVenueInput{OrganizationID: orgID, Slug: body.Slug, DisplayName: body.DisplayName, Timezone: body.Timezone})
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, venueResponse(created))
}

func (s *Server) GetVenue(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, venueID uuid.UUID) {
	if s.venues == nil {
		s.writeApplicationError(w, r, errors.New("venue service is not configured"))
		return
	}
	orgID, targetID, ok := s.resourceIDs(w, r, organizationID, venueID, "venue")
	if !ok {
		return
	}
	found, err := s.venues.GetVenue(r.Context(), orgID, targetID)
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, venueResponse(found))
}

func (s *Server) UpdateVenue(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, venueID uuid.UUID, params UpdateVenueParams) {
	if s.venues == nil {
		s.writeApplicationError(w, r, errors.New("venue service is not configured"))
		return
	}
	orgID, targetID, version, ok := s.resourceCommandInputs(w, r, organizationID, venueID, params.IfMatch, "venue")
	if !ok {
		return
	}
	var body UpdateVenue
	if !s.decodeJSONBody(w, r, &body) {
		return
	}
	updated, err := s.venues.UpdateVenue(r.Context(), venue.UpdateVenueInput{OrganizationID: orgID, VenueID: targetID, Slug: body.Slug, DisplayName: body.DisplayName, Timezone: body.Timezone, Version: version})
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf("\"%d\"", updated.Version))
	writeJSON(w, http.StatusOK, venueResponse(updated))
}

func (s *Server) ArchiveVenue(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, venueID uuid.UUID, params ArchiveVenueParams) {
	if s.venues == nil {
		s.writeApplicationError(w, r, errors.New("venue service is not configured"))
		return
	}
	orgID, targetID, version, ok := s.resourceCommandInputs(w, r, organizationID, venueID, params.IfMatch, "venue")
	if !ok {
		return
	}
	if _, err := s.venues.ArchiveVenue(r.Context(), venue.ArchiveVenueInput{OrganizationID: orgID, VenueID: targetID, Version: version}); err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) RestoreVenue(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, venueID uuid.UUID, params RestoreVenueParams) {
	if s.venues == nil {
		s.writeApplicationError(w, r, errors.New("venue service is not configured"))
		return
	}
	orgID, targetID, version, ok := s.resourceCommandInputs(w, r, organizationID, venueID, params.IfMatch, "venue")
	if !ok {
		return
	}
	value, err := s.venues.RestoreVenue(r.Context(), venue.RestoreVenueInput{OrganizationID: orgID, VenueID: targetID, Version: version})
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf("\"%d\"", value.Version))
	writeJSON(w, http.StatusOK, venueResponse(value))
}

func (s *Server) ListSpaces(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, venueID uuid.UUID, params ListSpacesParams) {
	if s.venues == nil {
		s.writeApplicationError(w, r, errors.New("venue service is not configured"))
		return
	}
	orgID, parentID, ok := s.resourceIDs(w, r, organizationID, venueID, "venue")
	if !ok {
		return
	}
	limit := int32(0)
	if params.Limit != nil {
		limit = *params.Limit
	}
	var after *venue.Cursor
	if params.Cursor != nil {
		decoded, decodeErr := venue.DecodeCursor(*params.Cursor)
		if decodeErr != nil {
			s.writeApplicationError(w, r, decodeErr)
			return
		}
		after = &decoded
	}
	page, err := s.venues.ListSpaces(r.Context(), orgID, parentID, limit, after)
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	response := SpacePage{Items: make([]Space, 0, len(page.Items))}
	response.Page.HasMore = page.NextCursor != nil
	if page.NextCursor != nil {
		cursor, encodeErr := venue.EncodeCursor(*page.NextCursor)
		if encodeErr != nil {
			s.writeApplicationError(w, r, encodeErr)
			return
		}
		response.Page.NextCursor = &cursor
	}
	for _, item := range page.Items {
		response.Items = append(response.Items, spaceResponse(item))
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) CreateSpace(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, venueID uuid.UUID) {
	if s.venues == nil {
		s.writeApplicationError(w, r, errors.New("venue service is not configured"))
		return
	}
	orgID, parentID, ok := s.resourceIDs(w, r, organizationID, venueID, "venue")
	if !ok {
		return
	}
	var body CreateSpace
	if !s.decodeJSONBody(w, r, &body) {
		return
	}
	created, err := s.venues.CreateSpace(r.Context(), venue.CreateSpaceInput{OrganizationID: orgID, VenueID: parentID, Slug: body.Slug, DisplayName: body.DisplayName, InventoryMode: venue.InventoryMode(body.InventoryMode), PhysicalCapacity: body.PhysicalCapacity})
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, spaceResponse(created))
}

func (s *Server) GetSpace(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, venueID uuid.UUID, spaceID uuid.UUID) {
	if s.venues == nil {
		s.writeApplicationError(w, r, errors.New("venue service is not configured"))
		return
	}
	orgID, parentID, ok := s.resourceIDs(w, r, organizationID, venueID, "venue")
	if !ok {
		return
	}
	targetID, err := identifier.FromUUID(spaceID)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "The space identifier is invalid.", nil)
		return
	}
	found, err := s.venues.GetSpace(r.Context(), orgID, parentID, targetID)
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, spaceResponse(found))
}

func (s *Server) ListGates(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, venueID uuid.UUID, params ListGatesParams) {
	if s.venues == nil {
		s.writeApplicationError(w, r, errors.New("venue service is not configured"))
		return
	}
	orgID, parentID, ok := s.resourceIDs(w, r, organizationID, venueID, "venue")
	if !ok {
		return
	}
	limit := int32(0)
	if params.Limit != nil {
		limit = *params.Limit
	}
	var after *venue.Cursor
	if params.Cursor != nil {
		decoded, decodeErr := venue.DecodeCursor(*params.Cursor)
		if decodeErr != nil {
			s.writeApplicationError(w, r, decodeErr)
			return
		}
		after = &decoded
	}
	page, err := s.venues.ListGates(r.Context(), orgID, parentID, limit, after)
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	response := GatePage{Items: make([]Gate, 0, len(page.Items))}
	response.Page.HasMore = page.NextCursor != nil
	if page.NextCursor != nil {
		cursor, encodeErr := venue.EncodeCursor(*page.NextCursor)
		if encodeErr != nil {
			s.writeApplicationError(w, r, encodeErr)
			return
		}
		response.Page.NextCursor = &cursor
	}
	for _, item := range page.Items {
		response.Items = append(response.Items, gateResponse(item))
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) CreateGate(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, venueID uuid.UUID) {
	if s.venues == nil {
		s.writeApplicationError(w, r, errors.New("venue service is not configured"))
		return
	}
	orgID, parentID, ok := s.resourceIDs(w, r, organizationID, venueID, "venue")
	if !ok {
		return
	}
	var body CreateGate
	if !s.decodeJSONBody(w, r, &body) {
		return
	}
	var spaceID identifier.ID
	if body.SpaceId != nil {
		var parseErr error
		spaceID, parseErr = identifier.FromUUID(*body.SpaceId)
		if parseErr != nil {
			writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "The space identifier is invalid.", nil)
			return
		}
	}
	created, err := s.venues.CreateGate(r.Context(), venue.CreateGateInput{OrganizationID: orgID, VenueID: parentID, SpaceID: spaceID, Code: body.Code, DisplayName: body.DisplayName})
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, gateResponse(created))
}

func (s *Server) GetGate(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, venueID uuid.UUID, gateID uuid.UUID) {
	if s.venues == nil {
		s.writeApplicationError(w, r, errors.New("venue service is not configured"))
		return
	}
	orgID, parentID, ok := s.resourceIDs(w, r, organizationID, venueID, "venue")
	if !ok {
		return
	}
	targetID, err := identifier.FromUUID(gateID)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "The gate identifier is invalid.", nil)
		return
	}
	found, err := s.venues.GetGate(r.Context(), orgID, parentID, targetID)
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, gateResponse(found))
}

func (s *Server) ListGAPools(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, venueID uuid.UUID, spaceID uuid.UUID, params ListGAPoolsParams) {
	if s.venues == nil {
		s.writeApplicationError(w, r, errors.New("venue service is not configured"))
		return
	}
	orgID, parentID, ok := s.resourceIDs(w, r, organizationID, venueID, "venue")
	if !ok {
		return
	}
	spaceIDValue, err := identifier.FromUUID(spaceID)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "The space identifier is invalid.", nil)
		return
	}
	if _, err := s.venues.GetSpace(r.Context(), orgID, parentID, spaceIDValue); err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	limit := int32(0)
	if params.Limit != nil {
		limit = *params.Limit
	}
	var after *venue.Cursor
	if params.Cursor != nil {
		decoded, decodeErr := venue.DecodeCursor(*params.Cursor)
		if decodeErr != nil {
			s.writeApplicationError(w, r, decodeErr)
			return
		}
		after = &decoded
	}
	page, err := s.venues.ListGAPools(r.Context(), orgID, spaceIDValue, limit, after)
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	response := GAPoolPage{Items: make([]GAPool, 0, len(page.Items))}
	response.Page.HasMore = page.NextCursor != nil
	if page.NextCursor != nil {
		cursor, encodeErr := venue.EncodeCursor(*page.NextCursor)
		if encodeErr != nil {
			s.writeApplicationError(w, r, encodeErr)
			return
		}
		response.Page.NextCursor = &cursor
	}
	for _, item := range page.Items {
		response.Items = append(response.Items, gaPoolResponse(item))
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) CreateGAPool(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, venueID uuid.UUID, spaceID uuid.UUID) {
	if s.venues == nil {
		s.writeApplicationError(w, r, errors.New("venue service is not configured"))
		return
	}
	orgID, parentID, ok := s.resourceIDs(w, r, organizationID, venueID, "venue")
	if !ok {
		return
	}
	spaceIDValue, err := identifier.FromUUID(spaceID)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "The space identifier is invalid.", nil)
		return
	}
	if _, err := s.venues.GetSpace(r.Context(), orgID, parentID, spaceIDValue); err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	var body CreateGAPool
	if !s.decodeJSONBody(w, r, &body) {
		return
	}
	created, err := s.venues.CreateGAPool(r.Context(), venue.CreateGAPoolInput{OrganizationID: orgID, SpaceID: spaceIDValue, Slug: body.Slug, DisplayName: body.DisplayName, PhysicalCapacity: body.PhysicalCapacity, SellableCapacity: body.SellableCapacity})
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, gaPoolResponse(created))
}

func (s *Server) GetGAPool(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, venueID uuid.UUID, spaceID uuid.UUID, poolID uuid.UUID) {
	if s.venues == nil {
		s.writeApplicationError(w, r, errors.New("venue service is not configured"))
		return
	}
	orgID, parentID, ok := s.resourceIDs(w, r, organizationID, venueID, "venue")
	if !ok {
		return
	}
	spaceIDValue, err := identifier.FromUUID(spaceID)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "The space identifier is invalid.", nil)
		return
	}
	if _, err := s.venues.GetSpace(r.Context(), orgID, parentID, spaceIDValue); err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	poolIDValue, err := identifier.FromUUID(poolID)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "The general-admission pool identifier is invalid.", nil)
		return
	}
	found, err := s.venues.GetGAPool(r.Context(), orgID, spaceIDValue, poolIDValue)
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, gaPoolResponse(found))
}

func (s *Server) ListSeatMaps(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, venueID uuid.UUID, spaceID uuid.UUID, params ListSeatMapsParams) {
	if s.venues == nil {
		s.writeApplicationError(w, r, errors.New("venue service is not configured"))
		return
	}
	orgID, parentID, ok := s.resourceIDs(w, r, organizationID, venueID, "venue")
	if !ok {
		return
	}
	spaceIDValue, err := identifier.FromUUID(spaceID)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "The space identifier is invalid.", nil)
		return
	}
	if _, err := s.venues.GetSpace(r.Context(), orgID, parentID, spaceIDValue); err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	limit := int32(0)
	if params.Limit != nil {
		limit = *params.Limit
	}
	var after *venue.Cursor
	if params.Cursor != nil {
		decoded, decodeErr := venue.DecodeCursor(*params.Cursor)
		if decodeErr != nil {
			s.writeApplicationError(w, r, decodeErr)
			return
		}
		after = &decoded
	}
	page, err := s.venues.ListSeatMaps(r.Context(), orgID, spaceIDValue, limit, after)
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	response := SeatMapPage{Items: make([]SeatMap, 0, len(page.Items))}
	response.Page.HasMore = page.NextCursor != nil
	if page.NextCursor != nil {
		cursor, encodeErr := venue.EncodeCursor(*page.NextCursor)
		if encodeErr != nil {
			s.writeApplicationError(w, r, encodeErr)
			return
		}
		response.Page.NextCursor = &cursor
	}
	for _, item := range page.Items {
		response.Items = append(response.Items, seatMapResponse(item))
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) CreateSeatMap(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, venueID uuid.UUID, spaceID uuid.UUID) {
	if s.venues == nil {
		s.writeApplicationError(w, r, errors.New("venue service is not configured"))
		return
	}
	orgID, parentID, spaceIDValue, ok := s.seatMapPathIDs(w, r, organizationID, venueID, spaceID)
	if !ok {
		return
	}
	if _, err := s.venues.GetSpace(r.Context(), orgID, parentID, spaceIDValue); err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	var body CreateSeatMap
	if !s.decodeJSONBody(w, r, &body) {
		return
	}
	created, err := s.venues.CreateSeatMap(r.Context(), venue.CreateSeatMapInput{OrganizationID: orgID, SpaceID: spaceIDValue, Name: body.Name, Content: seatMapContentInput(body.Content)})
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf("\"%d\"", created.Version))
	writeJSON(w, http.StatusCreated, seatMapResponse(created))
}

func (s *Server) GetSeatMap(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, venueID uuid.UUID, spaceID uuid.UUID, seatMapID uuid.UUID) {
	if s.venues == nil {
		s.writeApplicationError(w, r, errors.New("venue service is not configured"))
		return
	}
	orgID, _, spaceIDValue, mapID, ok := s.seatMapResourceIDs(w, r, organizationID, venueID, spaceID, seatMapID)
	if !ok {
		return
	}
	found, err := s.venues.GetSeatMap(r.Context(), orgID, spaceIDValue, mapID)
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf("\"%d\"", found.Version))
	writeJSON(w, http.StatusOK, seatMapResponse(found))
}

func (s *Server) UpdateSeatMap(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, venueID uuid.UUID, spaceID uuid.UUID, seatMapID uuid.UUID, params UpdateSeatMapParams) {
	if s.venues == nil {
		s.writeApplicationError(w, r, errors.New("venue service is not configured"))
		return
	}
	orgID, _, spaceIDValue, mapID, ok := s.seatMapResourceIDs(w, r, organizationID, venueID, spaceID, seatMapID)
	if !ok {
		return
	}
	version, err := parseIfMatch(params.IfMatch)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "If-Match must contain a positive seat-map version.", nil)
		return
	}
	var body UpdateSeatMap
	if !s.decodeJSONBody(w, r, &body) {
		return
	}
	updated, err := s.venues.UpdateSeatMap(r.Context(), venue.UpdateSeatMapInput{OrganizationID: orgID, SpaceID: spaceIDValue, SeatMapID: mapID, Name: body.Name, Content: seatMapContentInput(body.Content), Version: version})
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf("\"%d\"", updated.Version))
	writeJSON(w, http.StatusOK, seatMapResponse(updated))
}

func (s *Server) CloneSeatMap(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, venueID uuid.UUID, spaceID uuid.UUID, seatMapID uuid.UUID) {
	if s.venues == nil {
		s.writeApplicationError(w, r, errors.New("venue service is not configured"))
		return
	}
	orgID, _, spaceIDValue, mapID, ok := s.seatMapResourceIDs(w, r, organizationID, venueID, spaceID, seatMapID)
	if !ok {
		return
	}
	cloned, err := s.venues.CloneSeatMap(r.Context(), venue.CloneSeatMapInput{OrganizationID: orgID, SpaceID: spaceIDValue, SeatMapID: mapID})
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf("\"%d\"", cloned.Version))
	writeJSON(w, http.StatusCreated, seatMapResponse(cloned))
}

func (s *Server) PublishSeatMap(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, venueID uuid.UUID, spaceID uuid.UUID, seatMapID uuid.UUID, params PublishSeatMapParams) {
	if s.venues == nil {
		s.writeApplicationError(w, r, errors.New("venue service is not configured"))
		return
	}
	orgID, _, spaceIDValue, mapID, ok := s.seatMapResourceIDs(w, r, organizationID, venueID, spaceID, seatMapID)
	if !ok {
		return
	}
	version, err := parseIfMatch(params.IfMatch)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "If-Match must contain a positive seat-map version.", nil)
		return
	}
	published, err := s.venues.PublishSeatMap(r.Context(), venue.PublishSeatMapInput{OrganizationID: orgID, SpaceID: spaceIDValue, SeatMapID: mapID, Version: version})
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf("\"%d\"", published.Version))
	writeJSON(w, http.StatusOK, seatMapResponse(published))
}

func (s *Server) seatMapPathIDs(w http.ResponseWriter, r *http.Request, organizationID, venueID, spaceID uuid.UUID) (identifier.ID, identifier.ID, identifier.ID, bool) {
	orgID, venueIDValue, ok := s.resourceIDs(w, r, organizationID, venueID, "venue")
	if !ok {
		return identifier.ID{}, identifier.ID{}, identifier.ID{}, false
	}
	spaceIDValue, err := identifier.FromUUID(spaceID)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "The space identifier is invalid.", nil)
		return identifier.ID{}, identifier.ID{}, identifier.ID{}, false
	}
	return orgID, venueIDValue, spaceIDValue, true
}

func (s *Server) seatMapResourceIDs(w http.ResponseWriter, r *http.Request, organizationID, venueID, spaceID, seatMapID uuid.UUID) (identifier.ID, identifier.ID, identifier.ID, identifier.ID, bool) {
	orgID, venueIDValue, spaceIDValue, ok := s.seatMapPathIDs(w, r, organizationID, venueID, spaceID)
	if !ok {
		return identifier.ID{}, identifier.ID{}, identifier.ID{}, identifier.ID{}, false
	}
	mapID, err := identifier.FromUUID(seatMapID)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "The seat-map identifier is invalid.", nil)
		return identifier.ID{}, identifier.ID{}, identifier.ID{}, identifier.ID{}, false
	}
	return orgID, venueIDValue, spaceIDValue, mapID, true
}

func (s *Server) ListMemberships(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, params ListMembershipsParams) {
	if s.memberships == nil {
		s.writeApplicationError(w, r, errors.New("membership service is not configured"))
		return
	}
	orgID, err := identifier.FromUUID(organizationID)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "The organization identifier is invalid.", nil)
		return
	}
	limit := int32(0)
	if params.Limit != nil {
		limit = *params.Limit
	}
	var after *membership.Cursor
	if params.Cursor != nil {
		decoded, decodeErr := membership.DecodeCursor(*params.Cursor)
		if decodeErr != nil {
			s.writeApplicationError(w, r, decodeErr)
			return
		}
		after = &decoded
	}
	page, err := s.memberships.List(r.Context(), orgID, limit, after)
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	response := MembershipPage{Items: make([]Membership, 0, len(page.Items))}
	response.Page.HasMore = page.NextCursor != nil
	if page.NextCursor != nil {
		cursor, encodeErr := membership.EncodeCursor(*page.NextCursor)
		if encodeErr != nil {
			s.writeApplicationError(w, r, encodeErr)
			return
		}
		response.Page.NextCursor = &cursor
	}
	for _, item := range page.Items {
		response.Items = append(response.Items, membershipResponse(item))
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) ChangeMembershipRole(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, membershipID uuid.UUID, params ChangeMembershipRoleParams) {
	if s.memberships == nil {
		s.writeApplicationError(w, r, errors.New("membership service is not configured"))
		return
	}
	orgID, targetID, version, ok := s.membershipCommandInputs(w, r, organizationID, membershipID, params.IfMatch)
	if !ok {
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeAPIError(w, r, http.StatusUnsupportedMediaType, apperror.CodeUnsupportedMediaType, "Content-Type must be application/json.", nil)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	var body ChangeMembershipRole
	if err := decodeJSON(r.Body, &body); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeAPIError(w, r, http.StatusRequestEntityTooLarge, apperror.CodePayloadTooLarge, "Request body must not exceed 1 MiB.", nil)
			return
		}
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeMalformedRequest, "Request body must be one valid JSON object.", nil)
		return
	}
	updated, err := s.memberships.ChangeRole(r.Context(), membership.ChangeRoleInput{
		OrganizationID: orgID, MembershipID: targetID, Role: access.Role(body.Role), Version: version,
	})
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf("\"%d\"", updated.Version))
	writeJSON(w, http.StatusOK, membershipResponse(updated))
}

func (s *Server) RevokeMembership(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, membershipID uuid.UUID, params RevokeMembershipParams) {
	if s.memberships == nil {
		s.writeApplicationError(w, r, errors.New("membership service is not configured"))
		return
	}
	orgID, targetID, version, ok := s.membershipCommandInputs(w, r, organizationID, membershipID, params.IfMatch)
	if !ok {
		return
	}
	_, err := s.memberships.Revoke(r.Context(), membership.RevokeInput{
		OrganizationID: orgID, MembershipID: targetID, Version: version,
	})
	if err != nil {
		s.writeApplicationError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) membershipCommandInputs(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, membershipID uuid.UUID, ifMatch string) (identifier.ID, identifier.ID, int64, bool) {
	orgID, err := identifier.FromUUID(organizationID)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "The organization identifier is invalid.", nil)
		return identifier.ID{}, identifier.ID{}, 0, false
	}
	targetID, err := identifier.FromUUID(membershipID)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "The membership identifier is invalid.", nil)
		return identifier.ID{}, identifier.ID{}, 0, false
	}
	version, err := parseIfMatch(ifMatch)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "If-Match must contain a positive membership version.", nil)
		return identifier.ID{}, identifier.ID{}, 0, false
	}
	return orgID, targetID, version, true
}

func (s *Server) parseOrganizationID(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID) (identifier.ID, bool) {
	parsed, err := identifier.FromUUID(organizationID)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "The organization identifier is invalid.", nil)
		return identifier.ID{}, false
	}
	return parsed, true
}

func (s *Server) resourceIDs(w http.ResponseWriter, r *http.Request, organizationID, resourceID uuid.UUID, resourceName string) (identifier.ID, identifier.ID, bool) {
	orgID, ok := s.parseOrganizationID(w, r, organizationID)
	if !ok {
		return identifier.ID{}, identifier.ID{}, false
	}
	parsed, err := identifier.FromUUID(resourceID)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "The "+resourceName+" identifier is invalid.", nil)
		return identifier.ID{}, identifier.ID{}, false
	}
	return orgID, parsed, true
}

func (s *Server) resourceCommandInputs(w http.ResponseWriter, r *http.Request, organizationID, resourceID uuid.UUID, ifMatch, resourceName string) (identifier.ID, identifier.ID, int64, bool) {
	orgID, targetID, ok := s.resourceIDs(w, r, organizationID, resourceID, resourceName)
	if !ok {
		return identifier.ID{}, identifier.ID{}, 0, false
	}
	version, err := parseIfMatch(ifMatch)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "If-Match must contain a positive resource version.", nil)
		return identifier.ID{}, identifier.ID{}, 0, false
	}
	return orgID, targetID, version, true
}

func (s *Server) decodeJSONBody(w http.ResponseWriter, r *http.Request, destination any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeAPIError(w, r, http.StatusUnsupportedMediaType, apperror.CodeUnsupportedMediaType, "Content-Type must be application/json.", nil)
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	if err := decodeJSON(r.Body, destination); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeAPIError(w, r, http.StatusRequestEntityTooLarge, apperror.CodePayloadTooLarge, "Request body must not exceed 1 MiB.", nil)
			return false
		}
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeMalformedRequest, "Request body must be one valid JSON object.", nil)
		return false
	}
	return true
}

func (s *Server) invitationCommandInputs(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID, invitationID uuid.UUID, ifMatch string) (identifier.ID, identifier.ID, int64, bool) {
	orgID, err := identifier.FromUUID(organizationID)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "The organization identifier is invalid.", nil)
		return identifier.ID{}, identifier.ID{}, 0, false
	}
	targetID, err := identifier.FromUUID(invitationID)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "The invitation identifier is invalid.", nil)
		return identifier.ID{}, identifier.ID{}, 0, false
	}
	version, err := parseIfMatch(ifMatch)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, apperror.CodeInvalidRequest, "If-Match must contain a positive invitation version.", nil)
		return identifier.ID{}, identifier.ID{}, 0, false
	}
	return orgID, targetID, version, true
}

func parseIfMatch(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "W/") {
		value = strings.TrimSpace(strings.TrimPrefix(value, "W/"))
	}
	value = strings.Trim(value, "\"")
	version, err := strconv.ParseInt(value, 10, 64)
	if err != nil || version < 1 {
		return 0, errors.New("invalid If-Match version")
	}
	return version, nil
}

func etagMatches(value, current string) bool {
	for _, candidate := range strings.Split(value, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" {
			return true
		}
		candidate = strings.TrimPrefix(candidate, "W/")
		if candidate == current {
			return true
		}
	}
	return false
}

func optionalString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func optionalInt32(value *int) int32 {
	if value == nil {
		return 0
	}
	return int32(*value)
}

func membershipResponse(value membership.Membership) Membership {
	venueScope := make([]openapi_types.UUID, 0, len(value.VenueScope))
	for _, venueID := range value.VenueScope {
		venueScope = append(venueScope, venueID.UUID())
	}
	return Membership{Id: value.ID.UUID(), OrganizationId: value.OrganizationID.UUID(), UserId: value.UserID.UUID(),
		Role: MembershipRole(value.Role), Status: MembershipStatus(value.Status), VenueScope: venueScope,
		Version: value.Version, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt, RevokedAt: value.RevokedAt}
}

func venueResponse(value venue.Venue) Venue {
	return Venue{Id: value.ID.UUID(), OrganizationId: value.OrganizationID.UUID(), Slug: value.Slug, DisplayName: value.DisplayName, Status: VenueStatus(value.Status), Timezone: value.Timezone, Version: value.Version, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt}
}

func eventResponse(value event.Event) Event {
	return Event{Id: value.ID.UUID(), OrganizationId: value.OrganizationID.UUID(), Slug: value.Slug, Title: value.Title, ShortDescription: value.ShortDescription, LongDescription: value.LongDescription, Status: EventStatus(value.Status), CurrentRevision: value.CurrentRevision, Checksum: value.Checksum, Version: value.Version, PublishedAt: value.PublishedAt, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt}
}

func publicationReportResponse(value publication.Report) PublicationValidationReport {
	return PublicationValidationReport{EventId: value.EventID.UUID(), Valid: value.Valid, Blockers: publicationIssuesResponse(value.Blockers), Warnings: publicationIssuesResponse(value.Warnings), CheckedAt: value.CheckedAt}
}

func publicationIssuesResponse(values []publication.Issue) []PublicationValidationIssue {
	issues := make([]PublicationValidationIssue, 0, len(values))
	for _, value := range values {
		var resourceID *openapi_types.UUID
		if value.ResourceID != nil {
			parsed := value.ResourceID.UUID()
			resourceID = &parsed
		}
		issues = append(issues, PublicationValidationIssue{Code: value.Code, Message: value.Message, ResourceId: resourceID})
	}
	return issues
}

func availabilitySnapshotResponse(value inventory.AvailabilitySnapshot) AvailabilitySnapshot {
	seats := make([]AvailabilitySeat, 0, len(value.Seats))
	for _, seat := range value.Seats {
		seats = append(seats, AvailabilitySeat{Id: seat.ID.UUID(), SeatKey: seat.SeatKey, Label: seat.Label, Category: seat.Category, Sellable: seat.Sellable, Wheelchair: seat.Wheelchair, State: AvailabilitySeatState(seat.State), Version: seat.Version})
	}
	pools := make([]AvailabilityGAPool, 0, len(value.GAPools))
	for _, pool := range value.GAPools {
		pools = append(pools, AvailabilityGAPool{Id: pool.ID.UUID(), Slug: pool.Slug, SellableCapacity: pool.SellableCapacity, SoldQuantity: pool.SoldQuantity, HeldQuantity: pool.HeldQuantity, KilledQuantity: pool.KilledQuantity, CompedQuantity: pool.CompedQuantity, Available: pool.Available, Version: pool.Version})
	}
	return AvailabilitySnapshot{OrganizationId: value.OrganizationID.UUID(), SessionId: value.SessionID.UUID(), InventoryMode: InventoryMode(value.InventoryMode), SessionStatus: SessionStatus(value.SessionStatus), Revision: value.Revision, ServerTime: value.ServerTime, Seats: seats, GaPools: pools}
}

func entrySummaryResponse(value entry.Summary) EntrySummary {
	byResult := make([]EntryResultCount, 0, len(value.ByResult))
	for _, item := range value.ByResult {
		byResult = append(byResult, EntryResultCount{Result: item.Result, Count: item.Count})
	}
	byGate := make([]EntryGateCount, 0, len(value.ByGate))
	for _, item := range value.ByGate {
		var gateID *openapi_types.UUID
		if item.GateID != nil {
			parsed := item.GateID.UUID()
			gateID = &parsed
		}
		byGate = append(byGate, EntryGateCount{GateId: gateID, Count: item.Count})
	}
	byMinute := make([]EntryMinuteCount, 0, len(value.ByMinute))
	for _, item := range value.ByMinute {
		byMinute = append(byMinute, EntryMinuteCount{Minute: item.Minute.UTC(), Count: item.Count})
	}
	byTicketType := make([]EntryTicketTypeCount, 0, len(value.ByTicketType))
	for _, item := range value.ByTicketType {
		byTicketType = append(byTicketType, EntryTicketTypeCount{PriceTierId: item.PriceTierID.UUID(), Count: item.Count})
	}
	return EntrySummary{OrganizationId: value.OrganizationID.UUID(), SessionId: value.SessionID.UUID(), GeneratedAt: value.GeneratedAt.UTC(), DataAsOf: value.DataAsOf, DataDelaySeconds: int64(value.DataDelay / time.Second), TotalScans: value.TotalScans, ByResult: byResult, ByGate: byGate, ByMinute: byMinute, ByTicketType: byTicketType}
}

func sessionResponse(value session.Session) Session {
	var seatMapID *openapi_types.UUID
	if value.SeatMapVersionID != nil {
		parsed := value.SeatMapVersionID.UUID()
		seatMapID = &parsed
	}
	return Session{Id: value.ID.UUID(), OrganizationId: value.OrganizationID.UUID(), EventId: value.EventID.UUID(), EventRevision: value.EventRevision, VenueId: value.VenueID.UUID(), SpaceId: value.SpaceID.UUID(), SeatMapVersionId: seatMapID, InventoryMode: InventoryMode(value.InventoryMode), DoorsAt: value.DoorsAt, StartsAt: value.StartsAt, EndsAt: value.EndsAt, SalesStartAt: value.SalesStartAt, SalesEndAt: value.SalesEndAt, Timezone: value.Timezone, Status: SessionStatus(value.Status), Version: value.Version, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt}
}

func priceTierResponse(value pricing.PriceTier) PriceTier {
	return PriceTier{Id: value.ID.UUID(), OrganizationId: value.OrganizationID.UUID(), SessionId: value.SessionID.UUID(), Slug: value.Slug, DisplayName: value.DisplayName, Currency: value.Currency, AmountMinor: value.AmountMinor, MinimumQuantity: int(value.MinimumQuantity), MaximumQuantity: int(value.MaximumQuantity), SalesStartAt: value.SalesStartAt, SalesEndAt: value.SalesEndAt, Status: PriceTierStatus(value.Status), Version: value.Version, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt}
}

func salesChannelResponse(value channel.SalesChannel) SalesChannel {
	return SalesChannel{Id: value.ID.UUID(), OrganizationId: value.OrganizationID.UUID(), Key: value.Key, DisplayName: value.DisplayName, Type: SalesChannelType(value.Type), Configuration: value.Configuration, Status: SalesChannelStatus(value.Status), Version: value.Version, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt}
}

func allocationResponse(value channel.Allocation) SessionChannelAllocation {
	return SessionChannelAllocation{Id: value.ID.UUID(), OrganizationId: value.OrganizationID.UUID(), SessionId: value.SessionID.UUID(), ChannelId: value.ChannelID.UUID(), ScopeKey: value.ScopeKey, Mode: SessionChannelAllocationMode(value.Mode), Quantity: value.Quantity, Version: value.Version, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt}
}

func spaceResponse(value venue.Space) Space {
	return Space{Id: value.ID.UUID(), OrganizationId: value.OrganizationID.UUID(), VenueId: value.VenueID.UUID(), Slug: value.Slug, DisplayName: value.DisplayName, Status: SpaceStatus(value.Status), InventoryMode: InventoryMode(value.InventoryMode), PhysicalCapacity: value.PhysicalCapacity, Version: value.Version, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt}
}

func gateResponse(value venue.Gate) Gate {
	var spaceID *openapi_types.UUID
	if value.SpaceID != nil {
		parsed := value.SpaceID.UUID()
		spaceID = &parsed
	}
	return Gate{Id: value.ID.UUID(), OrganizationId: value.OrganizationID.UUID(), VenueId: value.VenueID.UUID(), SpaceId: spaceID, Code: value.Code, DisplayName: value.DisplayName, Status: SpaceStatus(value.Status), Version: value.Version, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt}
}

func gaPoolResponse(value venue.GAPoolTemplate) GAPool {
	return GAPool{Id: value.ID.UUID(), OrganizationId: value.OrganizationID.UUID(), SpaceId: value.SpaceID.UUID(), Slug: value.Slug, DisplayName: value.DisplayName, Status: SpaceStatus(value.Status), PhysicalCapacity: value.PhysicalCapacity, SellableCapacity: value.SellableCapacity, Version: value.Version, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt}
}

func seatMapContentInput(value SeatMapContent) venue.SeatMapContent {
	content := venue.SeatMapContent{Sections: make([]venue.SeatMapSection, 0, len(value.Sections))}
	for _, section := range value.Sections {
		mappedSection := venue.SeatMapSection{ID: section.Id, Label: section.Label, Rows: make([]venue.SeatMapRow, 0, len(section.Rows))}
		for _, row := range section.Rows {
			mappedRow := venue.SeatMapRow{ID: row.Id, Label: row.Label, Seats: make([]venue.SeatMapSeat, 0, len(row.Seats))}
			for _, seat := range row.Seats {
				mapped := venue.SeatMapSeat{ID: seat.Id, Label: seat.Label, Sellable: seat.Sellable}
				if seat.Category != nil {
					mapped.Category = *seat.Category
				}
				if seat.Wheelchair != nil {
					mapped.Wheelchair = *seat.Wheelchair
				}
				if seat.CompanionTo != nil {
					mapped.CompanionTo = *seat.CompanionTo
				}
				mappedRow.Seats = append(mappedRow.Seats, mapped)
			}
			mappedSection.Rows = append(mappedSection.Rows, mappedRow)
		}
		content.Sections = append(content.Sections, mappedSection)
	}
	return content
}

func seatMapResponse(value venue.SeatMap) SeatMap {
	content := SeatMapContent{Sections: make([]SeatMapSection, 0, len(value.Content.Sections))}
	for _, section := range value.Content.Sections {
		mappedSection := SeatMapSection{Id: section.ID, Label: section.Label, Rows: make([]SeatMapRow, 0, len(section.Rows))}
		for _, row := range section.Rows {
			mappedRow := SeatMapRow{Id: row.ID, Label: row.Label, Seats: make([]SeatMapSeat, 0, len(row.Seats))}
			for _, seat := range row.Seats {
				mapped := SeatMapSeat{Id: seat.ID, Label: seat.Label, Sellable: seat.Sellable}
				if seat.Category != "" {
					category := seat.Category
					mapped.Category = &category
				}
				if seat.Wheelchair {
					wheelchair := true
					mapped.Wheelchair = &wheelchair
				}
				if seat.CompanionTo != "" {
					companionTo := seat.CompanionTo
					mapped.CompanionTo = &companionTo
				}
				mappedRow.Seats = append(mappedRow.Seats, mapped)
			}
			mappedSection.Rows = append(mappedSection.Rows, mappedRow)
		}
		content.Sections = append(content.Sections, mappedSection)
	}
	return SeatMap{Id: value.ID.UUID(), OrganizationId: value.OrganizationID.UUID(), SpaceId: value.SpaceID.UUID(), Name: value.Name, Status: SeatMapStatus(value.Status), Revision: value.Revision, Checksum: value.Checksum, SeatCount: value.SeatCount, SellableCount: value.SellableCount, Content: content, Version: value.Version, PublishedAt: value.PublishedAt, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt}
}

func invitationResponse(value invitation.Invitation) Invitation {
	var membershipID *openapi_types.UUID
	if value.MembershipID != nil {
		parsed := value.MembershipID.UUID()
		membershipID = &parsed
	}
	return Invitation{Id: value.ID.UUID(), OrganizationId: value.OrganizationID.UUID(), Email: openapi_types.Email(value.Email),
		Role: MembershipRole(value.Role), Status: InvitationStatus(value.Status), Token: nil, MembershipId: membershipID,
		ExpiresAt: value.ExpiresAt, AcceptedAt: value.AcceptedAt, RevokedAt: value.RevokedAt, Version: value.Version,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt}
}

func issuedInvitationResponse(value invitation.IssueResult) IssuedInvitation {
	base := invitationResponse(value.Invitation)
	return IssuedInvitation{Id: base.Id, OrganizationId: base.OrganizationId, Email: base.Email, Role: base.Role,
		Status: IssuedInvitationStatus(base.Status), Token: value.Token, MembershipId: base.MembershipId,
		ExpiresAt: base.ExpiresAt, AcceptedAt: base.AcceptedAt, RevokedAt: base.RevokedAt, Version: base.Version,
		CreatedAt: base.CreatedAt, UpdatedAt: base.UpdatedAt}
}

func refundResponse(value refund.Refund) Refund {
	var providerRefundID *string
	if value.ProviderRefundID != "" {
		providerRefundID = &value.ProviderRefundID
	}
	return Refund{
		Id:               value.ID.UUID(),
		OrganizationId:   value.OrganizationID.UUID(),
		OrderId:          value.OrderID.UUID(),
		PaymentAttemptId: value.PaymentAttemptID.UUID(),
		AmountMinor:      value.AmountMinor,
		Currency:         value.Currency,
		Status:           RefundStatus(value.Status),
		IdempotencyKey:   value.IdempotencyKey,
		Reason:           value.Reason,
		ProviderRefundId: providerRefundID,
		Version:          value.Version,
		CreatedAt:        value.CreatedAt.UTC(),
		UpdatedAt:        value.UpdatedAt.UTC(),
	}
}

func (s *Server) writeRefund(w http.ResponseWriter, status int, value refund.Refund) {
	writeJSON(w, status, refundResponse(value))
}

func orderResponse(value order.Order) Order {
	var ownerUserID *openapi_types.UUID
	if value.OwnerUserID != nil {
		parsed := value.OwnerUserID.UUID()
		ownerUserID = &parsed
	}
	lines := make([]OrderLine, 0, len(value.Lines))
	for _, line := range value.Lines {
		lines = append(lines, OrderLine{LineNumber: line.LineNumber, PriceTierId: line.PriceTierID.UUID(), Quantity: line.Quantity, UnitMinor: line.UnitMinor, SubtotalMinor: line.SubtotalMinor})
	}
	return Order{Id: value.ID.UUID(), OrganizationId: value.OrganizationID.UUID(), CartId: value.CartID.UUID(), SessionId: value.SessionID.UUID(), HoldId: value.HoldID.UUID(), OrderNumber: value.OrderNumber, Currency: value.Currency, Status: OrderStatus(value.Status), SubtotalMinor: value.SubtotalMinor, DiscountMinor: value.DiscountMinor, FeesMinor: value.FeesMinor, TaxesMinor: value.TaxesMinor, TotalMinor: value.TotalMinor, OwnerUserId: ownerUserID, ConfirmedAt: value.ConfirmedAt, Version: value.Version, CreatedAt: value.CreatedAt.UTC(), UpdatedAt: value.UpdatedAt.UTC(), Lines: lines}
}

func (s *Server) writeApplicationError(w http.ResponseWriter, r *http.Request, err error) {
	var classified *apperror.Error
	if errors.As(err, &classified) {
		status := mapErrorStatus(classified.Code())
		details := make([]ErrorDetail, 0, len(classified.Details()))
		for _, detail := range classified.Details() {
			field := detail.Field
			details = append(details, ErrorDetail{Field: &field, Code: detail.Code, Message: detail.Message})
		}
		writeAPIError(w, r, status, classified.Code(), classified.Message(), details)
		return
	}
	s.logger.ErrorContext(r.Context(), "request failed", slog.Any("error", err), slog.Group("request", requestLogAttrs(r.Context())...))
	writeAPIError(w, r, http.StatusInternalServerError, apperror.CodeInternal, "An unexpected error occurred.", nil)
}

func mapErrorStatus(code apperror.Code) int {
	switch code {
	case apperror.CodeMalformedRequest, apperror.CodeInvalidRequest, apperror.CodeInvalidCursor:
		return http.StatusBadRequest
	case apperror.CodeUnauthenticated:
		return http.StatusUnauthorized
	case apperror.CodePermissionDenied:
		return http.StatusForbidden
	case apperror.CodeNotFound:
		return http.StatusNotFound
	case apperror.CodeConflict, apperror.CodeIdempotencyKeyReused:
		return http.StatusConflict
	case apperror.CodePreconditionFailed:
		return http.StatusPreconditionFailed
	case apperror.CodeValidationFailed:
		return http.StatusUnprocessableEntity
	case apperror.CodePayloadTooLarge:
		return http.StatusRequestEntityTooLarge
	case apperror.CodeUnsupportedMediaType:
		return http.StatusUnsupportedMediaType
	case apperror.CodeRateLimited:
		return http.StatusTooManyRequests
	case apperror.CodeDependencyUnavailable:
		return http.StatusServiceUnavailable
	case apperror.CodeDependencyTimeout:
		return http.StatusGatewayTimeout
	case apperror.CodeInternal:
		return http.StatusInternalServerError
	default:
		return http.StatusInternalServerError
	}
}

func organizationResponse(value organization.Organization) Organization {
	return Organization{Id: value.ID.UUID(), Slug: value.Slug, DisplayName: value.DisplayName,
		Status: OrganizationStatus(value.Status), DefaultLocale: value.DefaultLocale,
		DefaultTimezone: value.DefaultTimezone, DefaultCurrency: value.DefaultCurrency.String(),
		SettingsVersion: value.SettingsVersion, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt}
}

func apiKeyResponse(value access.APIKey) APIKey {
	scopes := make([]string, 0, len(value.Scopes))
	for _, scope := range value.Scopes {
		scopes = append(scopes, string(scope))
	}
	return APIKey{Id: value.ID.UUID(), OrganizationId: value.OrganizationID.UUID(), Prefix: value.Prefix, Name: value.Name, Scopes: scopes, ExpiresAt: value.ExpiresAt, RevokedAt: value.RevokedAt, LastUsedAt: value.LastUsedAt, Version: value.Version, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt}
}

func deviceResponse(value device.Device) Device {
	return Device{Id: value.ID.UUID(), OrganizationId: value.OrganizationID.UUID(), VenueId: value.VenueID.UUID(), Name: value.Name, Platform: value.Platform, AppVersion: value.AppVersion, State: DeviceState(value.State), LastSeenAt: value.LastSeenAt, Version: value.Version, CreatedAt: value.CreatedAt.UTC(), UpdatedAt: value.UpdatedAt.UTC()}
}

func issuedDeviceResponse(value device.IssuedDevice) IssuedDevice {
	return IssuedDevice{Device: deviceResponse(value.Device), Token: value.Token}
}

func deviceAssignmentResponse(value device.Assignment) DeviceAssignment {
	var sessionID, gateID *openapi_types.UUID
	if value.SessionID != nil {
		mapped := value.SessionID.UUID()
		sessionID = &mapped
	}
	if value.GateID != nil {
		mapped := value.GateID.UUID()
		gateID = &mapped
	}
	return DeviceAssignment{Id: value.ID.UUID(), OrganizationId: value.OrganizationID.UUID(), DeviceId: value.DeviceID.UUID(), SessionId: sessionID, GateId: gateID, Capability: DeviceAssignmentCapability(value.Capability), State: DeviceAssignmentState(value.State), ValidFrom: value.ValidFrom.UTC(), ValidUntil: value.ValidUntil, Version: value.Version, CreatedAt: value.CreatedAt.UTC(), UpdatedAt: value.UpdatedAt.UTC()}
}

func timeValue(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return value.UTC()
}

func auditEntryResponse(value audit.Entry) AuditEntry {
	response := AuditEntry{
		Id: value.ID.UUID(), OrganizationId: value.OrganizationID.UUID(), ActorType: value.ActorType,
		Action: value.Action, SubjectType: value.SubjectType, SubjectId: value.SubjectID,
		Result: AuditEntryResult(value.Result), OccurredAt: value.OccurredAt,
		ActorId: optionalAuditString(value.ActorID), EffectiveActorType: optionalAuditString(value.EffectiveActorType),
		EffectiveActorId: optionalAuditString(value.EffectiveActorID), Reason: optionalAuditString(value.Reason),
		RequestId: optionalAuditString(value.RequestID), TraceId: optionalAuditString(value.TraceID),
		SourceIp: optionalAuditString(value.SourceIP), BeforeData: auditDataResponse(value.BeforeData),
		AfterData: auditDataResponse(value.AfterData),
	}
	if value.DeviceID != nil {
		deviceID := value.DeviceID.UUID()
		response.DeviceId = &deviceID
	}
	return response
}

func optionalAuditString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func auditDataResponse(value json.RawMessage) *map[string]interface{} {
	redacted := audit.RedactJSON(value)
	if len(redacted) == 0 {
		return nil
	}
	var object map[string]interface{}
	if err := json.Unmarshal(redacted, &object); err != nil {
		return nil
	}
	return &object
}

func (s *Server) writeServiceError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, user.ErrInvalid):
		writeAPIError(w, r, http.StatusUnprocessableEntity, apperror.CodeValidationFailed, rootDetail(err), nil)
	case errors.Is(err, user.ErrNotFound):
		writeAPIError(w, r, http.StatusNotFound, apperror.CodeNotFound, "The requested user does not exist.", nil)
	case errors.Is(err, user.ErrConflict):
		writeAPIError(w, r, http.StatusConflict, apperror.CodeConflict, "A user with this email already exists.", nil)
	default:
		s.logger.ErrorContext(r.Context(), "request failed", slog.Any("error", err), slog.Group("request", requestLogAttrs(r.Context())...))
		writeAPIError(w, r, http.StatusInternalServerError, apperror.CodeInternal, "An unexpected error occurred.", nil)
	}
}

func userResponse(value user.User) User {
	return User{
		Id:        value.ID,
		Email:     openapi_types.Email(value.Email),
		Name:      value.Name,
		CreatedAt: value.CreatedAt,
		UpdatedAt: value.UpdatedAt,
	}
}

func decodeJSON(reader io.Reader, destination any) error {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode JSON body: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain a single JSON object")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeAPIError(w http.ResponseWriter, r *http.Request, status int, code apperror.Code, message string, details []ErrorDetail) {
	route := r.Pattern
	if route == "" {
		route = "unmatched"
	}
	if recorder, ok := w.(interface {
		RecordAPIError(string, string, string)
	}); ok {
		recorder.RecordAPIError(r.Method, route, string(code))
	}
	requestID := requestid.FromContext(r.Context())
	if !requestid.Valid(requestID) {
		requestID = requestid.New()
		w.Header().Set(requestid.Header, requestID)
	}
	response := ErrorResponse{Error: APIError{
		Code:      string(code),
		Message:   message,
		RequestId: requestID,
	}}
	if len(details) > 0 {
		if len(details) > apperror.MaxDetails {
			details = details[:apperror.MaxDetails]
		}
		response.Error.Details = &details
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(response)
}

func rootDetail(err error) string {
	message := err.Error()
	if index := strings.LastIndex(message, ": "); index >= 0 {
		return message[index+2:]
	}
	return message
}
