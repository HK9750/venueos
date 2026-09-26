package database

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrInvalidRetryPolicy = errors.New("invalid transaction retry policy")
	ErrRetriesExhausted   = errors.New("transaction retries exhausted")
	ErrNilTransactionWork = errors.New("transaction work must not be nil")
)

// TransactionBeginner is implemented by pgx pools and connections.
type TransactionBeginner interface {
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}

// RetryPolicy bounds retries for PostgreSQL serialization failures and deadlocks.
type RetryPolicy struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
}

// DefaultRetryPolicy is deliberately small because the entire transaction closure
// is repeated. Closures must not perform remote calls or other external side effects.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{MaxAttempts: 3, BaseDelay: 10 * time.Millisecond, MaxDelay: 100 * time.Millisecond}
}

// TransactionRunner executes short PostgreSQL transactions with bounded full-jitter
// retry for SQLSTATE 40001 (serialization failure) and 40P01 (deadlock detected).
type TransactionRunner struct {
	beginner TransactionBeginner
	policy   RetryPolicy
	sleep    func(context.Context, time.Duration) error
	jitter   func(time.Duration) time.Duration
}

func NewTransactionRunner(beginner TransactionBeginner, policy RetryPolicy) (*TransactionRunner, error) {
	if beginner == nil {
		return nil, fmt.Errorf("%w: transaction beginner is required", ErrInvalidRetryPolicy)
	}
	if policy.MaxAttempts < 1 || policy.MaxAttempts > 10 || policy.BaseDelay <= 0 || policy.MaxDelay < policy.BaseDelay {
		return nil, fmt.Errorf("%w: attempts must be 1-10 and delays must be positive and ordered", ErrInvalidRetryPolicy)
	}
	return &TransactionRunner{beginner: beginner, policy: policy, sleep: sleepContext, jitter: fullJitter}, nil
}

// Run invokes work inside a transaction. The work closure may be called more than
// once and therefore must contain only retry-safe database operations.
func (runner *TransactionRunner) Run(
	ctx context.Context,
	options pgx.TxOptions,
	work func(context.Context, pgx.Tx) error,
) error {
	if work == nil {
		return ErrNilTransactionWork
	}
	for attempt := 1; attempt <= runner.policy.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		tx, err := runner.beginner.BeginTx(ctx, options)
		if err != nil {
			return fmt.Errorf("begin transaction: %w", err)
		}

		err = work(ctx, tx)
		if err == nil {
			err = tx.Commit(ctx)
		} else {
			err = rollbackWithCause(ctx, tx, err)
		}
		if err == nil {
			return nil
		}
		if !IsRetryableTransactionError(err) {
			return err
		}
		if attempt == runner.policy.MaxAttempts {
			return fmt.Errorf("%w after %d attempts: %w", ErrRetriesExhausted, attempt, err)
		}
		if sleepErr := runner.sleep(ctx, runner.jitter(backoff(runner.policy, attempt))); sleepErr != nil {
			return sleepErr
		}
	}
	return ErrRetriesExhausted
}

// IsRetryableTransactionError classifies only the PostgreSQL concurrency failures
// for which replaying a side-effect-free transaction closure is safe.
func IsRetryableTransactionError(err error) bool {
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) {
		return false
	}
	return postgresError.Code == "40001" || postgresError.Code == "40P01"
}

func rollbackWithCause(ctx context.Context, tx pgx.Tx, cause error) error {
	rollbackErr := tx.Rollback(ctx)
	if rollbackErr == nil || errors.Is(rollbackErr, pgx.ErrTxClosed) {
		return cause
	}
	return errors.Join(cause, fmt.Errorf("rollback transaction: %w", rollbackErr))
}

func backoff(policy RetryPolicy, attempt int) time.Duration {
	delay := policy.BaseDelay
	for current := 1; current < attempt && delay < policy.MaxDelay; current++ {
		if delay > policy.MaxDelay/2 {
			return policy.MaxDelay
		}
		delay *= 2
	}
	if delay > policy.MaxDelay {
		return policy.MaxDelay
	}
	return delay
}

func fullJitter(maximum time.Duration) time.Duration {
	value, err := rand.Int(rand.Reader, big.NewInt(int64(maximum)+1))
	if err != nil {
		return maximum / 2
	}
	return time.Duration(value.Int64())
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
