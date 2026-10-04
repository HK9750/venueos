// Package postgres persists scanner devices with tenant predicates and atomic
// audit/outbox records for every lifecycle mutation.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/HK9750/venueos/internal/access"
	"github.com/HK9750/venueos/internal/database"
	"github.com/HK9750/venueos/internal/device"
	"github.com/HK9750/venueos/internal/device/postgres/sqlc"
	"github.com/HK9750/venueos/internal/platform/identifier"
	platformpostgres "github.com/HK9750/venueos/internal/platform/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type Repository struct {
	queries      *sqlc.Queries
	platform     *platformpostgres.Store
	transactions *database.TransactionRunner
}

func New(pool sqlc.DBTX, transactions *database.TransactionRunner) *Repository {
	return &Repository{queries: sqlc.New(pool), platform: platformpostgres.NewStore(pool), transactions: transactions}
}

func (repository *Repository) List(ctx context.Context, organizationID identifier.ID, limit int32, after *device.Cursor) (device.Page, error) {
	var afterCreatedAt time.Time
	var afterID pgtype.UUID
	if after != nil {
		afterCreatedAt = after.CreatedAt.UTC()
		afterID = pgtype.UUID{Bytes: after.ID.UUID(), Valid: true}
	}
	rows, err := repository.queries.ListDevices(ctx, sqlc.ListDevicesParams{OrganizationID: organizationID.UUID(), AfterCreatedAt: pgtype.Timestamptz{Time: afterCreatedAt, Valid: after != nil}, AfterID: afterID, LimitCount: limit + 1})
	if err != nil {
		return device.Page{}, fmt.Errorf("list devices: %w", err)
	}
	page := device.Page{Items: make([]device.Device, 0, len(rows))}
	for index, row := range rows {
		if int32(index) >= limit {
			mapped, mapErr := mapDeviceFields(row.ID, row.OrganizationID, row.VenueID, row.Name, row.Platform, row.AppVersion, row.State, row.LastSeenAt, row.Version, row.CreatedAt, row.UpdatedAt)
			if mapErr != nil {
				return device.Page{}, mapErr
			}
			page.NextCursor = &device.Cursor{OrganizationID: organizationID, CreatedAt: mapped.CreatedAt, ID: mapped.ID}
			break
		}
		mapped, mapErr := mapDeviceFields(row.ID, row.OrganizationID, row.VenueID, row.Name, row.Platform, row.AppVersion, row.State, row.LastSeenAt, row.Version, row.CreatedAt, row.UpdatedAt)
		if mapErr != nil {
			return device.Page{}, mapErr
		}
		page.Items = append(page.Items, mapped)
	}
	return page, nil
}

func (repository *Repository) Create(ctx context.Context, record device.CreateRecord) (device.Device, error) {
	var result device.Device
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		venue, err := queries.LockVenueForDevice(ctx, sqlc.LockVenueForDeviceParams{OrganizationID: record.Device.OrganizationID.UUID(), VenueID: record.Device.VenueID.UUID()})
		if errors.Is(err, pgx.ErrNoRows) {
			return device.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock venue for device: %w", err)
		}
		if venue.Status == "archived" {
			return device.ErrInvalidState
		}
		row, err := queries.InsertDevice(ctx, sqlc.InsertDeviceParams{
			ID: record.Device.ID.UUID(), OrganizationID: record.Device.OrganizationID.UUID(), VenueID: record.Device.VenueID.UUID(),
			Name: record.Device.Name, Platform: record.Device.Platform, AppVersion: record.Device.AppVersion,
			CredentialHash: record.CredentialHash[:], State: string(record.Device.State), Version: record.Device.Version,
			CreatedAt: record.Device.CreatedAt.UTC(), UpdatedAt: record.Device.UpdatedAt.UTC(),
		})
		if err != nil {
			return fmt.Errorf("insert device: %w", err)
		}
		result, err = mapDeviceFields(row.ID, row.OrganizationID, row.VenueID, row.Name, row.Platform, row.AppVersion, row.State, row.LastSeenAt, row.Version, row.CreatedAt, row.UpdatedAt)
		if err != nil {
			return err
		}
		return repository.recordMutation(ctx, tx, record.Device.OrganizationID, record.Device.ID, record.Device.Version, record.AuditID, record.OutboxID, record.ActorType, record.ActorID, record.Device.CreatedAt, "device.enrolled", map[string]any{"device_id": result.ID.String(), "venue_id": result.VenueID.String(), "state": string(result.State), "version": result.Version})
	})
	return result, err
}

