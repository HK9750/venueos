package requestid

import (
	"context"
	"testing"
)

func TestContextRoundTrip(t *testing.T) {
	ctx := WithContext(context.Background(), "request-1")
	if got := FromContext(ctx); got != "request-1" {
		t.Fatalf("FromContext() = %q, want request-1", got)
	}
}

func TestNew(t *testing.T) {
	first := New()
	second := New()
	if len(first) != 32 || first == second {
		t.Fatalf("New() returned %q and %q", first, second)
	}
}

func TestValid(t *testing.T) {
	if !Valid("client-request_123") {
		t.Fatal("Valid() rejected a safe request ID")
	}
	if Valid("request id with spaces") || Valid("") {
		t.Fatal("Valid() accepted an unsafe request ID")
	}
}
