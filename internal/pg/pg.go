// SPDX-License-Identifier: Apache-2.0

// Package pg connects to PostgreSQL and applies the embedded migrations.
package pg

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/AxiomOperator/dbr2/db"
)

// Connect opens a pool, retrying until PostgreSQL accepts connections or
// ctx/timeout expires (Compose starts services concurrently).
func Connect(ctx context.Context, url string, timeout time.Duration, log *slog.Logger) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("pg: parse DSN: %w", err)
	}
	cfg.ConnConfig.RuntimeParams["application_name"] = "dbr2"
	deadline := time.Now().Add(timeout)
	backoff := 500 * time.Millisecond
	for {
		pool, err := pgxpool.NewWithConfig(ctx, cfg)
		if err == nil {
			if err = pool.Ping(ctx); err == nil {
				return pool, nil
			}
			pool.Close()
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("pg: database not reachable after %s: %w", timeout, err)
		}
		log.WarnContext(ctx, "waiting for PostgreSQL", "err", err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 5*time.Second)
	}
}

func provider(pool *pgxpool.Pool) (*goose.Provider, error) {
	return goose.NewProvider(goose.DialectPostgres, stdlib.OpenDBFromPool(pool), migrationsFS())
}

// Migrate applies all pending migrations (forward only).
func Migrate(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) error {
	p, err := provider(pool)
	if err != nil {
		return err
	}
	res, err := p.Up(ctx)
	for _, r := range res {
		log.InfoContext(ctx, "migration applied", "version", r.Source.Version, "file", r.Source.Path, "duration", r.Duration)
	}
	if errors.Is(err, goose.ErrNoNextVersion) {
		return nil
	}
	return err
}

// Status reports applied/pending migrations.
func Status(ctx context.Context, pool *pgxpool.Pool) ([]*goose.MigrationStatus, error) {
	p, err := provider(pool)
	if err != nil {
		return nil, err
	}
	return p.Status(ctx)
}

// CurrentVersion returns the highest applied migration version.
func CurrentVersion(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	p, err := provider(pool)
	if err != nil {
		return 0, err
	}
	return p.GetDBVersion(ctx)
}

func migrationsFS() fsys { return sub(db.Migrations, "migrations") }
