package refund

import "testing"

func TestExecuteJobPayloadIsStrictAndTenantBound(t *testing.T) {
	input := ExecuteInput{OrganizationID: executionID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5"), RefundID: executionID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6")}
	raw, err := EncodeExecuteJobPayload(input)
	if err != nil {
		t.Fatalf("EncodeExecuteJobPayload() error = %v", err)
	}
	decoded, err := DecodeExecuteJobPayload(raw)
	if err != nil || decoded != input {
		t.Fatalf("DecodeExecuteJobPayload() = %#v, %v", decoded, err)
	}
	if _, err := DecodeExecuteJobPayload([]byte(`{"organization_id":"01890f3e-7b4c-7cc6-9c52-6d6f83394ef5","refund_id":"01890f3e-7b4c-7cc6-9c52-6d6f83394ef6","extra":true}`)); err == nil {
		t.Fatal("DecodeExecuteJobPayload accepted an unknown field")
	}
}
