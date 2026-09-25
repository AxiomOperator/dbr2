// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/user"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/valkey-io/valkey-go"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.temporal.io/sdk/client"
	tlog "go.temporal.io/sdk/log"

	"github.com/AxiomOperator/dbr2/db"
	"github.com/AxiomOperator/dbr2/internal/api"
	"github.com/AxiomOperator/dbr2/internal/audit"
	"github.com/AxiomOperator/dbr2/internal/auth"
	"github.com/AxiomOperator/dbr2/internal/config"
	"github.com/AxiomOperator/dbr2/internal/obs"
	"github.com/AxiomOperator/dbr2/internal/pg"
	"github.com/AxiomOperator/dbr2/internal/version"

	"github.com/go-chi/chi/v5"
)

func serve(ctx context.Context) error {
	cfg, err := config.LoadServer()
	if err != nil {
		return err
	}
	log := obs.Setup(cfg.Log.Level, cfg.Log.Format, binary, version.Of(version.Server))
	shutdownTel, err := obs.SetupTelemetry(ctx, cfg.Telemetry.Enabled, binary, version.Of(version.Server))
	if err != nil {
		return err
	}
	defer shutdownTel(context.Background())

	pool, err := pg.Connect(ctx, cfg.DatabaseURL, 2*time.Minute, log)
	if err != nil {
		return err
	}
	defer pool.Close()
	if cfg.AutoMigrate {
		if err := pg.Migrate(ctx, pool, log); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}

	svc, err := newAuthService(pool, log, cfg)
	if err != nil {
		return err
	}
	if cfg.Entra.Enabled() {
		svc.RegisterOIDC(auth.OIDCConfig{
			ID: "entra", DisplayName: "Microsoft Entra ID", Issuer: cfg.Entra.Issuer(),
			ClientID: cfg.Entra.ClientID, ClientSecret: cfg.Entra.ClientSecret,
			RedirectURL: cfg.PublicURL + "/api/v1/auth/oidc/entra/callback", GroupsClaim: cfg.Entra.GroupsClaim,
		})
		log.Info("Entra ID sign-in enabled", "issuer", cfg.Entra.Issuer())
	}
	created, err := svc.EnsureMasterAdmin(ctx, writeInitialPassword(cfg.InitialPWFile))
	if err != nil {
		return err
	}
	if created {
		log.Warn("master admin created; the initial password was written to a root-only file — sign in and change it",
			"username", cfg.MasterAdmin, "file", cfg.InitialPWFile)
	}

	checks := []api.ReadyCheck{{Name: "postgres", Critical: true, Check: pool.Ping}}
	tc, err := client.NewLazyClient(client.Options{
		HostPort: cfg.Temporal.Address, Namespace: cfg.Temporal.Namespace,
		Logger: tlog.NewStructuredLogger(log.With("component", "temporal-client")),
	})
	if err != nil {
		return err
	}
	defer tc.Close()
	checks = append(checks, api.ReadyCheck{Name: "temporal", Check: func(ctx context.Context) error {
		_, err := tc.CheckHealth(ctx, &client.CheckHealthRequest{})
		return err
	}})
	if cfg.ValkeyAddr != "" {
		vk, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{cfg.ValkeyAddr}, DisableCache: true})
		if err != nil {
			log.Warn("valkey unavailable at start-up (cache only; not fatal)", "err", err)
		} else {
			defer vk.Close()
			checks = append(checks, api.ReadyCheck{Name: "valkey", Check: func(ctx context.Context) error {
				return vk.Do(ctx, vk.B().Ping().Build()).Error()
			}})
		}
	}

	for _, hc := range cfg.HTTPChecks() {
		checks = append(checks, api.HTTPReadyCheck(hc.Name, hc.URL))
	}

	gw, fl, prot, stopGateway, err := startGateway(ctx, cfg, pool, log, tc)
	if err != nil {
		return err
	}
	defer stopGateway()
	_ = gw

	go janitor(ctx, pool, log)

	handler := api.NewHandler(&api.Deps{
		Auth: svc, Fleet: fl, Protection: prot, Log: log, Ready: checks, DocsPublic: cfg.DocsPublic, CookieSecure: cfg.CookieSecure,
		WebLoginPath: cfg.WebLoginPath, PublicURL: cfg.PublicURL, AllowedOrigins: cfg.AllowedOrigins(),
		TrustedProxies: cfg.TrustedProxies(),
	})
	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           otelhttp.NewHandler(handler, "dbr2-server"),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	errc := make(chan error, 1)
	go func() {
		log.Info("dbr2-server listening", "addr", cfg.HTTPAddr, "docs", api.DocsPath, "public_url", cfg.PublicURL)
		errc <- srv.ListenAndServe()
	}()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func newAuthService(pool *pgxpool.Pool, log *slog.Logger, cfg *config.Server) (*auth.Service, error) {
	return auth.NewService(pool, audit.NewRecorder(storeFor(pool), log), log, auth.Options{
		OrgID: uuid.MustParse(db.DefaultOrgID), MasterAdminUsername: cfg.MasterAdmin,
		SessionTTL: cfg.SessionTTL, SessionIdle: cfg.SessionIdle, SecretKey: cfg.SecretKey,
	})
}

