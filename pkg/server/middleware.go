package server

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/trace"

	"qonto-bulk-transfer/pkg/observability"
)

// maxLoggedBodyBytes caps how much of an error response body gets copied into the
// log line — enough for the {"error": "...", "details": [...]} shape every handler
// in this repo writes, without risking an unbounded log line if a body is ever huge.
const maxLoggedBodyBytes = 1024

// statusRecorder wraps http.ResponseWriter to capture the status code a handler
// wrote (the stdlib interface exposes no way to read it back) and, for error
// responses only, a copy of the body — so LoggingMiddleware can put the actual
// failure reason ("insufficient funds", a validation detail, ...) on the request
// log line instead of just the bare status code.
type statusRecorder struct {
	http.ResponseWriter
	status int
	body   []byte
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status >= http.StatusBadRequest && len(r.body) < maxLoggedBodyBytes {
		remaining := maxLoggedBodyBytes - len(r.body)
		if remaining > len(b) {
			remaining = len(b)
		}
		r.body = append(r.body, b[:remaining]...)
	}
	return r.ResponseWriter.Write(b)
}

// LoggingMiddleware starts the one root span for the whole request — replacing the
// per-handler StartSpan call each handler used to make on its own, since only the
// outermost layer can see the resulting trace/span ID after next.ServeHTTP returns
// to attach it to the log line below — then logs a single line per request. The
// message itself (not just its attributes) says "METHOD path -> status", and for a
// >=400 response appends the actual response body — the {"error": "...", "details":
// [...]} every handler in this repo writes — so the line is self-describing when
// read as plain text (a Grafana Logs panel, or `docker logs`, shows the message
// body by default; structured attributes only show up once a row is expanded, which
// made a static "http request" message useless on its own). The full detail is
// still attached as attributes too (method, path, status, duration_ms, trace/span
// ID, response_body) for structured filtering.
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		ctx, span := observability.StartSpan(r.Context(), r.Method+" "+r.URL.Path)
		defer span.End()
		r = r.WithContext(ctx)

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		msg := fmt.Sprintf("%s %s -> %d", r.Method, r.URL.Path, rec.status)
		if rec.status >= http.StatusBadRequest && len(rec.body) > 0 {
			msg += ": " + string(rec.body)
		}

		attrs := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration_ms", time.Since(start).Milliseconds(),
		}
		if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
			attrs = append(attrs, "trace_id", sc.TraceID().String(), "span_id", sc.SpanID().String())
		}
		if rec.status >= http.StatusBadRequest && len(rec.body) > 0 {
			attrs = append(attrs, "response_body", string(rec.body))
		}

		level := slog.LevelInfo
		if rec.status >= http.StatusInternalServerError {
			level = slog.LevelError
		} else if rec.status >= http.StatusBadRequest {
			level = slog.LevelWarn
		}
		slog.Log(ctx, level, msg, attrs...)
	})
}
