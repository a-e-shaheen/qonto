package store

import (
	"context"
	"fmt"
)

// InsertBatch records the business fact of an accepted bulk-transfer batch,
// separate from the idempotency ledger (idempotency.go) and separate from the
// individual transaction rows it produced (transactions.go).
func (r *Repository) InsertBatch(ctx context.Context, accountID int64, idempotencyKey string, totalCents int64) (int64, error) {
	sqlStr, args, err := psql.Insert("bulk_transfer_batches").
		Columns("bank_account_id", "idempotency_key", "total_amount_cents").
		Values(accountID, idempotencyKey, totalCents).
		Suffix("RETURNING id").
		ToSql()
	if err != nil {
		return 0, fmt.Errorf("build insert batch query: %w", err)
	}

	var batchID int64
	if err := r.db(ctx).QueryRow(ctx, sqlStr, args...).Scan(&batchID); err != nil {
		return 0, fmt.Errorf("insert batch: %w", err)
	}
	return batchID, nil
}
