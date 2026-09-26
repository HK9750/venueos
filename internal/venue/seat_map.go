package venue

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/HK9750/venueos/internal/access"
	"github.com/HK9750/venueos/internal/platform/apperror"
	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/internal/platform/validation"
)

type SeatMapStatus string

const (
	SeatMapDraft      SeatMapStatus = "draft"
	SeatMapValidating SeatMapStatus = "validating"
	SeatMapPublished  SeatMapStatus = "published"
	SeatMapRetired    SeatMapStatus = "retired"
)

var ErrSeatMapValidation = errors.New("seat map content is invalid")

type SeatMapSeat struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Category    string `json:"category,omitempty"`
	Sellable    bool   `json:"sellable"`
	Wheelchair  bool   `json:"wheelchair,omitempty"`
	CompanionTo string `json:"companion_to,omitempty"`
}

type SeatMapRow struct {
	ID    string        `json:"id"`
	Label string        `json:"label"`
	Seats []SeatMapSeat `json:"seats"`
}

type SeatMapSection struct {
	ID    string       `json:"id"`
	Label string       `json:"label"`
	Rows  []SeatMapRow `json:"rows"`
}

type SeatMapContent struct {
	Sections []SeatMapSection `json:"sections"`
}

type SeatMap struct {
	ID             identifier.ID
	OrganizationID identifier.ID
	SpaceID        identifier.ID
	Name           string
	Status         SeatMapStatus
	Revision       int64
	Checksum       string
	SeatCount      int32
	SellableCount  int32
	Content        SeatMapContent
	Version        int64
	PublishedAt    *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type CreateSeatMapInput struct {
	OrganizationID identifier.ID
	SpaceID        identifier.ID
	Name           string
	Content        SeatMapContent
}

type UpdateSeatMapInput struct {
	OrganizationID identifier.ID
	SpaceID        identifier.ID
	SeatMapID      identifier.ID
	Name           string
	Content        SeatMapContent
	Version        int64
}

type PublishSeatMapInput struct {
	OrganizationID identifier.ID
	SpaceID        identifier.ID
	SeatMapID      identifier.ID
	Version        int64
}

type CloneSeatMapInput struct {
	OrganizationID identifier.ID
	SpaceID        identifier.ID
	SeatMapID      identifier.ID
}

type CreateSeatMapRecord struct {
	SeatMap   SeatMap
	Checksum  []byte
	Content   []byte
	AuditID   identifier.ID
	OutboxID  identifier.ID
	ActorType string
	ActorID   string
}

type UpdateSeatMapRecord struct {
	Input      UpdateSeatMapInput
	Checksum   []byte
	Content    []byte
	SeatCount  int32
	Sellable   int32
	AuditID    identifier.ID
	OutboxID   identifier.ID
	ActorType  string
	ActorID    string
	OccurredAt time.Time
}

type PublishSeatMapRecord struct {
	Input      PublishSeatMapInput
	AuditID    identifier.ID
	OutboxID   identifier.ID
	ActorType  string
	ActorID    string
	OccurredAt time.Time
}

type CloneSeatMapRecord struct {
	OrganizationID identifier.ID
	SpaceID        identifier.ID
	SeatMapID      identifier.ID
	CloneID        identifier.ID
	Name           string
	AuditID        identifier.ID
	OutboxID       identifier.ID
	ActorType      string
	ActorID        string
	OccurredAt     time.Time
}

func (service *Service) CreateSeatMap(ctx context.Context, input CreateSeatMapInput) (SeatMap, error) {
	authorization, err := service.requireOrganization(ctx, input.OrganizationID, access.PermissionVenueManage)
	if err != nil {
		return SeatMap{}, err
	}
	if input.SpaceID.IsZero() {
		return SeatMap{}, apperror.New(apperror.CodeValidationFailed, "The seat-map space identifier is required.")
	}
	name, content, canonical, checksum, seatCount, sellableCount, err := normalizeSeatMap(input.Name, input.Content)
	if err != nil {
		return SeatMap{}, err
	}
	mapID, auditID, outboxID, err := newIDs3()
	if err != nil {
		return SeatMap{}, apperror.Wrap(err, apperror.CodeInternal, "A seat-map identifier could not be created.")
	}
	now := service.clock.Now().UTC()
	created, err := service.repository.CreateSeatMap(ctx, CreateSeatMapRecord{
		SeatMap:  SeatMap{ID: mapID, OrganizationID: authorization.OrganizationID(), SpaceID: input.SpaceID, Name: name, Status: SeatMapDraft, Revision: 0, Checksum: checksum, SeatCount: seatCount, SellableCount: sellableCount, Content: content, Version: 1, CreatedAt: now, UpdatedAt: now},
		Checksum: checksumBytes(checksum), Content: canonical,
		AuditID: auditID, OutboxID: outboxID, ActorType: actorType(authorization.Principal().Type()), ActorID: authorization.Principal().ID(),
	})
	return created, mapMutationError(err)
}

func (service *Service) GetSeatMap(ctx context.Context, organizationID, spaceID, seatMapID identifier.ID) (SeatMap, error) {
	authorization, err := service.requireOrganization(ctx, organizationID, access.PermissionVenueRead)
	if err != nil {
		return SeatMap{}, err
	}
	if spaceID.IsZero() || seatMapID.IsZero() {
		return SeatMap{}, apperror.Wrap(ErrNotFound, apperror.CodeNotFound, "The requested seat map does not exist.")
	}
	found, err := service.repository.GetSeatMap(ctx, authorization.OrganizationID(), spaceID, seatMapID)
	if errors.Is(err, ErrNotFound) {
		return SeatMap{}, apperror.Wrap(err, apperror.CodeNotFound, "The requested seat map does not exist.")
	}
	return found, err
}

func (service *Service) ListSeatMaps(ctx context.Context, organizationID, spaceID identifier.ID, limit int32, after *Cursor) (Page[SeatMap], error) {
	authorization, err := service.requireOrganization(ctx, organizationID, access.PermissionVenueRead)
	if err != nil {
		return Page[SeatMap]{}, err
	}
	limit, err = boundedLimit(limit)
	if err != nil {
		return Page[SeatMap]{}, err
	}
	if after != nil && (after.OrganizationID != authorization.OrganizationID() || after.ParentID != spaceID) {
		return Page[SeatMap]{}, apperror.New(apperror.CodeInvalidCursor, "The seat-map cursor is invalid.")
	}
	return service.repository.ListSeatMaps(ctx, authorization.OrganizationID(), spaceID, limit, after)
}

func (service *Service) UpdateSeatMap(ctx context.Context, input UpdateSeatMapInput) (SeatMap, error) {
	authorization, err := service.requireOrganization(ctx, input.OrganizationID, access.PermissionVenueManage)
	if err != nil {
		return SeatMap{}, err
	}
	if input.SpaceID.IsZero() || input.SeatMapID.IsZero() || input.Version < 1 {
		return SeatMap{}, apperror.New(apperror.CodeValidationFailed, "The seat-map update request is invalid.")
	}
	name, content, canonical, checksum, seatCount, sellableCount, err := normalizeSeatMap(input.Name, input.Content)
	if err != nil {
		return SeatMap{}, err
	}
	auditID, outboxID, err := newIDs2()
	if err != nil {
		return SeatMap{}, apperror.Wrap(err, apperror.CodeInternal, "A seat-map update identifier could not be created.")
	}
	updated, err := service.repository.UpdateSeatMap(ctx, UpdateSeatMapRecord{
		Input:    UpdateSeatMapInput{OrganizationID: authorization.OrganizationID(), SpaceID: input.SpaceID, SeatMapID: input.SeatMapID, Name: name, Content: content, Version: input.Version},
		Checksum: checksumBytes(checksum), Content: canonical, SeatCount: seatCount, Sellable: sellableCount,
		AuditID: auditID, OutboxID: outboxID, ActorType: actorType(authorization.Principal().Type()), ActorID: authorization.Principal().ID(), OccurredAt: service.clock.Now().UTC(),
	})
	return updated, mapMutationError(err)
}

func (service *Service) PublishSeatMap(ctx context.Context, input PublishSeatMapInput) (SeatMap, error) {
	authorization, err := service.requireOrganization(ctx, input.OrganizationID, access.PermissionVenueManage)
	if err != nil {
		return SeatMap{}, err
	}
	if input.SpaceID.IsZero() || input.SeatMapID.IsZero() || input.Version < 1 {
		return SeatMap{}, apperror.New(apperror.CodeValidationFailed, "The seat-map publish request is invalid.")
	}
	auditID, outboxID, err := newIDs2()
	if err != nil {
		return SeatMap{}, apperror.Wrap(err, apperror.CodeInternal, "A seat-map publication identifier could not be created.")
	}
	published, err := service.repository.PublishSeatMap(ctx, PublishSeatMapRecord{Input: PublishSeatMapInput{OrganizationID: authorization.OrganizationID(), SpaceID: input.SpaceID, SeatMapID: input.SeatMapID, Version: input.Version}, AuditID: auditID, OutboxID: outboxID, ActorType: actorType(authorization.Principal().Type()), ActorID: authorization.Principal().ID(), OccurredAt: service.clock.Now().UTC()})
	return published, mapMutationError(err)
}

func (service *Service) CloneSeatMap(ctx context.Context, input CloneSeatMapInput) (SeatMap, error) {
	authorization, err := service.requireOrganization(ctx, input.OrganizationID, access.PermissionVenueManage)
	if err != nil {
		return SeatMap{}, err
	}
	if input.SpaceID.IsZero() || input.SeatMapID.IsZero() {
		return SeatMap{}, apperror.New(apperror.CodeValidationFailed, "The seat-map clone request is invalid.")
	}
	cloneID, auditID, outboxID, err := newIDs3()
	if err != nil {
		return SeatMap{}, apperror.Wrap(err, apperror.CodeInternal, "A seat-map clone identifier could not be created.")
	}
	found, err := service.repository.GetSeatMap(ctx, authorization.OrganizationID(), input.SpaceID, input.SeatMapID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return SeatMap{}, apperror.Wrap(err, apperror.CodeNotFound, "The requested seat map does not exist.")
		}
		return SeatMap{}, err
	}
	cloned, err := service.repository.CloneSeatMap(ctx, CloneSeatMapRecord{OrganizationID: authorization.OrganizationID(), SpaceID: input.SpaceID, SeatMapID: input.SeatMapID, CloneID: cloneID, Name: found.Name + " copy", AuditID: auditID, OutboxID: outboxID, ActorType: actorType(authorization.Principal().Type()), ActorID: authorization.Principal().ID(), OccurredAt: service.clock.Now().UTC()})
	return cloned, mapMutationError(err)
}

