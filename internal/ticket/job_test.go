package ticket

import (
	"testing"
	"time"
)

func TestIssueJobPayloadRoundTripIsStrictAndUTC(t *testing.T) {
	input := IssueInput{
		OrganizationID:  mustID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5"),
		EntitlementID:   mustID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6"),
		OrderLineID:     mustID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef7"),
		SessionID:       mustID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef8"),
		PublicReference: "VOS-ABC12345", NotBefore: time.Date(2026, 10, 3, 12, 0, 0, 0, time.FixedZone("PKT", 5*60*60)),
		ExpiresAt: time.Date(2026, 10, 4, 12, 0, 0, 0, time.FixedZone("PKT", 5*60*60)), EntryOpensAt: time.Date(2026, 10, 3, 11, 30, 0, 0, time.FixedZone("PKT", 5*60*60)), EntryClosesAt: time.Date(2026, 10, 3, 15, 0, 0, 0, time.FixedZone("PKT", 5*60*60)),
	}
	raw, err := EncodeIssueJobPayload(input)
	if err != nil {
		t.Fatalf("EncodeIssueJobPayload() error = %v", err)
	}
	decoded, err := DecodeIssueJobPayload(raw)
	if err != nil {
		t.Fatalf("DecodeIssueJobPayload() error = %v", err)
	}
	if decoded.OrganizationID != input.OrganizationID || decoded.EntitlementID != input.EntitlementID || decoded.PublicReference != input.PublicReference || !decoded.NotBefore.Equal(input.NotBefore.UTC()) || decoded.NotBefore.Location() != time.UTC {
		t.Fatalf("decoded payload = %#v", decoded)
	}
	if _, err := DecodeIssueJobPayload(append(raw[:len(raw)-1], []byte(`,"unexpected":true}`)...)); err == nil {
		t.Fatal("DecodeIssueJobPayload accepted an unknown field")
	}
}

func TestIssueJobPayloadRejectsMalformedWindows(t *testing.T) {
	input := IssueInput{OrganizationID: mustID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5"), EntitlementID: mustID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6"), OrderLineID: mustID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef7"), SessionID: mustID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef8"), NotBefore: time.Now(), ExpiresAt: time.Now().Add(-time.Minute), EntryOpensAt: time.Now(), EntryClosesAt: time.Now().Add(time.Hour)}
	if _, err := EncodeIssueJobPayload(input); err == nil {
		t.Fatalf("EncodeIssueJobPayload() error = %v", err)
	}
}
