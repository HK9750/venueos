//go:build integration

package postgres_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"testing"
	"time"

	"github.com/HK9750/venueos/internal/database"
	"github.com/HK9750/venueos/internal/invitation"
	invitationpostgres "github.com/HK9750/venueos/internal/invitation/postgres"
	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	postgrescontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestInvitationRepositoryLifecycleIsAtomic(t *testing.T) {
	ctx := context.Background()
	container, err := postgrescontainer.Run(ctx, "postgres:18-alpine",
		postgrescontainer.WithDatabase("app"), postgrescontainer.WithUsername("app"), postgrescontainer.WithPassword("app"), postgrescontainer.BasicWaitStrategies())
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
	repository := invitationpostgres.New(pool, runner)

	organizationID, userID := newID(t), newID(t)
	_, err = pool.Exec(ctx, `INSERT INTO organizations (id, slug, display_name, default_locale, default_timezone, default_currency) VALUES ($1, 'invite-venue', 'Invite Venue', 'en', 'UTC', 'USD')`, organizationID.UUID())
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO users (id, email, name) VALUES ($1, 'staff@example.com', 'Staff')`, userID.UUID())
	require.NoError(t, err)
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	tokenHash := sha256.Sum256([]byte("first invitation token"))
	created, err := repository.Issue(ctx, invitation.IssueRecord{
		Invitation: invitation.Invitation{ID: newID(t), OrganizationID: organizationID, Email: "staff@example.com", Role: "finance", Status: invitation.StatusInvited, ExpiresAt: now.Add(24 * time.Hour), Version: 1, CreatedAt: now, UpdatedAt: now},
		TokenHash:  tokenHash, AuditID: newID(t), OutboxID: newID(t), ActorType: "user", ActorID: "owner",
	})
	require.NoError(t, err)
	require.Equal(t, invitation.StatusInvited, created.Status)

	accepted, err := repository.Accept(ctx, invitation.AcceptRecord{TokenHash: tokenHash, UserID: userID, InvitationAuditID: newID(t), InvitationOutboxID: newID(t), MembershipID: newID(t), MembershipAuditID: newID(t), MembershipOutboxID: newID(t), ActorType: "user", ActorID: userID.String(), OccurredAt: now.Add(time.Hour)})
	require.NoError(t, err)
	require.Equal(t, invitation.StatusAccepted, accepted.Status)
	require.NotNil(t, accepted.MembershipID)
	_, err = repository.Accept(ctx, invitation.AcceptRecord{TokenHash: tokenHash, UserID: userID, InvitationAuditID: newID(t), InvitationOutboxID: newID(t), MembershipID: newID(t), MembershipAuditID: newID(t), MembershipOutboxID: newID(t), ActorType: "user", ActorID: userID.String(), OccurredAt: now.Add(2 * time.Hour)})
	require.ErrorIs(t, err, invitation.ErrAlreadyUsed)

	var auditCount, outboxCount int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM audit_entries`).Scan(&auditCount))
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events`).Scan(&outboxCount))
	require.Equal(t, 3, auditCount)
	require.Equal(t, 3, outboxCount)

	expiring, err := repository.Issue(ctx, invitation.IssueRecord{Invitation: invitation.Invitation{ID: newID(t), OrganizationID: organizationID, Email: "expiry@example.com", Role: "support", Status: invitation.StatusInvited, ExpiresAt: now.Add(-time.Minute), Version: 1, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)}, TokenHash: sha256.Sum256([]byte("expired invitation token")), AuditID: newID(t), OutboxID: newID(t), ActorType: "user", ActorID: "owner"})
	require.NoError(t, err)
	require.Equal(t, invitation.StatusInvited, expiring.Status)
	count, err := repository.ExpireDue(ctx, now, 10)
	require.NoError(t, err)
	require.Equal(t, 1, count)

	var status string
	require.NoError(t, pool.QueryRow(ctx, `SELECT status FROM invitations WHERE id = $1`, expiring.ID.UUID()).Scan(&status))
	require.Equal(t, string(invitation.StatusExpired), status)
}

func newID(t *testing.T) identifier.ID {
	t.Helper()
	id, err := identifier.New()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
