// Package jobs is the Postgres persistence layer for jobs.
package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"ainote/migrations"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // registers pgx5://
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

// Migrate applies all pending migrations. The pgx5 driver holds a Postgres
// advisory lock while migrating, so concurrent callers are serialised.
func Migrate(ctx context.Context, databaseURL string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("migrations source: %w", err)
	}
	url := databaseURL
	switch {
	case strings.HasPrefix(url, "postgresql://"):
		url = "pgx5://" + strings.TrimPrefix(url, "postgresql://")
	case strings.HasPrefix(url, "postgres://"):
		url = "pgx5://" + strings.TrimPrefix(url, "postgres://")
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, url)
	if err != nil {
		return fmt.Errorf("migrate init: %w", err)
	}
	defer func() {
		if srcErr, dbErr := m.Close(); srcErr != nil || dbErr != nil {
			slog.Warn("migrate close", "source_error", srcErr, "database_error", dbErr)
		}
	}()

	done := make(chan error, 1)
	go func() { done <- m.Up() }()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, migrate.ErrNoChange) {
			return fmt.Errorf("migrate up: %w", err)
		}
		return nil
	case <-ctx.Done():
		m.GracefulStop <- true
		// Wait for Up to stop before Close tears down source/db under it.
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, migrate.ErrNoChange) {
				slog.Warn("migrate stopped by cancellation", "error", err)
			}
		case <-time.After(30 * time.Second):
			slog.Error("migrate did not stop within 30s of cancellation")
		}
		return fmt.Errorf("migrate: %w", ctx.Err())
	}
}
