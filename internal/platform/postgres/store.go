// Package postgres persists cross-cutting VenueOS platform records.
package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/internal/platform/inbox"
	"github.com/HK9750/venueos/internal/platform/jobqueue"
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

func (store *Store) InsertInboxMessage(ctx context.Context, message inbox.Message) (bool, error) {
	if err := inbox.ValidateMessage(message); err != nil {
		return false, err
	}
	if strings.TrimSpace(message.PayloadReference) != message.PayloadReference || len(message.PayloadReference) > 1000 {
		return false, fmt.Errorf("%w: payload reference is invalid", inbox.ErrInvalidMessage)
	}
	rows, err := store.queries.InsertInboxMessage(ctx, sqlc.InsertInboxMessageParams{
		ID: message.ID.UUID(), OrganizationID: nullableUUID(message.OrganizationID), Source: message.Source,
		MessageID: message.MessageID, PayloadReference: nullableText(message.PayloadReference),
		PayloadSha256: message.PayloadSHA256[:], AvailableAt: message.AvailableAt.UTC(),
		ReceivedAt: message.ReceivedAt.UTC(), UpdatedAt: message.UpdatedAt.UTC(),
	})
	if err != nil {
		return false, fmt.Errorf("insert inbox message: %w", err)
	}
	if rows == 1 {
		return true, nil
	}
	existing, err := store.queries.GetInboxMessageByKey(ctx, sqlc.GetInboxMessageByKeyParams{Source: message.Source, MessageID: message.MessageID})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, inbox.ErrNotFound
	}
	if err != nil {
		return false, fmt.Errorf("get duplicate inbox message: %w", err)
	}
	if !bytes.Equal(existing.PayloadSha256, message.PayloadSHA256[:]) {
		return false, inbox.ErrPayloadConflict
	}
	return false, nil
}

func (store *Store) ClaimInboxMessages(ctx context.Context, owner string, batchSize, maxAttempts int32, lease time.Duration, now time.Time) ([]inbox.Message, error) {
	if owner == "" || len(owner) > 128 || batchSize < 1 || batchSize > 100 || maxAttempts < 1 || maxAttempts > 20 || lease <= 0 || now.IsZero() {
		return nil, fmt.Errorf("%w: inbox owner, batch, attempts, lease, and time are required", inbox.ErrInvalidMessage)
	}
	rows, err := store.queries.ClaimInboxMessages(ctx, sqlc.ClaimInboxMessagesParams{
		LeaseOwner: nullableText(owner), Now: now.UTC(), LeaseSeconds: lease.Seconds(), MaxAttempts: maxAttempts, BatchSize: batchSize,
	})
	if err != nil {
		return nil, fmt.Errorf("claim inbox messages: %w", err)
	}
	messages := make([]inbox.Message, 0, len(rows))
	for _, row := range rows {
		message, mapErr := mapInboxMessage(row)
		if mapErr != nil {
			return nil, mapErr
		}
		messages = append(messages, message)
	}
	return messages, nil
}

func (store *Store) MarkInboxMessageProcessed(ctx context.Context, id identifier.ID, owner string, processedAt time.Time) error {
	if err := validateInboxLease(id, owner, processedAt); err != nil {
		return err
	}
	rows, err := store.queries.MarkInboxMessageProcessed(ctx, sqlc.MarkInboxMessageProcessedParams{
		ProcessedAt: pgtype.Timestamptz{Time: processedAt.UTC(), Valid: true}, ID: id.UUID(), LeaseOwner: nullableText(owner),
	})
	if err != nil {
		return fmt.Errorf("mark inbox message processed: %w", err)
	}
	if rows != 1 {
		return inbox.ErrLeaseLost
	}
	return nil
}

func (store *Store) RetryInboxMessage(ctx context.Context, id identifier.ID, owner string, availableAt time.Time, errorCode, errorMessage string) error {
	if err := validateInboxFailure(id, owner, availableAt, errorCode, errorMessage); err != nil {
		return err
	}
	rows, err := store.queries.RetryInboxMessage(ctx, sqlc.RetryInboxMessageParams{
		AvailableAt: availableAt.UTC(), ErrorCode: nullableText(errorCode), ErrorMessage: nullableText(errorMessage), ID: id.UUID(), LeaseOwner: nullableText(owner),
	})
	if err != nil {
		return fmt.Errorf("retry inbox message: %w", err)
	}
	if rows != 1 {
		return inbox.ErrLeaseLost
	}
	return nil
}

