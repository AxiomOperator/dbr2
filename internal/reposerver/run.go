// SPDX-License-Identifier: Apache-2.0

package reposerver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/AxiomOperator/dbr2/internal/config"
	"github.com/AxiomOperator/dbr2/internal/mountguard"
	"github.com/AxiomOperator/dbr2/internal/version"
)

// Run serves the reposerver until ctx is canceled: storage guard and
// watchdog, the supervised Kopia server (once initialized), /healthz and the
// management API.
func Run(parent context.Context, cfg *config.RepoServer, g *mountguard.Guard, log *slog.Logger) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	if _, _, err := net.SplitHostPort(cfg.KopiaAddr); err != nil {
		return fmt.Errorf("DBR2_REPOSERVER_KOPIA_ADDR %q: %w", cfg.KopiaAddr, err)
	}
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

	st := State{Dir: cfg.StateDir}
	if err := st.Prepare(); err != nil {
		return err
	}
	host, _ := os.Hostname()
	names := append([]string{}, cfg.TLSNames...)
	names = append(names, "localhost", "127.0.0.1", "::1")
	if host != "" {
		names = append(names, host)
	}
	fp, missing, err := st.EnsureCert(names)
	if err != nil {
		return fmt.Errorf("tls certificate: %w", err)
	}
	if len(missing) > 0 {
		log.Warn("the existing Kopia server certificate does not cover some configured names; clients pin the fingerprint, so it is kept (delete tls.crt/tls.key and re-enroll agents to rotate)", "names", missing)
	}
	ctrl, err := st.EnsureControlPassword()
	if err != nil {
		return fmt.Errorf("control password: %w", err)
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	svc := &Service{
		RepositoryID: cfg.RepositoryID, StoragePath: cfg.Path, KopiaAddr: cfg.KopiaAddr, State: st,
		GuardCheck: w.Probe, StorageHealth: w.Healthy, CertSHA256: fp, ControlPassword: ctrl,
		Log: log, Lifetime: ctx,
	}
	runner := &ExecRunner{Exe: exe, ConfigFile: st.ConfigFile(), HomeDir: st.KopiaDir(), Password: svc.Password, Log: log}
	sup := &Supervisor{Runner: runner, Invocation: svc.ServerInvocation, GuardCheck: w.Probe,
		ProbeAddr: loopback(cfg.KopiaAddr), Fingerprint: fp, Log: log}
	svc.Runner, svc.Server = runner, sup

	initialized, err := svc.Load()
	if err != nil {
		return err
	}
	if initialized {
		sup.Start(ctx)
	} else {
		log.Warn("repository not initialized; waiting for POST /v1/initialize on the management API")
	}

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	serve := func(srv *http.Server, name string) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errs <- fmt.Errorf("%s listener: %w", name, err)
			}
		}()
		go func() {
			<-ctx.Done()
			sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = srv.Shutdown(sctx)
		}()
	}

	health := http.NewServeMux()
	health.HandleFunc("GET /healthz", func(rw http.ResponseWriter, _ *http.Request) {
		storageOK, msg := w.Healthy()
		running := sup.Running()
		init := svc.Initialized()
		ok := storageOK && (!init || running)
		if storageOK && init && !running {
			msg = "kopia server is not running"
			if e := sup.LastError(); e != "" {
				msg += ": " + e
			}
		}
		rw.Header().Set("Content-Type", "application/json")
		if !ok {
			rw.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(rw).Encode(map[string]any{"healthy": ok, "error": msg,
			"storage_healthy": storageOK, "initialized": init, "server_running": running,
			"repository_id": cfg.RepositoryID, "path": cfg.Path, "version": version.Of(version.RepoServer)})
	})
	serve(&http.Server{Addr: cfg.HealthAddr, Handler: health, ReadHeaderTimeout: 5 * time.Second}, "health")

	if cfg.InternalToken == "" {
		log.Error("management API disabled: DBR2_INTERNAL_TOKEN[_FILE] is not set")
	} else {
		api := &API{Backend: svc, Token: cfg.InternalToken, Log: log}
		serve(&http.Server{Addr: cfg.MgmtAddr, Handler: api.Handler(), ReadHeaderTimeout: 5 * time.Second,
			WriteTimeout: 10 * time.Minute}, "management")
	}
	log.Info("dbr2-reposerver started", "path", cfg.Path, "repository_id", cfg.RepositoryID,
		"health", cfg.HealthAddr, "mgmt", cfg.MgmtAddr, "kopia", cfg.KopiaAddr, "cert_sha256", fp,
		"initialized", initialized, "kopia_version", KopiaVersion())

	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-errs:
		cancel()
	}
	sup.Wait()
	wg.Wait()
	return runErr
}