func (repository *Repository) Get(ctx context.Context, organizationID, deviceID identifier.ID) (device.Device, error) {
	row, err := repository.queries.GetDevice(ctx, sqlc.GetDeviceParams{OrganizationID: organizationID.UUID(), ID: deviceID.UUID()})
	if errors.Is(err, pgx.ErrNoRows) {
		return device.Device{}, device.ErrNotFound
	}
	if err != nil {
		return device.Device{}, fmt.Errorf("get device: %w", err)
	}
	return mapDeviceFields(row.ID, row.OrganizationID, row.VenueID, row.Name, row.Platform, row.AppVersion, row.State, row.LastSeenAt, row.Version, row.CreatedAt, row.UpdatedAt)
}

func (repository *Repository) LookupCredential(ctx context.Context, hash [32]byte) (device.Device, error) {
	row, err := repository.queries.LookupDeviceByCredentialHash(ctx, hash[:])
	if errors.Is(err, pgx.ErrNoRows) {
		return device.Device{}, device.ErrNotFound
	}
	if err != nil {
		return device.Device{}, fmt.Errorf("lookup device credential: %w", err)
	}
	return mapDeviceFields(row.ID, row.OrganizationID, row.VenueID, row.Name, row.Platform, row.AppVersion, row.State, row.LastSeenAt, row.Version, row.CreatedAt, row.UpdatedAt)
}

func (repository *Repository) ChangeState(ctx context.Context, record device.ChangeStateRecord) (device.Device, error) {
	var result device.Device
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		current, err := queries.GetDeviceForUpdate(ctx, sqlc.GetDeviceForUpdateParams{OrganizationID: record.OrganizationID.UUID(), ID: record.DeviceID.UUID()})
		if errors.Is(err, pgx.ErrNoRows) {
			return device.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock device for state change: %w", err)
		}
		if current.Version != record.ExpectedVersion {
			return device.ErrVersionConflict
		}
		if current.State == string(device.StateRevoked) && record.State != device.StateRevoked {
			return device.ErrInvalidState
		}
		if current.State == string(record.State) {
			result, err = mapDeviceFields(current.ID, current.OrganizationID, current.VenueID, current.Name, current.Platform, current.AppVersion, current.State, current.LastSeenAt, current.Version, current.CreatedAt, current.UpdatedAt)
			return err
		}
		row, err := queries.ChangeDeviceState(ctx, sqlc.ChangeDeviceStateParams{State: string(record.State), UpdatedAt: record.OccurredAt.UTC(), OrganizationID: record.OrganizationID.UUID(), ID: record.DeviceID.UUID(), ExpectedVersion: record.ExpectedVersion})
		if errors.Is(err, pgx.ErrNoRows) {
			return device.ErrVersionConflict
		}
		if err != nil {
			return fmt.Errorf("change device state: %w", err)
		}
		result, err = mapDeviceFields(row.ID, row.OrganizationID, row.VenueID, row.Name, row.Platform, row.AppVersion, row.State, row.LastSeenAt, row.Version, row.CreatedAt, row.UpdatedAt)
		if err != nil {
			return err
		}
		return repository.recordMutation(ctx, tx, record.OrganizationID, record.DeviceID, result.Version, record.AuditID, record.OutboxID, record.ActorType, record.ActorID, record.OccurredAt, "device.state_changed", map[string]any{"device_id": result.ID.String(), "state": string(result.State), "version": result.Version})
	})
	return result, err
}

