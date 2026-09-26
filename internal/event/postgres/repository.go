// Package postgres persists tenant-scoped events and immutable revisions.
package postgres

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/HK9750/venueos/internal/database"
	"github.com/HK9750/venueos/internal/event"
	"github.com/HK9750/venueos/internal/event/postgres/sqlc"
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

func (repository *Repository) Create(ctx context.Context, record event.CreateRecord) (event.Event, error) {
	var result event.Event
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		_, err := queries.InsertEvent(ctx, sqlc.InsertEventParams{ID: record.Event.ID.UUID(), OrganizationID: record.Event.OrganizationID.UUID(), Slug: record.Event.Slug, Status: string(record.Event.Status), CurrentRevision: 0, Version: 1, CreatedAt: record.Event.CreatedAt})
		if err != nil {
			var postgresError *pgconn.PgError
			if errors.As(err, &postgresError) && postgresError.Code == "23505" && postgresError.ConstraintName == "uq_events_organization_slug" {
				return event.ErrSlugConflict
			}
			return fmt.Errorf("insert event: %w", err)
		}
		if _, err := queries.InsertEventRevision(ctx, sqlc.InsertEventRevisionParams{ID: record.Event.ID.UUID(), OrganizationID: record.Event.OrganizationID.UUID(), EventID: record.Event.ID.UUID(), Title: record.Event.Title, ShortDescription: record.Event.ShortDescription, LongDescription: record.Event.LongDescription, Checksum: record.Checksum, CreatedAt: record.Event.CreatedAt}); err != nil {
			return fmt.Errorf("insert event revision: %w", err)
		}
		row, err := queries.GetEventProjection(ctx, sqlc.GetEventProjectionParams{OrganizationID: record.Event.OrganizationID.UUID(), ID: record.Event.ID.UUID()})
		if err != nil {
			return fmt.Errorf("load created event: %w", err)
		}
		result = mapProjection(row)
		return repository.recordMutation(ctx, tx, record.Event.OrganizationID, record.Event.ID, record.AuditID, record.OutboxID, record.ActorType, record.ActorID, "event.created", result.Version, eventPayload(result), result.CreatedAt)
	})
	return result, err
}

func (repository *Repository) Get(ctx context.Context, organizationID, eventID identifier.ID) (event.Event, error) {
	row, err := repository.queries.GetEventProjection(ctx, sqlc.GetEventProjectionParams{OrganizationID: organizationID.UUID(), ID: eventID.UUID()})
	if errors.Is(err, pgx.ErrNoRows) {
		return event.Event{}, event.ErrNotFound
	}
	if err != nil {
		return event.Event{}, fmt.Errorf("get event: %w", err)
	}
	return mapProjection(row), nil
}

