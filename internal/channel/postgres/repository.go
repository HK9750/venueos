// Package postgres persists sales channels.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/HK9750/venueos/internal/channel"
	"github.com/HK9750/venueos/internal/channel/postgres/sqlc"
	"github.com/HK9750/venueos/internal/database"
	"github.com/HK9750/venueos/internal/platform/identifier"
	platformpostgres "github.com/HK9750/venueos/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type Repository struct {
	queries      *sqlc.Queries
	platform     *platformpostgres.Store
	transactions *database.TransactionRunner
}

func New(pool sqlc.DBTX, transactions *database.TransactionRunner) *Repository {
	return &Repository{queries: sqlc.New(pool), platform: platformpostgres.NewStore(pool), transactions: transactions}
}

func (repository *Repository) Create(ctx context.Context, record channel.CreateRecord) (channel.SalesChannel, error) {
	configuration, err := json.Marshal(record.Channel.Configuration)
	if err != nil {
		return channel.SalesChannel{}, fmt.Errorf("marshal channel configuration: %w", err)
	}
	var result channel.SalesChannel
	err = repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		row, err := queries.InsertSalesChannel(ctx, sqlc.InsertSalesChannelParams{ID: record.Channel.ID.UUID(), OrganizationID: record.Channel.OrganizationID.UUID(), ChannelKey: record.Channel.Key, DisplayName: record.Channel.DisplayName, ChannelType: string(record.Channel.Type), Configuration: configuration, Status: string(record.Channel.Status), Version: record.Channel.Version, CreatedAt: record.Channel.CreatedAt})
		if err != nil {
			var postgresError *pgconn.PgError
			if errors.As(err, &postgresError) && postgresError.Code == "23505" && postgresError.ConstraintName == "uq_sales_channels_organization_key" {
				return channel.ErrKeyConflict
			}
			return fmt.Errorf("insert sales channel: %w", err)
		}
		result, err = mapSalesChannel(row)
		if err != nil {
			return err
		}
		return repository.recordMutation(ctx, tx, record, result)
	})
	return result, err
}