func normalizeSeatMap(name string, content SeatMapContent) (string, SeatMapContent, []byte, string, int32, int32, error) {
	name = strings.TrimSpace(name)
	collector := validation.Collector{}
	if name == "" || utf8.RuneCountInString(name) > 160 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		collector.Add("name", "invalid", "Use 1 to 160 visible characters.")
	}
	if len(content.Sections) == 0 || len(content.Sections) > 500 {
		collector.Add("content.sections", "invalid", "A seat map must contain 1 to 500 sections.")
	}
	if err := collector.Error("The seat-map request is invalid."); err != nil {
		return "", SeatMapContent{}, nil, "", 0, 0, err
	}
	sectionIDs := map[string]struct{}{}
	rowIDs := map[string]struct{}{}
	seatIDs := map[string]struct{}{}
	seatByID := map[string]SeatMapSeat{}
	seatCount := 0
	sellableCount := 0
	for sectionIndex := range content.Sections {
		section := &content.Sections[sectionIndex]
		section.ID = strings.TrimSpace(section.ID)
		section.Label = strings.TrimSpace(section.Label)
		if !validMapText(section.ID, 64) || !validMapText(section.Label, 120) {
			return "", SeatMapContent{}, nil, "", 0, 0, seatMapValidation("section identifiers and labels must be visible and bounded")
		}
		if _, exists := sectionIDs[section.ID]; exists {
			return "", SeatMapContent{}, nil, "", 0, 0, seatMapValidation("section identifiers must be unique")
		}
		sectionIDs[section.ID] = struct{}{}
		if len(section.Rows) == 0 || len(section.Rows) > 5000 {
			return "", SeatMapContent{}, nil, "", 0, 0, seatMapValidation("each section must contain 1 to 5000 rows")
		}
		for rowIndex := range section.Rows {
			row := &section.Rows[rowIndex]
			row.ID = strings.TrimSpace(row.ID)
			row.Label = strings.TrimSpace(row.Label)
			if !validMapText(row.ID, 64) || !validMapText(row.Label, 120) {
				return "", SeatMapContent{}, nil, "", 0, 0, seatMapValidation("row identifiers and labels must be visible and bounded")
			}
			if _, exists := rowIDs[row.ID]; exists {
				return "", SeatMapContent{}, nil, "", 0, 0, seatMapValidation("row identifiers must be unique")
			}
			rowIDs[row.ID] = struct{}{}
			if len(row.Seats) == 0 || len(row.Seats) > 100000 {
				return "", SeatMapContent{}, nil, "", 0, 0, seatMapValidation("each row must contain 1 to 100000 seats")
			}
			labels := map[string]struct{}{}
			for seatIndex := range row.Seats {
				seat := &row.Seats[seatIndex]
				seat.ID = strings.TrimSpace(seat.ID)
				seat.Label = strings.TrimSpace(seat.Label)
				seat.Category = strings.TrimSpace(seat.Category)
				seat.CompanionTo = strings.TrimSpace(seat.CompanionTo)
				if !validMapText(seat.ID, 64) || !validMapText(seat.Label, 64) || len(seat.Category) > 64 || (seat.Category != "" && strings.IndexFunc(seat.Category, unicode.IsControl) >= 0) || len(seat.CompanionTo) > 64 {
					return "", SeatMapContent{}, nil, "", 0, 0, seatMapValidation("seat identifiers, labels, categories, and companion references must be bounded")
				}
				if _, exists := seatIDs[seat.ID]; exists {
					return "", SeatMapContent{}, nil, "", 0, 0, seatMapValidation("seat identifiers must be unique")
				}
				if _, exists := labels[seat.Label]; exists {
					return "", SeatMapContent{}, nil, "", 0, 0, seatMapValidation("seat labels must be unique within a row")
				}
				seatIDs[seat.ID] = struct{}{}
				labels[seat.Label] = struct{}{}
				seatByID[seat.ID] = *seat
				seatCount++
				if seat.Sellable {
					sellableCount++
				}
			}
		}
	}
	if seatCount == 0 || seatCount > 100000 {
		return "", SeatMapContent{}, nil, "", 0, 0, seatMapValidation("a seat map must contain 1 to 100000 seats")
	}
	for _, seat := range seatByID {
		if seat.CompanionTo != "" {
			target, exists := seatByID[seat.CompanionTo]
			if !exists || !target.Wheelchair || seat.ID == seat.CompanionTo {
				return "", SeatMapContent{}, nil, "", 0, 0, seatMapValidation("companion seats must reference a distinct wheelchair seat")
			}
		}
	}
	sort.Slice(content.Sections, func(i, j int) bool { return content.Sections[i].ID < content.Sections[j].ID })
	for sectionIndex := range content.Sections {
		sort.Slice(content.Sections[sectionIndex].Rows, func(i, j int) bool {
			return content.Sections[sectionIndex].Rows[i].ID < content.Sections[sectionIndex].Rows[j].ID
		})
		for rowIndex := range content.Sections[sectionIndex].Rows {
			sort.Slice(content.Sections[sectionIndex].Rows[rowIndex].Seats, func(i, j int) bool {
				return content.Sections[sectionIndex].Rows[rowIndex].Seats[i].ID < content.Sections[sectionIndex].Rows[rowIndex].Seats[j].ID
			})
		}
	}
	canonical, err := json.Marshal(content)
	if err != nil {
		return "", SeatMapContent{}, nil, "", 0, 0, apperror.Wrap(err, apperror.CodeInternal, "Seat-map content could not be canonicalized.")
	}
	digest := sha256.Sum256(canonical)
	return name, content, canonical, hex.EncodeToString(digest[:]), int32(seatCount), int32(sellableCount), nil
}

func validMapText(value string, max int) bool {
	return value != "" && utf8.RuneCountInString(value) <= max && strings.IndexFunc(value, unicode.IsControl) < 0
}

func checksumBytes(value string) []byte {
	decoded, _ := hex.DecodeString(value)
	return decoded
}

func seatMapValidation(message string) error {
	return apperror.Wrap(ErrSeatMapValidation, apperror.CodeValidationFailed, message)
}
