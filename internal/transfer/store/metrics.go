package store

import (
	"go.opentelemetry.io/otel/metric"

	"qonto-bulk-transfer/pkg/observability"
)

// lockWaitDuration times FindAccountForUpdate's SELECT ... FOR UPDATE call.
// Postgres doesn't expose a clean "time spent purely waiting for the lock" signal
// to the client — this measures the whole call's latency, which is dominated by
// lock-wait time whenever there's real contention on the row (the query itself,
// against a single-row indexed lookup, is otherwise sub-millisecond).
var lockWaitDuration = newLockWaitHistogram()

func newLockWaitHistogram() metric.Float64Histogram {
	h, _ := observability.Meter().Float64Histogram("db_lock_wait_duration_seconds",
		metric.WithDescription("Latency of the SELECT ... FOR UPDATE bank_accounts lookup"),
		metric.WithUnit("s"))
	return h
}
