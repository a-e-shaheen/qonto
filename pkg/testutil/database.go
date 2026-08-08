// Package testutil provides shared setup for integration tests that need a real
// Postgres instance (docker compose --profile testing up). Not built into the
// production binary — only ever imported from _test.go files.
package testutil

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" driver pgtestdb connects with
	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/golangmigrator"
)

func adminHost() string {
	if h := os.Getenv("TEST_DB_HOST"); h != "" {
		return h
	}
	return "localhost"
}

func adminPort() string {
	if p := os.Getenv("TEST_DB_PORT"); p != "" {
		return p
	}
	// Matches docker-compose.yml's "testing" profile mapping — remapped off the
	// default 5432 because this machine already runs another project's Postgres
	// there.
	return "5433"
}

// migrationsDir resolves the repo's migrations/ directory relative to this file's
// own location, rather than the caller's — so it works the same whether it's
// imported from internal/transfer/store or internal/transfer/service.
func migrationsDir() string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")
}

// OpenDB provisions a fresh, fully-migrated Postgres database for one test, cloned
// from a cached migrated template (see github.com/peterldowns/pgtestdb: migrations
// run once per unique migration-set hash, then every test gets its own database via
// fast CREATE DATABASE ... TEMPLATE). Every call is isolated — safe to run under
// t.Parallel(), with no shared state and no truncate-between-tests race, unlike a
// single shared database reset between tests.
func OpenDB(t *testing.T) *pgxpool.Pool {
	t.Helper()

	migrator := golangmigrator.New(migrationsDir())
	conf := pgtestdb.Custom(t, pgtestdb.Config{
		DriverName: "pgx",
		Host:       adminHost(),
		Port:       adminPort(),
		User:       "qonto",
		Password:   "qonto",
		Database:   "qonto",
		Options:    "sslmode=disable",
		// pg_partman's CREATE EXTENSION needs a superuser-capable role — matching
		// what this app's own migration user needs in real deployments too;
		// pgtestdb's default restricted role (NOSUPERUSER NOCREATEDB NOCREATEROLE)
		// can't create extensions.
		TestRole: &pgtestdb.Role{
			Username:     "qonto_test",
			Password:     "qonto_test",
			Capabilities: "SUPERUSER",
		},
	}, migrator)

	pool, err := pgxpool.New(context.Background(), conf.URL())
	if err != nil {
		t.Fatalf("open pool for test database: %v", err)
	}
	t.Cleanup(pool.Close)

	return pool
}
