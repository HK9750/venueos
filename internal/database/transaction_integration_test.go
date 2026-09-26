//go:build integration

package database_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/HK9750/venueos/internal/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	postgrescontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestTransactionRunnerCommitsRollsBackAndRetries(t *testing.T) {
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
	pool, err := pgxpool.New(ctx, databaseURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	_, err = pool.Exec(ctx, `CREATE TABLE transaction_test (id integer PRIMARY KEY)`)
	require.NoError(t, err)

	runner, err := database.NewTransactionRunner(pool, database.RetryPolicy{
		MaxAttempts: 3,
		BaseDelay:   time.Microsecond,
		MaxDelay:    time.Millisecond,
	})
	require.NoError(t, err)

	expected := errors.New("reject mutation")
	err = runner.Run(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		_, execErr := tx.Exec(ctx, `INSERT INTO transaction_test (id) VALUES (1)`)
		require.NoError(t, execErr)
		return expected
	})
	require.ErrorIs(t, err, expected)
	requireRowCount(t, ctx, pool, 0)

	attempts := 0
	err = runner.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		attempts++
		if attempts == 1 {
			return &pgconn.PgError{Code: "40001", Message: "synthetic serialization failure"}
		}
		_, execErr := tx.Exec(ctx, `INSERT INTO transaction_test (id) VALUES (2)`)
		return execErr
	})
	require.NoError(t, err)
	require.Equal(t, 2, attempts)
	requireRowCount(t, ctx, pool, 1)

	attempts = 0
	err = runner.Run(ctx, pgx.TxOptions{}, func(context.Context, pgx.Tx) error {
		attempts++
		return &pgconn.PgError{Code: "40P01", Message: "synthetic deadlock"}
	})
	require.ErrorIs(t, err, database.ErrRetriesExhausted)
	require.Equal(t, 3, attempts)
}

func requireRowCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, expected int) {
	t.Helper()
	var count int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM transaction_test`).Scan(&count))
	require.Equal(t, expected, count)
}
