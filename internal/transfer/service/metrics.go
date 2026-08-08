package service

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/metric"

	"qonto-bulk-transfer/pkg/observability"
)

// balanceInvariantViolations should never fire: FindAccountForUpdate's row lock
// plus the balance>=total check earlier in the same transaction make a negative
// post-debit balance impossible under correct code. It exists as a tripwire —
// page immediately if it ever increments, since it means that guarantee broke.
var balanceInvariantViolations = newBalanceInvariantCounter()

func newBalanceInvariantCounter() metric.Int64Counter {
	c, _ := observability.Meter().Int64Counter("balance_invariant_violations_total",
		metric.WithDescription("A bug or race condition let an account balance go negative — critical, page immediately if > 0"))
	return c
}

// checkBalanceInvariant records and returns an error if newBalance is negative,
// which aborts the enclosing txn.Atomic block (rolling back rather than
// committing a corrupted balance).
func checkBalanceInvariant(ctx context.Context, accountID, newBalance int64) error {
	if newBalance >= 0 {
		return nil
	}
	balanceInvariantViolations.Add(ctx, 1)
	err := fmt.Errorf("balance invariant violated: account %d balance is %d cents after debit", accountID, newBalance)
	observability.RecordError(ctx, err)
	return err
}
