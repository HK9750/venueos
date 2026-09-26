package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/HK9750/venueos/internal/access"
	"github.com/HK9750/venueos/internal/access/postgres/sqlc"
	"github.com/HK9750/venueos/internal/database"
	"github.com/HK9750/venueos/internal/platform/identifier"
	platformpostgres "github.com/HK9750/venueos/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
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

func (repository *Repository) CreateAPIKey(ctx context.Context, record access.CreateAPIKeyRecord) (access.APIKey, error) {
	var result access.APIKey
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		organization, err := queries.LockOrganizationForAPIKey(ctx, record.APIKey.OrganizationID.UUID())
		if errors.Is(err, pgx.ErrNoRows) {
			return access.ErrAPIKeyNotFound
		}
		if err != nil {
			return fmt.Errorf("lock organization for API key: %w", err)
		}
		if organization.Status != "active" {
			return access.ErrAPIKeyInactive
		}
		row, err := queries.InsertAPIKey(ctx, sqlc.InsertAPIKeyParams{
			ID: record.APIKey.ID.UUID(), OrganizationID: record.APIKey.OrganizationID.UUID(),
			Prefix: record.APIKey.Prefix, Name: record.APIKey.Name, SecretHash: record.SecretHash[:],
			Scopes: permissionStrings(record.APIKey.Scopes), CreatedByType: string(record.APIKey.CreatedByType),
			CreatedByID: record.APIKey.CreatedByID, ExpiresAt: nullableTime(record.APIKey.ExpiresAt),
			Version: record.APIKey.Version, CreatedAt: record.APIKey.CreatedAt.UTC(), UpdatedAt: record.APIKey.UpdatedAt.UTC(),
		})
		if err != nil {
			return fmt.Errorf("insert API key: %w", err)
		}
		mapped, err := mapAPIKey(row)
		if err != nil {
			return err
		}
		result = mapped
		return repository.recordCreateMutation(ctx, tx, record, result, "api_key.created")
	})
	return result, err
}

func (repository *Repository) RevokeAPIKey(ctx context.Context, record access.RevokeAPIKeyRecord) (access.APIKey, error) {
	var result access.APIKey
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		current, err := queries.GetAPIKeyForUpdate(ctx, sqlc.GetAPIKeyForUpdateParams{OrganizationID: record.OrganizationID.UUID(), ID: record.APIKeyID.UUID()})
		if errors.Is(err, pgx.ErrNoRows) {
			return access.ErrAPIKeyNotFound
		}
		if err != nil {
			return fmt.Errorf("lock API key for revocation: %w", err)
		}
		if current.RevokedAt.Valid {
			mapped, mapErr := mapAPIKey(current)
			if mapErr != nil {
				return mapErr
			}
			result = mapped
			return access.ErrAPIKeyInactive
		}
		if current.Version != record.ExpectedVersion {
			return access.ErrAPIKeyVersion
		}
		row, err := queries.RevokeAPIKey(ctx, sqlc.RevokeAPIKeyParams{
			RevokedAt: pgtype.Timestamptz{Time: record.RevokedAt.UTC(), Valid: true},
			UpdatedAt: record.RevokedAt.UTC(), OrganizationID: record.OrganizationID.UUID(),
			ID: record.APIKeyID.UUID(), ExpectedVersion: record.ExpectedVersion,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return access.ErrAPIKeyVersion
		}
		if err != nil {
			return fmt.Errorf("revoke API key: %w", err)
		}
		mapped, err := mapAPIKey(row)
		if err != nil {
			return err
		}
		result = mapped
		return repository.recordRevokeMutation(ctx, tx, record, result, "api_key.revoked")
	})
	return result, err
}

func (repository *Repository) GetAPIKey(ctx context.Context, organizationID, keyID identifier.ID) (access.APIKey, error) {
	row, err := repository.queries.GetAPIKey(ctx, sqlc.GetAPIKeyParams{OrganizationID: organizationID.UUID(), ID: keyID.UUID()})
	if errors.Is(err, pgx.ErrNoRows) {
		return access.APIKey{}, access.ErrAPIKeyNotFound
	}
	if err != nil {
		return access.APIKey{}, fmt.Errorf("get API key: %w", err)
	}
	return mapAPIKey(row)
}

