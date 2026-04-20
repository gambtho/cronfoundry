package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// Migrate applies all up-migrations from MigrationsFS against the database at
// dsn. The call is idempotent: re-running against an up-to-date database is a
// no-op.
func Migrate(ctx context.Context, dsn string) error {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("db: migrate: parse DSN: %w", err)
	}
	// goose speaks database/sql; adapt pgx's ConnConfig into one.
	db := stdlib.OpenDB(*cfg.ConnConfig)
	defer db.Close()

	return migrateDB(ctx, db)
}

func migrateDB(ctx context.Context, db *sql.DB) error {
	goose.SetBaseFS(MigrationsFS)
	goose.SetLogger(goose.NopLogger())

	if err := goose.SetDialect(string(goose.DialectPostgres)); err != nil {
		return fmt.Errorf("db: migrate: set dialect: %w", err)
	}
	if err := goose.UpContext(ctx, db, "migrations"); err != nil {
		// An empty migrations dir is valid during early bring-up. In that case
		// fall back to EnsureDBVersionContext so the goose_db_version
		// bookkeeping table is still created — leaving the database in the
		// same state it would be in after a successful no-op Up.
		if !errors.Is(err, goose.ErrNoMigrationFiles) {
			return fmt.Errorf("db: migrate: up: %w", err)
		}
		if _, err := goose.EnsureDBVersionContext(ctx, db); err != nil {
			return fmt.Errorf("db: migrate: ensure version table: %w", err)
		}
	}
	return nil
}
