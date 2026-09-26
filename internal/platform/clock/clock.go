// Package clock defines injectable UTC time sources for domain and application code.
package clock

import "time"

// Clock supplies the current time.
type Clock interface {
	Now() time.Time
}

// System supplies wall-clock time normalized to UTC.
type System struct{}

func (System) Now() time.Time { return time.Now().UTC() }

// Fixed supplies one immutable instant, useful for deterministic tests.
type Fixed struct {
	instant time.Time
}

func NewFixed(instant time.Time) Fixed { return Fixed{instant: instant.UTC()} }

func (clock Fixed) Now() time.Time { return clock.instant }
