package txn

import (
	"go.opentelemetry.io/otel/metric"

	"qonto-bulk-transfer/pkg/observability"
)

// transactionDuration times the whole BEGIN...COMMIT/ROLLBACK block — how long the
// row lock(s) taken inside it are held, which is what determines how much a slow
// transaction throttles everyone else contending for the same account.
var transactionDuration = newTransactionDurationHistogram()

func newTransactionDurationHistogram() metric.Float64Histogram {
	h, _ := observability.Meter().Float64Histogram("db_transaction_duration_seconds",
		metric.WithDescription("Time spent inside a BEGIN...COMMIT/ROLLBACK block"),
		metric.WithUnit("s"))
	return h
}
