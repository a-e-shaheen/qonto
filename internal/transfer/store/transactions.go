package store

import (
	"context"
	"fmt"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"

	"qonto-bulk-transfer/internal/transfer/model"
)

// InsertTransactions bulk-loads one row per credit transfer via COPY — the fastest
// path pgx offers for inserting many rows in one call, and the reason a
// bulk-transfer batch's rows specifically (unlike every other write in this
// repository) don't go through squirrel: COPY is a distinct wire-protocol
// operation, not a builder-generated SQL statement. Must be called from inside a
// txn.Atomic block: outside one, this would run as its own autonomous, non-atomic
// operation against the pool instead of joining the caller's transaction.
func (r *Repository) InsertTransactions(ctx context.Context, accountID, batchID int64, transfers []model.CreditTransfer) error {
	rows := make([][]any, len(transfers))
	for i, t := range transfers {
		rows[i] = []any{
			t.CounterpartyName, t.CounterpartyIBAN, t.CounterpartyBIC,
			t.AmountCents, t.Currency, accountID, t.Description, batchID,
		}
	}

	_, err := r.db(ctx).CopyFrom(ctx,
		pgx.Identifier{"transactions"},
		[]string{
			"counterparty_name", "counterparty_iban", "counterparty_bic",
			"amount_cents", "amount_currency", "bank_account_id", "description", "batch_id",
		},
		pgx.CopyFromRows(rows),
	)
	if err != nil {
		return fmt.Errorf("insert transactions: %w", err)
	}
	return nil
}

// TransactionHistoryPage fetches up to limit transactions for accountID, ordered
// most-recent-first, starting after cursor (the zero Cursor means "start from the
// most recent"). Read-only — never expected to run inside a txn.Atomic block, so it
// falls back to the plain pool via db(ctx).
func (r *Repository) TransactionHistoryPage(ctx context.Context, accountID int64, cursor model.Cursor, limit int) ([]model.TransactionHistoryItem, error) {
	qb := psql.Select(
		"id", "counterparty_name", "counterparty_iban", "counterparty_bic",
		"amount_cents", "amount_currency", "description", "created_at",
	).
		From("transactions").
		Where(sq.Eq{"bank_account_id": accountID}).
		OrderBy("created_at DESC", "id DESC").
		Limit(uint64(limit))

	if !cursor.CreatedAt.IsZero() {
		qb = qb.Where(sq.Expr("(created_at, id) < (?, ?)", cursor.CreatedAt, cursor.ID))
	}

	sqlStr, args, err := qb.ToSql()
	if err != nil {
		return nil, fmt.Errorf("build transaction history query: %w", err)
	}

	rows, err := r.db(ctx).Query(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("query transaction history: %w", err)
	}
	defer rows.Close()

	var items []model.TransactionHistoryItem
	for rows.Next() {
		var item model.TransactionHistoryItem
		if err := rows.Scan(
			&item.ID, &item.CounterpartyName, &item.CounterpartyIBAN, &item.CounterpartyBIC,
			&item.AmountCents, &item.Currency, &item.Description, &item.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan transaction history row: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate transaction history rows: %w", err)
	}
	return items, nil
}
