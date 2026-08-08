package database

import (
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres" // registers the "postgres" driver
	_ "github.com/golang-migrate/migrate/v4/source/file"       // registers the "file" source
)

// Migrate applies every pending migration under migrationsDir to the database at
// dsn. It is idempotent: running it against an already-up-to-date database is a
// no-op, so it's safe to call unconditionally on every startup.
func Migrate(migrationsDir, dsn string) error {
	m, err := migrate.New("file://"+migrationsDir, dsn)
	if err != nil {
		return fmt.Errorf("init migrate: %w", err)
	}
	defer func() { _, _ = m.Close() }()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("run migrations: %w", err)
	}
	return nil
}
