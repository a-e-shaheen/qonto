package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"

	"qonto-bulk-transfer/internal/transfer/model"
)

// FindAccountForUpdate looks up the bank account matching iban+bic and locks its
// row (SELECT ... FOR UPDATE) for the remainder of the caller's transaction. Must
// only be called from inside a txn.Atomic block — the lock is meaningless (and the
// call fails outside a transaction, since Postgres rejects FOR UPDATE without one)
// otherwise.
func (r *Repository) FindAccountForUpdate(ctx context.Context, iban, bic string) (model.Account, bool, error) {
	start := time.Now()
	defer func() { lockWaitDuration.Record(ctx, time.Since(start).Seconds()) }()

	sqlStr, args, err := psql.Select("id", "organization_name", "balance_cents", "iban", "bic").
		From("bank_accounts").
		Where(sq.Eq{"iban": iban, "bic": bic}).
		Suffix("FOR UPDATE").
		ToSql()
	if err != nil {
		return model.Account{}, false, fmt.Errorf("build find account query: %w", err)
	}

	var a model.Account
	err = r.db(ctx).QueryRow(ctx, sqlStr, args...).
		Scan(&a.ID, &a.OrganizationName, &a.BalanceCents, &a.IBAN, &a.BIC)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Account{}, false, nil
	}
	if err != nil {
		return model.Account{}, false, fmt.Errorf("find account for update: %w", err)
	}
	return a, true, nil
}

// DebitAccount subtracts amountCents from the account's balance and returns the
// resulting balance, so the caller can enforce the never-negative invariant
// against the value Postgres actually committed rather than a locally-computed
// one. Must be called after FindAccountForUpdate has already verified sufficient
// funds under the same transaction's row lock — this method itself does not
// re-check the balance.
func (r *Repository) DebitAccount(ctx context.Context, accountID, amountCents int64) (int64, error) {
	sqlStr, args, err := psql.Update("bank_accounts").
		Set("balance_cents", sq.Expr("balance_cents - ?", amountCents)).
		Where(sq.Eq{"id": accountID}).
		Suffix("RETURNING balance_cents").
		ToSql()
	if err != nil {
		return 0, fmt.Errorf("build debit account query: %w", err)
	}

	var newBalance int64
	if err := r.db(ctx).QueryRow(ctx, sqlStr, args...).Scan(&newBalance); err != nil {
		return 0, fmt.Errorf("debit account: %w", err)
	}
	return newBalance, nil
}