func (repository *Repository) ListAssignments(ctx context.Context, organizationID, deviceID identifier.ID, limit int32, after *device.AssignmentCursor) (device.AssignmentPage, error) {
	var afterCreatedAt time.Time
	var afterID pgtype.UUID
	if after != nil {
		afterCreatedAt = after.CreatedAt.UTC()
		afterID = pgtype.UUID{Bytes: after.ID.UUID(), Valid: true}
	}
	rows, err := repository.queries.ListDeviceAssignments(ctx, sqlc.ListDeviceAssignmentsParams{OrganizationID: organizationID.UUID(), DeviceID: deviceID.UUID(), AfterCreatedAt: pgtype.Timestamptz{Time: afterCreatedAt, Valid: after != nil}, AfterID: afterID, LimitCount: limit + 1})
	if err != nil {
		return device.AssignmentPage{}, fmt.Errorf("list device assignments: %w", err)
	}
	page := device.AssignmentPage{Items: make([]device.Assignment, 0, len(rows))}
	for index, row := range rows {
		mapped, mapErr := mapAssignmentFields(row.ID, row.OrganizationID, row.DeviceID, row.SessionID, row.GateID, row.Capability, row.State, row.ValidFrom, row.ValidUntil, row.Version, row.CreatedAt, row.UpdatedAt)
		if mapErr != nil {
			return device.AssignmentPage{}, mapErr
		}
		if int32(index) >= limit {
			page.NextCursor = &device.AssignmentCursor{OrganizationID: organizationID, DeviceID: deviceID, CreatedAt: mapped.CreatedAt, ID: mapped.ID}
			break
		}
		page.Items = append(page.Items, mapped)
	}
	return page, nil
}

func (repository *Repository) CreateAssignment(ctx context.Context, record device.CreateAssignmentRecord) (device.Assignment, error) {
	var result device.Assignment
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		current, err := queries.LockDeviceForAssignment(ctx, sqlc.LockDeviceForAssignmentParams{OrganizationID: record.Assignment.OrganizationID.UUID(), DeviceID: record.Assignment.DeviceID.UUID()})
		if errors.Is(err, pgx.ErrNoRows) {
			return device.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock device for assignment: %w", err)
		}
		if current.State != string(device.StateActive) {
			return device.ErrInvalidState
		}
		row, err := queries.InsertDeviceAssignment(ctx, sqlc.InsertDeviceAssignmentParams{
			ID: record.Assignment.ID.UUID(), OrganizationID: record.Assignment.OrganizationID.UUID(), DeviceID: record.Assignment.DeviceID.UUID(),
			SessionID: nullableID(record.Assignment.SessionID), GateID: nullableID(record.Assignment.GateID), Capability: string(record.Assignment.Capability),
			State: record.Assignment.State, ValidFrom: record.Assignment.ValidFrom.UTC(), ValidUntil: nullableTime(record.Assignment.ValidUntil), Version: record.Assignment.Version,
			CreatedAt: record.Assignment.CreatedAt.UTC(), UpdatedAt: record.Assignment.UpdatedAt.UTC(),
		})
		if err != nil {
			var postgresError *pgconn.PgError
			if errors.As(err, &postgresError) && postgresError.Code == "23505" && postgresError.ConstraintName == "uq_device_assignments_scope" {
				return device.ErrAssignmentConflict
			}
			return fmt.Errorf("insert device assignment: %w", err)
		}
		result, err = mapAssignmentFields(row.ID, row.OrganizationID, row.DeviceID, row.SessionID, row.GateID, row.Capability, row.State, row.ValidFrom, row.ValidUntil, row.Version, row.CreatedAt, row.UpdatedAt)
		if err != nil {
			return err
		}
		return repository.recordAssignmentMutation(ctx, tx, record.Assignment.OrganizationID, record.Assignment.ID, record.Assignment.Version, record.AuditID, record.OutboxID, record.ActorType, record.ActorID, record.Assignment.CreatedAt, "device.assignment_created", map[string]any{"assignment_id": result.ID.String(), "device_id": result.DeviceID.String(), "capability": string(result.Capability), "version": result.Version})
	})
	return result, err
}