func (repository *Repository) RotateAPIKey(ctx context.Context, record access.RotateAPIKeyRecord) (access.APIKey, error) {
	var result access.APIKey
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		current, err := queries.GetAPIKeyForUpdate(ctx, sqlc.GetAPIKeyForUpdateParams{OrganizationID: record.OrganizationID.UUID(), ID: record.APIKeyID.UUID()})
		if errors.Is(err, pgx.ErrNoRows) {
			return access.ErrAPIKeyNotFound
		}
		if err != nil {
			return fmt.Errorf("lock API key for rotation: %w", err)
		}
		if current.RevokedAt.Valid {
			return access.ErrAPIKeyInactive
		}
		if current.Version != record.ExpectedVersion {
			return access.ErrAPIKeyVersion
		}
		if _, err := queries.InsertAPIKey(ctx, sqlc.InsertAPIKeyParams{
			ID: record.Replacement.ID.UUID(), OrganizationID: record.Replacement.OrganizationID.UUID(),
			Prefix: record.Replacement.Prefix, Name: record.Replacement.Name, SecretHash: record.SecretHash[:],
			Scopes: permissionStrings(record.Replacement.Scopes), CreatedByType: string(record.Replacement.CreatedByType),
			CreatedByID: record.Replacement.CreatedByID, ExpiresAt: nullableTime(record.Replacement.ExpiresAt),
			Version: record.Replacement.Version, CreatedAt: record.Replacement.CreatedAt.UTC(), UpdatedAt: record.Replacement.UpdatedAt.UTC(),
		}); err != nil {
			return fmt.Errorf("insert rotated API key: %w", err)
		}
		revoked, err := queries.RevokeAPIKey(ctx, sqlc.RevokeAPIKeyParams{
			RevokedAt: pgtype.Timestamptz{Time: record.RotatedAt.UTC(), Valid: true},
			UpdatedAt: record.RotatedAt.UTC(), OrganizationID: record.OrganizationID.UUID(), ID: record.APIKeyID.UUID(), ExpectedVersion: record.ExpectedVersion,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return access.ErrAPIKeyVersion
		}
		if err != nil {
			return fmt.Errorf("revoke previous API key during rotation: %w", err)
		}
		result, err = mapAPIKey(sqlc.ApiKey{
			ID: record.Replacement.ID.UUID(), OrganizationID: record.Replacement.OrganizationID.UUID(), Prefix: record.Replacement.Prefix,
			Name: record.Replacement.Name, Scopes: permissionStrings(record.Replacement.Scopes), CreatedByType: string(record.Replacement.CreatedByType),
			CreatedByID: record.Replacement.CreatedByID, ExpiresAt: nullableTime(record.Replacement.ExpiresAt), Version: record.Replacement.Version,
			CreatedAt: record.Replacement.CreatedAt, UpdatedAt: record.Replacement.UpdatedAt,
		})
		if err != nil {
			return err
		}
		platform := repository.platform.WithTx(tx)
		data, err := json.Marshal(map[string]any{
			"api_key_id": record.APIKeyID.String(), "replacement_id": result.ID.String(),
			"replacement_prefix": result.Prefix, "version": revoked.Version, "rotation_overlap": false,
		})
		if err != nil {
			return fmt.Errorf("marshal API key rotation audit data: %w", err)
		}
		if err := platform.InsertAuditEntry(ctx, platformpostgres.AuditEntry{
			ID: record.AuditID, OrganizationID: record.OrganizationID, ActorType: record.ActorType, ActorID: record.ActorID,
			Action: "api_key.rotated", SubjectType: "api_key", SubjectID: record.APIKeyID.String(), Result: "success",
			AfterData: data, OccurredAt: record.RotatedAt,
		}); err != nil {
			return err
		}
		payload, err := json.Marshal(map[string]any{
			"api_key_id": record.APIKeyID.String(), "replacement_id": result.ID.String(),
			"version": revoked.Version, "rotation_overlap": false,
		})
		if err != nil {
			return fmt.Errorf("marshal API key rotation event: %w", err)
		}
		return platform.InsertOutboxEvent(ctx, platformpostgres.OutboxEvent{
			ID: record.OutboxID, OrganizationID: record.OrganizationID, AggregateType: "api_key", AggregateID: record.APIKeyID,
			AggregateVersion: revoked.Version, EventType: "api_key.rotated", SchemaVersion: 1,
			Payload: payload, AvailableAt: record.RotatedAt, OccurredAt: record.RotatedAt,
		})
	})
	return result, err
}

func (repository *Repository) LookupAPIKey(ctx context.Context, prefix string) (access.StoredAPIKey, error) {
	row, err := repository.queries.LookupAPIKeyByPrefix(ctx, prefix)
	if errors.Is(err, pgx.ErrNoRows) {
		return access.StoredAPIKey{}, access.ErrAPIKeyNotFound
	}
	if err != nil {
		return access.StoredAPIKey{}, fmt.Errorf("lookup API key: %w", err)
	}
	key, err := mapAPIKey(row)
	if err != nil {
		return access.StoredAPIKey{}, err
	}
	if len(row.SecretHash) != 32 {
		return access.StoredAPIKey{}, errors.New("persisted API key verifier has invalid length")
	}
	var verifier [32]byte
	copy(verifier[:], row.SecretHash)
	return access.StoredAPIKey{APIKey: key, SecretHash: verifier}, nil
}

