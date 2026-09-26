// Package postgres persists invitations and their acceptance membership in one transaction.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/HK9750/venueos/internal/access"
	"github.com/HK9750/venueos/internal/database"
	"github.com/HK9750/venueos/internal/invitation"
	"github.com/HK9750/venueos/internal/invitation/postgres/sqlc"
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

func (repository *Repository) Issue(ctx context.Context, record invitation.IssueRecord) (invitation.Invitation, error) {
	var result invitation.Invitation
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		organization, err := queries.LockOrganizationForInvitation(ctx, record.Invitation.OrganizationID.UUID())
		if errors.Is(err, pgx.ErrNoRows) {
			return invitation.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock organization for invitation: %w", err)
		}
		if organization.Status != "active" {
			return invitation.ErrOrganizationInactive
		}
		row, err := queries.InsertInvitation(ctx, sqlc.InsertInvitationParams{
			ID: record.Invitation.ID.UUID(), OrganizationID: record.Invitation.OrganizationID.UUID(),
			NormalizedEmail: record.Invitation.Email, Role: string(record.Invitation.Role), TokenHash: record.TokenHash[:],
			ExpiresAt: record.Invitation.ExpiresAt, CreatedByType: record.ActorType, Column8: record.ActorID,
			Version: record.Invitation.Version, CreatedAt: record.Invitation.CreatedAt,
		})
		if err != nil {
			var postgresError *pgconn.PgError
			if errors.As(err, &postgresError) && postgresError.Code == "23505" && postgresError.ConstraintName == "uq_invitations_active_email" {
				return invitation.ErrActiveExists
			}
			return fmt.Errorf("insert invitation: %w", err)
		}
		mapped, err := mapInvitation(row)
		if err != nil {
			return err
		}
		result = mapped
		return repository.recordMutation(ctx, tx, record.Invitation.OrganizationID, record.Invitation.ID,
			record.AuditID, record.OutboxID, record.ActorType, record.ActorID,
			"invitation.created", "invitation.created", 1,
			map[string]any{"invitation_id": record.Invitation.ID.String(), "email": record.Invitation.Email, "role": string(record.Invitation.Role), "expires_at": record.Invitation.ExpiresAt},
			record.Invitation.CreatedAt)
	})
	return result, err
}

func (repository *Repository) Accept(ctx context.Context, record invitation.AcceptRecord) (invitation.Invitation, error) {
	var result invitation.Invitation
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		current, err := queries.GetInvitationForUpdateByToken(ctx, record.TokenHash[:])
		if errors.Is(err, pgx.ErrNoRows) {
			return invitation.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("load invitation: %w", err)
		}
		switch current.Status {
		case string(invitation.StatusAccepted), string(invitation.StatusRevoked), string(invitation.StatusCancelled):
			return invitation.ErrAlreadyUsed
		case string(invitation.StatusExpired):
			return invitation.ErrExpired
		case string(invitation.StatusInvited):
		default:
			return invitation.ErrNotFound
		}
		organization, err := queries.LockOrganizationForInvitation(ctx, current.OrganizationID)
		if errors.Is(err, pgx.ErrNoRows) {
			return invitation.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock organization for invitation acceptance: %w", err)
		}
		if organization.Status != "active" {
			return invitation.ErrOrganizationInactive
		}
		if !record.OccurredAt.Before(current.ExpiresAt) {
			return invitation.ErrExpired
		}
		user, err := queries.GetUserForInvitation(ctx, record.UserID.UUID())
		if errors.Is(err, pgx.ErrNoRows) {
			return invitation.ErrEmailMismatch
		}
		if err != nil {
			return fmt.Errorf("load invitation user: %w", err)
		}
		if strings.ToLower(strings.TrimSpace(user.Email)) != current.NormalizedEmail {
			return invitation.ErrEmailMismatch
		}
		_, err = queries.GetMembershipForInvitation(ctx, sqlc.GetMembershipForInvitationParams{OrganizationID: current.OrganizationID, UserID: record.UserID.UUID()})
		if err == nil {
			return invitation.ErrAlreadyMember
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("check existing membership: %w", err)
		}
		membershipRow, err := queries.InsertMembershipFromInvitation(ctx, sqlc.InsertMembershipFromInvitationParams{
			ID: record.MembershipID.UUID(), OrganizationID: current.OrganizationID, UserID: record.UserID.UUID(), Role: current.Role, CreatedAt: record.OccurredAt,
		})
		if err != nil {
			var postgresError *pgconn.PgError
			if errors.As(err, &postgresError) && postgresError.Code == "23505" && postgresError.ConstraintName == "uq_memberships_organization_user" {
				return invitation.ErrAlreadyMember
			}
			return fmt.Errorf("create accepted membership: %w", err)
		}
		row, err := queries.AcceptInvitation(ctx, sqlc.AcceptInvitationParams{
			ID: current.ID, MembershipID: pgtype.UUID{Bytes: [16]byte(record.MembershipID.UUID()), Valid: true},
			AcceptedAt: pgtype.Timestamptz{Time: record.OccurredAt.UTC(), Valid: true}, Version: current.Version,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return invitation.ErrAlreadyUsed
		}
		if err != nil {
			return fmt.Errorf("accept invitation: %w", err)
		}
		result, err = mapInvitationAccept(row)
		if err != nil {
			return err
		}
		organizationID, err := identifier.FromUUID(current.OrganizationID)
		if err != nil {
			return fmt.Errorf("map invitation organization ID: %w", err)
		}
		invitationID, err := identifier.FromUUID(current.ID)
		if err != nil {
			return fmt.Errorf("map invitation ID: %w", err)
		}
		if err := repository.recordMutation(ctx, tx, organizationID, invitationID,
			record.InvitationAuditID, record.InvitationOutboxID, record.ActorType, record.ActorID,
			"invitation.accepted", "invitation.accepted", result.Version,
			map[string]any{"invitation_id": result.ID.String(), "membership_id": record.MembershipID.String(), "status": string(result.Status), "version": result.Version}, record.OccurredAt); err != nil {
			return err
		}
		return repository.recordMutation(ctx, tx, organizationID, record.MembershipID,
			record.MembershipAuditID, record.MembershipOutboxID, record.ActorType, record.ActorID,
			"membership.created", "membership.created", membershipRow.Version,
			map[string]any{"membership_id": record.MembershipID.String(), "user_id": record.UserID.String(), "role": current.Role, "version": membershipRow.Version}, record.OccurredAt)
	})
	return result, err
}

