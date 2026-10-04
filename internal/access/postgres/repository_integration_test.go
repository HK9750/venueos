//go:build integration

package postgres_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/HK9750/venueos/internal/access"
	accesspostgres "github.com/HK9750/venueos/internal/access/postgres"
	"github.com/HK9750/venueos/internal/database"
	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	postgrescontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestAPIKeyRepositoryLifecyclePersistsOnlyVerifierAndAudit(t *testing.T) {
	ctx := context.Background()
	container, err := postgrescontainer.Run(ctx, "postgres:18-alpine", postgrescontainer.WithDatabase("app"), postgrescontainer.WithUsername("app"), postgrescontainer.WithPassword("app"), postgrescontainer.BasicWaitStrategies())
	require.NoError(t, err)
	testcontainers.CleanupContainer(t, container)
	databaseURL, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	db, err := sql.Open("pgx", databaseURL)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	goose.SetBaseFS(migrations.FS)
	require.NoError(t, goose.SetDialect("postgres"))
	require.NoError(t, goose.UpContext(ctx, db, "."))
	pool, err := pgxpool.New(ctx, databaseURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	runner, err := database.NewTransactionRunner(pool, database.DefaultRetryPolicy())
	require.NoError(t, err)
	repository := accesspostgres.New(pool, runner)
	organizationID, err := identifier.New()
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO organizations (id, slug, display_name, default_locale, default_timezone, default_currency) VALUES ($1, 'api-key-test', 'API Key Test', 'en', 'UTC', 'USD')`, organizationID.UUID())
	require.NoError(t, err)
	keyID, err := identifier.New()
	require.NoError(t, err)
	now := time.Now().UTC().Truncate(time.Microsecond)
	var verifier [32]byte
	verifier[0] = 9
	created, err := repository.CreateAPIKey(ctx, access.CreateAPIKeyRecord{
		APIKey: access.APIKey{
			ID: keyID, OrganizationID: organizationID, Prefix: "vos_123456789abc", Name: "Reporting",
			Scopes: []access.Permission{access.PermissionReportRead}, CreatedByType: access.PrincipalUser,
			CreatedByID: "user-1", Version: 1, CreatedAt: now, UpdatedAt: now,
		},
		SecretHash: verifier, AuditID: newAPIKeyIntegrationID(t), OutboxID: newAPIKeyIntegrationID(t), ActorType: "user", ActorID: "user-1",
	})
	require.NoError(t, err)
	require.Equal(t, keyID, created.ID)
	stored, err := repository.LookupAPIKey(ctx, created.Prefix)
	require.NoError(t, err)
	require.Equal(t, verifier, stored.SecretHash)
	require.Equal(t, []access.Permission{access.PermissionReportRead}, stored.Scopes)
	page, err := repository.ListAPIKeys(ctx, organizationID, 10, nil)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Equal(t, created.ID, page.Items[0].ID)
	require.Nil(t, page.NextCursor)
	revoked, err := repository.RevokeAPIKey(ctx, access.RevokeAPIKeyRecord{
		OrganizationID: organizationID, APIKeyID: keyID, ExpectedVersion: created.Version,
		RevokedAt: now.Add(time.Minute), AuditID: newAPIKeyIntegrationID(t), OutboxID: newAPIKeyIntegrationID(t), ActorType: "user", ActorID: "user-1",
	})
	require.NoError(t, err)
	require.Equal(t, int64(2), revoked.Version)
	require.NotNil(t, revoked.RevokedAt)
	retried, err := repository.RevokeAPIKey(ctx, access.RevokeAPIKeyRecord{
		OrganizationID: organizationID, APIKeyID: keyID, ExpectedVersion: created.Version,
		RevokedAt: now.Add(2 * time.Minute), AuditID: newAPIKeyIntegrationID(t), OutboxID: newAPIKeyIntegrationID(t), ActorType: "user", ActorID: "user-1",
	})
	require.NoError(t, err)
	require.Equal(t, revoked.ID, retried.ID)
	require.Equal(t, revoked.Version, retried.Version)
	require.ErrorIs(t, repository.TouchAPIKey(ctx, keyID, now.Add(2*time.Minute)), access.ErrAPIKeyInactive)
	var auditCount, outboxCount int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM audit_entries WHERE organization_id = $1`, organizationID.UUID()).Scan(&auditCount))
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE organization_id = $1`, organizationID.UUID()).Scan(&outboxCount))
	require.Equal(t, 2, auditCount)
	require.Equal(t, 2, outboxCount)
}

func newAPIKeyIntegrationID(t *testing.T) identifier.ID {
	t.Helper()
	id, err := identifier.New()
	require.NoError(t, err)
	return id
}
