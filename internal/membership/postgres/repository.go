// Package postgres persists tenant membership state and its authorization audit.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/HK9750/venueos/internal/access"
	"github.com/HK9750/venueos/internal/database"
	"github.com/HK9750/venueos/internal/membership"
	"github.com/HK9750/venueos/internal/membership/postgres/sqlc"
	"github.com/HK9750/venueos/internal/platform/identifier"
	platformpostgres "github.com/HK9750/venueos/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
)

type Repository struct {
	queries      *sqlc.Queries
	platform     *platformpostgres.Store
	transactions *database.TransactionRunner
}

func New(pool sqlc.DBTX, transactions *database.TransactionRunner) *Repository {
	return &Repository{queries: sqlc.New(pool), platform: platformpostgres.NewStore(pool), transactions: transactions}
}

func (repository *Repository) List(ctx context.Context, organizationID identifier.ID, limit int32, after *membership.Cursor) (membership.Page, error) {
	fetchLimit := limit + 1
	var rows []sqlc.Membership
	var err error
	if after == nil {
		rows, err = repository.queries.ListMemberships(ctx, sqlc.ListMembershipsParams{
			OrganizationID: organizationID.UUID(), Limit: fetchLimit,
		})
	} else {
		rows, err = repository.queries.ListMembershipsAfter(ctx, sqlc.ListMembershipsAfterParams{
			OrganizationID: organizationID.UUID(), CursorCreatedAt: after.CreatedAt.UTC(),
			CursorID: after.ID.UUID(), PageLimit: fetchLimit,
		})
	}
	if err != nil {
		return membership.Page{}, fmt.Errorf("list memberships: %w", err)
	}
	page := membership.Page{Items: make([]membership.Membership, 0, len(rows))}
	for _, row := range rows {
		mapped, mapErr := mapMembership(row)
		if mapErr != nil {
			return membership.Page{}, mapErr
		}
		page.Items = append(page.Items, mapped)
	}
	if len(page.Items) > int(limit) {
		page.Items = page.Items[:limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor = &membership.Cursor{OrganizationID: organizationID, CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return page, nil
}

func (repository *Repository) ChangeRole(ctx context.Context, record membership.ChangeRoleRecord) (membership.Membership, error) {
	var result membership.Membership
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		if _, err := queries.LockOrganization(ctx, record.OrganizationID.UUID()); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return membership.ErrNotFound
			}
			return fmt.Errorf("lock organization for membership role change: %w", err)
		}
		current, err := queries.GetMembershipForUpdate(ctx, sqlc.GetMembershipForUpdateParams{
			OrganizationID: record.OrganizationID.UUID(), ID: record.MembershipID.UUID(),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return membership.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("load membership for role change: %w", err)
		}
		if current.Status != string(membership.StatusActive) || current.Version != record.Version {
			return membership.ErrVersionConflict
		}
		if current.Role == string(record.Role) {
			result, err = mapMembership(current)
			return err
		}
		if current.Role == "owner" && record.Role != "owner" {
			owners, countErr := queries.CountActiveOwners(ctx, record.OrganizationID.UUID())
			if countErr != nil {
				return fmt.Errorf("count active owners: %w", countErr)
			}
			if owners <= 1 {
				return membership.ErrLastOwner
			}
		}
		updated, err := queries.UpdateMembershipRole(ctx, sqlc.UpdateMembershipRoleParams{
			OrganizationID: record.OrganizationID.UUID(), ID: record.MembershipID.UUID(),
			Version: record.Version, Role: string(record.Role), UpdatedAt: record.OccurredAt.UTC(),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return membership.ErrVersionConflict
		}
		if err != nil {
			return fmt.Errorf("update membership role: %w", err)
		}
		mapped, err := mapMembership(updated)
		if err != nil {
			return err
		}
		result = mapped
		return repository.recordMutation(ctx, tx, record.OrganizationID, record.MembershipID,
			record.AuditID, record.OutboxID, record.ActorType, record.ActorID,
			"membership.role_changed", "membership.role_changed", map[string]any{
				"membership_id": record.MembershipID.String(), "user_id": mapped.UserID.String(),
				"role": string(mapped.Role), "version": mapped.Version,
			}, record.OccurredAt)
	})
	return result, err
}

func (repository *Repository) Revoke(ctx context.Context, record membership.RevokeRecord) (membership.Membership, error) {
	var result membership.Membership
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		if _, err := queries.LockOrganization(ctx, record.OrganizationID.UUID()); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return membership.ErrNotFound
			}
			return fmt.Errorf("lock organization for membership revocation: %w", err)
		}
		current, err := queries.GetMembershipForUpdate(ctx, sqlc.GetMembershipForUpdateParams{
			OrganizationID: record.OrganizationID.UUID(), ID: record.MembershipID.UUID(),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return membership.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("load membership for revocation: %w", err)
		}
		if current.Status == string(membership.StatusRevoked) {
			result, err = mapMembership(current)
			return err
		}
		if current.Version != record.Version {
			return membership.ErrVersionConflict
		}
		if current.Role == "owner" {
			owners, countErr := queries.CountActiveOwners(ctx, record.OrganizationID.UUID())
			if countErr != nil {
				return fmt.Errorf("count active owners: %w", countErr)
			}
			if owners <= 1 {
				return membership.ErrLastOwner
			}
		}
		updated, err := queries.RevokeMembership(ctx, sqlc.RevokeMembershipParams{
			OrganizationID: record.OrganizationID.UUID(), ID: record.MembershipID.UUID(),
			Version: record.Version, UpdatedAt: record.OccurredAt.UTC(),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return membership.ErrVersionConflict
		}
		if err != nil {
			return fmt.Errorf("revoke membership: %w", err)
		}
		mapped, err := mapMembership(updated)
		if err != nil {
			return err
		}
		result = mapped
		return repository.recordMutation(ctx, tx, record.OrganizationID, record.MembershipID,
			record.AuditID, record.OutboxID, record.ActorType, record.ActorID,
			"membership.revoked", "membership.revoked", map[string]any{
				"membership_id": record.MembershipID.String(), "user_id": mapped.UserID.String(),
				"status": string(mapped.Status), "version": mapped.Version,
			}, record.OccurredAt)
	})
	return result, err
}

