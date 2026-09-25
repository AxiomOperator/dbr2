// SPDX-License-Identifier: Apache-2.0

// Command dbr2-reposerver serves one DBR² Repository (component
// `reposerver`, ADR-0002): the storage-safety layer (mount guard, repository
// sentinel, stall watchdog), the embedded Kopia repository server (Kopia's
// public `cli` package, run as a supervised child process of this binary;
// ADR-0007) and the internal management API (see internal/reposerver).
//
//	dbr2-reposerver [serve]   check storage, run the Kopia server, serve
//	                          /healthz and the management API
//	dbr2-reposerver init      write the sentinel on an empty, correctly mounted path
//	dbr2-reposerver check     run the guard once and exit non-zero on failure
//	dbr2-reposerver version
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/AxiomOperator/dbr2/internal/config"
	"github.com/AxiomOperator/dbr2/internal/healthcheck"
	"github.com/AxiomOperator/dbr2/internal/mountguard"
	"github.com/AxiomOperator/dbr2/internal/obs"
	"github.com/AxiomOperator/dbr2/internal/reposerver"
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
	if cmd == reposerver.KopiaSubcommand {
		// Hidden: the embedded Kopia CLI, only run as a child of `serve`.
		reposerver.KopiaMain(os.Args[2:])
		return
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
	return reposerver.Run(ctx, cfg, g, log)
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", binary, err)
	os.Exit(1)
}
