// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AxiomOperator/dbr2/db"
	"github.com/AxiomOperator/dbr2/internal/audit"
	"github.com/AxiomOperator/dbr2/internal/auth"
	"github.com/AxiomOperator/dbr2/internal/config"
	"github.com/AxiomOperator/dbr2/internal/events"
	"github.com/AxiomOperator/dbr2/internal/notify"
)

// startNotify builds the notification service (Phase 7) and starts its
// dispatcher (outbox fan-out, email/webhook delivery with retries, agent
// offline/online alerts). It is safe to run on every dbr2-server instance.
func startNotify(ctx context.Context, cfg *config.Server, pool *pgxpool.Pool, log *slog.Logger, presence notify.Presence, bus *events.Bus) (*notify.Service, error) {
	box, err := auth.NewSecretBox(cfg.SecretKey)
	if err != nil {
		return nil, err
	}
	svc := notify.New(pool, box, audit.NewRecorder(storeFor(pool), log), presence, log,
		notify.Options{OrgID: uuid.MustParse(db.DefaultOrgID), PublicURL: cfg.PublicURL})
	svc.SetEvents(bus)
	go svc.Run(ctx)
	return svc, nil
}
