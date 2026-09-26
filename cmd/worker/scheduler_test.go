package main

import (
	"testing"
	"time"
)

func TestRecurringSchedulesAreBoundedAndDueImmediately(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	schedules := recurringSchedules(now)
	if len(schedules) != 4 {
		t.Fatalf("schedule count = %d, want 4", len(schedules))
	}
	if schedules[0].NextRunAt != now || schedules[1].NextRunAt != now || schedules[2].NextRunAt != now || schedules[3].NextRunAt != now {
		t.Fatalf("schedules should be due immediately: %#v", schedules)
	}
	if schedules[0].Interval != time.Minute || schedules[1].Interval != 5*time.Second || schedules[2].Interval != 10*time.Minute || schedules[3].Interval != time.Hour {
		t.Fatalf("unexpected schedule intervals: %#v", schedules)
	}
}
