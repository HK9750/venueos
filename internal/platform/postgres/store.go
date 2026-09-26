// Package postgres persists cross-cutting VenueOS platform records.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/internal/platform/postgres/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var (
	ErrInvalidRecord            = errors.New("invalid platform record")
	ErrIdempotencyNotFound      = errors.New("idempotency record not found")
	ErrIdempotencyNotProcessing = errors.New("idempotency record is not processing")
)

type Store struct {
	queries *sqlc.Queries
}

func NewStore(db sqlc.DBTX) *Store { return &Store{queries: sqlc.New(db)} }

func (store *Store) WithTx(tx pgx.Tx) *Store {
	return &Store{queries: store.queries.WithTx(tx)}
}

type AuditEntry struct {
	ID                 identifier.ID
	OrganizationID     identifier.ID
	ActorType          string
	ActorID            string
	EffectiveActorType string
	EffectiveActorID   string
	Action             string
	SubjectType        string
	SubjectID          string
	Result             string
	Reason             string
	RequestID          string
	TraceID            string
	SourceIP           *netip.Addr
	DeviceID           *identifier.ID
	BeforeData         json.RawMessage
	AfterData          json.RawMessage
	OccurredAt         time.Time
}

func (store *Store) InsertAuditEntry(ctx context.Context, entry AuditEntry) error {
	if entry.ID.IsZero() || entry.OrganizationID.IsZero() || entry.OccurredAt.IsZero() {
		return fmt.Errorf("%w: audit ID, organization ID, and occurred time are required", ErrInvalidRecord)
	}
	if err := store.queries.InsertAuditEntry(ctx, sqlc.InsertAuditEntryParams{
		ID:                 entry.ID.UUID(),
		OrganizationID:     entry.OrganizationID.UUID(),
		ActorType:          entry.ActorType,
		ActorID:            nullableText(entry.ActorID),
		EffectiveActorType: nullableText(entry.EffectiveActorType),
		EffectiveActorID:   nullableText(entry.EffectiveActorID),
		Action:             entry.Action,
		SubjectType:        entry.SubjectType,
		SubjectID:          entry.SubjectID,
		Result:             entry.Result,
		Reason:             nullableText(entry.Reason),
		RequestID:          nullableText(entry.RequestID),
		TraceID:            nullableText(entry.TraceID),
		SourceIp:           entry.SourceIP,
		DeviceID:           nullableUUID(entry.DeviceID),
		BeforeData:         entry.BeforeData,
		AfterData:          entry.AfterData,
		OccurredAt:         entry.OccurredAt.UTC(),
	}); err != nil {
		return fmt.Errorf("insert audit entry: %w", err)
	}
	return nil
}

type OutboxEvent struct {
	ID               identifier.ID
	OrganizationID   identifier.ID
	AggregateType    string
	AggregateID      identifier.ID
	AggregateVersion int64
	EventType        string
	SchemaVersion    int32
	Payload          json.RawMessage
	CorrelationID    string
	CausationID      *identifier.ID
	AvailableAt      time.Time
	OccurredAt       time.Time
}

func (store *Store) InsertOutboxEvent(ctx context.Context, event OutboxEvent) error {
	if event.ID.IsZero() || event.OrganizationID.IsZero() || event.AggregateID.IsZero() ||
		event.AvailableAt.IsZero() || event.OccurredAt.IsZero() || !json.Valid(event.Payload) {
		return fmt.Errorf("%w: outbox IDs, times, and valid JSON payload are required", ErrInvalidRecord)
	}
	if err := store.queries.InsertOutboxEvent(ctx, sqlc.InsertOutboxEventParams{
		ID:               event.ID.UUID(),
		OrganizationID:   event.OrganizationID.UUID(),
		AggregateType:    event.AggregateType,
		AggregateID:      event.AggregateID.UUID(),
		AggregateVersion: event.AggregateVersion,
		EventType:        event.EventType,
		SchemaVersion:    event.SchemaVersion,
		Payload:          event.Payload,
		CorrelationID:    nullableText(event.CorrelationID),
		CausationID:      nullableUUID(event.CausationID),
		AvailableAt:      event.AvailableAt.UTC(),
		OccurredAt:       event.OccurredAt.UTC(),
	}); err != nil {
		return fmt.Errorf("insert outbox event: %w", err)
	}
	return nil
}

type IdempotencyClaim struct {
	ID                 identifier.ID
	OrganizationID     identifier.ID
	PrincipalType      string
	PrincipalID        string
	Operation          string
	Key                string
	RequestFingerprint [32]byte
	LockedUntil        time.Time
	ExpiresAt          time.Time
	CreatedAt          time.Time
}

