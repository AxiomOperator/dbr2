// SPDX-License-Identifier: Apache-2.0

//go:build integration

// Package testutil provides integration-test infrastructure (Docker via
// testcontainers). Only built with -tags integration.
package testutil

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/AxiomOperator/dbr2/internal/pg"
)

// PostgresImage is the pinned PostgreSQL image used in tests and deployment.
const PostgresImage = "docker.io/library/postgres:18.6-trixie"

// Postgres starts a throwaway PostgreSQL 18, applies the migrations and
// returns a pool plus the DSN. The container is removed when the test ends.
func Pool(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	ctx := context.Background()
	c, err := tcpostgres.Run(ctx, PostgresImage,
		tcpostgres.WithDatabase("dbr2"), tcpostgres.WithUsername("dbr2"), tcpostgres.WithPassword("dbr2-test"),
		tcpostgres.BasicWaitStrategies())
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })
	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	log := Logger()
	pool, err := pg.Connect(ctx, dsn, time.Minute, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pg.Migrate(ctx, pool, log); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return pool, dsn
}

// Logger returns a logger that discards output (set DBR2_TEST_LOG to see it).
func Logger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
