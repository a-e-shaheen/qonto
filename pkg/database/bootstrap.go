package database

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"qonto-bulk-transfer/seed"
)

// Bootstrap runs pending migrations and, if seedOnStartup is set, loads the fixture
// dataset. Seeding is intentionally kept conceptually separate from migrations (see
// seed.Seed's own idempotency check) even though both run from this single startup
// call — schema definition and data loading are different concerns that happen to
// share a call site for convenience in local/dev.
func Bootstrap(ctx context.Context, pool *pgxpool.Pool, migrationsDir, dsn string, seedOnStartup bool) error {
	if err := Migrate(migrationsDir, dsn); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if seedOnStartup {
		if err := seed.NewSeeder(pool).Seed(ctx); err != nil {
			return fmt.Errorf("seed: %w", err)
		}
	}
	return nil
}
