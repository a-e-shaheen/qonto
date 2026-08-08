package database

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"qonto-bulk-transfer/pkg/observability"
)

// queryTiming is stashed in ctx between a Trace*Start and its matching
// Trace*End call, carrying what End needs but Start is the only one given:
// when the operation began, and what kind of operation it was (Start sees the
// SQL text; End only sees the outcome).
type queryTiming struct {
	start     time.Time
	operation string
}

type queryTimingKey struct{}

// QueryTracer implements pgx.QueryTracer and pgx.CopyFromTracer, so every SQL
// statement *and* every COPY (see internal/transfer/store's bulk transaction
// insert — the one write in this codebase that doesn't go through
// TraceQueryStart/End at all) gets both an OTel span and a
// db_query_duration_seconds/db_queries_total metric.
type QueryTracer struct{}

// NewQueryTracer builds a QueryTracer.
func NewQueryTracer() *QueryTracer {
	return &QueryTracer{}
}

// TraceQueryStart implements pgx.QueryTracer.
func (t *QueryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	ctx = context.WithValue(ctx, queryTimingKey{}, queryTiming{start: time.Now(), operation: sqlOperation(data.SQL)})
	if !trace.SpanFromContext(ctx).IsRecording() {
		return ctx
	}
	attrs := []attribute.KeyValue{
		attribute.String("db.system", "postgresql"),
		attribute.String("db.statement", data.SQL),
	}
	attrs = append(attrs, queryArgsAttr(data.Args)...)
	ctx, _ = observability.StartSpan(ctx, phasedSpanName(ctx, "postgres.query"),
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attrs...),
	)
	return ctx
}

// queryArgsAttr renders a query's bound parameters as a single JSON-array
// attribute (e.g. `["FR104746...", "OIVUSCLQXXX"]`) — this is exactly what "what
// was actually passed for this SELECT/UPDATE" means when reading a trace, which
// db.statement alone (just the "$1, $2" placeholders) can't answer. Every value
// that reaches here is either an account identifier, an amount, or an
// idempotency key — none of it is a secret that needs redacting, unlike, say, an
// auth token — so nothing is filtered out. Returns nil (no attribute at all) for
// a parameterless statement or if a value fails to marshal, rather than
// attaching an empty/broken attribute.
func queryArgsAttr(args []any) []attribute.KeyValue {
	if len(args) == 0 {
		return nil
	}
	argsJSON, err := json.Marshal(args)
	if err != nil {
		return nil
	}
	return []attribute.KeyValue{attribute.String("db.args", string(argsJSON))}
}

// TraceQueryEnd implements pgx.QueryTracer.
func (t *QueryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	recordQueryMetric(ctx, data.Err)
	endSpan(ctx, data.Err)
}

// TraceCopyFromStart implements pgx.CopyFromTracer.
func (t *QueryTracer) TraceCopyFromStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceCopyFromStartData) context.Context {
	ctx = context.WithValue(ctx, queryTimingKey{}, queryTiming{start: time.Now(), operation: "COPY"})
	if !trace.SpanFromContext(ctx).IsRecording() {
		return ctx
	}
	ctx, _ = observability.StartSpan(ctx, phasedSpanName(ctx, "postgres.copy_from"),
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("db.system", "postgresql"),
			attribute.String("db.sql.table", strings.Join(data.TableName, ".")),
		),
	)
	return ctx
}

// TraceCopyFromEnd implements pgx.CopyFromTracer.
func (t *QueryTracer) TraceCopyFromEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceCopyFromEndData) {
	recordQueryMetric(ctx, data.Err)
	endSpan(ctx, data.Err)
}

// phasedSpanName prefixes base with whatever business-level phase the caller
// tagged onto ctx via observability.WithPhase (e.g. "check_idempotency:
// postgres.query"), so a trace waterfall shows which step a query belongs to
// without a separate span level for that step — set once per phase, in the
// service layer, at the call site rather than here.
func phasedSpanName(ctx context.Context, base string) string {
	if phase, ok := observability.PhaseFromContext(ctx); ok && phase != "" {
		return phase + ": " + base
	}
	return base
}

func endSpan(ctx context.Context, err error) {
	span := trace.SpanFromContext(ctx)
	defer span.End()
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		// Independent of the span-recording gate above: concurrency-conflict
		// counts must not depend on trace sampling.
		if isConcurrencyConflict(err) {
			concurrencyConflicts.Add(ctx, 1)
		}
	}
}

func recordQueryMetric(ctx context.Context, err error) {
	timing, ok := ctx.Value(queryTimingKey{}).(queryTiming)
	if !ok {
		return
	}
	status := "ok"
	if err != nil {
		status = "error"
	}
	attrs := metric.WithAttributes(
		attribute.String("operation", timing.operation),
		attribute.String("status", status),
	)
	queryDuration.Record(ctx, time.Since(timing.start).Seconds(), attrs)
	queriesTotal.Add(ctx, 1, attrs)
}

// sqlOperation extracts a low-cardinality label from a SQL statement's first
// keyword (SELECT/INSERT/UPDATE/...) rather than using the raw statement text,
// which would make every distinct query its own metric series.
func sqlOperation(sql string) string {
	sql = strings.TrimSpace(sql)
	if sql == "" {
		return "UNKNOWN"
	}
	end := strings.IndexAny(sql, " \t\n(")
	if end == -1 {
		end = len(sql)
	}
	return strings.ToUpper(sql[:end])
}