func (repository *Repository) List(ctx context.Context, organizationID identifier.ID, limit int32, after *channel.Cursor) (channel.Page, error) {
	fetchLimit := limit + 1
	var rows []sqlc.SalesChannel
	var err error
	if after == nil {
		rows, err = repository.queries.ListSalesChannels(ctx, sqlc.ListSalesChannelsParams{OrganizationID: organizationID.UUID(), Limit: fetchLimit})
	} else {
		rows, err = repository.queries.ListSalesChannelsAfter(ctx, sqlc.ListSalesChannelsAfterParams{OrganizationID: organizationID.UUID(), CursorCreatedAt: after.CreatedAt.UTC(), CursorID: after.ID.UUID(), PageLimit: fetchLimit})
	}
	if err != nil {
		return channel.Page{}, fmt.Errorf("list sales channels: %w", err)
	}
	page := channel.Page{Items: make([]channel.SalesChannel, 0, len(rows))}
	for _, row := range rows {
		mapped, mapErr := mapSalesChannel(row)
		if mapErr != nil {
			return channel.Page{}, mapErr
		}
		page.Items = append(page.Items, mapped)
	}
	if len(page.Items) > int(limit) {
		page.Items = page.Items[:limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor = &channel.Cursor{OrganizationID: organizationID, CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return page, nil
}

func (repository *Repository) CreateAllocation(ctx context.Context, record channel.CreateAllocationRecord) (channel.Allocation, error) {
	var result channel.Allocation
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		sessionRow, err := queries.LockSessionForAllocation(ctx, sqlc.LockSessionForAllocationParams{OrganizationID: record.Allocation.OrganizationID.UUID(), EventID: record.EventID.UUID(), ID: record.Allocation.SessionID.UUID()})
		if errors.Is(err, pgx.ErrNoRows) {
			return channel.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock session for allocation: %w", err)
		}
		if record.Allocation.Mode == channel.AllocationHardReserved {
			existing, err := queries.SumHardSessionAllocations(ctx, sqlc.SumHardSessionAllocationsParams{OrganizationID: record.Allocation.OrganizationID.UUID(), SessionID: record.Allocation.SessionID.UUID()})
			if err != nil {
				return fmt.Errorf("sum hard allocations: %w", err)
			}
			if existing > sessionRow.PhysicalCapacity-record.Allocation.Quantity {
				return channel.ErrAllocationConflict
			}
		}
		row, err := queries.InsertSessionChannelAllocation(ctx, sqlc.InsertSessionChannelAllocationParams{ID: record.Allocation.ID.UUID(), OrganizationID: record.Allocation.OrganizationID.UUID(), SessionID: record.Allocation.SessionID.UUID(), ChannelID: record.Allocation.ChannelID.UUID(), ScopeKey: record.Allocation.ScopeKey, AllocationMode: string(record.Allocation.Mode), Quantity: record.Allocation.Quantity, Version: record.Allocation.Version, CreatedAt: record.Allocation.CreatedAt})
		if err != nil {
			var postgresError *pgconn.PgError
			if errors.As(err, &postgresError) && postgresError.Code == "23505" && postgresError.ConstraintName == "uq_session_channel_allocations_scope" {
				return channel.ErrAllocationExists
			}
			if errors.As(err, &postgresError) && postgresError.Code == "23503" {
				return channel.ErrNotFound
			}
			return fmt.Errorf("insert session channel allocation: %w", err)
		}
		result = mapAllocation(row)
		return repository.recordAllocationMutation(ctx, tx, record, result)
	})
	return result, err
}

func (repository *Repository) ListAllocations(ctx context.Context, organizationID, sessionID identifier.ID, limit int32, after *channel.AllocationCursor) (channel.AllocationPage, error) {
	fetchLimit := limit + 1
	var rows []sqlc.SessionChannelAllocation
	var err error
	if after == nil {
		rows, err = repository.queries.ListSessionChannelAllocations(ctx, sqlc.ListSessionChannelAllocationsParams{OrganizationID: organizationID.UUID(), SessionID: sessionID.UUID(), Limit: fetchLimit})
	} else {
		rows, err = repository.queries.ListSessionChannelAllocationsAfter(ctx, sqlc.ListSessionChannelAllocationsAfterParams{OrganizationID: organizationID.UUID(), SessionID: sessionID.UUID(), CursorCreatedAt: after.CreatedAt.UTC(), CursorID: after.ID.UUID(), PageLimit: fetchLimit})
	}
	if err != nil {
		return channel.AllocationPage{}, fmt.Errorf("list session channel allocations: %w", err)
	}
	page := channel.AllocationPage{Items: make([]channel.Allocation, 0, len(rows))}
	for _, row := range rows {
		page.Items = append(page.Items, mapAllocation(row))
	}
	if len(page.Items) > int(limit) {
		page.Items = page.Items[:limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor = &channel.AllocationCursor{OrganizationID: organizationID, SessionID: sessionID, CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return page, nil
}

func (repository *Repository) recordMutation(ctx context.Context, tx pgx.Tx, record channel.CreateRecord, result channel.SalesChannel) error {
	platform := repository.platform.WithTx(tx)
	data, err := json.Marshal(map[string]any{"sales_channel_id": result.ID.String(), "key": result.Key, "type": string(result.Type), "status": string(result.Status)})
	if err != nil {
		return fmt.Errorf("marshal channel audit data: %w", err)
	}
	if err := platform.InsertAuditEntry(ctx, platformpostgres.AuditEntry{ID: record.AuditID, OrganizationID: result.OrganizationID, ActorType: record.ActorType, ActorID: record.ActorID, Action: "sales_channel.created", SubjectType: "sales_channel", SubjectID: result.ID.String(), Result: "success", AfterData: data, OccurredAt: result.CreatedAt}); err != nil {
		return err
	}
	return platform.InsertOutboxEvent(ctx, platformpostgres.OutboxEvent{ID: record.OutboxID, OrganizationID: result.OrganizationID, AggregateType: "sales_channel", AggregateID: result.ID, AggregateVersion: result.Version, EventType: "sales_channel.created", SchemaVersion: 1, Payload: data, AvailableAt: result.CreatedAt, OccurredAt: result.CreatedAt})
}

func (repository *Repository) recordAllocationMutation(ctx context.Context, tx pgx.Tx, record channel.CreateAllocationRecord, result channel.Allocation) error {
	platform := repository.platform.WithTx(tx)
	data, err := json.Marshal(map[string]any{"allocation_id": result.ID.String(), "session_id": result.SessionID.String(), "channel_id": result.ChannelID.String(), "scope_key": result.ScopeKey, "mode": string(result.Mode), "quantity": result.Quantity})
	if err != nil {
		return fmt.Errorf("marshal allocation audit data: %w", err)
	}
	if err := platform.InsertAuditEntry(ctx, platformpostgres.AuditEntry{ID: record.AuditID, OrganizationID: result.OrganizationID, ActorType: record.ActorType, ActorID: record.ActorID, Action: "session_channel_allocation.created", SubjectType: "session_channel_allocation", SubjectID: result.ID.String(), Result: "success", AfterData: data, OccurredAt: result.CreatedAt}); err != nil {
		return err
	}
	return platform.InsertOutboxEvent(ctx, platformpostgres.OutboxEvent{ID: record.OutboxID, OrganizationID: result.OrganizationID, AggregateType: "session_channel_allocation", AggregateID: result.ID, AggregateVersion: result.Version, EventType: "session_channel_allocation.created", SchemaVersion: 1, Payload: data, AvailableAt: result.CreatedAt, OccurredAt: result.CreatedAt})
}

func mapSalesChannel(row sqlc.SalesChannel) (channel.SalesChannel, error) {
	organizationID, _ := identifier.FromUUID(row.OrganizationID)
	channelID, _ := identifier.FromUUID(row.ID)
	configuration := map[string]any{}
	if err := json.Unmarshal(row.Configuration, &configuration); err != nil {
		return channel.SalesChannel{}, fmt.Errorf("decode channel configuration: %w", err)
	}
	return channel.SalesChannel{ID: channelID, OrganizationID: organizationID, Key: row.ChannelKey, DisplayName: row.DisplayName, Type: channel.Type(row.ChannelType), Configuration: configuration, Status: channel.Status(row.Status), Version: row.Version, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}, nil
}

func mapAllocation(row sqlc.SessionChannelAllocation) channel.Allocation {
	organizationID, _ := identifier.FromUUID(row.OrganizationID)
	sessionID, _ := identifier.FromUUID(row.SessionID)
	channelID, _ := identifier.FromUUID(row.ChannelID)
	allocationID, _ := identifier.FromUUID(row.ID)
	return channel.Allocation{ID: allocationID, OrganizationID: organizationID, SessionID: sessionID, ChannelID: channelID, ScopeKey: row.ScopeKey, Mode: channel.AllocationMode(row.AllocationMode), Quantity: row.Quantity, Version: row.Version, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}
