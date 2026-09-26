package postgres

import (
	"testing"
	"time"
)

func TestScheduleIntervalSecondsRequiresWholeBoundedSeconds(t *testing.T) {
	for _, test := range []struct {
		name     string
		interval time.Duration
		want     int32
		valid    bool
	}{
		{name: "one second", interval: time.Second, want: 1, valid: true},
		{name: "one minute", interval: time.Minute, want: 60, valid: true},
		{name: "subsecond", interval: 500 * time.Millisecond},
		{name: "fractional second", interval: 1500 * time.Millisecond},
		{name: "too long", interval: 25 * time.Hour},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := scheduleIntervalSeconds(test.interval)
			if test.valid {
				if err != nil || got != test.want {
					t.Fatalf("scheduleIntervalSeconds() = %d, %v; want %d, nil", got, err, test.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("scheduleIntervalSeconds(%s) unexpectedly succeeded with %d", test.interval, got)
			}
		})
	}
}
