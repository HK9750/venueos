package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"time"

	"github.com/HK9750/venueos/internal/platform/identifier"
	platformpostgres "github.com/HK9750/venueos/internal/platform/postgres"
	"github.com/HK9750/venueos/internal/ticket"
	"github.com/HK9750/venueos/internal/ticket/postgres/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func (repository *Repository) Issue(ctx context.Context, record ticket.IssueRecord) (ticket.Ticket, bool, error) {
	if repository == nil || repository.transactions == nil || repository.platform == nil {
		return ticket.Ticket{}, false, errors.New("ticket issuance repository is not configured")
	}
	if err := record.Ticket.Validate(); err != nil {
		return ticket.Ticket{}, false, err
	}
	if record.AuditID.IsZero() || record.OutboxID.IsZero() || record.OccurredAt.IsZero() || record.ActorType != "system" || record.ActorID != "" {
		return ticket.Ticket{}, false, fmt.Errorf("%w: issuance mutation metadata is invalid", ticket.ErrInvalidTicket)
	}
	var result ticket.Ticket
	var replay bool
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		entitlement, err := queries.LockEntitlementForIssue(ctx, sqlc.LockEntitlementForIssueParams{OrganizationID: record.Ticket.OrganizationID.UUID(), EntitlementID: record.Ticket.EntitlementID.UUID()})
		if errors.Is(err, pgx.ErrNoRows) {
			return ticket.ErrEntitlementNotFound
		}
		if err != nil {
			return fmt.Errorf("lock ticket entitlement: %w", err)
		}
		if entitlement.OrderLineID != record.Ticket.OrderLineID.UUID() || entitlement.SessionID != record.Ticket.SessionID.UUID() {
			return ticket.ErrEntitlementNotFound
		}
		if entitlement.State != "pending" {
			existing, getErr := queries.GetTicketByEntitlement(ctx, sqlc.GetTicketByEntitlementParams{OrganizationID: record.Ticket.OrganizationID.UUID(), EntitlementID: record.Ticket.EntitlementID.UUID()})
			if errors.Is(getErr, pgx.ErrNoRows) {
				return ticket.ErrEntitlementNotIssuable
			}
			if getErr != nil {
				return fmt.Errorf("get issued ticket replay: %w", getErr)
			}
			result, getErr = mapTicket(existing)
			if getErr != nil {
				return getErr
			}
			replay = true
			return nil
		}
		row, err := queries.InsertTicket(ctx, sqlc.InsertTicketParams{
			ID: record.Ticket.ID.UUID(), OrganizationID: record.Ticket.OrganizationID.UUID(), EntitlementID: record.Ticket.EntitlementID.UUID(),
			OrderLineID: record.Ticket.OrderLineID.UUID(), SessionID: record.Ticket.SessionID.UUID(), PublicReference: record.Ticket.PublicReference,
			State: string(record.Ticket.Status), Version: record.Ticket.Version, SigningKeyID: record.Ticket.SigningKeyID,
			NotBefore: record.Ticket.NotBefore.UTC(), ExpiresAt: record.Ticket.ExpiresAt.UTC(), EntryOpensAt: record.Ticket.EntryOpensAt.UTC(),
			EntryClosesAt: record.Ticket.EntryClosesAt.UTC(), IssuedAt: record.Ticket.IssuedAt.UTC(), CreatedAt: record.Ticket.CreatedAt.UTC(), UpdatedAt: record.Ticket.UpdatedAt.UTC(),
		})
		if err != nil {
			var postgresError *pgconn.PgError
			if errors.As(err, &postgresError) && postgresError.Code == "23505" && (postgresError.ConstraintName == "uq_tickets_entitlement" || postgresError.ConstraintName == "uq_tickets_public_reference") {
				if postgresError.ConstraintName == "uq_tickets_public_reference" {
					return ticket.ErrPublicReferenceConflict
				}
				existing, getErr := queries.GetTicketByEntitlement(ctx, sqlc.GetTicketByEntitlementParams{OrganizationID: record.Ticket.OrganizationID.UUID(), EntitlementID: record.Ticket.EntitlementID.UUID()})
				if getErr != nil {
					return fmt.Errorf("get concurrent ticket replay: %w", getErr)
				}
				result, getErr = mapTicket(existing)
				if getErr != nil {
					return getErr
				}
				replay = true
				return nil
			}
			return fmt.Errorf("insert ticket: %w", err)
		}
		marked, err := queries.MarkEntitlementIssued(ctx, sqlc.MarkEntitlementIssuedParams{OrganizationID: record.Ticket.OrganizationID.UUID(), EntitlementID: record.Ticket.EntitlementID.UUID(), UpdatedAt: record.OccurredAt.UTC()})
		if errors.Is(err, pgx.ErrNoRows) {
			return ticket.ErrEntitlementNotIssuable
		}
		if err != nil {
			return fmt.Errorf("mark entitlement issued: %w", err)
		}
		if marked.ID != record.Ticket.EntitlementID.UUID() {
			return fmt.Errorf("mark entitlement issued returned a different entitlement")
		}
		result, err = mapTicket(row)
		if err != nil {
			return err
		}
		after, marshalErr := json.Marshal(map[string]any{"ticket_id": result.ID.String(), "entitlement_id": result.EntitlementID.String(), "session_id": result.SessionID.String(), "version": result.Version, "signing_key_id": result.SigningKeyID, "status": string(result.Status)})
		if marshalErr != nil {
			return fmt.Errorf("marshal ticket issuance audit data: %w", marshalErr)
		}
		platform := repository.platform.WithTx(tx)
		if err := platform.InsertAuditEntry(ctx, platformpostgres.AuditEntry{ID: record.AuditID, OrganizationID: result.OrganizationID, ActorType: record.ActorType, SubjectType: "ticket", SubjectID: result.ID.String(), Action: "ticket.issued", Result: "success", AfterData: after, OccurredAt: record.OccurredAt.UTC()}); err != nil {
			return err
		}
		if err := platform.InsertOutboxEvent(ctx, platformpostgres.OutboxEvent{ID: record.OutboxID, OrganizationID: result.OrganizationID, AggregateType: "ticket", AggregateID: result.ID, AggregateVersion: result.Version, EventType: "ticket.issued", SchemaVersion: 1, Payload: after, AvailableAt: record.OccurredAt.UTC(), OccurredAt: record.OccurredAt.UTC()}); err != nil {
			return err
		}
		return nil
	})
	return result, replay, err
}

