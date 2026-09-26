package venue

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/HK9750/venueos/internal/access"
	"github.com/HK9750/venueos/internal/platform/apperror"
	"github.com/HK9750/venueos/internal/platform/clock"
	"github.com/HK9750/venueos/internal/platform/identifier"
)

type fakeRepository struct {
	venueResult    Venue
	spaceResult    Space
	err            error
	createdVenue   CreateVenueRecord
	updatedVenue   UpdateVenueRecord
	createdSeatMap CreateSeatMapRecord
}

func (repository *fakeRepository) CreateVenue(_ context.Context, record CreateVenueRecord) (Venue, error) {
	repository.createdVenue = record
	return repository.venueResult, repository.err
}
func (repository *fakeRepository) GetVenue(context.Context, identifier.ID, identifier.ID) (Venue, error) {
	return repository.venueResult, repository.err
}
func (repository *fakeRepository) ListVenues(context.Context, identifier.ID, int32, *Cursor) (Page[Venue], error) {
	return Page[Venue]{}, repository.err
}
func (repository *fakeRepository) UpdateVenue(_ context.Context, record UpdateVenueRecord) (Venue, error) {
	repository.updatedVenue = record
	return repository.venueResult, repository.err
}
func (repository *fakeRepository) ArchiveVenue(context.Context, ArchiveVenueRecord) (Venue, error) {
	return repository.venueResult, repository.err
}
func (repository *fakeRepository) RestoreVenue(context.Context, RestoreVenueRecord) (Venue, error) {
	return repository.venueResult, repository.err
}
func (repository *fakeRepository) CreateSpace(context.Context, CreateSpaceRecord) (Space, error) {
	return Space{}, repository.err
}
func (repository *fakeRepository) GetSpace(context.Context, identifier.ID, identifier.ID, identifier.ID) (Space, error) {
	return repository.spaceResult, repository.err
}
func (repository *fakeRepository) ListSpaces(context.Context, identifier.ID, identifier.ID, int32, *Cursor) (Page[Space], error) {
	return Page[Space]{}, repository.err
}
func (repository *fakeRepository) CreateGate(context.Context, CreateGateRecord) (Gate, error) {
	return Gate{}, repository.err
}
func (repository *fakeRepository) GetGate(context.Context, identifier.ID, identifier.ID, identifier.ID) (Gate, error) {
	return Gate{}, repository.err
}
func (repository *fakeRepository) ListGates(context.Context, identifier.ID, identifier.ID, int32, *Cursor) (Page[Gate], error) {
	return Page[Gate]{}, repository.err
}
func (repository *fakeRepository) CreateGAPool(context.Context, CreateGAPoolRecord) (GAPoolTemplate, error) {
	return GAPoolTemplate{}, repository.err
}
func (repository *fakeRepository) GetGAPool(context.Context, identifier.ID, identifier.ID, identifier.ID) (GAPoolTemplate, error) {
	return GAPoolTemplate{}, repository.err
}
func (repository *fakeRepository) ListGAPools(context.Context, identifier.ID, identifier.ID, int32, *Cursor) (Page[GAPoolTemplate], error) {
	return Page[GAPoolTemplate]{}, repository.err
}

func (repository *fakeRepository) CreateSeatMap(_ context.Context, record CreateSeatMapRecord) (SeatMap, error) {
	repository.createdSeatMap = record
	return SeatMap{}, repository.err
}
func (repository *fakeRepository) GetSeatMap(context.Context, identifier.ID, identifier.ID, identifier.ID) (SeatMap, error) {
	return SeatMap{}, repository.err
}
func (repository *fakeRepository) ListSeatMaps(context.Context, identifier.ID, identifier.ID, int32, *Cursor) (Page[SeatMap], error) {
	return Page[SeatMap]{}, repository.err
}
func (repository *fakeRepository) UpdateSeatMap(context.Context, UpdateSeatMapRecord) (SeatMap, error) {
	return SeatMap{}, repository.err
}
func (repository *fakeRepository) PublishSeatMap(context.Context, PublishSeatMapRecord) (SeatMap, error) {
	return SeatMap{}, repository.err
}
func (repository *fakeRepository) CloneSeatMap(context.Context, CloneSeatMapRecord) (SeatMap, error) {
	return SeatMap{}, repository.err
}

func TestVenueCreateRequiresManageAndNormalizesSlug(t *testing.T) {
	organizationID := newID(t)
	repository := &fakeRepository{venueResult: Venue{ID: newID(t), OrganizationID: organizationID, Status: StatusDraft}}
	service := NewService(repository, clock.NewFixed(time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)))
	created, err := service.CreateVenue(tenantContext(t, organizationID, access.RoleAdmin), CreateVenueInput{OrganizationID: organizationID, Slug: " Main-Hall ", DisplayName: " Main Hall ", Timezone: "Asia/Karachi"})
	if err != nil {
		t.Fatal(err)
	}
	if repository.createdVenue.Venue.Slug != "main-hall" || repository.createdVenue.Venue.DisplayName != "Main Hall" || created.Status != StatusDraft {
		t.Fatalf("create record/result = %#v / %#v", repository.createdVenue, created)
	}
	_, err = service.CreateVenue(tenantContext(t, organizationID, access.RoleReadOnly), CreateVenueInput{OrganizationID: organizationID, Slug: "other", DisplayName: "Other", Timezone: "UTC"})
	var classified *apperror.Error
	if err == nil || !errors.As(err, &classified) || classified.Code() != apperror.CodePermissionDenied {
		t.Fatalf("read-only create error = %v", err)
	}
}