func (repository *Repository) List(ctx context.Context, organizationID identifier.ID, limit int32, after *event.Cursor) (event.Page, error) {
	fetchLimit := limit + 1
	page := event.Page{Items: make([]event.Event, 0, fetchLimit)}
	if after == nil {
		rows, err := repository.queries.ListEvents(ctx, sqlc.ListEventsParams{OrganizationID: organizationID.UUID(), Limit: fetchLimit})
		if err != nil {
			return event.Page{}, fmt.Errorf("list events: %w", err)
		}
		for _, row := range rows {
			page.Items = append(page.Items, mapListRow(row))
		}
	} else {
		rows, err := repository.queries.ListEventsAfter(ctx, sqlc.ListEventsAfterParams{OrganizationID: organizationID.UUID(), CursorCreatedAt: after.CreatedAt.UTC(), CursorID: after.ID.UUID(), PageLimit: fetchLimit})
		if err != nil {
			return event.Page{}, fmt.Errorf("list events after cursor: %w", err)
		}
		for _, row := range rows {
			page.Items = append(page.Items, mapAfterRow(row))
		}
	}
	if len(page.Items) > int(limit) {
		page.Items = page.Items[:limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor = &event.Cursor{OrganizationID: organizationID, CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return page, nil
}

func (repository *Repository) Update(ctx context.Context, record event.UpdateRecord) (event.Event, error) {
	var result event.Event
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		current, err := queries.GetEventForUpdate(ctx, sqlc.GetEventForUpdateParams{OrganizationID: record.Input.OrganizationID.UUID(), ID: record.Input.EventID.UUID()})
		if errors.Is(err, pgx.ErrNoRows) {
			return event.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("load event for update: %w", err)
		}
		if current.Status == string(event.StatusCancelled) || current.Status == string(event.StatusArchived) {
			return event.ErrInvalidState
		}
		if current.Version != record.Input.Version {
			return event.ErrVersionConflict
		}
		draft, err := queries.GetDraftRevisionForUpdate(ctx, sqlc.GetDraftRevisionForUpdateParams{OrganizationID: record.Input.OrganizationID.UUID(), EventID: record.Input.EventID.UUID()})
		if errors.Is(err, pgx.ErrNoRows) {
			return event.ErrInvalidState
		}
		if err != nil {
			return fmt.Errorf("load event draft: %w", err)
		}
		if _, err := queries.UpdateEventIdentity(ctx, sqlc.UpdateEventIdentityParams{OrganizationID: record.Input.OrganizationID.UUID(), ID: record.Input.EventID.UUID(), Slug: record.Input.Slug, UpdatedAt: record.OccurredAt.UTC(), Version: record.Input.Version}); err != nil {
			var postgresError *pgconn.PgError
			if errors.As(err, &postgresError) && postgresError.Code == "23505" && postgresError.ConstraintName == "uq_events_organization_slug" {
				return event.ErrSlugConflict
			}
			return fmt.Errorf("update event identity: %w", err)
		}
		if err := queries.UpdateEventDraftRevision(ctx, sqlc.UpdateEventDraftRevisionParams{OrganizationID: record.Input.OrganizationID.UUID(), EventID: record.Input.EventID.UUID(), Title: record.Input.Title, ShortDescription: record.Input.ShortDescription, LongDescription: record.Input.LongDescription, Checksum: record.Checksum, UpdatedAt: record.OccurredAt.UTC(), Version: draft.Version}); err != nil {
			return fmt.Errorf("update event draft: %w", err)
		}
		row, err := queries.GetEventProjection(ctx, sqlc.GetEventProjectionParams{OrganizationID: record.Input.OrganizationID.UUID(), ID: record.Input.EventID.UUID()})
		if err != nil {
			return fmt.Errorf("load updated event: %w", err)
		}
		result = mapProjection(row)
		return repository.recordMutation(ctx, tx, record.Input.OrganizationID, record.Input.EventID, record.AuditID, record.OutboxID, record.ActorType, record.ActorID, "event.updated", result.Version, eventPayload(result), record.OccurredAt)
	})
	return result, err
}

func (repository *Repository) Publish(ctx context.Context, record event.PublishRecord) (event.Event, error) {
	var result event.Event
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		current, err := queries.GetEventForUpdate(ctx, sqlc.GetEventForUpdateParams{OrganizationID: record.Input.OrganizationID.UUID(), ID: record.Input.EventID.UUID()})
		if errors.Is(err, pgx.ErrNoRows) {
			return event.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("load event for publish: %w", err)
		}
		if current.Status == string(event.StatusCancelled) || current.Status == string(event.StatusArchived) {
			return event.ErrInvalidState
		}
		if current.Version != record.Input.Version {
			return event.ErrVersionConflict
		}
		draft, err := queries.GetDraftRevisionForUpdate(ctx, sqlc.GetDraftRevisionForUpdateParams{OrganizationID: record.Input.OrganizationID.UUID(), EventID: record.Input.EventID.UUID()})
		if errors.Is(err, pgx.ErrNoRows) {
			return event.ErrInvalidState
		}
		if err != nil {
			return fmt.Errorf("load event draft for publish: %w", err)
		}
		next, err := queries.NextEventRevision(ctx, sqlc.NextEventRevisionParams{OrganizationID: record.Input.OrganizationID.UUID(), EventID: record.Input.EventID.UUID()})
		if err != nil {
			return fmt.Errorf("get next event revision: %w", err)
		}
		if err := queries.RetirePublishedEventRevisions(ctx, sqlc.RetirePublishedEventRevisionsParams{OrganizationID: record.Input.OrganizationID.UUID(), EventID: record.Input.EventID.UUID(), UpdatedAt: record.OccurredAt.UTC()}); err != nil {
			return fmt.Errorf("retire previous event revisions: %w", err)
		}
		if err := queries.PublishEventRevision(ctx, sqlc.PublishEventRevisionParams{OrganizationID: record.Input.OrganizationID.UUID(), EventID: record.Input.EventID.UUID(), Revision: int64(next), PublishedAt: pgtype.Timestamptz{Time: record.OccurredAt.UTC(), Valid: true}, Version: draft.Version}); err != nil {
			return fmt.Errorf("publish event revision: %w", err)
		}
		if err := queries.PublishEventAggregate(ctx, sqlc.PublishEventAggregateParams{OrganizationID: record.Input.OrganizationID.UUID(), ID: record.Input.EventID.UUID(), CurrentRevision: int64(next), UpdatedAt: record.OccurredAt.UTC(), Version: record.Input.Version}); err != nil {
			return fmt.Errorf("publish event aggregate: %w", err)
		}
		row, err := queries.GetEventProjection(ctx, sqlc.GetEventProjectionParams{OrganizationID: record.Input.OrganizationID.UUID(), ID: record.Input.EventID.UUID()})
		if err != nil {
			return fmt.Errorf("load published event: %w", err)
		}
		result = mapProjection(row)
		return repository.recordMutation(ctx, tx, record.Input.OrganizationID, record.Input.EventID, record.AuditID, record.OutboxID, record.ActorType, record.ActorID, "event.published", result.Version, eventPayload(result), record.OccurredAt)
	})
	return result, err
}

