// Package identifier provides opaque UUID-backed identifiers for VenueOS resources.
package identifier

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
)

var ErrInvalid = errors.New("invalid identifier")

// ID is an opaque UUID-compatible resource identifier. New IDs use UUIDv7.
type ID uuid.UUID

// New returns a time-ordered UUIDv7 identifier.
func New() (ID, error) {
	value, err := uuid.NewV7()
	if err != nil {
		return ID{}, fmt.Errorf("generate UUIDv7: %w", err)
	}
	return ID(value), nil
}

// Parse accepts a canonical, non-zero UUID string.
func Parse(value string) (ID, error) {
	parsed, err := uuid.Parse(value)
	if err != nil || parsed == uuid.Nil || parsed.String() != value {
		return ID{}, fmt.Errorf("%w: expected canonical UUID", ErrInvalid)
	}
	return ID(parsed), nil
}

// FromUUID converts an existing non-zero UUID.
func FromUUID(value uuid.UUID) (ID, error) {
	if value == uuid.Nil {
		return ID{}, fmt.Errorf("%w: identifier must not be zero", ErrInvalid)
	}
	return ID(value), nil
}

// UUID returns the underlying UUID for infrastructure adapters.
func (id ID) UUID() uuid.UUID { return uuid.UUID(id) }

func (id ID) String() string { return uuid.UUID(id).String() }

// IsZero reports whether the identifier is uninitialized.
func (id ID) IsZero() bool { return uuid.UUID(id) == uuid.Nil }

func (id ID) MarshalText() ([]byte, error) {
	if id.IsZero() {
		return nil, fmt.Errorf("%w: identifier must not be zero", ErrInvalid)
	}
	return []byte(id.String()), nil
}

func (id *ID) UnmarshalText(text []byte) error {
	parsed, err := Parse(string(text))
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}
