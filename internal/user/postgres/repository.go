// Package postgres adapts PostgreSQL and SQLC queries to the user repository port.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/HK9750/venueos/internal/user"
	"github.com/HK9750/venueos/internal/user/postgres/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	queries *sqlc.Queries
}

func New(pool *pgxpool.Pool) *Repository {
	return &Repository{queries: sqlc.New(pool)}
}

func (r *Repository) Create(ctx context.Context, input user.CreateInput) (user.User, error) {
	row, err := r.queries.CreateUser(ctx, sqlc.CreateUserParams{Email: input.Email, Name: input.Name})
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) && postgresError.Code == "23505" && postgresError.ConstraintName == "users_email_lower_unique_idx" {
			return user.User{}, user.ErrConflict
		}
		return user.User{}, fmt.Errorf("insert user: %w", err)
	}
	return toDomain(row.ID, row.Email, row.Name, row.CreatedAt, row.UpdatedAt), nil
}

func (r *Repository) Get(ctx context.Context, id uuid.UUID) (user.User, error) {
	row, err := r.queries.GetUser(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return user.User{}, user.ErrNotFound
	}
	if err != nil {
		return user.User{}, fmt.Errorf("select user: %w", err)
	}
	return toDomain(row.ID, row.Email, row.Name, row.CreatedAt, row.UpdatedAt), nil
}

func (r *Repository) List(ctx context.Context, limit, offset int32) ([]user.User, error) {
	rows, err := r.queries.ListUsers(ctx, sqlc.ListUsersParams{Limit: limit, Offset: offset})
	if err != nil {
		return nil, fmt.Errorf("select users: %w", err)
	}
	users := make([]user.User, 0, len(rows))
	for _, row := range rows {
		users = append(users, toDomain(row.ID, row.Email, row.Name, row.CreatedAt, row.UpdatedAt))
	}
	return users, nil
}

func toDomain(id uuid.UUID, email, name string, createdAt, updatedAt time.Time) user.User {
	return user.User{ID: id, Email: email, Name: name, CreatedAt: createdAt, UpdatedAt: updatedAt}
}
