package database

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestIsRetryableTransactionError(t *testing.T) {
	for _, code := range []string{"40001", "40P01"} {
		err := &pgconn.PgError{Code: code}
		if !IsRetryableTransactionError(err) {
			t.Errorf("SQLSTATE %s was not retryable", code)
		}
	}
	for _, err := range []error{errors.New("network failure"), &pgconn.PgError{Code: "23505"}} {
		if IsRetryableTransactionError(err) {
			t.Errorf("error %v was retryable", err)
		}
	}
}

func TestBackoffIsBounded(t *testing.T) {
	policy := RetryPolicy{MaxAttempts: 5, BaseDelay: 10 * time.Millisecond, MaxDelay: 25 * time.Millisecond}
	want := []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 25 * time.Millisecond, 25 * time.Millisecond}
	for index, expected := range want {
		if got := backoff(policy, index+1); got != expected {
			t.Errorf("backoff(%d) = %s, want %s", index+1, got, expected)
		}
	}
}