// InsertIdempotencyClaim reports false when the scope/key already exists. Callers
// must then lock and compare the existing fingerprint before replaying a result.
func (store *Store) InsertIdempotencyClaim(ctx context.Context, claim IdempotencyClaim) (bool, error) {
	if claim.ID.IsZero() || claim.OrganizationID.IsZero() || claim.LockedUntil.IsZero() ||
		claim.ExpiresAt.IsZero() || claim.CreatedAt.IsZero() {
		return false, fmt.Errorf("%w: idempotency IDs and times are required", ErrInvalidRecord)
	}
	rows, err := store.queries.InsertIdempotencyRecord(ctx, sqlc.InsertIdempotencyRecordParams{
		ID:                 claim.ID.UUID(),
		OrganizationID:     claim.OrganizationID.UUID(),
		PrincipalType:      claim.PrincipalType,
		PrincipalID:        claim.PrincipalID,
		Operation:          claim.Operation,
		IdempotencyKey:     claim.Key,
		RequestFingerprint: claim.RequestFingerprint[:],
		LockedUntil:        pgtype.Timestamptz{Time: claim.LockedUntil.UTC(), Valid: true},
		ExpiresAt:          claim.ExpiresAt.UTC(),
		CreatedAt:          claim.CreatedAt.UTC(),
	})
	if err != nil {
		return false, fmt.Errorf("insert idempotency claim: %w", err)
	}
	return rows == 1, nil
}

type IdempotencyRecord struct {
	ID                 identifier.ID
	OrganizationID     identifier.ID
	RequestFingerprint [32]byte
	State              string
	LockedUntil        *time.Time
	ResponseStatus     *int16
	ResponseBody       json.RawMessage
	ResourceType       string
	ResourceID         string
	ExpiresAt          time.Time
}

func (store *Store) GetIdempotencyRecordForUpdate(
	ctx context.Context,
	organizationID identifier.ID,
	principalType string,
	principalID string,
	operation string,
	key string,
) (IdempotencyRecord, error) {
	if organizationID.IsZero() {
		return IdempotencyRecord{}, fmt.Errorf("%w: organization ID is required", ErrInvalidRecord)
	}
	row, err := store.queries.GetIdempotencyRecordForUpdate(ctx, sqlc.GetIdempotencyRecordForUpdateParams{
		OrganizationID: organizationID.UUID(),
		PrincipalType:  principalType,
		PrincipalID:    principalID,
		Operation:      operation,
		IdempotencyKey: key,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return IdempotencyRecord{}, ErrIdempotencyNotFound
	}
	if err != nil {
		return IdempotencyRecord{}, fmt.Errorf("select idempotency record: %w", err)
	}
	record, err := mapIdempotencyRecord(row)
	if err != nil {
		return IdempotencyRecord{}, err
	}
	return record, nil
}

type IdempotencyCompletion struct {
	ID             identifier.ID
	OrganizationID identifier.ID
	State          string
	ResponseStatus int16
	ResponseBody   json.RawMessage
	ResourceType   string
	ResourceID     string
	UpdatedAt      time.Time
}

func (store *Store) CompleteIdempotencyRecord(ctx context.Context, completion IdempotencyCompletion) error {
	if completion.ID.IsZero() || completion.OrganizationID.IsZero() || completion.UpdatedAt.IsZero() {
		return fmt.Errorf("%w: idempotency IDs and updated time are required", ErrInvalidRecord)
	}
	rows, err := store.queries.CompleteIdempotencyRecord(ctx, sqlc.CompleteIdempotencyRecordParams{
		ID:             completion.ID.UUID(),
		OrganizationID: completion.OrganizationID.UUID(),
		State:          completion.State,
		ResponseStatus: pgtype.Int2{Int16: completion.ResponseStatus, Valid: true},
		ResponseBody:   completion.ResponseBody,
		ResourceType:   nullableText(completion.ResourceType),
		ResourceID:     nullableText(completion.ResourceID),
		UpdatedAt:      completion.UpdatedAt.UTC(),
	})
	if err != nil {
		return fmt.Errorf("complete idempotency record: %w", err)
	}
	if rows != 1 {
		return ErrIdempotencyNotProcessing
	}
	return nil
}

func mapIdempotencyRecord(row sqlc.IdempotencyRecord) (IdempotencyRecord, error) {
	id, err := identifier.FromUUID(row.ID)
	if err != nil {
		return IdempotencyRecord{}, fmt.Errorf("map idempotency ID: %w", err)
	}
	organizationID, err := identifier.FromUUID(row.OrganizationID)
	if err != nil {
		return IdempotencyRecord{}, fmt.Errorf("map idempotency organization ID: %w", err)
	}
	record := IdempotencyRecord{
		ID:             id,
		OrganizationID: organizationID,
		State:          row.State,
		ResponseBody:   row.ResponseBody,
		ResourceType:   row.ResourceType.String,
		ResourceID:     row.ResourceID.String,
		ExpiresAt:      row.ExpiresAt,
	}
	copy(record.RequestFingerprint[:], row.RequestFingerprint)
	if row.LockedUntil.Valid {
		value := row.LockedUntil.Time
		record.LockedUntil = &value
	}
	if row.ResponseStatus.Valid {
		value := row.ResponseStatus.Int16
		record.ResponseStatus = &value
	}
	return record, nil
}

func nullableText(value string) pgtype.Text {
	return pgtype.Text{String: value, Valid: value != ""}
}

func nullableUUID(value *identifier.ID) pgtype.UUID {
	if value == nil || value.IsZero() {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: [16]byte(value.UUID()), Valid: true}
}
