package order

import (
	"testing"
	"time"
)

func TestCursorRoundTripAndBoundedDecode(t *testing.T) {
	organizationID := mustOrderID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	orderID := mustOrderID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6")
	want := Cursor{OrganizationID: organizationID, ID: orderID, CreatedAt: time.Date(2026, 10, 4, 12, 0, 0, 123, time.UTC)}
	encoded, err := EncodeCursor(want)
	if err != nil {
		t.Fatalf("EncodeCursor() error = %v", err)
	}
	got, err := DecodeCursor(encoded)
	if err != nil || got != want {
		t.Fatalf("DecodeCursor() = %#v, %v; want %#v", got, err, want)
	}
	if _, err := DecodeCursor("not-a-cursor"); err == nil {
		t.Fatal("DecodeCursor() accepted malformed input")
	}
}