func (repository *Repository) Clone(ctx context.Context, record event.CloneRecord) (event.Event, error) {
	var result event.Event
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		current, err := queries.GetEventForUpdate(ctx, sqlc.GetEventForUpdateParams{OrganizationID: record.OrganizationID.UUID(), ID: record.EventID.UUID()})
		if errors.Is(err, pgx.ErrNoRows) {
			return event.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("load event for clone: %w", err)
		}
		if current.Status == string(event.StatusCancelled) || current.Status == string(event.StatusArchived) || current.CurrentRevision == 0 {
			return event.ErrInvalidState
		}
		if _, err := queries.GetDraftRevisionForUpdate(ctx, sqlc.GetDraftRevisionForUpdateParams{OrganizationID: record.OrganizationID.UUID(), EventID: record.EventID.UUID()}); err == nil {
			return event.ErrInvalidState
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("check existing event draft: %w", err)
		}
		if _, err := queries.CloneEventDraft(ctx, sqlc.CloneEventDraftParams{ID: record.CloneID.UUID(), OrganizationID: record.OrganizationID.UUID(), EventID: record.EventID.UUID(), CreatedAt: record.OccurredAt.UTC()}); errors.Is(err, pgx.ErrNoRows) {
			return event.ErrInvalidState
		} else if err != nil {
			return fmt.Errorf("clone event revision: %w", err)
		}
		if err := queries.IncrementEventVersion(ctx, sqlc.IncrementEventVersionParams{OrganizationID: record.OrganizationID.UUID(), ID: record.EventID.UUID(), UpdatedAt: record.OccurredAt.UTC()}); err != nil {
			return fmt.Errorf("increment event version: %w", err)
		}
		row, err := queries.GetEventProjection(ctx, sqlc.GetEventProjectionParams{OrganizationID: record.OrganizationID.UUID(), ID: record.EventID.UUID()})
		if err != nil {
			return fmt.Errorf("load cloned event: %w", err)
		}
		result = mapProjection(row)
		return repository.recordMutation(ctx, tx, record.OrganizationID, record.EventID, record.AuditID, record.OutboxID, record.ActorType, record.ActorID, "event.cloned", result.Version, map[string]any{"event_id": record.EventID.String(), "draft_revision": true, "current_revision": result.CurrentRevision}, record.OccurredAt)
	})
	return result, err
}

