// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AxiomOperator/dbr2/internal/config"
	"github.com/AxiomOperator/dbr2/internal/obs"
	"github.com/AxiomOperator/dbr2/internal/pg"
	"github.com/AxiomOperator/dbr2/internal/platform"
	"github.com/AxiomOperator/dbr2/internal/version"
)

// restorePlatform implements `dbr2-server admin restore-platform` (ADR-0008
// platform recovery runbook, docs/operations/platform-recovery.md). It lives
// in dbr2-server rather than the dbr2 CLI because it needs direct database
// access on a fresh installation (the CLI only talks to a running API).
func restorePlatform(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("restore-platform", flag.ContinueOnError)
	bundlePath := fs.String("bundle", "", "Platform Recovery Bundle (dbr2-platform-<timestamp>.tar.zst.age)")
	identity := fs.String("identity", "", "age identity file of an escrow holder (AGE-SECRET-KEY-1… or an unencrypted SSH private key)")
	secretsDir := fs.String("secrets-dir", "", "write the restored secrets here as Compose secret files (0600), e.g. deployments/docker-compose/secrets")
	stateDir := fs.String("reposerver-state-dir", "", "write each Repository's reposerver state archive here (<repository-id>.tar, 0600)")
	importState := fs.Bool("import-reposerver", false, "POST each state archive to its reposerver (/v1/state-import; the reposerver must be running and uninitialized)")
	dbURL := fs.String("database-url", "", "PostgreSQL URL of the target dbr2 database as a superuser (default: DBR2_DATABASE_URL[_FILE])")
	force := fs.Bool("force", false, "replace a database that already holds agents or Repositories")
	verifyOnly := fs.Bool("verify-only", false, "decrypt and verify the bundle, print its manifest summary and stop (drills)")
	noDatabase := fs.Bool("no-database", false, "skip the database restore (e.g. to run --import-reposerver once the reposervers use the restored token)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *bundlePath == "" || *identity == "" {
		return errors.New("--bundle and --identity are required")
	}

	ids, err := platform.LoadIdentities(*identity)
	if err != nil {
		return err
	}
	f, err := os.Open(*bundlePath)
	if err != nil {
		return err
	}
	b, err := platform.Open(f, ids...)
	f.Close()
	if err != nil {
		return err
	}
	m := &b.Manifest
	fmt.Printf("Bundle verified: created %s by DBR² %s, schema version %d, %d tables, %d secrets, %d Repositories.\n",
		m.CreatedAt.Format(time.RFC3339), m.PlatformVersion, m.SchemaVersion, len(m.Tables), len(m.Secrets), len(m.Repositories))
	for _, r := range m.Repositories {
		if r.Missing {
			fmt.Printf("  WARNING: reposerver state of Repository %s (%s) is NOT in this bundle: %s\n", r.Name, r.ID, r.Error)
		}
	}
	if *verifyOnly {
		return nil
	}

	if *noDatabase {
		return restoreFiles(ctx, b, nil, *secretsDir, *stateDir, *importState)
	}
	url := *dbURL
	if url == "" {
		cfg, err := config.LoadDatabaseOnly()
		if err != nil {
			return fmt.Errorf("%w (or pass --database-url)", err)
		}
		url = cfg.DatabaseURL
	}
	log := obs.Setup("warn", "text", binary, version.Of(version.Server))
	pool, err := pg.Connect(ctx, url, 30*time.Second, log)
	if err != nil {
		return err
	}
	defer pool.Close()

	res, err := platform.Restore(ctx, pool, b, platform.RestoreOptions{Force: *force,
		Progress: func(format string, a ...any) { fmt.Printf(format+"\n", a...) }})
	if err != nil {
		if errors.Is(err, platform.ErrNotEmpty) {
			return fmt.Errorf("%w\n(restore into a fresh installation; --force replaces everything in the target database)", err)
		}
		return err
	}
	fmt.Printf("Database restored: %d tables, %d rows, %d sequences reset (schema version %d", res.Tables, res.Rows, res.Sequences, res.SchemaVersion)
	if res.MigratedTo > res.SchemaVersion {
		fmt.Printf(", migrated to %d", res.MigratedTo)
	}
	fmt.Println(").")
	return restoreFiles(ctx, b, pool, *secretsDir, *stateDir, *importState)
}

// restoreFiles writes the secrets and reposerver state and optionally
// imports the state into the reposervers, then prints the next steps.
func restoreFiles(ctx context.Context, b *platform.Bundle, pool *pgxpool.Pool, secretsDir, stateDir string, importState bool) error {
	if secretsDir != "" {
		files, err := platform.WriteSecrets(secretsDir, b)
		if err != nil {
			return fmt.Errorf("write secrets: %w", err)
		}
		fmt.Printf("Secrets written (0600): %s\n", strings.Join(files, ", "))
	} else {
		fmt.Println("Secrets were not written (no --secrets-dir): the restored database needs the bundle's DBR2_SECRET_KEY.")
	}
	if stateDir != "" {
		files, err := platform.WriteReposerverState(stateDir, b)
		if err != nil {
			return fmt.Errorf("write reposerver state: %w", err)
		}
		fmt.Printf("Reposerver state archives written (0600): %s\n", strings.Join(files, ", "))
	}
	importFailed := false
	if importState {
		results, err := platform.ImportReposerverState(ctx, pool, b, nil)
		if err != nil {
			return err
		}
		for _, r := range results {
			if r.Err != nil {
				importFailed = true
				fmt.Printf("  Repository %s (%s): state import FAILED at %s: %v\n", r.Name, r.RepositoryID, r.ManagementURL, r.Err)
			} else {
				fmt.Printf("  Repository %s (%s): state imported at %s\n", r.Name, r.RepositoryID, r.ManagementURL)
			}
		}
	}

	fmt.Print(`
Next steps (docs/operations/platform-recovery.md):
  1. Start the stack with the restored secrets (dbr2_secret_key, dbr2_internal_token,
     dbr2_entra_client_secret) — Compose expects them in deployments/docker-compose/secrets/
     (chmod 644 each file, as init-secrets.sh does, so non-root services can read them).
     Temporal starts with a fresh database; schedules are recreated from PostgreSQL.
  2. If the reposerver state was not imported, start the reposervers with the restored
     internal token and import it (restore-platform --no-database --import-reposerver,
     or POST each <repository-id>.tar to /v1/state-import).
  3. Run 'dbr2 admin reindex --repository <name>' for every Repository (ADR-0003).
  4. Agents reconnect with their existing certificates (the agent CA was restored).
  5. Verify: sign in, check hosts are online, run a backup, and run a platform backup.
`)
	if importFailed {
		return errors.New("one or more reposerver state imports failed")
	}
	return nil
}
