// Package publication validates whether an event catalog is ready to publish.
package publication

import (
	"context"
	"errors"
	"time"

	"github.com/HK9750/venueos/internal/access"
	"github.com/HK9750/venueos/internal/platform/apperror"
	"github.com/HK9750/venueos/internal/platform/clock"
	"github.com/HK9750/venueos/internal/platform/identifier"
)

var ErrNotFound = errors.New("event not found")

type Issue struct {
	Code       string
	Message    string
	ResourceID *identifier.ID
}

type Report struct {
	OrganizationID identifier.ID
	EventID        identifier.ID
	Valid          bool
	Blockers       []Issue
	Warnings       []Issue
	CheckedAt      time.Time
}

type EventSnapshot struct {
	Status          string
	CurrentRevision int64
	Sessions        []SessionSnapshot
}

type SessionSnapshot struct {
	ID                     identifier.ID
	Status                 string
	InventoryMode          string
	SeatMapVersionID       *identifier.ID
	VenueStatus            string
	SpaceStatus            string
	SeatMapStatus          string
	PriceTierCount         int64
	SeatSellableCapacity   int64
	GAPoolCount            int64
	GASellableCapacity     int64
	HardAllocationQuantity int64
}

type Repository interface {
	Load(context.Context, identifier.ID, identifier.ID) (EventSnapshot, error)
}

type Service struct {
	repository Repository
	clock      clock.Clock
}

func NewService(repository Repository, timeSource clock.Clock) *Service {
	return &Service{repository: repository, clock: timeSource}
}

func (service *Service) Validate(ctx context.Context, organizationID, eventID identifier.ID) (Report, error) {
	authorization, err := access.Require(ctx, access.PermissionCatalogPublish)
	if err != nil {
		if errors.Is(err, access.ErrUnauthenticated) {
			return Report{}, apperror.Wrap(err, apperror.CodeUnauthenticated, "Authentication is required.")
		}
		return Report{}, apperror.Wrap(err, apperror.CodePermissionDenied, "Publication validation is not permitted.")
	}
	if organizationID.IsZero() || authorization.OrganizationID() != organizationID || eventID.IsZero() {
		return Report{}, apperror.Wrap(ErrNotFound, apperror.CodeNotFound, "The requested event does not exist.")
	}
	snapshot, err := service.repository.Load(ctx, organizationID, eventID)
	if errors.Is(err, ErrNotFound) {
		return Report{}, apperror.Wrap(err, apperror.CodeNotFound, "The requested event does not exist.")
	}
	if err != nil {
		return Report{}, err
	}
	report := Report{OrganizationID: organizationID, EventID: eventID, Valid: true, CheckedAt: service.clock.Now().UTC(), Blockers: []Issue{}, Warnings: []Issue{}}
	if snapshot.Status == "cancelled" || snapshot.Status == "archived" {
		report.addBlocker("event_state", "The event is not publishable in its current state.", nil)
	}
	if len(snapshot.Sessions) == 0 {
		report.addWarning("no_sessions", "The event has no scheduled sessions yet.", nil)
	}
	for _, session := range snapshot.Sessions {
		if session.Status == "cancelled" || session.Status == "completed" {
			continue
		}
		if session.VenueStatus != "active" {
			report.addBlocker("venue_not_active", "The session venue must be active.", &session.ID)
		}
		if session.SpaceStatus != "active" {
			report.addBlocker("space_not_active", "The session space must be active.", &session.ID)
		}
		if session.InventoryMode == "assigned_seating" && (session.SeatMapVersionID == nil || session.SeatMapStatus != "published") {
			report.addBlocker("seat_map_not_published", "Assigned-seat sessions require a published seat map.", &session.ID)
		}
		if session.InventoryMode == "assigned_seating" && session.SeatMapVersionID != nil && session.SeatMapStatus == "published" && session.SeatSellableCapacity < 1 {
			report.addBlocker("invalid_inventory_capacity", "Assigned-seat sessions require at least one sellable seat.", &session.ID)
		}
		if session.InventoryMode == "general_admission" && (session.GAPoolCount < 1 || session.GASellableCapacity < 1) {
			report.addBlocker("invalid_inventory_capacity", "General-admission sessions require a sellable inventory pool.", &session.ID)
		}
		capacity := session.SeatSellableCapacity
		if session.InventoryMode == "general_admission" {
			capacity = session.GASellableCapacity
		}
		if session.HardAllocationQuantity > capacity {
			report.addBlocker("allocation_overflow", "Hard channel allocations cannot exceed sellable session capacity.", &session.ID)
		}
		if session.PriceTierCount == 0 {
			report.addBlocker("missing_price_tier", "Every publishable session requires at least one price tier.", &session.ID)
		}
	}
	return report, nil
}

func (service *Service) ValidateForPublish(ctx context.Context, organizationID, eventID identifier.ID) (bool, error) {
	report, err := service.Validate(ctx, organizationID, eventID)
	if err != nil {
		return false, err
	}
	return report.Valid, nil
}

func (report *Report) addBlocker(code, message string, resourceID *identifier.ID) {
	report.Valid = false
	report.Blockers = append(report.Blockers, Issue{Code: code, Message: message, ResourceID: resourceID})
}

func (report *Report) addWarning(code, message string, resourceID *identifier.ID) {
	report.Warnings = append(report.Warnings, Issue{Code: code, Message: message, ResourceID: resourceID})
}
