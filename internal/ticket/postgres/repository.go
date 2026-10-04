// Package postgres adapts durable ticket verification keys to the ticket port.
// It intentionally exposes public keys only; private signing material belongs
// to the configured secret or KMS adapter.
package postgres

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"

	"github.com/HK9750/venueos/internal/database"
	platformpostgres "github.com/HK9750/venueos/internal/platform/postgres"
	"github.com/HK9750/venueos/internal/ticket"
	"github.com/HK9750/venueos/internal/ticket/postgres/sqlc"
	"github.com/jackc/pgx/v5"
)

type Repository struct {
	queries      *sqlc.Queries
	platform     *platformpostgres.Store
	transactions *database.TransactionRunner
}

func New(pool sqlc.DBTX) *Repository {
	return &Repository{queries: sqlc.New(pool), platform: platformpostgres.NewStore(pool)}
}

func NewForIssuance(pool sqlc.DBTX, transactions *database.TransactionRunner) *Repository {
	return &Repository{queries: sqlc.New(pool), platform: platformpostgres.NewStore(pool), transactions: transactions}
}

func (repository *Repository) Resolve(ctx context.Context, keyID string) (ed25519.PublicKey, ticket.KeyStatus, error) {
	row, err := repository.queries.ResolveTicketKey(ctx, keyID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", ticket.ErrUnknownKey
	}
	if err != nil {
		return nil, "", fmt.Errorf("resolve ticket signing key: %w", err)
	}
	if len(row.PublicKey) != ed25519.PublicKeySize {
		return nil, "", ticket.ErrUnknownKey
	}
	status := ticket.KeyStatus(row.State)
	switch status {
	case ticket.KeyActive, ticket.KeyVerifyOnly, ticket.KeyRevoked:
		return ed25519.PublicKey(append([]byte(nil), row.PublicKey...)), status, nil
	default:
		return nil, "", ticket.ErrUnknownKey
	}
}

var _ ticket.KeyResolver = (*Repository)(nil)
