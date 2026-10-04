package ticket

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/HK9750/venueos/internal/platform/identifier"
)

const (
	// IssueJobType is the durable worker job name emitted after an order is
	// confirmed. The entitlement is the idempotency boundary for issuance.
	IssueJobType = "ticket.issue"
	// IssueJobSchemaVersion is incremented only when the payload contract
	// changes incompatibly.
	IssueJobSchemaVersion int32 = 1
	maxIssueJobBytes            = 16 << 10
)

// IssueJobPayload is the versioned, provider-neutral payload consumed by the
// ticket issuer. It contains immutable order/session facts, never payment data
// or a signing key. The database remains the authority for entitlement state.
type IssueJobPayload struct {
	OrganizationID  string `json:"organization_id"`
	EntitlementID   string `json:"entitlement_id"`
	OrderLineID     string `json:"order_line_id"`
	SessionID       string `json:"session_id"`
	PublicReference string `json:"public_reference,omitempty"`
	NotBefore       string `json:"not_before"`
	ExpiresAt       string `json:"expires_at"`
	EntryOpensAt    string `json:"entry_opens_at"`
	EntryClosesAt   string `json:"entry_closes_at"`
}

// EncodeIssueJobPayload validates and encodes a ticket issuance job. The
// compact format is bounded before it reaches the durable jobs table.
func EncodeIssueJobPayload(input IssueInput) ([]byte, error) {
	payload, err := newIssueJobPayload(input)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode ticket issuance job: %w", err)
	}
	if len(encoded) > maxIssueJobBytes {
		return nil, fmt.Errorf("ticket issuance job exceeds %d bytes", maxIssueJobBytes)
	}
	return encoded, nil
}

// DecodeIssueJobPayload strictly decodes a version-one issuance payload and
// reconstructs the domain input with UTC timestamps and parsed identifiers.
func DecodeIssueJobPayload(raw []byte) (IssueInput, error) {
	if len(raw) == 0 || len(raw) > maxIssueJobBytes {
		return IssueInput{}, fmt.Errorf("ticket issuance job payload is outside the allowed size")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var payload IssueJobPayload
	if err := decoder.Decode(&payload); err != nil {
		return IssueInput{}, fmt.Errorf("decode ticket issuance job: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return IssueInput{}, fmt.Errorf("decode ticket issuance job: trailing data")
	}
	input, err := issueInputFromPayload(payload)
	if err != nil {
		return IssueInput{}, err
	}
	// Use the zero time for structural validation so an otherwise valid job may
	// wait in the queue until its not-before window without being rejected here.
	if err := validateIssueInput(input, time.Time{}); err != nil {
		return IssueInput{}, fmt.Errorf("decode ticket issuance job: %w", err)
	}
	return input, nil
}

func newIssueJobPayload(input IssueInput) (IssueJobPayload, error) {
	if input.OrganizationID.IsZero() || input.EntitlementID.IsZero() || input.OrderLineID.IsZero() || input.SessionID.IsZero() || input.NotBefore.IsZero() || input.ExpiresAt.IsZero() || input.EntryOpensAt.IsZero() || input.EntryClosesAt.IsZero() {
		return IssueJobPayload{}, fmt.Errorf("ticket issuance job identifiers and time windows are required")
	}
	if !input.ExpiresAt.After(input.NotBefore) || !input.EntryClosesAt.After(input.EntryOpensAt) {
		return IssueJobPayload{}, fmt.Errorf("ticket issuance job time windows are invalid")
	}
	if input.PublicReference != "" && !validPublicReference(input.PublicReference) {
		return IssueJobPayload{}, fmt.Errorf("ticket issuance job public reference is invalid")
	}
	return IssueJobPayload{
		OrganizationID: input.OrganizationID.String(), EntitlementID: input.EntitlementID.String(),
		OrderLineID: input.OrderLineID.String(), SessionID: input.SessionID.String(),
		PublicReference: input.PublicReference, NotBefore: input.NotBefore.UTC().Format(time.RFC3339Nano),
		ExpiresAt: input.ExpiresAt.UTC().Format(time.RFC3339Nano), EntryOpensAt: input.EntryOpensAt.UTC().Format(time.RFC3339Nano),
		EntryClosesAt: input.EntryClosesAt.UTC().Format(time.RFC3339Nano),
	}, nil
}

func issueInputFromPayload(payload IssueJobPayload) (IssueInput, error) {
	organizationID, err := identifier.Parse(payload.OrganizationID)
	if err != nil {
		return IssueInput{}, fmt.Errorf("decode ticket issuance job organization ID: %w", err)
	}
	entitlementID, err := identifier.Parse(payload.EntitlementID)
	if err != nil {
		return IssueInput{}, fmt.Errorf("decode ticket issuance job entitlement ID: %w", err)
	}
	orderLineID, err := identifier.Parse(payload.OrderLineID)
	if err != nil {
		return IssueInput{}, fmt.Errorf("decode ticket issuance job order line ID: %w", err)
	}
	sessionID, err := identifier.Parse(payload.SessionID)
	if err != nil {
		return IssueInput{}, fmt.Errorf("decode ticket issuance job session ID: %w", err)
	}
	parseTime := func(field, value string) (time.Time, error) {
		parsed, parseErr := time.Parse(time.RFC3339Nano, value)
		if parseErr != nil || parsed.IsZero() {
			return time.Time{}, fmt.Errorf("decode ticket issuance job %s: invalid timestamp", field)
		}
		return parsed.UTC(), nil
	}
	notBefore, err := parseTime("not_before", payload.NotBefore)
	if err != nil {
		return IssueInput{}, err
	}
	expiresAt, err := parseTime("expires_at", payload.ExpiresAt)
	if err != nil {
		return IssueInput{}, err
	}
	entryOpensAt, err := parseTime("entry_opens_at", payload.EntryOpensAt)
	if err != nil {
		return IssueInput{}, err
	}
	entryClosesAt, err := parseTime("entry_closes_at", payload.EntryClosesAt)
	if err != nil {
		return IssueInput{}, err
	}
	return IssueInput{OrganizationID: organizationID, EntitlementID: entitlementID, OrderLineID: orderLineID, SessionID: sessionID, PublicReference: payload.PublicReference, NotBefore: notBefore, ExpiresAt: expiresAt, EntryOpensAt: entryOpensAt, EntryClosesAt: entryClosesAt}, nil
}
