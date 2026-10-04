package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/HK9750/venueos/internal/audit"
	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/internal/platform/postgres/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
)

// ListAuditEntries reads only the verified tenant's append-only audit stream.
// Ordering is deterministic so the cursor remains stable under concurrent writes.
func (store *Store) ListAuditEntries(ctx context.Context, filter audit.ListInput) (audit.Page, error) {
	if filter.OrganizationID.IsZero() || filter.Limit < 1 || filter.Limit > audit.MaxLimit {
		return audit.Page{}, fmt.Errorf("%w: organization and bounded limit are required", ErrInvalidRecord)
	}
	params := sqlc.ListAuditEntriesParams{
		OrganizationID: filter.OrganizationID.UUID(),
		ActorType:      nullableText(filter.ActorType),
		ActorID:        nullableText(filter.ActorID),
		Action:         nullableText(filter.Action),
		SubjectType:    nullableText(filter.SubjectType),
		SubjectID:      nullableText(filter.SubjectID),
		Result:         nullableText(filter.Result),
		RequestID:      nullableText(filter.RequestID),
		OccurredFrom:   nullableAuditTime(filter.OccurredFrom),
		OccurredUntil:  nullableAuditTime(filter.OccurredUntil),
		PageLimit:      filter.Limit + 1,
	}
	if filter.After != nil {
		params.CursorOccurredAt = pgtype.Timestamptz{Time: filter.After.OccurredAt.UTC(), Valid: true}
		params.CursorID = pgtype.UUID{Bytes: [16]byte(filter.After.ID.UUID()), Valid: true}
	}
	rows, err := store.queries.ListAuditEntries(ctx, params)
	if err != nil {
		return audit.Page{}, fmt.Errorf("list audit entries: %w", err)
	}
	page := audit.Page{Items: make([]audit.Entry, 0, len(rows))}
	for _, row := range rows {
		entry, mapErr := mapAuditEntry(row)
		if mapErr != nil {
			return audit.Page{}, mapErr
		}
		page.Items = append(page.Items, entry)
	}
	if len(page.Items) > int(filter.Limit) {
		page.Items = page.Items[:filter.Limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor = &audit.Cursor{
			OrganizationID: filter.OrganizationID,
			OccurredAt:     last.OccurredAt,
			ID:             last.ID,
			FilterHash:     audit.FilterHash(filter),
		}
	}
	return page, nil
}

func nullableAuditTime(value *time.Time) pgtype.Timestamptz {
	if value == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
}

func mapAuditEntry(row sqlc.AuditEntry) (audit.Entry, error) {
	id, err := identifier.FromUUID(row.ID)
	if err != nil {
		return audit.Entry{}, fmt.Errorf("map audit entry ID: %w", err)
	}
	organizationID, err := identifier.FromUUID(row.OrganizationID)
	if err != nil {
		return audit.Entry{}, fmt.Errorf("map audit organization ID: %w", err)
	}
	entry := audit.Entry{
		ID:                 id,
		OrganizationID:     organizationID,
		ActorType:          row.ActorType,
		ActorID:            row.ActorID.String,
		EffectiveActorType: row.EffectiveActorType.String,
		EffectiveActorID:   row.EffectiveActorID.String,
		Action:             row.Action,
		SubjectType:        row.SubjectType,
		SubjectID:          row.SubjectID,
		Result:             row.Result,
		Reason:             row.Reason.String,
		RequestID:          row.RequestID.String,
		TraceID:            row.TraceID.String,
		BeforeData:         append([]byte(nil), row.BeforeData...),
		AfterData:          append([]byte(nil), row.AfterData...),
		OccurredAt:         row.OccurredAt.UTC(),
	}
	if row.SourceIp != nil {
		entry.SourceIP = row.SourceIp.String()
	}
	if row.DeviceID.Valid {
		deviceID, deviceErr := identifier.FromUUID(row.DeviceID.Bytes)
		if deviceErr != nil {
			return audit.Entry{}, fmt.Errorf("map audit device ID: %w", deviceErr)
		}
		entry.DeviceID = &deviceID
	}
	return entry, nil
}
