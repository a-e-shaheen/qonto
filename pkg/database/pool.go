// Package database owns pgx pool construction, schema migrations, and the
// startup-seed step — the parts of the app that only run once at boot, as opposed to
// the per-request store layer in internal/transfer/store.
package database

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Config configures the connection pool. DSN is the only required field; the rest
// have sane defaults for a small service.
type Config struct {
	DSN      string `env:"DSN,required"`
	MaxConns int32  `env:"MAX_CONNS" envDefault:"10"`
}

// NewPool opens a pgx connection pool and verifies it's reachable before returning.
// Every query executed through the pool gets its own OTel span (see querytracer.go),
// nested under whatever span is already in the calling context.
func NewPool(ctx context.Context, cfg Config) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	poolCfg.MaxConns = cfg.MaxConns
	poolCfg.ConnConfig.Tracer = NewQueryTracer()

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create connection pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	if err := registerPoolMetrics(pool); err != nil {
		pool.Close()
		return nil, fmt.Errorf("register pool metrics: %w", err)
	}
	return pool, nil
}
