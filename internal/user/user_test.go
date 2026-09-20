package user

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fakeRepository struct {
	created CreateInput
}

func (f *fakeRepository) Create(_ context.Context, input CreateInput) (User, error) {
	f.created = input
	return User{ID: uuid.New(), Email: input.Email, Name: input.Name, CreatedAt: time.Now(), UpdatedAt: time.Now()}, nil
}

func (*fakeRepository) Get(context.Context, uuid.UUID) (User, error) {
	return User{}, ErrNotFound
}

func (*fakeRepository) List(context.Context, int32, int32) ([]User, error) {
	return []User{}, nil
}

func TestServiceCreateNormalizesInput(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository)

	created, err := service.Create(context.Background(), CreateInput{Email: "  ADA@EXAMPLE.COM ", Name: " Ada "})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Email != "ada@example.com" || created.Name != "Ada" {
		t.Fatalf("Create() = %#v, want normalized values", created)
	}
}

func TestServiceCreateRejectsInvalidInput(t *testing.T) {
	service := NewService(&fakeRepository{})

	_, err := service.Create(context.Background(), CreateInput{Email: "not-an-email", Name: "Ada"})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Create() error = %v, want ErrInvalid", err)
	}
}

func TestServiceCreateMeasuresNameLengthInCharacters(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository)
	name := strings.Repeat("界", 100)

	created, err := service.Create(context.Background(), CreateInput{Email: "ada@example.com", Name: name})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Name != name {
		t.Fatalf("Create() name = %q, want Unicode name preserved", created.Name)
	}
}