func (repository *Repository) recordMutation(ctx context.Context, tx pgx.Tx, organizationID, membershipID, auditID, outboxID identifier.ID, actorType, actorID, action, eventType string, after map[string]any, occurredAt time.Time) error {
	platform := repository.platform.WithTx(tx)
	afterData, err := json.Marshal(after)
	if err != nil {
		return fmt.Errorf("marshal membership audit data: %w", err)
	}
	if err := platform.InsertAuditEntry(ctx, platformpostgres.AuditEntry{
		ID: auditID, OrganizationID: organizationID, ActorType: actorType, ActorID: actorID,
		Action: action, SubjectType: "membership", SubjectID: membershipID.String(),
		Result: "success", AfterData: afterData, OccurredAt: occurredAt,
	}); err != nil {
		return err
	}
	payload, err := json.Marshal(after)
	if err != nil {
		return fmt.Errorf("marshal membership event: %w", err)
	}
	return platform.InsertOutboxEvent(ctx, platformpostgres.OutboxEvent{
		ID: outboxID, OrganizationID: organizationID, AggregateType: "membership",
		AggregateID: membershipID, AggregateVersion: after["version"].(int64),
		EventType: eventType, SchemaVersion: 1, Payload: payload,
		AvailableAt: occurredAt, OccurredAt: occurredAt,
	})
}

func mapMembership(row sqlc.Membership) (membership.Membership, error) {
	id, err := identifier.FromUUID(row.ID)
	if err != nil {
		return membership.Membership{}, fmt.Errorf("map membership ID: %w", err)
	}
	organizationID, err := identifier.FromUUID(row.OrganizationID)
	if err != nil {
		return membership.Membership{}, fmt.Errorf("map membership organization ID: %w", err)
	}
	userID, err := identifier.FromUUID(row.UserID)
	if err != nil {
		return membership.Membership{}, fmt.Errorf("map membership user ID: %w", err)
	}
	role := accessRole(row.Role)
	if !role.Valid() {
		return membership.Membership{}, fmt.Errorf("unknown persisted membership role %q", row.Role)
	}
	value := membership.Membership{
		ID: id, OrganizationID: organizationID, UserID: userID, Role: role,
		Status: membership.Status(row.Status), VenueScope: make([]identifier.ID, 0, len(row.VenueScope)),
		Version: row.Version, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
	for _, rawID := range row.VenueScope {
		venueID, parseErr := identifier.FromUUID(rawID)
		if parseErr != nil {
			return membership.Membership{}, fmt.Errorf("map membership venue scope: %w", parseErr)
		}
		value.VenueScope = append(value.VenueScope, venueID)
	}
	if row.RevokedAt.Valid {
		revokedAt := row.RevokedAt.Time
		value.RevokedAt = &revokedAt
	}
	return value, nil
}

func accessRole(value string) access.Role { return access.Role(value) }