func (store *Store) DeadLetterInboxMessage(ctx context.Context, id identifier.ID, owner string, deadLetteredAt time.Time, errorCode, errorMessage string) error {
	if err := validateInboxFailure(id, owner, deadLetteredAt, errorCode, errorMessage); err != nil {
		return err
	}
	rows, err := store.queries.DeadLetterInboxMessage(ctx, sqlc.DeadLetterInboxMessageParams{
		DeadLetteredAt: deadLetteredAt.UTC(), ErrorCode: nullableText(errorCode), ErrorMessage: nullableText(errorMessage), ID: id.UUID(), LeaseOwner: nullableText(owner),
	})
	if err != nil {
		return fmt.Errorf("dead-letter inbox message: %w", err)
	}
	if rows != 1 {
		return inbox.ErrLeaseLost
	}
	return nil
}

func (store *Store) DeadLetterExhaustedInboxLeases(ctx context.Context, maxAttempts int32, now time.Time) (int64, error) {
	if maxAttempts < 1 || maxAttempts > 20 || now.IsZero() {
		return 0, fmt.Errorf("%w: inbox attempts and time are required", inbox.ErrInvalidMessage)
	}
	rows, err := store.queries.DeadLetterExhaustedInboxLeases(ctx, sqlc.DeadLetterExhaustedInboxLeasesParams{
		Now: now.UTC(), MaxAttempts: maxAttempts,
	})
	if err != nil {
		return 0, fmt.Errorf("dead-letter exhausted inbox leases: %w", err)
	}
	return rows, nil
}

func validateInboxLease(id identifier.ID, owner string, at time.Time) error {
	if id.IsZero() || owner == "" || len(owner) > 128 || at.IsZero() {
		return fmt.Errorf("%w: inbox ID, owner, and time are required", inbox.ErrInvalidMessage)
	}
	return nil
}

func validateInboxFailure(id identifier.ID, owner string, at time.Time, errorCode, errorMessage string) error {
	if err := validateInboxLease(id, owner, at); err != nil {
		return err
	}
	if len(errorCode) < 1 || len(errorCode) > 64 || len(errorMessage) < 1 || len(errorMessage) > 1000 {
		return fmt.Errorf("%w: bounded inbox failure code and message are required", inbox.ErrInvalidMessage)
	}
	return nil
}

