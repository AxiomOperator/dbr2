// Command dbr2-api is the spike's API server.
//
//	dbr2-api serve   [-addr 127.0.0.1:18888] [-docs-public]   run the server
//	dbr2-api openapi [-format yaml|json] [-o api/openapi.yaml] export the spec (no server)
//	dbr2-api version                                            print versions
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/AxiomOperator/dbr2/spikes/huma-swagger/internal/api"
	"github.com/AxiomOperator/dbr2/spikes/huma-swagger/internal/version"
)

func main() {
	if err := version.Check(); err != nil {
		log.Fatal(err)
	}
	cmd, args := "serve", os.Args[1:]
	if len(args) > 0 && args[0][0] != '-' {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "serve":
		err = serve(args)
	case "openapi":
		err = exportSpec(args)
	case "version", "--version":
		fmt.Printf("dbr2-api %s (platform %s, api %s)\n", version.Server, version.Platform, version.API)
	default:
		err = fmt.Errorf("unknown command %q", cmd)
	}
	if err != nil {
		log.Fatal(err)
	}
}

// exportSpec builds the API on a throwaway router and writes the OpenAPI
// document without listening on a port (CI: `dbr2-api openapi -o api/openapi.yaml`).
func exportSpec(args []string) error {
	fs := flag.NewFlagSet("openapi", flag.ExitOnError)
	format := fs.String("format", "yaml", "output format: yaml or json")
	out := fs.String("o", "-", "output file (- for stdout)")
	_ = fs.Parse(args)

	spec := api.NewAPI(chi.NewMux()).OpenAPI()
	var (
		b   []byte
		err error
	)
	switch *format {
	case "yaml":
		b, err = spec.YAML()
	case "json":
		b, err = spec.MarshalJSON()
	default:
		err = fmt.Errorf("unknown format %q", *format)
	}
	if err != nil {
		return err
	}
	if *out == "-" {
		_, err = os.Stdout.Write(b)
		return err
	}
	return os.WriteFile(*out, b, 0o644)
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", "127.0.0.1:18888", "listen address")
	docsPublic := fs.Bool("docs-public", false, "serve /api/docs and /api/openapi.* without authentication (api.docs.public)")
	_ = fs.Parse(args)

	// Toy credentials for the spike. Phase 1: OIDC (Entra ID) + sessions.
	tokens := api.StaticTokens{
		"admin-token":  {Subject: "admin", Permissions: []string{"applications:read", "backups:create"}},
		"viewer-token": {Subject: "viewer", Permissions: []string{"applications:read"}},
	}
	srv := &http.Server{
		Addr:              *addr,
		Handler:           api.NewHandler(api.Config{DocsPublic: *docsPublic, Authenticator: tokens}),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	log.Printf("dbr2-api %s listening on http://%s (docs public: %v)", version.Server, *addr, *docsPublic)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
