// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.temporal.io/sdk/client"
	"google.golang.org/grpc"

	"github.com/AxiomOperator/dbr2/db"
	controlv1 "github.com/AxiomOperator/dbr2/internal/agentpb/control/v1"
	"github.com/AxiomOperator/dbr2/internal/audit"
	"github.com/AxiomOperator/dbr2/internal/auth"
	"github.com/AxiomOperator/dbr2/internal/config"
	"github.com/AxiomOperator/dbr2/internal/fleet"
	"github.com/AxiomOperator/dbr2/internal/gateway"
	"github.com/AxiomOperator/dbr2/internal/protection"
)

// startGateway bootstraps the agent CA and starts the Agent Gateway (mTLS,
// agent-facing) and the internal control listener (dbr2-worker).
func startGateway(ctx context.Context, cfg *config.Server, pool *pgxpool.Pool, log *slog.Logger, tc client.Client) (*gateway.Gateway, *fleet.Service, *protection.Service, func(), error) {
	orgID := uuid.MustParse(db.DefaultOrgID)
	box, err := auth.NewSecretBox(cfg.SecretKey)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	q := storeFor(pool)
	ca, created, err := gateway.LoadOrCreateCA(ctx, q, box, orgID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if created {
		log.Warn("agent CA created — back up the database and DBR2_SECRET_KEY (key escrow arrives with ADR-0008)", "ca_sha256", ca.Fingerprint())
	}
	host, _ := os.Hostname()
	names := append([]string{}, cfg.GatewayHostnames...)
	if gh, _, err := net.SplitHostPort(cfg.GatewayPublicAddress); err == nil {
		names = append(names, gh)
	}
	if host != "" {
		names = append(names, host)
	}
	rec := audit.NewRecorder(q, log)
	gw, err := gateway.New(gateway.Config{OrgID: orgID, Hostnames: names, InstanceID: host}, pool, box, ca, rec, log)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	lis, err := net.Listen("tcp", cfg.GatewayAddr)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	agentSrv := gw.GRPCServer()
	go func() {
		log.Info("agent gateway listening (mTLS)", "addr", cfg.GatewayAddr, "public_address", cfg.GatewayPublicAddress,
			"certificate_names", names, "ca_sha256", ca.Fingerprint())
		if err := agentSrv.Serve(lis); err != nil {
			log.Error("agent gateway stopped", "err", err)
		}
	}()
	stops := []func(){func() { graceful(agentSrv.GracefulStop, agentSrv.Stop) }}

	fl := fleet.New(fleet.Options{OrgID: orgID, GatewayAddress: cfg.GatewayPublicAddress, TaskQueue: cfg.Temporal.TaskQueue},
		pool, rec, gw, box, tc)
	prot := protection.New(protection.Options{OrgID: orgID, TaskQueue: cfg.Temporal.TaskQueue, InternalToken: cfg.InternalToken},
		pool, rec, fl, gw, tc)

	if cfg.InternalToken == "" {
		log.Warn("DBR2_INTERNAL_TOKEN not set: control listener disabled; dbr2-worker cannot dispatch to agents")
	} else {
		clis, err := net.Listen("tcp", cfg.ControlAddr)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		ctrl := gw.ControlServer(cfg.InternalToken, func(g *grpc.Server) {
			controlv1.RegisterPlatformServiceServer(g, prot.Platform())
		})
		go func() {
			log.Info("gateway control listener (internal)", "addr", cfg.ControlAddr)
			if err := ctrl.Serve(clis); err != nil {
				log.Error("control listener stopped", "err", err)
			}
		}()
		stops = append(stops, func() { graceful(ctrl.GracefulStop, ctrl.Stop) })
	}
	return gw, fl, prot, func() {
		for _, s := range stops {
			s()
		}
	}, nil
}

// graceful stops a gRPC server, forcing it after 10 s (streams are long-lived).
func graceful(stop, force func()) {
	done := make(chan struct{})
	go func() { stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		force()
	}
}
