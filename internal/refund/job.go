package refund

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/HK9750/venueos/internal/platform/identifier"
)

const (
	ExecuteJobType                = "refund.execute"
	ExecuteJobSchemaVersion int32 = 1
	maxExecuteJobBytes            = 2048
)

type ExecuteJobPayload struct {
	OrganizationID string `json:"organization_id"`
	RefundID       string `json:"refund_id"`
}

func EncodeExecuteJobPayload(input ExecuteInput) ([]byte, error) {
	if input.OrganizationID.IsZero() || input.RefundID.IsZero() {
		return nil, fmt.Errorf("refund execution job identifiers are required")
	}
	raw, err := json.Marshal(ExecuteJobPayload{OrganizationID: input.OrganizationID.String(), RefundID: input.RefundID.String()})
	if err != nil {
		return nil, fmt.Errorf("encode refund execution job: %w", err)
	}
	if len(raw) > maxExecuteJobBytes {
		return nil, fmt.Errorf("refund execution job exceeds %d bytes", maxExecuteJobBytes)
	}
	return raw, nil
}

func DecodeExecuteJobPayload(raw []byte) (ExecuteInput, error) {
	if len(raw) == 0 || len(raw) > maxExecuteJobBytes {
		return ExecuteInput{}, fmt.Errorf("refund execution job payload is outside the allowed size")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var payload ExecuteJobPayload
	if err := decoder.Decode(&payload); err != nil {
		return ExecuteInput{}, fmt.Errorf("decode refund execution job: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return ExecuteInput{}, fmt.Errorf("decode refund execution job: trailing data")
	}
	organizationID, err := identifier.Parse(payload.OrganizationID)
	if err != nil {
		return ExecuteInput{}, fmt.Errorf("decode refund execution job organization ID: %w", err)
	}
	refundID, err := identifier.Parse(payload.RefundID)
	if err != nil {
		return ExecuteInput{}, fmt.Errorf("decode refund execution job refund ID: %w", err)
	}
	return ExecuteInput{OrganizationID: organizationID, RefundID: refundID}, nil
}
