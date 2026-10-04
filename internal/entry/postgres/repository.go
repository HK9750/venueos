// Package postgres implements the authoritative online scan transaction.
package postgres

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/HK9750/venueos/internal/database"
	"github.com/HK9750/venueos/internal/entry"
	"github.com/HK9750/venueos/internal/entry/postgres/sqlc"
	"github.com/HK9750/venueos/internal/platform/identifier"
	platformpostgres "github.com/HK9750/venueos/internal/platform/postgres"
	"github.com/HK9750/venueos/internal/ticket"
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

func (repository *Repository) RecordOnlineScan(ctx context.Context, input entry.OnlineScanInput) (entry.ScanResult, error) {
	if err := input.Validate(); err != nil {
		return entry.ScanResult{}, err
	}
	var result entry.ScanResult
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		device, err := queries.LockDeviceForScan(ctx, sqlc.LockDeviceForScanParams{OrganizationID: input.OrganizationID.UUID(), ID: input.DeviceID.UUID()})
		if errors.Is(err, pgx.ErrNoRows) {
			return entry.ErrDeviceNotFound
		}
		if err != nil {
			return fmt.Errorf("lock entry device: %w", err)
		}
		if device.State != "active" {
			return entry.ErrDeviceInactive
		}
		databaseNow, err := queries.GetDatabaseTime(ctx)
		if err != nil {
			return fmt.Errorf("get database time for scan: %w", err)
		}
		authorized, err := queries.IsDeviceAuthorizedForScan(ctx, sqlc.IsDeviceAuthorizedForScanParams{
			OrganizationID: input.OrganizationID.UUID(), DeviceID: input.DeviceID.UUID(), SessionID: pgtype.UUID{Bytes: input.Claims.SessionID.UUID(), Valid: true}, GateID: nullableUUID(input.GateID), AtTime: databaseNow.UTC(),
		})
		if err != nil {
			return fmt.Errorf("check device scan assignment: %w", err)
		}
		if !authorized {
			return entry.ErrDeviceNotAuthorized
		}
		ticketRow, err := queries.LockTicketForScan(ctx, sqlc.LockTicketForScanParams{OrganizationID: input.OrganizationID.UUID(), ID: input.Claims.TicketID.UUID()})
		if errors.Is(err, pgx.ErrNoRows) {
			return entry.ErrScanNotFound
		}
		if err != nil {
			return fmt.Errorf("lock ticket for scan: %w", err)
		}
		organizationID, err := identifier.FromUUID(ticketRow.OrganizationID)
		if err != nil {
			return fmt.Errorf("map scan organization: %w", err)
		}
		ticketID, err := identifier.FromUUID(ticketRow.ID)
		if err != nil {
			return fmt.Errorf("map scan ticket: %w", err)
		}
		sessionID, err := identifier.FromUUID(ticketRow.SessionID)
		if err != nil {
			return fmt.Errorf("map scan session: %w", err)
		}
		decision := entry.Decide(input.Claims, entry.Snapshot{OrganizationID: organizationID, TicketID: ticketID, SessionID: sessionID, Version: ticketRow.Version, Status: entry.TicketStatus(ticketRow.State), EntryOpensAt: ticketRow.EntryOpensAt.UTC(), EntryClosesAt: ticketRow.EntryClosesAt.UTC()}, databaseNow.UTC())
		metadata := input.Metadata
		if len(metadata) == 0 {
			metadata = []byte(`{}`)
		}
		attemptID, err := identifier.New()
		if err != nil {
			return fmt.Errorf("generate scan attempt ID: %w", err)
		}
		_, err = queries.InsertScanAttempt(ctx, sqlc.InsertScanAttemptParams{ID: attemptID.UUID(), OrganizationID: input.OrganizationID.UUID(), SessionID: sessionID.UUID(), TicketID: ticketID.UUID(), DeviceID: input.DeviceID.UUID(), DeviceScanID: input.DeviceScanID, TicketVersion: input.Claims.TicketVersion, Result: string(decision.Code), CredentialSha256: input.CredentialSHA256[:], GateID: nullableUUID(input.GateID), ScannedAt: input.ScannedAt.UTC(), Metadata: metadata})
		if err != nil {
			var postgresError *pgconn.PgError
			if errors.As(err, &postgresError) && postgresError.Code == "23505" && postgresError.ConstraintName == "uq_scan_attempts_device_key" {
				existing, getErr := queries.GetScanAttemptByDeviceKey(ctx, sqlc.GetScanAttemptByDeviceKeyParams{OrganizationID: input.OrganizationID.UUID(), DeviceID: input.DeviceID.UUID(), DeviceScanID: input.DeviceScanID})
				if getErr != nil {
					return fmt.Errorf("get duplicate scan attempt: %w", getErr)
				}
				if len(existing.CredentialSha256) != sha256.Size || !equalHash(existing.CredentialSha256, input.CredentialSHA256) {
					return entry.ErrScanConflict
				}
				existingID, mapErr := identifier.FromUUID(existing.ID)
				if mapErr != nil {
					return fmt.Errorf("map duplicate scan attempt: %w", mapErr)
				}
				result = entry.ScanResult{ScanAttemptID: existingID, Decision: entry.Decision{Code: entry.ResultCode(existing.Result), Admittable: existing.Result == string(entry.ResultAdmitted), TicketID: ticketID}, Replay: true}
				return nil
			}
			return fmt.Errorf("insert scan attempt: %w", err)
		}
		var admissionID *identifier.ID
		if decision.Admittable {
			updated, updateErr := queries.MarkTicketAdmitted(ctx, sqlc.MarkTicketAdmittedParams{OrganizationID: input.OrganizationID.UUID(), ID: ticketID.UUID(), AdmittedAt: pgtype.Timestamptz{Time: databaseNow.UTC(), Valid: true}, Version: ticketRow.Version})
			if errors.Is(updateErr, pgx.ErrNoRows) {
				return entry.ErrScanConflict
			}
			if updateErr != nil {
				return fmt.Errorf("mark ticket admitted: %w", updateErr)
			}
			admissionValue, newErr := identifier.New()
			if newErr != nil {
				return fmt.Errorf("generate admission ID: %w", newErr)
			}
			admissionID = &admissionValue
			if err := queries.InsertAdmission(ctx, sqlc.InsertAdmissionParams{ID: admissionValue.UUID(), OrganizationID: input.OrganizationID.UUID(), TicketID: ticketID.UUID(), ScanAttemptID: attemptID.UUID(), SessionID: sessionID.UUID(), DeviceID: input.DeviceID.UUID(), GateID: nullableUUID(input.GateID), AdmittedAt: databaseNow.UTC()}); err != nil {
				return fmt.Errorf("insert admission: %w", err)
			}
			if err := recordTicketMutation(ctx, repository.platform.WithTx(tx), input, ticketID, updated.Version, databaseNow.UTC()); err != nil {
				return err
			}
		}
		if err := recordScanMutation(ctx, repository.platform.WithTx(tx), input, attemptID, ticketID, sessionID, decision, databaseNow.UTC()); err != nil {
			return err
		}
		result = entry.ScanResult{ScanAttemptID: attemptID, AdmissionID: admissionID, Decision: decision}
		return nil
	})
	return result, err
}

