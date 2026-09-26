package validation

import (
	"testing"

	"github.com/HK9750/venueos/internal/platform/apperror"
)

func TestCollectorReturnsClassifiedBoundedError(t *testing.T) {
	var collector Collector
	if collector.Error("invalid") != nil {
		t.Fatal("empty collector returned an error")
	}
	for index := 0; index < MaxDetails+10; index++ {
		collector.Add("name", "invalid", "Name is invalid.")
	}

	err := collector.Error("The request is invalid.")
	code, ok := apperror.CodeOf(err)
	if !ok || code != apperror.CodeValidationFailed {
		t.Fatalf("CodeOf() = %q, %v", code, ok)
	}
	details := collector.Details()
	if len(details) != MaxDetails {
		t.Fatalf("details length = %d, want %d", len(details), MaxDetails)
	}
	details[0].Message = "mutated"
	if collector.Details()[0].Message != "Name is invalid." {
		t.Fatal("Details() exposed mutable collector state")
	}
}
