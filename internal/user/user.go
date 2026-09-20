// Package user contains the example user domain and application behavior.
package user

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

var (
	ErrNotFound = errors.New("user not found")
	ErrConflict = errors.New("user already exists")
	ErrInvalid  = errors.New("invalid user")
)

type User struct {
	ID        uuid.UUID
	Email     string
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type CreateInput struct {
	Email string
	Name  string
}

type Repository interface {
	Create(context.Context, CreateInput) (User, error)
	Get(context.Context, uuid.UUID) (User, error)
	List(context.Context, int32, int32) ([]User, error)
}

type Service struct {
	repository Repository
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository}
}

func (s *Service) Create(ctx context.Context, input CreateInput) (User, error) {
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	input.Name = strings.TrimSpace(input.Name)

	if len(input.Email) > 254 {
		return User{}, fmt.Errorf("%w: email must be at most 254 characters", ErrInvalid)
	}
	address, err := mail.ParseAddress(input.Email)
	if err != nil || address.Address != input.Email {
		return User{}, fmt.Errorf("%w: email is not valid", ErrInvalid)
	}
	if input.Name == "" || utf8.RuneCountInString(input.Name) > 100 {
		return User{}, fmt.Errorf("%w: name must contain 1 to 100 characters", ErrInvalid)
	}

	created, err := s.repository.Create(ctx, input)
	if err != nil {
		return User{}, fmt.Errorf("create user: %w", err)
	}
	return created, nil
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (User, error) {
	if id == uuid.Nil {
		return User{}, fmt.Errorf("%w: ID must not be empty", ErrInvalid)
	}
	found, err := s.repository.Get(ctx, id)
	if err != nil {
		return User{}, fmt.Errorf("get user: %w", err)
	}
	return found, nil
}

func (s *Service) List(ctx context.Context, limit, offset int32) ([]User, error) {
	if limit < 1 || limit > 100 {
		return nil, fmt.Errorf("%w: limit must be between 1 and 100", ErrInvalid)
	}
	if offset < 0 {
		return nil, fmt.Errorf("%w: offset must not be negative", ErrInvalid)
	}
	users, err := s.repository.List(ctx, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	return users, nil
}
