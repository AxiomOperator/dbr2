// SPDX-License-Identifier: Apache-2.0

// Command dbr2-worker runs DBR²'s Temporal workflows and activities
// (component `worker`, ADR-0009). Activities reach agents through the Agent
// Gateway from Phase 2 on (ADR-0001).
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"go.temporal.io/sdk/worker"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	controlv1 "github.com/AxiomOperator/dbr2/internal/agentpb/control/v1"

	"github.com/AxiomOperator/dbr2/internal/config"
	"github.com/AxiomOperator/dbr2/internal/healthcheck"
	"github.com/AxiomOperator/dbr2/internal/obs"
	"github.com/AxiomOperator/dbr2/internal/temporalx"
	"github.com/AxiomOperator/dbr2/internal/version"
	"github.com/AxiomOperator/dbr2/workflows"
	"github.com/AxiomOperator/dbr2/workflows/diag"
	"github.com/AxiomOperator/dbr2/workflows/hosts"
)

const binary = "dbr2-worker"

func main() {
	if err := version.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "--version", "-version":
			fmt.Println(version.String(binary, version.Worker))
			return
		case "healthcheck":
			addr := os.Getenv("DBR2_HEALTH_ADDR")
			if addr == "" {
				addr = ":8082"
			}
			healthcheck.Run(healthcheck.LocalURL(addr, "/healthz"))
		default:
			fmt.Fprintf(os.Stderr, "usage: %s [version|healthcheck]\n", binary)
			os.Exit(2)
		}
	}
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", binary, err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cfg, err := config.LoadWorker()
	if err != nil {
		return err
	}
	log := obs.Setup(cfg.Log.Level, cfg.Log.Format, binary, version.Of(version.Worker))
	shutdown, err := obs.SetupTelemetry(ctx, cfg.Telemetry.Enabled, binary, version.Of(version.Worker))
	if err != nil {
		return err
	}
	defer shutdown(context.Background())

	c, err := temporalx.Dial(cfg.Temporal.Address, cfg.Temporal.Namespace, log)
	if err != nil {
		return err
	}
	defer c.Close()

	w := worker.New(c, cfg.Temporal.TaskQueue, worker.Options{
		// Build ID = worker version (ADR-0015); used for worker versioning later.
		Identity: binary + "@" + hostname() + "/" + version.Of(version.Worker),
	})
	// Internal control channel to the Agent Gateway (deployment network only;
	// authenticated with the internal token).
	ctrlConn, err := grpc.NewClient(cfg.GatewayControlAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer ctrlConn.Close()
	if cfg.InternalToken == "" {
		log.Warn("DBR2_INTERNAL_TOKEN not set: agent commands will fail")
	}
	workflows.Register(w, &diag.Activities{}, &hosts.Activities{
		Control: controlv1.NewGatewayControlServiceClient(ctrlConn), Token: cfg.InternalToken,
	})

	var healthy atomic.Bool
	srv := &http.Server{Addr: cfg.HealthAddr, ReadHeaderTimeout: 5 * time.Second,
		Handler: http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/healthz" || !healthy.Load() {
				http.Error(rw, "not ready", http.StatusServiceUnavailable)
				return
			}
			_, _ = rw.Write([]byte("ok\n"))
		})}
	go func() { _ = srv.ListenAndServe() }()
	defer srv.Close()

	if err := w.Start(); err != nil {
		return fmt.Errorf("start worker (task queue %s): %w", cfg.Temporal.TaskQueue, err)
	}
	healthy.Store(true)
	log.Info("dbr2-worker started", "task_queue", cfg.Temporal.TaskQueue, "namespace", cfg.Temporal.Namespace)
	<-ctx.Done()
	healthy.Store(false)
	log.Info("dbr2-worker stopping")
	w.Stop()
	return nil
}

func hostname() string {
	h, _ := os.Hostname()
	return h
}
