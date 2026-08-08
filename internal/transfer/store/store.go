// Package store is the SQL layer for the bulk-transfer feature: squirrel-built
// queries against bank_accounts, idempotency_keys, bulk_transfer_batches, and a
// pgx CopyFrom for the one bulk-row insert (transactions).
package store

import (
	"context"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"qonto-bulk-transfer/pkg/txn"
)

// psql is the shared squirrel statement builder, configured for Postgres's
// dollar-sign placeholders.
var psql = sq.StatementBuilder.PlaceholderFormat(sq.Dollar)

// DBTX is satisfied by both *pgxpool.Pool and pgx.Tx, so every method below runs
// against whichever one is appropriate without knowing which it got.
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	CopyFrom(ctx context.Context, tableName pgx.Identifier, columnNames []string, rowSrc pgx.CopyFromSource) (int64, error)
}

// Repository implements the bulk-transfer store against Postgres.
type Repository struct {
	pool DBTX
}

// New builds a Repository bound to pool (normally a *pgxpool.Pool).
func New(pool DBTX) *Repository {
	return &Repository{pool: pool}
}

// db picks the active transaction stashed by txn.Atomic if one exists for ctx,
// otherwise falls back to the plain pool. This is what lets the same method work
// both inside a service's Atomic block and for standalone reads (e.g. pagination)
// without a tx parameter on every call.
func (r *Repository) db(ctx context.Context) DBTX {
	if tx, ok := txn.FromContext(ctx); ok {
		return tx
	}
	return r.pool
}

// isUniqueViolation reports whether err is a Postgres unique-constraint violation
// (SQLSTATE 23505).
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if pe, ok := asPgError(err); ok {
		pgErr = pe
	} else {
		return false
	}
	return pgErr.Code == "23505"
}

func asPgError(err error) (*pgconn.PgError, bool) {
	var pgErr *pgconn.PgError
	if err == nil {
		return nil, false
	}
	if pe, ok := err.(*pgconn.PgError); ok {
		return pe, true
	}
	type wrapper interface{ Unwrap() error }
	for w, ok := err.(wrapper); ok; w, ok = err.(wrapper) {
		err = w.Unwrap()
		if pe, ok := err.(*pgconn.PgError); ok {
			return pe, true
		}
	}
	return pgErr, false
}
