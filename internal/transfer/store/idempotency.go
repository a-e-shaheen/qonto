package store

import (
	"context"
	"errors"
	"fmt"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"
)

// AdvisoryLock takes a transaction-scoped Postgres advisory lock keyed on key's
// hash, serializing any two in-flight requests that share the same idempotency key
// even before either has resolved which account it targets — the FOR UPDATE lock in
// FindAccountForUpdate only serializes requests that have already gotten that far.
// Released automatically on commit or rollback. Must be called from inside a
// txn.Atomic block.
func (r *Repository) AdvisoryLock(ctx context.Context, key string) error {
	if _, err := r.db(ctx).Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext($1))", key); err != nil {
		return fmt.Errorf("acquire advisory lock: %w", err)
	}
	return nil
}

// FindIdempotencyRecordStatus looks up the HTTP status code recorded for a
// previous attempt at key, if any. The status code alone is enough to replay the
// response: every outcome this service produces is fully determined by its status
// (204 always has no body, 404/422 always carry the same fixed message), so there's
// nothing else worth persisting per key — see service.responseBodyFor.
func (r *Repository) FindIdempotencyRecordStatus(ctx context.Context, key string) (int, bool, error) {
	sqlStr, args, err := psql.Select("response_status").
		From("idempotency_keys").
		Where(sq.Eq{"key": key}).
		ToSql()
	if err != nil {
		return 0, false, fmt.Errorf("build find idempotency record query: %w", err)
	}

	var status int
	err = r.db(ctx).QueryRow(ctx, sqlStr, args...).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("find idempotency record: %w", err)
	}
	return status, true, nil
}

// RecordIdempotency stores the final status code for key so a retried request can
// replay it instead of being re-evaluated. A unique-constraint violation (two
// concurrent inserts for the same key) is treated as "someone else just committed
// this key" rather than propagated as an error — defense in depth on top of
// AdvisoryLock, not the primary mechanism it relies on.
func (r *Repository) RecordIdempotency(ctx context.Context, key string, status int) error {
	sqlStr, args, err := psql.Insert("idempotency_keys").
		Columns("key", "response_status").
		Values(key, status).
		ToSql()
	if err != nil {
		return fmt.Errorf("build record idempotency query: %w", err)
	}
	if _, err := r.db(ctx).Exec(ctx, sqlStr, args...); err != nil {
		if isUniqueViolation(err) {
			return nil
		}
		return fmt.Errorf("record idempotency: %w", err)
	}
	return nil
}