func mapTicket(row sqlc.Ticket) (ticket.Ticket, error) {
	organizationID, err := identifier.FromUUID(row.OrganizationID)
	if err != nil {
		return ticket.Ticket{}, fmt.Errorf("map ticket organization: %w", err)
	}
	ticketID, err := identifier.FromUUID(row.ID)
	if err != nil {
		return ticket.Ticket{}, fmt.Errorf("map ticket ID: %w", err)
	}
	entitlementID, err := identifier.FromUUID(row.EntitlementID)
	if err != nil {
		return ticket.Ticket{}, fmt.Errorf("map ticket entitlement: %w", err)
	}
	orderLineID, err := identifier.FromUUID(row.OrderLineID)
	if err != nil {
		return ticket.Ticket{}, fmt.Errorf("map ticket order line: %w", err)
	}
	sessionID, err := identifier.FromUUID(row.SessionID)
	if err != nil {
		return ticket.Ticket{}, fmt.Errorf("map ticket session: %w", err)
	}
	value := ticket.Ticket{ID: ticketID, OrganizationID: organizationID, EntitlementID: entitlementID, OrderLineID: orderLineID, SessionID: sessionID, PublicReference: row.PublicReference, Status: ticket.Status(row.State), Version: row.Version, SigningKeyID: row.SigningKeyID, NotBefore: row.NotBefore.UTC(), ExpiresAt: row.ExpiresAt.UTC(), EntryOpensAt: row.EntryOpensAt.UTC(), EntryClosesAt: row.EntryClosesAt.UTC(), IssuedAt: row.IssuedAt.UTC(), CreatedAt: row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC()}
	if row.AdmittedAt.Valid {
		value.AdmittedAt = timePtr(row.AdmittedAt.Time)
	}
	if row.VoidAt.Valid {
		value.VoidAt = timePtr(row.VoidAt.Time)
	}
	if row.RefundedAt.Valid {
		value.RefundedAt = timePtr(row.RefundedAt.Time)
	}
	if row.ExpiredAt.Valid {
		value.ExpiredAt = timePtr(row.ExpiredAt.Time)
	}
	if err := value.Validate(); err != nil {
		return ticket.Ticket{}, err
	}
	return value, nil
}

func timePtr(value time.Time) *time.Time {
	utc := value.UTC()
	return &utc
}

var _ ticket.IssuanceRepository = (*Repository)(nil)