func (repository *Repository) Revoke(ctx context.Context, record invitation.RevokeRecord) (invitation.Invitation, error) {
	var result invitation.Invitation
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		organization, err := queries.LockOrganizationForInvitation(ctx, record.OrganizationID.UUID())
		if errors.Is(err, pgx.ErrNoRows) {
			return invitation.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock organization for invitation revocation: %w", err)
		}
		if organization.Status != "active" {
			return invitation.ErrOrganizationInactive
		}
		current, err := queries.GetInvitationForUpdate(ctx, sqlc.GetInvitationForUpdateParams{OrganizationID: record.OrganizationID.UUID(), ID: record.InvitationID.UUID()})
		if errors.Is(err, pgx.ErrNoRows) {
			return invitation.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("load invitation for revocation: %w", err)
		}
		if current.Status != string(invitation.StatusInvited) || current.Version != record.Version {
			return invitation.ErrAlreadyUsed
		}
		row, err := queries.RevokeInvitation(ctx, sqlc.RevokeInvitationParams{OrganizationID: record.OrganizationID.UUID(), ID: record.InvitationID.UUID(), Version: record.Version, RevokedAt: pgtype.Timestamptz{Time: record.OccurredAt.UTC(), Valid: true}})
		if errors.Is(err, pgx.ErrNoRows) {
			return invitation.ErrAlreadyUsed
		}
		if err != nil {
			return fmt.Errorf("revoke invitation: %w", err)
		}
		result, err = mapInvitationRevoke(row)
		if err != nil {
			return err
		}
		return repository.recordMutation(ctx, tx, record.OrganizationID, record.InvitationID, record.AuditID, record.OutboxID,
			record.ActorType, record.ActorID, "invitation.revoked", "invitation.revoked", result.Version,
			map[string]any{"invitation_id": result.ID.String(), "status": string(result.Status), "version": result.Version}, record.OccurredAt)
	})
	return result, err
}

func (repository *Repository) ExpireDue(ctx context.Context, now time.Time, limit int32) (int, error) {
	count := 0
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		due, err := queries.ListDueInvitations(ctx, sqlc.ListDueInvitationsParams{ExpiresAt: now.UTC(), Limit: limit})
		if err != nil {
			return fmt.Errorf("list due invitations: %w", err)
		}
		for _, item := range due {
			row, err := queries.ExpireInvitation(ctx, sqlc.ExpireInvitationParams{ID: item.ID, UpdatedAt: now.UTC(), Version: item.Version})
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				return fmt.Errorf("expire invitation: %w", err)
			}
			value, err := mapInvitationExpire(row)
			if err != nil {
				return err
			}
			auditID, err := identifier.New()
			if err != nil {
				return err
			}
			outboxID, err := identifier.New()
			if err != nil {
				return err
			}
			if err := repository.recordMutation(ctx, tx, value.OrganizationID, value.ID, auditID, outboxID, "system", "", "invitation.expired", "invitation.expired", value.Version,
				map[string]any{"invitation_id": value.ID.String(), "status": string(value.Status), "version": value.Version}, now.UTC()); err != nil {
				return err
			}
			count++
		}
		return nil
	})
	return count, err
}