func TestVenueCrossTenantAndCursorValidation(t *testing.T) {
	organizationID := newID(t)
	service := NewService(&fakeRepository{}, clock.System{})
	_, err := service.GetVenue(tenantContext(t, organizationID, access.RoleAdmin), newID(t), newID(t))
	var classified *apperror.Error
	if err == nil || !errors.As(err, &classified) || classified.Code() != apperror.CodeNotFound {
		t.Fatalf("cross-tenant venue error = %v", err)
	}
	cursor := Cursor{OrganizationID: organizationID, CreatedAt: time.Now().UTC(), ID: newID(t)}
	encoded, err := EncodeCursor(cursor)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeCursor(encoded)
	if err != nil || decoded.OrganizationID != cursor.OrganizationID || decoded.ID != cursor.ID {
		t.Fatalf("cursor = %#v, error = %v", decoded, err)
	}
	if _, err := service.ListVenues(tenantContext(t, newID(t), access.RoleAdmin), organizationID, 1, &cursor); err == nil {
		t.Fatal("cross-tenant cursor accepted")
	}
}

func TestSpaceValidationRejectsInvalidCapacity(t *testing.T) {
	organizationID := newID(t)
	service := NewService(&fakeRepository{}, clock.System{})
	_, err := service.CreateSpace(tenantContext(t, organizationID, access.RoleAdmin), CreateSpaceInput{OrganizationID: organizationID, VenueID: newID(t), Slug: "room", DisplayName: "Room", InventoryMode: InventoryGA, PhysicalCapacity: 0})
	var classified *apperror.Error
	if err == nil || !errors.As(err, &classified) || classified.Code() != apperror.CodeValidationFailed {
		t.Fatalf("capacity error = %v", err)
	}
}

func TestSeatMapCanonicalizesAndCountsContent(t *testing.T) {
	organizationID := newID(t)
	repository := &fakeRepository{}
	service := NewService(repository, clock.NewFixed(time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)))
	content := SeatMapContent{Sections: []SeatMapSection{{ID: "main", Label: "Main", Rows: []SeatMapRow{{ID: "row-a", Label: "A", Seats: []SeatMapSeat{{ID: "seat-2", Label: "2", Sellable: false}, {ID: "seat-1", Label: "1", Sellable: true}}}}}}}
	_, err := service.CreateSeatMap(tenantContext(t, organizationID, access.RoleAdmin), CreateSeatMapInput{OrganizationID: organizationID, SpaceID: newID(t), Name: " Main Map ", Content: content})
	if err != nil {
		t.Fatal(err)
	}
	if repository.createdSeatMap.SeatMap.Name != "Main Map" || repository.createdSeatMap.SeatMap.SeatCount != 2 || repository.createdSeatMap.SeatMap.SellableCount != 1 {
		t.Fatalf("seat map record = %#v", repository.createdSeatMap.SeatMap)
	}
	if got := repository.createdSeatMap.SeatMap.Content.Sections[0].Rows[0].Seats[0].ID; got != "seat-1" {
		t.Fatalf("canonical seat order = %q", got)
	}
	if len(repository.createdSeatMap.Checksum) != 32 || len(repository.createdSeatMap.Content) == 0 {
		t.Fatalf("canonical payload/checksum missing: %d/%d", len(repository.createdSeatMap.Content), len(repository.createdSeatMap.Checksum))
	}
}

func TestSeatMapRejectsInvalidCompanionReference(t *testing.T) {
	organizationID := newID(t)
	service := NewService(&fakeRepository{}, clock.System{})
	_, err := service.CreateSeatMap(tenantContext(t, organizationID, access.RoleAdmin), CreateSeatMapInput{OrganizationID: organizationID, SpaceID: newID(t), Name: "Map", Content: SeatMapContent{Sections: []SeatMapSection{{ID: "main", Label: "Main", Rows: []SeatMapRow{{ID: "row-a", Label: "A", Seats: []SeatMapSeat{{ID: "seat-1", Label: "1", Sellable: true, CompanionTo: "missing"}}}}}}}})
	var classified *apperror.Error
	if err == nil || !errors.As(err, &classified) || classified.Code() != apperror.CodeValidationFailed {
		t.Fatalf("companion validation error = %v", err)
	}
}

func tenantContext(t *testing.T, organizationID identifier.ID, role access.Role) context.Context {
	t.Helper()
	principal, err := access.NewPrincipal(access.PrincipalUser, "venue-test-user")
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := access.NewAuthorizationForRole(principal, organizationID, role)
	if err != nil {
		t.Fatal(err)
	}
	return access.WithAuthorization(context.Background(), authorization)
}

func newID(t *testing.T) identifier.ID {
	t.Helper()
	id, err := identifier.New()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
