package apperror

import (
	"errors"
	"testing"
)

func TestStableCodesAreValid(t *testing.T) {
	codes := []Code{
		CodeMalformedRequest, CodeInvalidRequest, CodeValidationFailed,
		CodeInvalidCursor, CodeUnauthenticated, CodePermissionDenied,
		CodeNotFound, CodeConflict, CodeIdempotencyKeyReused,
		CodePreconditionFailed, CodePayloadTooLarge, CodeUnsupportedMediaType,
		CodeRateLimited, CodeInternal, CodeDependencyUnavailable,
		CodeDependencyTimeout,
	}
	for _, code := range codes {
		if !code.Valid() {
			t.Errorf("code %q is not valid", code)
		}
	}
	if Code("new_unreviewed_code").Valid() {
		t.Fatal("unknown code is valid")
	}
}

func TestErrorPreservesCauseAndProtectsDetails(t *testing.T) {
	cause := errors.New("database host and secret")
	details := []Detail{{Field: "currency", Code: "invalid", Message: "Use a supported currency."}}
	err := Wrap(cause, CodeValidationFailed, "The request is invalid.", details...)
	details[0].Message = "mutated"

	if !errors.Is(err, cause) {
		t.Fatal("wrapped cause is not preserved")
	}
	if err.Error() != "validation_failed: The request is invalid." {
		t.Fatalf("Error() = %q", err.Error())
	}
	returned := err.Details()
	returned[0].Message = "also mutated"
	if err.Details()[0].Message != "Use a supported currency." {
		t.Fatal("Details() exposed mutable internal state")
	}
	if code, ok := CodeOf(err); !ok || code != CodeValidationFailed {
		t.Fatalf("CodeOf() = %q, %v", code, ok)
	}
}

func TestErrorBoundsDetails(t *testing.T) {
	details := make([]Detail, MaxDetails+1)
	err := New(CodeValidationFailed, "The request is invalid.", details...)
	if got := len(err.Details()); got != MaxDetails {
		t.Fatalf("details length = %d, want %d", got, MaxDetails)
	}
}