// janitor purges expired sessions and abandoned OIDC login state hourly.
func janitor(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) {
	q := storeFor(pool)
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		if n, err := q.DeleteExpiredSessions(ctx); err == nil && n > 0 {
			log.Info("purged expired sessions", "count", n)
		}
		_, _ = q.DeleteExpiredOIDCAuthRequests(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func migrate(ctx context.Context, args []string) error {
	cfg, err := config.LoadDatabaseOnly()
	if err != nil {
		return err
	}
	log := obs.Setup(cfg.Log.Level, "text", binary, version.Of(version.Server))
	pool, err := pg.Connect(ctx, cfg.DatabaseURL, 30*time.Second, log)
	if err != nil {
		return err
	}
	defer pool.Close()
	sub := "up"
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "up":
		return pg.Migrate(ctx, pool, log)
	case "status":
		st, err := pg.Status(ctx, pool)
		if err != nil {
			return err
		}
		for _, s := range st {
			fmt.Printf("%-8s %05d  %s\n", s.State, s.Source.Version, s.Source.Path)
		}
		return nil
	}
	return fmt.Errorf("unknown migrate subcommand %q", sub)
}

func resetMasterPassword(ctx context.Context, pwFile string, disableTOTP bool) error {
	cfg, err := config.LoadServer()
	if err != nil {
		return err
	}
	log := obs.Setup("warn", "text", binary, version.Of(version.Server))
	pool, err := pg.Connect(ctx, cfg.DatabaseURL, 30*time.Second, log)
	if err != nil {
		return err
	}
	defer pool.Close()
	svc, err := newAuthService(pool, log, cfg)
	if err != nil {
		return err
	}
	pw, generated := "", false
	if pwFile != "" {
		b, err := os.ReadFile(pwFile)
		if err != nil {
			return err
		}
		pw = string(trimNewline(b))
	} else {
		if pw, err = auth.GeneratePassword(); err != nil {
			return err
		}
		generated = true
	}
	operator := "unknown"
	if u, err := user.Current(); err == nil {
		operator = u.Username
	}
	if h, err := os.Hostname(); err == nil {
		operator += "@" + h
	}
	if err := svc.ResetMasterPassword(ctx, pw, disableTOTP, operator+" (dbr2-server admin reset-master-password)"); err != nil {
		return err
	}
	fmt.Println("Master admin password reset. All master admin sessions were revoked.")
	if disableTOTP {
		fmt.Println("TOTP was disabled; re-enroll after signing in.")
	}
	if generated {
		fmt.Printf("New password: %s\n", pw)
	}
	return nil
}

func trimNewline(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

// writeSpec exports the OpenAPI document without a database.
func writeSpec(path string) error {
	a := api.NewAPI(chi.NewMux(), &api.Deps{})
	var (
		b   []byte
		err error
	)
	if len(path) > 5 && path[len(path)-5:] == ".json" {
		b, err = a.OpenAPI().MarshalJSON()
	} else {
		b, err = a.OpenAPI().YAML()
	}
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}
