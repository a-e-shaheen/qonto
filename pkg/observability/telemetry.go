// Package observability bootstraps OpenTelemetry tracing, metrics, and logs, and
// provides small helpers used consistently across handler, service, and store
// layers so that a context.Context carrying an active span produces a connected
// trace end to end, every layer records metrics against the same meter, and
// log/slog output ships through the same pipeline — regardless of which layer
// started it.
package observability

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	logglobal "go.opentelemetry.io/otel/log/global"
	lognoop "go.opentelemetry.io/otel/log/noop"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	nooptrace "go.opentelemetry.io/otel/trace/noop"
)

// Config controls whether and how traces, metrics, and logs are exported.
// Leaving OTLPEndpoint empty disables all three (no-op providers are installed)
// rather than failing startup — useful for running the binary with no collector
// around.
type Config struct {
	ServiceName    string  `env:"OTEL_SERVICE_NAME" envDefault:"qonto-bulk-transfer"`
	OTLPEndpoint   string  `env:"OTEL_EXPORTER_OTLP_ENDPOINT"`
	OTLPInsecure   bool    `env:"OTEL_EXPORTER_OTLP_INSECURE" envDefault:"true"`
	SampleRatio    float64 `env:"OTEL_TRACE_SAMPLE_RATIO" envDefault:"1"`
	MetricInterval string  `env:"OTEL_METRIC_EXPORT_INTERVAL" envDefault:"10s"`
}

// Telemetry owns the process-wide tracer/meter/logger providers and their clean
// shutdown.
type Telemetry struct {
	tracerProvider *sdktrace.TracerProvider
	meterProvider  *sdkmetric.MeterProvider
	loggerProvider *sdklog.LoggerProvider
}

// New configures the global OTel tracer, meter, and logger providers from cfg.
// Call Shutdown on graceful shutdown to flush anything still buffered for
// export. Call NewSlogLogger afterward (and slog.SetDefault it) to route
// log/slog output through the logger provider installed here.
func New(ctx context.Context, cfg Config) (*Telemetry, error) {
	if cfg.OTLPEndpoint == "" {
		otel.SetTracerProvider(nooptrace.NewTracerProvider())
		otel.SetMeterProvider(metricnoop.NewMeterProvider())
		logglobal.SetLoggerProvider(lognoop.NewLoggerProvider())
		return &Telemetry{}, nil
	}

	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(
		semconv.ServiceName(cfg.ServiceName),
	))
	if err != nil {
		return nil, fmt.Errorf("build otel resource: %w", err)
	}

	tp, err := newTracerProvider(ctx, cfg, res)
	if err != nil {
		return nil, err
	}
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))

	mp, err := newMeterProvider(ctx, cfg, res)
	if err != nil {
		return nil, err
	}
	otel.SetMeterProvider(mp)

	lp, err := newLoggerProvider(ctx, cfg, res)
	if err != nil {
		return nil, err
	}
	logglobal.SetLoggerProvider(lp)

	return &Telemetry{tracerProvider: tp, meterProvider: mp, loggerProvider: lp}, nil
}

func newTracerProvider(ctx context.Context, cfg Config, res *resource.Resource) (*sdktrace.TracerProvider, error) {
	exporterOpts := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(cfg.OTLPEndpoint)}
	if cfg.OTLPInsecure {
		exporterOpts = append(exporterOpts, otlptracegrpc.WithInsecure())
	}
	exporter, err := otlptracegrpc.New(ctx, exporterOpts...)
	if err != nil {
		return nil, fmt.Errorf("create otlp trace exporter: %w", err)
	}

	return sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SampleRatio))),
	), nil
}

func newMeterProvider(ctx context.Context, cfg Config, res *resource.Resource) (*sdkmetric.MeterProvider, error) {
	interval, err := time.ParseDuration(cfg.MetricInterval)
	if err != nil {
		return nil, fmt.Errorf("parse OTEL_METRIC_EXPORT_INTERVAL: %w", err)
	}

	exporterOpts := []otlpmetricgrpc.Option{otlpmetricgrpc.WithEndpoint(cfg.OTLPEndpoint)}
	if cfg.OTLPInsecure {
		exporterOpts = append(exporterOpts, otlpmetricgrpc.WithInsecure())
	}
	exporter, err := otlpmetricgrpc.New(ctx, exporterOpts...)
	if err != nil {
		return nil, fmt.Errorf("create otlp metric exporter: %w", err)
	}

	return sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter, sdkmetric.WithInterval(interval))),
	), nil
}

// newLoggerProvider exports log/slog records (via NewSlogLogger's bridge) over
// OTLP to the same collector traces/metrics already go to — lgtm's embedded
// collector forwards OTLP logs to Loki, which is what actually makes them show
// up in Grafana; log/slog alone only ever wrote to this process's stdout.
func newLoggerProvider(ctx context.Context, cfg Config, res *resource.Resource) (*sdklog.LoggerProvider, error) {
	exporterOpts := []otlploggrpc.Option{otlploggrpc.WithEndpoint(cfg.OTLPEndpoint)}
	if cfg.OTLPInsecure {
		exporterOpts = append(exporterOpts, otlploggrpc.WithInsecure())
	}
	exporter, err := otlploggrpc.New(ctx, exporterOpts...)
	if err != nil {
		return nil, fmt.Errorf("create otlp log exporter: %w", err)
	}

	return sdklog.NewLoggerProvider(
		sdklog.WithResource(res),
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exporter)),
	), nil
}

// Shutdown flushes any buffered spans/metrics/logs and stops the exporters. Safe
// to call even if telemetry was disabled (New with an empty endpoint).
func (t *Telemetry) Shutdown(ctx context.Context) error {
	if t.tracerProvider != nil {
		if err := t.tracerProvider.ForceFlush(ctx); err != nil {
			return fmt.Errorf("flush tracer provider: %w", err)
		}
		if err := t.tracerProvider.Shutdown(ctx); err != nil {
			return fmt.Errorf("shutdown tracer provider: %w", err)
		}
	}
	if t.meterProvider != nil {
		if err := t.meterProvider.ForceFlush(ctx); err != nil {
			return fmt.Errorf("flush meter provider: %w", err)
		}
		if err := t.meterProvider.Shutdown(ctx); err != nil {
			return fmt.Errorf("shutdown meter provider: %w", err)
		}
	}
	if t.loggerProvider != nil {
		if err := t.loggerProvider.ForceFlush(ctx); err != nil {
			return fmt.Errorf("flush logger provider: %w", err)
		}
		if err := t.loggerProvider.Shutdown(ctx); err != nil {
			return fmt.Errorf("shutdown logger provider: %w", err)
		}
	}
	return nil
}
