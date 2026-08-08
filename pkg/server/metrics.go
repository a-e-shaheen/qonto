package server

import (
	"net/http"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"qonto-bulk-transfer/pkg/observability"
)

// redMetrics holds the RED-pattern (Rate/Errors/Duration) HTTP instruments,
// created once against the process-wide meter and shared by every route's
// MetricsMiddleware wrapper.
type redMetrics struct {
	requestsTotal metric.Int64Counter
	duration      metric.Float64Histogram
	inFlight      metric.Int64UpDownCounter
}

// httpMetrics is created at package-init time. This is safe even before a real
// MeterProvider is installed (observability.New runs later, during app startup):
// otel.Meter returns a forwarding proxy, and instruments created against it start
// recording for real once the real provider is set — see observability.Meter.
var httpMetrics = newRedMetrics()

func newRedMetrics() redMetrics {
	m := observability.Meter()

	requestsTotal, _ := m.Int64Counter("http_requests_total",
		metric.WithDescription("Total incoming HTTP requests, by method/route/status_code"))
	duration, _ := m.Float64Histogram("http_request_duration_seconds",
		metric.WithDescription("End-to-end HTTP request latency"),
		metric.WithUnit("s"))
	inFlight, _ := m.Int64UpDownCounter("http_in_flight_requests",
		metric.WithDescription("HTTP requests currently being processed"))

	return redMetrics{requestsTotal: requestsTotal, duration: duration, inFlight: inFlight}
}

// MetricsMiddleware wraps a single handler with RED metrics labeled by the given
// route. It's applied per-route (at registration time, one call per pattern) —
// not as one blanket wrapper over an entire mux — specifically so `route` is the
// registered pattern (e.g. "GET /accounts/{id}/transactions") rather than the raw
// request path, which would blow up label cardinality with one series per account
// ID.
func MetricsMiddleware(route string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		methodAttr := attribute.String("method", r.Method)
		routeAttr := attribute.String("route", route)

		httpMetrics.inFlight.Add(ctx, 1, metric.WithAttributes(methodAttr, routeAttr))
		defer httpMetrics.inFlight.Add(ctx, -1, metric.WithAttributes(methodAttr, routeAttr))

		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		attrs := metric.WithAttributes(methodAttr, routeAttr, attribute.Int("status_code", rec.status))
		httpMetrics.requestsTotal.Add(ctx, 1, attrs)
		httpMetrics.duration.Record(ctx, time.Since(start).Seconds(), metric.WithAttributes(methodAttr, routeAttr))
	})
}
