package clock

import (
	"testing"
	"time"
)

func TestFixedNormalizesToUTC(t *testing.T) {
	location := time.FixedZone("test", 5*60*60)
	instant := time.Date(2026, time.September, 23, 12, 30, 0, 0, location)
	clock := NewFixed(instant)

	if got := clock.Now(); got.Location() != time.UTC || !got.Equal(instant) {
		t.Fatalf("Now() = %v in %v, want same instant in UTC", got, got.Location())
	}
}

func TestSystemReturnsUTC(t *testing.T) {
	if got := (System{}).Now(); got.Location() != time.UTC {
		t.Fatalf("Now() location = %v, want UTC", got.Location())
	}
}
