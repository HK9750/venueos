// Package validation collects bounded, transport-neutral field validation details.
package validation

import "github.com/HK9750/venueos/internal/platform/apperror"

// MaxDetails is the maximum number of validation details exposed to a caller.
const MaxDetails = apperror.MaxDetails

// Collector accumulates validation failures. Its zero value is ready for use.
type Collector struct {
	details []apperror.Detail
}

// Add records a safe field or rule failure. Additional failures beyond MaxDetails
// are intentionally omitted to keep error responses bounded.
func (collector *Collector) Add(field, code, message string) {
	if len(collector.details) >= MaxDetails {
		return
	}
	collector.details = append(collector.details, apperror.Detail{
		Field: field, Code: code, Message: message,
	})
}

func (collector *Collector) HasErrors() bool { return len(collector.details) > 0 }

// Details returns a copy that callers may safely transform for transport.
func (collector *Collector) Details() []apperror.Detail {
	return append([]apperror.Detail(nil), collector.details...)
}

// Error returns nil when the collector is empty or a classified validation error.
func (collector *Collector) Error(message string) error {
	if !collector.HasErrors() {
		return nil
	}
	return apperror.New(apperror.CodeValidationFailed, message, collector.details...)
}