func (repository *Repository) RevokeAssignment(ctx context.Context, record device.RevokeAssignmentRecord) (device.Assignment, error) {
	var result device.Assignment
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		current, err := queries.GetDeviceAssignmentForUpdate(ctx, sqlc.GetDeviceAssignmentForUpdateParams{OrganizationID: record.OrganizationID.UUID(), DeviceID: record.DeviceID.UUID(), ID: record.AssignmentID.UUID()})
		if errors.Is(err, pgx.ErrNoRows) {
			return device.ErrAssignmentNotFound
		}
		if err != nil {
			return fmt.Errorf("lock device assignment: %w", err)
		}
		if current.Version != record.ExpectedVersion {
			return device.ErrAssignmentVersion
		}
		if current.State == "revoked" {
			result, err = mapAssignmentFields(current.ID, current.OrganizationID, current.DeviceID, current.SessionID, current.GateID, current.Capability, current.State, current.ValidFrom, current.ValidUntil, current.Version, current.CreatedAt, current.UpdatedAt)
			return err
		}
		row, err := queries.RevokeDeviceAssignment(ctx, sqlc.RevokeDeviceAssignmentParams{UpdatedAt: record.OccurredAt.UTC(), OrganizationID: record.OrganizationID.UUID(), DeviceID: record.DeviceID.UUID(), ID: record.AssignmentID.UUID(), ExpectedVersion: record.ExpectedVersion})
		if errors.Is(err, pgx.ErrNoRows) {
			return device.ErrAssignmentVersion
		}
		if err != nil {
			return fmt.Errorf("revoke device assignment: %w", err)
		}
		result, err = mapAssignmentFields(row.ID, row.OrganizationID, row.DeviceID, row.SessionID, row.GateID, row.Capability, row.State, row.ValidFrom, row.ValidUntil, row.Version, row.CreatedAt, row.UpdatedAt)
		if err != nil {
			return err
		}
		return repository.recordAssignmentMutation(ctx, tx, record.OrganizationID, record.AssignmentID, result.Version, record.AuditID, record.OutboxID, record.ActorType, record.ActorID, record.OccurredAt, "device.assignment_revoked", map[string]any{"assignment_id": result.ID.String(), "device_id": result.DeviceID.String(), "version": result.Version})
	})
	return result, err
}

func (repository *Repository) recordMutation(ctx context.Context, tx pgx.Tx, organizationID, deviceID identifier.ID, version int64, auditID, outboxID identifier.ID, actorType, actorID string, occurredAt time.Time, action string, value map[string]any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal device mutation: %w", err)
	}
	platform := repository.platform.WithTx(tx)
	occurredAt = occurredAt.UTC()
	if err := platform.InsertAuditEntry(ctx, platformpostgres.AuditEntry{ID: auditID, OrganizationID: organizationID, ActorType: actorType, ActorID: actorID, Action: action, SubjectType: "device", SubjectID: deviceID.String(), Result: "success", AfterData: data, OccurredAt: occurredAt}); err != nil {
		return err
	}
	return platform.InsertOutboxEvent(ctx, platformpostgres.OutboxEvent{ID: outboxID, OrganizationID: organizationID, AggregateType: "device", AggregateID: deviceID, AggregateVersion: version, EventType: action, SchemaVersion: 1, Payload: data, AvailableAt: occurredAt, OccurredAt: occurredAt})
}

