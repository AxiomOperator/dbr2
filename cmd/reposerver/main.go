// SPDX-License-Identifier: Apache-2.0

// Command dbr2-reposerver serves one DBR² Repository (component
// `reposerver`, ADR-0002). Phase 1 ships the storage-safety layer — the
// mount guard, the repository sentinel and the stall watchdog — plus health
// reporting. The embedded Kopia repository server arrives in Phase 4.
//
//	dbr2-reposerver [serve]   check storage, then serve /healthz
//	dbr2-reposerver init      write the sentinel on an empty, correctly mounted path
//	dbr2-reposerver check     run the guard once and exit non-zero on failure
//	dbr2-reposerver version
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/AxiomOperator/dbr2/internal/config"
	"github.com/AxiomOperator/dbr2/internal/healthcheck"
	"github.com/AxiomOperator/dbr2/internal/mountguard"
	"github.com/AxiomOperator/dbr2/internal/obs"
	"github.com/AxiomOperator/dbr2/internal/version"
)

const binary = "dbr2-reposerver"

func main() {
	if err := version.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	if cmd == "healthcheck" {
		addr := os.Getenv("DBR2_HEALTH_ADDR")
		if addr == "" {
			addr = ":8081"
		}
		healthcheck.Run(healthcheck.LocalURL(addr, "/healthz"))
	}
	if cmd == "version" || cmd == "--version" || cmd == "-version" {
		fmt.Println(version.String(binary, version.RepoServer))
		return
	}
	cfg, err := config.LoadRepoServer()
	if err != nil {
		fatal(err)
	}
	log := obs.Setup(cfg.Log.Level, cfg.Log.Format, binary, version.Of(version.RepoServer))
	g := &mountguard.Guard{Path: cfg.Path, ExpectFSType: cfg.ExpectFSType, RequireMountPoint: cfg.RequireMountPoint, RepositoryID: cfg.RepositoryID}
	switch cmd {
	case "init":
		if err := g.Initialize(); err != nil {
			fatal(err)
		}
		fmt.Printf("repository %s initialized at %s\n", cfg.RepositoryID, cfg.Path)
	case "check":
		if err := g.Check(); err != nil {
			fatal(err)
		}
		fmt.Println("storage OK")
	case "serve":
		if err := serve(cfg, g, log); err != nil {
			fatal(err)
		}
	default:
		fatal(fmt.Errorf("unknown command %q (serve|init|check|version)", cmd))
	}
}

func serve(cfg *config.RepoServer, g *mountguard.Guard, log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Refuse to start on unsafe storage (ADR-0002): never fall back to the
	// local disk under an unmounted mount point.
	if cfg.InitIfEmpty {
		// Development profile only: initialize an empty, correctly mounted
		// path. Initialize itself refuses unmounted or non-empty paths.
		if err := g.Initialize(); err != nil {
			return fmt.Errorf("storage guard (init-if-empty): %w", err)
		}
	}
	if err := g.Check(); err != nil {
		return fmt.Errorf("storage guard: %w (run `dbr2-reposerver init` once on a new, empty repository mount)", err)
	}
	w := &mountguard.Watchdog{Guard: g, Interval: cfg.WatchdogInterval, Timeout: cfg.WatchdogTimeout,
		OnChange: func(err error) {
			if err != nil {
				log.Error("repository storage unhealthy", "path", cfg.Path, "err", err)
			} else {
				log.Info("repository storage healthy", "path", cfg.Path)
			}
		}}
	go w.Run(ctx)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(rw http.ResponseWriter, _ *http.Request) {
		ok, msg := w.Healthy()
		rw.Header().Set("Content-Type", "application/json")
		if !ok {
			rw.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(rw).Encode(map[string]any{"healthy": ok, "error": msg,
			"repository_id": cfg.RepositoryID, "path": cfg.Path, "version": version.Of(version.RepoServer)})
	})
	srv := &http.Server{Addr: cfg.HealthAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	log.Info("dbr2-reposerver started (storage guard active; Kopia repository server arrives in Phase 4)",
		"path", cfg.Path, "repository_id", cfg.RepositoryID, "health", cfg.HealthAddr)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", binary, err)
	os.Exit(1)
}