func mapInboxMessage(row sqlc.InboxMessage) (inbox.Message, error) {
	id, err := identifier.FromUUID(row.ID)
	if err != nil {
		return inbox.Message{}, fmt.Errorf("map inbox message ID: %w", err)
	}
	if len(row.PayloadSha256) != 32 {
		return inbox.Message{}, fmt.Errorf("%w: inbox payload hash must be 32 bytes", inbox.ErrInvalidMessage)
	}
	message := inbox.Message{
		ID: id, Source: row.Source, MessageID: row.MessageID, State: row.State,
		AttemptCount: row.AttemptCount, AvailableAt: row.AvailableAt, ReceivedAt: row.ReceivedAt,
		UpdatedAt: row.UpdatedAt,
	}
	copy(message.PayloadSHA256[:], row.PayloadSha256)
	if row.OrganizationID.Valid {
		organizationID, orgErr := identifier.FromUUID(row.OrganizationID.Bytes)
		if orgErr != nil {
			return inbox.Message{}, fmt.Errorf("map inbox organization ID: %w", orgErr)
		}
		message.OrganizationID = &organizationID
	}
	if row.PayloadReference.Valid {
		message.PayloadReference = row.PayloadReference.String
	}
	if row.LeaseExpiresAt.Valid {
		value := row.LeaseExpiresAt.Time
		message.LeaseExpiresAt = &value
	}
	if row.ProcessedAt.Valid {
		value := row.ProcessedAt.Time
		message.ProcessedAt = &value
	}
	return message, nil
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
	AttemptCount     int32
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

func (store *Store) ClaimOutboxEvents(ctx context.Context, owner string, batchSize, maxAttempts int32, lease time.Duration, now time.Time) ([]OutboxEvent, error) {
	if owner == "" || len(owner) > 128 || batchSize < 1 || batchSize > 100 || maxAttempts < 1 || lease <= 0 || now.IsZero() {
		return nil, fmt.Errorf("%w: outbox owner, batch, attempts, lease, and time are required", ErrInvalidRecord)
	}
	rows, err := store.queries.ClaimOutboxEvents(ctx, sqlc.ClaimOutboxEventsParams{
		LeaseOwner: nullableText(owner), Now: now.UTC(), LeaseSeconds: lease.Seconds(), MaxAttempts: maxAttempts, BatchSize: batchSize,
	})
	if err != nil {
		return nil, fmt.Errorf("claim outbox events: %w", err)
	}
	events := make([]OutboxEvent, 0, len(rows))
	for _, row := range rows {
		event, mapErr := mapOutboxEvent(row)
		if mapErr != nil {
			return nil, mapErr
		}
		events = append(events, event)
	}
	return events, nil
}

func (store *Store) MarkOutboxPublished(ctx context.Context, id identifier.ID, owner string, publishedAt time.Time) error {
	if err := validateOutboxLease(id, owner, publishedAt); err != nil {
		return err
	}
	rows, err := store.queries.MarkOutboxPublished(ctx, sqlc.MarkOutboxPublishedParams{PublishedAt: pgtype.Timestamptz{Time: publishedAt.UTC(), Valid: true}, ID: id.UUID(), LeaseOwner: nullableText(owner)})
	if err != nil {
		return fmt.Errorf("mark outbox event published: %w", err)
	}
	if rows != 1 {
		return fmt.Errorf("mark outbox event published: %w", jobqueue.ErrLeaseLost)
	}
	return nil
}

func (store *Store) RetryOutboxEvent(ctx context.Context, id identifier.ID, owner string, availableAt time.Time, errorCode, errorMessage string) error {
	if err := validateOutboxFailure(id, owner, availableAt, errorCode, errorMessage); err != nil {
		return err
	}
	rows, err := store.queries.RetryOutboxEvent(ctx, sqlc.RetryOutboxEventParams{AvailableAt: availableAt.UTC(), ErrorCode: nullableText(errorCode), ErrorMessage: nullableText(errorMessage), ID: id.UUID(), LeaseOwner: nullableText(owner)})
	if err != nil {
		return fmt.Errorf("retry outbox event: %w", err)
	}
	if rows != 1 {
		return fmt.Errorf("retry outbox event: %w", jobqueue.ErrLeaseLost)
	}
	return nil
}

func (store *Store) DeadLetterOutboxEvent(ctx context.Context, id identifier.ID, owner string, deadLetteredAt time.Time, errorCode, errorMessage string) error {
	if err := validateOutboxFailure(id, owner, deadLetteredAt, errorCode, errorMessage); err != nil {
		return err
	}
	rows, err := store.queries.DeadLetterOutboxEvent(ctx, sqlc.DeadLetterOutboxEventParams{DeadLetteredAt: pgtype.Timestamptz{Time: deadLetteredAt.UTC(), Valid: true}, ErrorCode: nullableText(errorCode), ErrorMessage: nullableText(errorMessage), ID: id.UUID(), LeaseOwner: nullableText(owner)})
	if err != nil {
		return fmt.Errorf("dead-letter outbox event: %w", err)
	}
	if rows != 1 {
		return fmt.Errorf("dead-letter outbox event: %w", jobqueue.ErrLeaseLost)
	}
	return nil
}

func (store *Store) DeadLetterExhaustedOutboxLeases(ctx context.Context, maxAttempts int32, now time.Time) (int64, error) {
	if maxAttempts < 1 || now.IsZero() {
		return 0, fmt.Errorf("%w: outbox attempts and time are required", ErrInvalidRecord)
	}
	rows, err := store.queries.DeadLetterExhaustedOutboxLeases(ctx, sqlc.DeadLetterExhaustedOutboxLeasesParams{Now: pgtype.Timestamptz{Time: now.UTC(), Valid: true}, MaxAttempts: maxAttempts})
	if err != nil {
		return 0, fmt.Errorf("dead-letter exhausted outbox leases: %w", err)
	}
	return rows, nil
}

func validateOutboxLease(id identifier.ID, owner string, at time.Time) error {
	if id.IsZero() || owner == "" || len(owner) > 128 || at.IsZero() {
		return fmt.Errorf("%w: outbox ID, owner, and time are required", ErrInvalidRecord)
	}
	return nil
}

func validateOutboxFailure(id identifier.ID, owner string, at time.Time, errorCode, errorMessage string) error {
	if err := validateOutboxLease(id, owner, at); err != nil {
		return err
	}
	if len(errorCode) < 1 || len(errorCode) > 64 || len(errorMessage) < 1 || len(errorMessage) > 1000 {
		return fmt.Errorf("%w: bounded outbox failure code and message are required", ErrInvalidRecord)
	}
	return nil
}

func mapOutboxEvent(row sqlc.OutboxEvent) (OutboxEvent, error) {
	id, err := identifier.FromUUID(row.ID)
	if err != nil {
		return OutboxEvent{}, fmt.Errorf("map outbox event ID: %w", err)
	}
	organizationID, err := identifier.FromUUID(row.OrganizationID)
	if err != nil {
		return OutboxEvent{}, fmt.Errorf("map outbox organization ID: %w", err)
	}
	aggregateID, err := identifier.FromUUID(row.AggregateID)
	if err != nil {
		return OutboxEvent{}, fmt.Errorf("map outbox aggregate ID: %w", err)
	}
	if !json.Valid(row.Payload) {
		return OutboxEvent{}, fmt.Errorf("map outbox event: invalid JSON payload")
	}
	event := OutboxEvent{ID: id, OrganizationID: organizationID, AggregateType: row.AggregateType, AggregateID: aggregateID, AggregateVersion: row.AggregateVersion, EventType: row.EventType, SchemaVersion: row.SchemaVersion, Payload: row.Payload, AvailableAt: row.AvailableAt, OccurredAt: row.OccurredAt, AttemptCount: row.AttemptCount}
	return event, nil
}

type RealtimeEvent struct {
	ID                 identifier.ID
	OrganizationID     identifier.ID
	SessionID          identifier.ID
	Topic              string
	Sequence           int64
	EventType          string
	SchemaVersion      int32
	Payload            json.RawMessage
	OccurredAt         time.Time
	RetentionExpiresAt time.Time
}

func (store *Store) InsertRealtimeEvent(ctx context.Context, event RealtimeEvent) error {
	if event.ID.IsZero() || event.OrganizationID.IsZero() || event.SessionID.IsZero() || event.Sequence < 1 || event.SchemaVersion < 1 || event.OccurredAt.IsZero() || event.RetentionExpiresAt.Before(event.OccurredAt) || !json.Valid(event.Payload) {
		return fmt.Errorf("%w: realtime event IDs, sequence, schema, payload, and times are required", ErrInvalidRecord)
	}
	if err := store.queries.InsertRealtimeEvent(ctx, sqlc.InsertRealtimeEventParams{
		ID: event.ID.UUID(), OrganizationID: event.OrganizationID.UUID(), SessionID: event.SessionID.UUID(), Topic: event.Topic,
		Sequence: event.Sequence, EventType: event.EventType, SchemaVersion: event.SchemaVersion, Payload: event.Payload,
		OccurredAt: event.OccurredAt.UTC(), RetentionExpiresAt: event.RetentionExpiresAt.UTC(),
	}); err != nil {
		return fmt.Errorf("insert realtime event: %w", err)
	}
	return nil
}

func (store *Store) DeleteExpiredRealtimeEvents(ctx context.Context, limit int32) (int64, error) {
	if limit < 1 || limit > 1000 {
		return 0, fmt.Errorf("%w: realtime event prune limit must be between 1 and 1000", ErrInvalidRecord)
	}
	rows, err := store.queries.DeleteExpiredRealtimeEvents(ctx, limit)
	if err != nil {
		return 0, fmt.Errorf("delete expired realtime events: %w", err)
	}
	return rows, nil
}

func (store *Store) DeleteExpiredIdempotencyRecords(ctx context.Context, limit int32, now time.Time) (int64, error) {
	if limit < 1 || limit > 1000 || now.IsZero() {
		return 0, fmt.Errorf("%w: idempotency prune limit and time are required", ErrInvalidRecord)
	}
	rows, err := store.queries.DeleteExpiredIdempotencyRecords(ctx, sqlc.DeleteExpiredIdempotencyRecordsParams{Now: now.UTC(), BatchLimit: limit})
	if err != nil {
		return 0, fmt.Errorf("delete expired idempotency records: %w", err)
	}
	return rows, nil
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
		OrganizationID:     pgtype.UUID{Bytes: [16]byte(claim.OrganizationID.UUID()), Valid: true},
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
		OrganizationID: pgtype.UUID{Bytes: [16]byte(organizationID.UUID()), Valid: true},
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
		OrganizationID: pgtype.UUID{Bytes: [16]byte(completion.OrganizationID.UUID()), Valid: true},
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
	if !row.OrganizationID.Valid {
		return IdempotencyRecord{}, fmt.Errorf("map idempotency organization ID: missing tenant scope")
	}
	organizationID, err := identifier.FromUUID(row.OrganizationID.Bytes)
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