func (repository *Repository) TouchAPIKey(ctx context.Context, id identifier.ID, at time.Time) error {
	rows, err := repository.queries.TouchAPIKey(ctx, sqlc.TouchAPIKeyParams{LastUsedAt: pgtype.Timestamptz{Time: at.UTC(), Valid: true}, UpdatedAt: at.UTC(), ID: id.UUID()})
	if err != nil {
		return fmt.Errorf("touch API key: %w", err)
	}
	if rows != 1 {
		return access.ErrAPIKeyInactive
	}
	return nil
}

func (repository *Repository) recordCreateMutation(ctx context.Context, tx pgx.Tx, record access.CreateAPIKeyRecord, result access.APIKey, eventType string) error {
	return repository.recordMutationValues(ctx, tx, record.APIKey.OrganizationID, result.ID, record.AuditID, record.OutboxID, record.ActorType, record.ActorID, eventType, result)
}

func (repository *Repository) recordMutationValues(ctx context.Context, tx pgx.Tx, organizationID, keyID, auditID, outboxID identifier.ID, actorType, actorID, eventType string, result access.APIKey) error {
	platform := repository.platform.WithTx(tx)
	data, err := json.Marshal(map[string]any{
		"api_key_id": result.ID.String(), "prefix": result.Prefix, "name": result.Name,
		"scopes": permissionStrings(result.Scopes), "version": result.Version,
		"revoked": result.RevokedAt != nil,
	})
	if err != nil {
		return fmt.Errorf("marshal API key audit data: %w", err)
	}
	if err := platform.InsertAuditEntry(ctx, platformpostgres.AuditEntry{
		ID: auditID, OrganizationID: organizationID, ActorType: actorType, ActorID: actorID,
		Action: eventType, SubjectType: "api_key", SubjectID: keyID.String(), Result: "success",
		AfterData: data, OccurredAt: result.UpdatedAt,
	}); err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{
		"api_key_id": result.ID.String(), "prefix": result.Prefix,
		"version": result.Version, "revoked": result.RevokedAt != nil,
	})
	if err != nil {
		return fmt.Errorf("marshal API key event: %w", err)
	}
	return platform.InsertOutboxEvent(ctx, platformpostgres.OutboxEvent{
		ID: outboxID, OrganizationID: organizationID, AggregateType: "api_key", AggregateID: keyID,
		AggregateVersion: result.Version, EventType: eventType, SchemaVersion: 1,
		Payload: payload, AvailableAt: result.UpdatedAt, OccurredAt: result.UpdatedAt,
	})
}

func (repository *Repository) recordRevokeMutation(ctx context.Context, tx pgx.Tx, record access.RevokeAPIKeyRecord, result access.APIKey, eventType string) error {
	return repository.recordMutationValues(ctx, tx, record.OrganizationID, result.ID, record.AuditID, record.OutboxID, record.ActorType, record.ActorID, eventType, result)
}

func mapAPIKey(row sqlc.ApiKey) (access.APIKey, error) {
	id, err := identifier.FromUUID(row.ID)
	if err != nil {
		return access.APIKey{}, fmt.Errorf("map API key ID: %w", err)
	}
	organizationID, err := identifier.FromUUID(row.OrganizationID)
	if err != nil {
		return access.APIKey{}, fmt.Errorf("map API key organization ID: %w", err)
	}
	createdByType, err := access.NewPrincipal(access.PrincipalType(row.CreatedByType), row.CreatedByID)
	if err != nil {
		return access.APIKey{}, fmt.Errorf("map API key creator: %w", err)
	}
	scopes := make([]access.Permission, 0, len(row.Scopes))
	for _, scope := range row.Scopes {
		scopes = append(scopes, access.Permission(scope))
	}
	result := access.APIKey{ID: id, OrganizationID: organizationID, Prefix: row.Prefix, Name: row.Name, Scopes: scopes,
		CreatedByType: createdByType.Type(), CreatedByID: createdByType.ID(), Version: row.Version,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
	if row.ExpiresAt.Valid {
		value := row.ExpiresAt.Time
		result.ExpiresAt = &value
	}
	if row.RevokedAt.Valid {
		value := row.RevokedAt.Time
		result.RevokedAt = &value
	}
	if row.LastUsedAt.Valid {
		value := row.LastUsedAt.Time
		result.LastUsedAt = &value
	}
	return result, nil
}

func permissionStrings(scopes []access.Permission) []string {
	values := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		values = append(values, string(scope))
	}
	return values
}

func nullableTime(value *time.Time) pgtype.Timestamptz {
	if value == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
}
