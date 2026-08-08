package observability

import (
	"context"
	"errors"
	"log/slog"
	"os"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	logglobal "go.opentelemetry.io/otel/log/global"
)

// NewSlogLogger returns an *slog.Logger that fans out to both stdout (so
// `docker compose logs app` / `make run` keep working exactly as before) and
// whatever logger provider New installed — which, when telemetry is enabled,
// ships records via OTLP to lgtm's embedded collector, which forwards them to
// Loki, which is what actually makes them show up in Grafana. Call this after
// New and pass the result to slog.SetDefault so every existing slog.Info/Error
// call in the app is covered without changing call sites.
func NewSlogLogger() *slog.Logger {
	stdout := slog.NewTextHandler(os.Stdout, nil)
	otelHandler := otelslog.NewHandler(instrumentationName, otelslog.WithLoggerProvider(logglobal.GetLoggerProvider()))
	return slog.New(newFanoutHandler(stdout, otelHandler))
}

// fanoutHandler is a minimal slog.Handler that forwards every record to each of
// its handlers — the stdlib doesn't ship one, and pulling in a dependency for
// ~30 lines wasn't worth it.
type fanoutHandler struct {
	handlers []slog.Handler
}

func newFanoutHandler(handlers ...slog.Handler) slog.Handler {
	return &fanoutHandler{handlers: handlers}
}

func (f *fanoutHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range f.handlers {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (f *fanoutHandler) Handle(ctx context.Context, record slog.Record) error {
	var errs []error
	for _, h := range f.handlers {
		if !h.Enabled(ctx, record.Level) {
			continue
		}
		if err := h.Handle(ctx, record.Clone()); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (f *fanoutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		next[i] = h.WithAttrs(attrs)
	}
	return &fanoutHandler{handlers: next}
}

func (f *fanoutHandler) WithGroup(name string) slog.Handler {
	next := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		next[i] = h.WithGroup(name)
	}
	return &fanoutHandler{handlers: next}
}