func (repository *Repository) ListEntrySummary(ctx context.Context, input entry.SummaryInput) (entry.Summary, error) {
	if input.OrganizationID.IsZero() || input.SessionID.IsZero() || input.From.IsZero() || input.Until.IsZero() || !input.Until.After(input.From) {
		return entry.Summary{}, entry.ErrInvalidSummary
	}
	meta, err := repository.queries.GetEntrySummaryMeta(ctx, sqlc.GetEntrySummaryMetaParams{
		OrganizationID: input.OrganizationID.UUID(), SessionID: input.SessionID.UUID(), FromTime: input.From.UTC(), UntilTime: input.Until.UTC(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return entry.Summary{}, entry.ErrSessionNotFound
	}
	if err != nil {
		return entry.Summary{}, fmt.Errorf("get entry summary metadata: %w", err)
	}
	rows, err := repository.queries.ListEntrySummaryRows(ctx, sqlc.ListEntrySummaryRowsParams{
		OrganizationID: input.OrganizationID.UUID(), SessionID: input.SessionID.UUID(), FromTime: input.From.UTC(), UntilTime: input.Until.UTC(),
	})
	if err != nil {
		return entry.Summary{}, fmt.Errorf("list entry summary rows: %w", err)
	}

	resultCounts := make(map[string]int64)
	gateCounts := make(map[string]entry.GateCount)
	minuteCounts := make(map[time.Time]int64)
	ticketTypeCounts := make(map[identifier.ID]int64)
	for _, row := range rows {
		resultCounts[row.Result] += row.ScanCount
		minute := row.Minute.UTC()
		minuteCounts[minute] += row.ScanCount
		ticketTypeID, mapErr := identifier.FromUUID(row.PriceTierID)
		if mapErr != nil {
			return entry.Summary{}, fmt.Errorf("map entry summary ticket type: %w", mapErr)
		}
		ticketTypeCounts[ticketTypeID] += row.ScanCount
		gateKey := ""
		var gateID *identifier.ID
		if row.GateID.Valid {
			mapped, mapErr := identifier.FromUUID(row.GateID.Bytes)
			if mapErr != nil {
				return entry.Summary{}, fmt.Errorf("map entry summary gate: %w", mapErr)
			}
			gateID = &mapped
			gateKey = mapped.String()
		}
		gate := gateCounts[gateKey]
		if gate.GateID == nil && gateID != nil {
			gate.GateID = gateID
		}
		gate.Count += row.ScanCount
		gateCounts[gateKey] = gate
	}

	value := entry.Summary{OrganizationID: input.OrganizationID, SessionID: input.SessionID, GeneratedAt: meta.GeneratedAt.UTC(), TotalScans: meta.ScanCount,
		ByResult: make([]entry.ResultCount, 0, len(resultCounts)), ByGate: make([]entry.GateCount, 0, len(gateCounts)), ByMinute: make([]entry.MinuteCount, 0, len(minuteCounts)), ByTicketType: make([]entry.TicketTypeCount, 0, len(ticketTypeCounts))}
	if meta.ScanCount > 0 {
		latest, latestErr := repository.queries.GetLatestEntryReceivedAt(ctx, sqlc.GetLatestEntryReceivedAtParams{OrganizationID: input.OrganizationID.UUID(), SessionID: input.SessionID.UUID(), FromTime: input.From.UTC(), UntilTime: input.Until.UTC()})
		if errors.Is(latestErr, pgx.ErrNoRows) {
			return entry.Summary{}, fmt.Errorf("get latest entry receipt: %w", latestErr)
		}
		if latestErr != nil {
			return entry.Summary{}, fmt.Errorf("get latest entry receipt: %w", latestErr)
		}
		latest = latest.UTC()
		value.DataAsOf = &latest
		value.DataDelay = value.GeneratedAt.Sub(latest)
		if value.DataDelay < 0 {
			value.DataDelay = 0
		}
	}
	for result, count := range resultCounts {
		value.ByResult = append(value.ByResult, entry.ResultCount{Result: result, Count: count})
	}
	for _, count := range gateCounts {
		value.ByGate = append(value.ByGate, count)
	}
	for minute, count := range minuteCounts {
		value.ByMinute = append(value.ByMinute, entry.MinuteCount{Minute: minute, Count: count})
	}
	for priceTierID, count := range ticketTypeCounts {
		value.ByTicketType = append(value.ByTicketType, entry.TicketTypeCount{PriceTierID: priceTierID, Count: count})
	}
	return entry.SortSummary(value), nil
}

func recordScanMutation(ctx context.Context, platform *platformpostgres.Store, input entry.OnlineScanInput, attemptID, ticketID, sessionID identifier.ID, decision entry.Decision, occurredAt time.Time) error {
	auditID, err := identifier.New()
	if err != nil {
		return err
	}
	outboxID, err := identifier.New()
	if err != nil {
		return err
	}
	hash := hex.EncodeToString(input.CredentialSHA256[:])
	payload, err := json.Marshal(map[string]any{"scan_attempt_id": attemptID.String(), "ticket_id": ticketID.String(), "session_id": sessionID.String(), "device_id": input.DeviceID.String(), "result": string(decision.Code), "credential_sha256": hash})
	if err != nil {
		return err
	}
	if err := platform.InsertAuditEntry(ctx, platformpostgres.AuditEntry{ID: auditID, OrganizationID: input.OrganizationID, ActorType: input.ActorType, ActorID: input.ActorID, Action: "entry.scan_recorded", SubjectType: "scan_attempt", SubjectID: attemptID.String(), Result: string(decision.Code), AfterData: payload, OccurredAt: occurredAt}); err != nil {
		return err
	}
	return platform.InsertOutboxEvent(ctx, platformpostgres.OutboxEvent{ID: outboxID, OrganizationID: input.OrganizationID, AggregateType: "scan_attempt", AggregateID: attemptID, AggregateVersion: 1, EventType: "entry.scan_recorded", SchemaVersion: 1, Payload: payload, AvailableAt: occurredAt, OccurredAt: occurredAt})
}

func recordTicketMutation(ctx context.Context, platform *platformpostgres.Store, input entry.OnlineScanInput, ticketID identifier.ID, version int64, occurredAt time.Time) error {
	auditID, err := identifier.New()
	if err != nil {
		return err
	}
	outboxID, err := identifier.New()
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{"ticket_id": ticketID.String(), "device_id": input.DeviceID.String(), "state": string(ticket.StatusAdmitted), "version": version})
	if err != nil {
		return err
	}
	if err := platform.InsertAuditEntry(ctx, platformpostgres.AuditEntry{ID: auditID, OrganizationID: input.OrganizationID, ActorType: input.ActorType, ActorID: input.ActorID, Action: "ticket.admitted", SubjectType: "ticket", SubjectID: ticketID.String(), Result: "success", AfterData: payload, OccurredAt: occurredAt}); err != nil {
		return err
	}
	return platform.InsertOutboxEvent(ctx, platformpostgres.OutboxEvent{ID: outboxID, OrganizationID: input.OrganizationID, AggregateType: "ticket", AggregateID: ticketID, AggregateVersion: version, EventType: "ticket.admitted", SchemaVersion: 1, Payload: payload, AvailableAt: occurredAt, OccurredAt: occurredAt})
}

func nullableUUID(value *identifier.ID) pgtype.UUID {
	if value == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: value.UUID(), Valid: true}
}

func equalHash(value []byte, expected [32]byte) bool {
	if len(value) != sha256.Size {
		return false
	}
	return subtle.ConstantTimeCompare(value, expected[:]) == 1
}

var _ entry.Repository = (*Repository)(nil)
var _ entry.SummaryRepository = (*Repository)(nil)