func (repository *Repository) recordMutation(ctx context.Context, tx pgx.Tx, organizationID, aggregateID, auditID, outboxID identifier.ID, actorType, actorID, action string, version int64, after map[string]any, occurredAt time.Time) error {
	platform := repository.platform.WithTx(tx)
	afterData, err := json.Marshal(after)
	if err != nil {
		return fmt.Errorf("marshal event audit data: %w", err)
	}
	aggregateType := strings.SplitN(action, ".", 2)[0]
	if err := platform.InsertAuditEntry(ctx, platformpostgres.AuditEntry{ID: auditID, OrganizationID: organizationID, ActorType: actorType, ActorID: actorID, Action: action, SubjectType: aggregateType, SubjectID: aggregateID.String(), Result: "success", AfterData: afterData, OccurredAt: occurredAt}); err != nil {
		return err
	}
	return platform.InsertOutboxEvent(ctx, platformpostgres.OutboxEvent{ID: outboxID, OrganizationID: organizationID, AggregateType: aggregateType, AggregateID: aggregateID, AggregateVersion: version, EventType: action, SchemaVersion: 1, Payload: afterData, AvailableAt: occurredAt, OccurredAt: occurredAt})
}

func mapProjection(row sqlc.GetEventProjectionRow) event.Event {
	organizationID, _ := identifier.FromUUID(row.OrganizationID)
	eventID, _ := identifier.FromUUID(row.ID)
	value := event.Event{ID: eventID, OrganizationID: organizationID, Slug: row.Slug, Title: row.Title, ShortDescription: row.ShortDescription, LongDescription: row.LongDescription, Status: event.Status(row.Status), CurrentRevision: row.CurrentRevision, Checksum: hex.EncodeToString(row.Checksum), Version: row.Version, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
	if row.PublishedAt.Valid {
		publishedAt := row.PublishedAt.Time.UTC()
		value.PublishedAt = &publishedAt
	}
	return value
}

func mapListRow(row sqlc.ListEventsRow) event.Event {
	return mapEventValues(row.ID, row.OrganizationID, row.Slug, row.Status, row.CurrentRevision, row.Version, row.CreatedAt, row.UpdatedAt, row.Title, row.ShortDescription, row.LongDescription, row.Checksum, row.PublishedAt)
}

func mapAfterRow(row sqlc.ListEventsAfterRow) event.Event {
	return mapEventValues(row.ID, row.OrganizationID, row.Slug, row.Status, row.CurrentRevision, row.Version, row.CreatedAt, row.UpdatedAt, row.Title, row.ShortDescription, row.LongDescription, row.Checksum, row.PublishedAt)
}

func mapEventValues(id, organizationID uuid.UUID, slug, status string, currentRevision, version int64, createdAt, updatedAt time.Time, title, shortDescription, longDescription string, checksum []byte, publishedAt pgtype.Timestamptz) event.Event {
	parsedOrganization, _ := identifier.FromUUID(organizationID)
	parsedEvent, _ := identifier.FromUUID(id)
	value := event.Event{ID: parsedEvent, OrganizationID: parsedOrganization, Slug: slug, Title: title, ShortDescription: shortDescription, LongDescription: longDescription, Status: event.Status(status), CurrentRevision: currentRevision, Checksum: hex.EncodeToString(checksum), Version: version, CreatedAt: createdAt, UpdatedAt: updatedAt}
	if publishedAt.Valid {
		published := publishedAt.Time.UTC()
		value.PublishedAt = &published
	}
	return value
}

func eventPayload(value event.Event) map[string]any {
	return map[string]any{"event_id": value.ID.String(), "slug": value.Slug, "status": string(value.Status), "current_revision": value.CurrentRevision, "version": value.Version, "checksum": value.Checksum}
}
