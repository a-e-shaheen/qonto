package observability

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

const instrumentationName = "qonto-bulk-transfer"

// Tracer returns the process-wide tracer. Layers call this instead of
// otel.Tracer(...) directly so the instrumentation name stays consistent everywhere.
func Tracer() trace.Tracer {
	return otel.Tracer(instrumentationName)
}

// Meter returns the process-wide meter. Layers call this instead of
// otel.Meter(...) directly, same reasoning as Tracer above. Safe to call — and to
// create instruments against — before New has run: otel.Meter returns a forwarding
// proxy that starts recording for real once the real MeterProvider is installed.
func Meter() metric.Meter {
	return otel.Meter(instrumentationName)
}

// StartSpan starts a child span under whatever span (if any) ctx already carries,
// which is how a trace stays connected as ctx is threaded from the HTTP handler,
// through the service, into the store, and down into pgx.
func StartSpan(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	return Tracer().Start(ctx, name, opts...)
}

// RecordOutcome marks the current span Ok and attaches an "outcome" attribute, plus
// any extra attributes the caller passes (e.g. the resulting HTTP status code) — the
// trace is where this information belongs, rather than persisted in the database
// alongside the idempotency record. Use this for expected business results
// (insufficient funds, account not found, a replayed retry) that are not
// infrastructure failures — reserve span.RecordError + codes.Error for genuine
// errors, so on-call alerting on span status doesn't page for ordinary declined
// transfers.
func RecordOutcome(ctx context.Context, outcome string, attrs ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(append([]attribute.KeyValue{attribute.String("outcome", outcome)}, attrs...)...)
	span.SetStatus(codes.Ok, "")
}

// RecordError marks the current span as failed. Use this for genuine infrastructure
// or unexpected errors, not for expected business outcomes.
func RecordError(ctx context.Context, err error) {
	span := trace.SpanFromContext(ctx)
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}