func (repository *Repository) recordMutation(ctx context.Context, tx pgx.Tx, organizationID, aggregateID, auditID, outboxID identifier.ID, actorType, actorID, action, eventType string, version int64, after map[string]any, occurredAt time.Time) error {
	platform := repository.platform.WithTx(tx)
	afterData, err := json.Marshal(after)
	if err != nil {
		return fmt.Errorf("marshal invitation audit data: %w", err)
	}
	aggregateType := strings.SplitN(action, ".", 2)[0]
	if err := platform.InsertAuditEntry(ctx, platformpostgres.AuditEntry{ID: auditID, OrganizationID: organizationID, ActorType: actorType, ActorID: actorID,
		Action: action, SubjectType: aggregateType, SubjectID: aggregateID.String(), Result: "success", AfterData: afterData, OccurredAt: occurredAt}); err != nil {
		return err
	}
	payload, err := json.Marshal(after)
	if err != nil {
		return fmt.Errorf("marshal invitation event: %w", err)
	}
	return platform.InsertOutboxEvent(ctx, platformpostgres.OutboxEvent{ID: outboxID, OrganizationID: organizationID, AggregateType: aggregateType, AggregateID: aggregateID,
		AggregateVersion: version, EventType: eventType, SchemaVersion: 1, Payload: payload, AvailableAt: occurredAt, OccurredAt: occurredAt})
}

func mapInvitation(row sqlc.InsertInvitationRow) (invitation.Invitation, error) {
	return mapInvitationFields(row.ID, row.OrganizationID, row.NormalizedEmail, row.Role, row.Status, row.ExpiresAt, row.CreatedByType, row.CreatedByID, row.MembershipID, row.AcceptedAt, row.RevokedAt, row.Version, row.CreatedAt, row.UpdatedAt)
}

func mapInvitationAccept(row sqlc.AcceptInvitationRow) (invitation.Invitation, error) {
	return mapInvitationFields(row.ID, row.OrganizationID, row.NormalizedEmail, row.Role, row.Status, row.ExpiresAt, row.CreatedByType, row.CreatedByID, row.MembershipID, row.AcceptedAt, row.RevokedAt, row.Version, row.CreatedAt, row.UpdatedAt)
}

func mapInvitationRevoke(row sqlc.RevokeInvitationRow) (invitation.Invitation, error) {
	return mapInvitationFields(row.ID, row.OrganizationID, row.NormalizedEmail, row.Role, row.Status, row.ExpiresAt, row.CreatedByType, row.CreatedByID, row.MembershipID, row.AcceptedAt, row.RevokedAt, row.Version, row.CreatedAt, row.UpdatedAt)
}

func mapInvitationExpire(row sqlc.ExpireInvitationRow) (invitation.Invitation, error) {
	return mapInvitationFields(row.ID, row.OrganizationID, row.NormalizedEmail, row.Role, row.Status, row.ExpiresAt, row.CreatedByType, row.CreatedByID, row.MembershipID, row.AcceptedAt, row.RevokedAt, row.Version, row.CreatedAt, row.UpdatedAt)
}

func mapInvitationFields(id, organizationID uuid.UUID, email, role, status string, expiresAt time.Time, createdByType string, createdByID pgtype.Text, membershipID pgtype.UUID, acceptedAt, revokedAt pgtype.Timestamptz, version int64, createdAt, updatedAt time.Time) (invitation.Invitation, error) {
	invitationID, err := identifier.FromUUID(id)
	if err != nil {
		return invitation.Invitation{}, fmt.Errorf("map invitation ID: %w", err)
	}
	organizationIDValue, err := identifier.FromUUID(organizationID)
	if err != nil {
		return invitation.Invitation{}, fmt.Errorf("map invitation organization ID: %w", err)
	}
	parsedRole := accessRole(role)
	if !parsedRole.Valid() {
		return invitation.Invitation{}, fmt.Errorf("unknown persisted invitation role %q", role)
	}
	value := invitation.Invitation{ID: invitationID, OrganizationID: organizationIDValue, Email: email, Role: parsedRole, Status: invitation.Status(status),
		ExpiresAt: expiresAt, CreatedByType: createdByType, Version: version, CreatedAt: createdAt, UpdatedAt: updatedAt}
	if createdByID.Valid {
		value.CreatedByID = createdByID.String
	}
	if membershipID.Valid {
		parsed, parseErr := identifier.FromUUID(membershipID.Bytes)
		if parseErr != nil {
			return invitation.Invitation{}, fmt.Errorf("map invitation membership ID: %w", parseErr)
		}
		value.MembershipID = &parsed
	}
	if acceptedAt.Valid {
		value.AcceptedAt = &acceptedAt.Time
	}
	if revokedAt.Valid {
		value.RevokedAt = &revokedAt.Time
	}
	return value, nil
}

func accessRole(value string) access.Role { return access.Role(value) }
