package identifier

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestNewReturnsUUIDv7(t *testing.T) {
	id, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if id.IsZero() {
		t.Fatal("New() returned zero ID")
	}
	if version := id.UUID().Version(); version != 7 {
		t.Fatalf("version = %d, want 7", version)
	}
}

func TestParseRequiresCanonicalNonZeroUUID(t *testing.T) {
	valid := "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5"
	id, err := Parse(valid)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if id.String() != valid {
		t.Fatalf("String() = %q, want %q", id.String(), valid)
	}

	for _, value := range []string{"", uuid.Nil.String(), "01890F3E-7B4C-7CC6-9C52-6D6F83394EF5", "not-a-uuid"} {
		if _, err := Parse(value); !errors.Is(err, ErrInvalid) {
			t.Errorf("Parse(%q) error = %v, want ErrInvalid", value, err)
		}
	}
}

func TestIDJSONRoundTrip(t *testing.T) {
	original, err := Parse("01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var decoded ID
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if decoded != original {
		t.Fatalf("decoded = %v, want %v", decoded, original)
	}
}
