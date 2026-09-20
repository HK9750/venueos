//go:build integration

package postgres_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/HK9750/venueos/internal/user"
	userpostgres "github.com/HK9750/venueos/internal/user/postgres"
	"github.com/HK9750/venueos/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	postgrescontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestRepository(t *testing.T) {
	ctx := context.Background()
	container, err := postgrescontainer.Run(ctx,
		"postgres:18-alpine",
		postgrescontainer.WithDatabase("app"),
		postgrescontainer.WithUsername("app"),
		postgrescontainer.WithPassword("app"),
		postgrescontainer.BasicWaitStrategies(),
	)
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

	repository := userpostgres.New(pool)
	created, err := repository.Create(ctx, user.CreateInput{Email: "ada@example.com", Name: "Ada"})
	require.NoError(t, err)
	require.NotEmpty(t, created.ID)

	found, err := repository.Get(ctx, created.ID)
	require.NoError(t, err)
	require.Equal(t, created.Email, found.Email)

	listed, err := repository.List(ctx, 20, 0)
	require.NoError(t, err)
	require.Len(t, listed, 1)

	_, err = repository.Create(ctx, user.CreateInput{Email: "ADA@EXAMPLE.COM", Name: "Duplicate"})
	require.True(t, errors.Is(err, user.ErrConflict))
}
