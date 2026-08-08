// Package txn is a small unit-of-work helper: it opens a Postgres transaction and
// stashes it in the context so store-layer calls made through that context
// transparently join it, without a tx parameter being threaded through every
// service/store method signature. Kept single-driver (pgx) rather than
// generic-over-driver-type, since this project only ever talks to one database.
package txn

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"qonto-bulk-transfer/pkg/observability"
)

type txKey struct{}

// Beginner is satisfied by *pgxpool.Pool. Kept minimal so tests can fake it without
// a real pool.
type Beginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Transactor runs functions inside a single atomic Postgres transaction.
type Transactor struct {
	db Beginner
}

// New builds a Transactor bound to db (normally a *pgxpool.Pool).
func New(db Beginner) *Transactor {
	return &Transactor{db: db}
}

// FromContext returns the transaction stashed in ctx by an in-flight Atomic call, if
// any. Store-layer code uses this to pick the right execution target: the active
// transaction if one exists, otherwise the plain pool.
func FromContext(ctx context.Context) (pgx.Tx, bool) {
	tx, ok := ctx.Value(txKey{}).(pgx.Tx)
	return tx, ok
}

// Atomic runs fn inside one transaction. A nil return from fn commits; any other
// return value (or a panic, which is re-raised after rolling back) rolls back.
//
// A nil error from fn always means "the transaction completed cleanly" — that
// includes expected business outcomes like an insufficient-funds rejection, which
// fn should record as a normal write (see observability.RecordOutcome) and still
// return nil for. Only genuine failures (a DB error, a cancelled context, a panic)
// should cause fn to return a non-nil error; those are the only cases that mark the
// span below as errored, so on-call alerting on span status doesn't fire for
// ordinary declined transfers.
func (t *Transactor) Atomic(ctx context.Context, fn func(ctx context.Context) error) (err error) {
	ctx, span := observability.StartSpan(ctx, "txn.Atomic", trace.WithSpanKind(trace.SpanKindInternal))
	start := time.Now()
	defer func() {
		transactionDuration.Record(ctx, time.Since(start).Seconds())
		span.End()
	}()

	tx, err := t.db.Begin(ctx)
	if err != nil {
		observability.RecordError(ctx, fmt.Errorf("begin transaction: %w", err))
		return fmt.Errorf("begin transaction: %w", err)
	}

	txCtx := context.WithValue(ctx, txKey{}, tx)

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			observability.RecordError(ctx, fmt.Errorf("panic in atomic block: %v", p))
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			observability.RecordError(ctx, err)
			return
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			err = fmt.Errorf("commit transaction: %w", commitErr)
			observability.RecordError(ctx, err)
			return
		}
		span.SetStatus(codes.Ok, "")
	}()

	err = fn(txCtx)
	return err
}
