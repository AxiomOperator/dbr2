// SPDX-License-Identifier: Apache-2.0

// Command dbr2-server is the DBR² control plane (component `server`): the
// REST API with Swagger UI docs, authentication, RBAC and audit. Later phases
// add the Agent Gateway.
//
//	dbr2-server [serve]                          run the API server
//	dbr2-server migrate [up|status]              apply / show database migrations
//	dbr2-server openapi -o api/openapi.yaml      export the OpenAPI document (no DB needed)
//	dbr2-server admin reset-master-password      reset the master admin (run on the server host)
//	dbr2-server admin restore-platform           restore a Platform Recovery Bundle (ADR-0008)
//	dbr2-server version
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/AxiomOperator/dbr2/internal/healthcheck"
	"github.com/AxiomOperator/dbr2/internal/version"
)

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

const binary = "dbr2-server"

func main() {
	if err := version.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "serve":
		err = serve(ctx)
	case "migrate":
		err = migrate(ctx, args)
	case "openapi":
		err = exportOpenAPI(args)
	case "admin":
		err = admin(ctx, args)
	case "healthcheck":
		healthcheck.Run(healthcheck.LocalURL(envOr("DBR2_HTTP_ADDR", ":8080"), "/api/v1/health/live"))
	case "version", "--version", "-version":
		fmt.Println(version.String(binary, version.Server))
	case "help", "-h", "--help":
		usage()
	default:
		usage()
		err = fmt.Errorf("unknown command %q", cmd)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", binary, err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `Usage:
  dbr2-server [serve]                        Run the API server
  dbr2-server migrate [up|status]            Apply or show database migrations
  dbr2-server openapi -o <file>              Export the OpenAPI document (.yaml or .json)
  dbr2-server admin reset-master-password    Reset the master admin password (server host only)
      [--password-file <file>] [--disable-totp]
  dbr2-server admin restore-platform         Restore a Platform Recovery Bundle into a fresh install (ADR-0008)
      --bundle <file> --identity <age identity file> [--secrets-dir <dir>]
      [--reposerver-state-dir <dir>] [--import-reposerver] [--database-url <url>] [--force]
  dbr2-server healthcheck                   Probe the local liveness endpoint (Docker HEALTHCHECK)
  dbr2-server version
`)
}

func exportOpenAPI(args []string) error {
	fs := flag.NewFlagSet("openapi", flag.ContinueOnError)
	out := fs.String("o", "api/openapi.yaml", "output file (.yaml or .json)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return writeSpec(*out)
}

func admin(ctx context.Context, args []string) error {
	if len(args) > 0 && args[0] == "restore-platform" {
		return restorePlatform(ctx, args[1:])
	}
	if len(args) == 0 || args[0] != "reset-master-password" {
		return errors.New("usage: dbr2-server admin reset-master-password [--password-file f] [--disable-totp]\n" +
			"       dbr2-server admin restore-platform --bundle FILE --identity FILE [flags]")
	}
	fs := flag.NewFlagSet("reset-master-password", flag.ContinueOnError)
	pwFile := fs.String("password-file", "", "read the new password from this file (default: generate one)")
	disableTOTP := fs.Bool("disable-totp", false, "also disable TOTP (lost authenticator)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	return resetMasterPassword(ctx, *pwFile, *disableTOTP)
}

// writeInitialPassword stores the generated master admin password in a
// root-only file (0600, never overwritten) and never logs it.
func writeInitialPassword(path string) func(username, password string) error {
	return func(username, password string) error {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = fmt.Fprintf(f, "username: %s\npassword: %s\ncreated: %s\n\nSign in, change this password, then delete this file.\n",
			username, password, time.Now().UTC().Format(time.RFC3339))
		return err
	}
}