func (repository *Repository) recordAssignmentMutation(ctx context.Context, tx pgx.Tx, organizationID, assignmentID identifier.ID, version int64, auditID, outboxID identifier.ID, actorType, actorID string, occurredAt time.Time, action string, value map[string]any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal device assignment mutation: %w", err)
	}
	occurredAt = occurredAt.UTC()
	platform := repository.platform.WithTx(tx)
	if err := platform.InsertAuditEntry(ctx, platformpostgres.AuditEntry{ID: auditID, OrganizationID: organizationID, ActorType: actorType, ActorID: actorID, Action: action, SubjectType: "device_assignment", SubjectID: assignmentID.String(), Result: "success", AfterData: data, OccurredAt: occurredAt}); err != nil {
		return err
	}
	return platform.InsertOutboxEvent(ctx, platformpostgres.OutboxEvent{ID: outboxID, OrganizationID: organizationID, AggregateType: "device_assignment", AggregateID: assignmentID, AggregateVersion: version, EventType: action, SchemaVersion: 1, Payload: data, AvailableAt: occurredAt, OccurredAt: occurredAt})
}

func mapDeviceFields(rawID, rawOrganizationID, rawVenueID uuid.UUID, name, platform, appVersion, state string, lastSeenAt pgtype.Timestamptz, version int64, createdAt, updatedAt time.Time) (device.Device, error) {
	id, err := identifier.FromUUID(rawID)
	if err != nil {
		return device.Device{}, fmt.Errorf("map device ID: %w", err)
	}
	organizationID, err := identifier.FromUUID(rawOrganizationID)
	if err != nil {
		return device.Device{}, fmt.Errorf("map device organization ID: %w", err)
	}
	venueID, err := identifier.FromUUID(rawVenueID)
	if err != nil {
		return device.Device{}, fmt.Errorf("map device venue ID: %w", err)
	}
	value := device.Device{ID: id, OrganizationID: organizationID, VenueID: venueID, Name: name, Platform: platform, AppVersion: appVersion, State: device.State(state), Version: version, CreatedAt: createdAt.UTC(), UpdatedAt: updatedAt.UTC()}
	if lastSeenAt.Valid {
		lastSeen := lastSeenAt.Time.UTC()
		value.LastSeenAt = &lastSeen
	}
	return value, nil
}

func mapAssignmentFields(rawID, rawOrganizationID, rawDeviceID uuid.UUID, rawSessionID, rawGateID pgtype.UUID, capability, state string, validFrom time.Time, validUntil pgtype.Timestamptz, version int64, createdAt, updatedAt time.Time) (device.Assignment, error) {
	id, err := identifier.FromUUID(rawID)
	if err != nil {
		return device.Assignment{}, fmt.Errorf("map assignment ID: %w", err)
	}
	organizationID, err := identifier.FromUUID(rawOrganizationID)
	if err != nil {
		return device.Assignment{}, fmt.Errorf("map assignment organization ID: %w", err)
	}
	deviceID, err := identifier.FromUUID(rawDeviceID)
	if err != nil {
		return device.Assignment{}, fmt.Errorf("map assignment device ID: %w", err)
	}
	value := device.Assignment{ID: id, OrganizationID: organizationID, DeviceID: deviceID, Capability: access.Permission(capability), State: state, ValidFrom: validFrom.UTC(), Version: version, CreatedAt: createdAt.UTC(), UpdatedAt: updatedAt.UTC()}
	if rawSessionID.Valid {
		mapped, mapErr := identifier.FromUUID(rawSessionID.Bytes)
		if mapErr != nil {
			return device.Assignment{}, fmt.Errorf("map assignment session ID: %w", mapErr)
		}
		value.SessionID = &mapped
	}
	if rawGateID.Valid {
		mapped, mapErr := identifier.FromUUID(rawGateID.Bytes)
		if mapErr != nil {
			return device.Assignment{}, fmt.Errorf("map assignment gate ID: %w", mapErr)
		}
		value.GateID = &mapped
	}
	if validUntil.Valid {
		until := validUntil.Time.UTC()
		value.ValidUntil = &until
	}
	return value, nil
}

func nullableID(value *identifier.ID) uuid.UUID {
	if value == nil {
		return uuid.Nil
	}
	return value.UUID()
}

func nullableTime(value *time.Time) pgtype.Timestamptz {
	if value == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
}

var _ device.Repository = (*Repository)(nil)
var _ device.AssignmentRepository = (*Repository)(nil)
var _ device.CredentialRepository = (*Repository)(nil)
