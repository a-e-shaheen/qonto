// Command server runs the bulk-transfer HTTP API: it loads config, boots
// telemetry, opens the database pool, runs migrations and the startup seed, wires
// the store/service/handler layers, and serves until SIGINT/SIGTERM.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/caarlos0/env/v11"

	"qonto-bulk-transfer/internal/transfer/server"
	"qonto-bulk-transfer/internal/transfer/service"
	"qonto-bulk-transfer/internal/transfer/store"
	"qonto-bulk-transfer/pkg/database"
	"qonto-bulk-transfer/pkg/observability"
	pkgserver "qonto-bulk-transfer/pkg/server"
	"qonto-bulk-transfer/pkg/txn"
)

type config struct {
	Database      database.Config      `envPrefix:"DB_"`
	HTTP          pkgserver.Config     `envPrefix:"HTTP_"`
	Telemetry     observability.Config //nolint:govet // env tags live on Config's own fields
	MigrationsDir string               `env:"MIGRATIONS_DIR" envDefault:"migrations"`
	SeedOnStartup bool                 `env:"SEED_ON_STARTUP" envDefault:"true"`
}

func main() {
	if err := run(); err != nil {
		slog.Error("fatal error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()

	var cfg config
	if err := env.Parse(&cfg); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	telemetry, err := observability.New(ctx, cfg.Telemetry)
	if err != nil {
		return fmt.Errorf("init telemetry: %w", err)
	}
	// Every existing slog.Info/Error call in the app (this file, pkg/server's
	// request-logging middleware, etc.) now ships via OTLP to lgtm's Loki,
	// alongside stdout, with zero changes at those call sites.
	slog.SetDefault(observability.NewSlogLogger())

	pool, err := database.NewPool(ctx, cfg.Database)
	if err != nil {
		return fmt.Errorf("init database pool: %w", err)
	}
	defer pool.Close()

	if err := database.Bootstrap(ctx, pool, cfg.MigrationsDir, cfg.Database.DSN, cfg.SeedOnStartup); err != nil {
		return fmt.Errorf("bootstrap database: %w", err)
	}

	repo := store.New(pool)
	transactor := txn.New(pool)
	svc := service.New(repo, transactor)
	handler := server.New(svc)

	httpServer := pkgserver.New(cfg.HTTP, pkgserver.LoggingMiddleware(handler.Routes()))

	return pkgserver.RunUntilSignal(ctx, httpServer.RunWithContext, func(shutdownCtx context.Context) {
		if err := telemetry.Shutdown(shutdownCtx); err != nil {
			slog.Error("shutdown telemetry", "error", err)
		}
	})
}
