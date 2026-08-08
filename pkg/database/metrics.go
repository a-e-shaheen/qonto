package database

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/metric"

	"qonto-bulk-transfer/pkg/observability"
)

// concurrencyConflicts counts Postgres serialization failures (40001) and
// deadlocks (40P01) across every query. Given this service only ever takes one
// row lock per transaction (the bank_accounts FOR UPDATE), deadlocks aren't
// expected in normal operation — a nonzero count here means either lock ordering
// assumptions broke, or (for 40001) something is running at SERIALIZABLE
// isolation somewhere it shouldn't be.
var concurrencyConflicts = newConcurrencyConflictsCounter()

func newConcurrencyConflictsCounter() metric.Int64Counter {
	c, _ := observability.Meter().Int64Counter("db_concurrency_conflicts_total",
		metric.WithDescription("Postgres serialization failures or deadlocks across all queries"))
	return c
}

func isConcurrencyConflict(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "40001" || pgErr.Code == "40P01"
}

// queryDuration and queriesTotal cover every individual SQL statement and
// every CopyFrom, labeled by operation (SELECT/INSERT/UPDATE/COPY/...) and
// outcome. Unlike the per-query OTel spans in querytracer.go — which only
// exist if the surrounding request happened to be trace-sampled — these are
// recorded unconditionally, so query latency/throughput/error-rate stay
// dashboardable and alertable even at a fractional trace sample ratio.
var queryDuration = newQueryDurationHistogram()
var queriesTotal = newQueriesTotalCounter()

func newQueryDurationHistogram() metric.Float64Histogram {
	h, _ := observability.Meter().Float64Histogram("db_query_duration_seconds",
		metric.WithDescription("Duration of individual SQL statements and COPY operations, by operation"),
		metric.WithUnit("s"))
	return h
}

func newQueriesTotalCounter() metric.Int64Counter {
	c, _ := observability.Meter().Int64Counter("db_queries_total",
		metric.WithDescription("Individual SQL statements and COPY operations executed, by operation and outcome"))
	return c
}

// registerPoolMetrics exposes pgx's own pool statistics as an OTel observable
// gauge. pgxpool only tracks cumulative acquire count/duration, not a per-acquire
// value, so this reports the cumulative average wait per acquire since process
// start — an honest approximation given what pgx actually instruments, not a true
// per-request histogram.
func registerPoolMetrics(pool *pgxpool.Pool) error {
	_, err := observability.Meter().Float64ObservableGauge(
		"db_connection_pool_wait_duration_seconds",
		metric.WithDescription("Cumulative average time spent waiting to acquire a pooled connection"),
		metric.WithUnit("s"),
		metric.WithFloat64Callback(func(_ context.Context, o metric.Float64Observer) error {
			stat := pool.Stat()
			if stat.AcquireCount() == 0 {
				o.Observe(0)
				return nil
			}
			o.Observe(stat.AcquireDuration().Seconds() / float64(stat.AcquireCount()))
			return nil
		}),
	)
	return err
}
