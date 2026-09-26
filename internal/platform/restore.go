// SPDX-License-Identifier: Apache-2.0

package platform

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/AxiomOperator/dbr2/db"
	"github.com/AxiomOperator/dbr2/internal/repoclient"
)

// RestoreOptions tune a database restore.
type RestoreOptions struct {
	// Force restores over a database that already holds agents or
	// Repositories (everything in it is replaced).
	Force bool
	// Progress receives human-readable progress lines (nil = silent).
	Progress func(format string, a ...any)
}

// RestoreResult summarizes a database restore.
type RestoreResult struct {
	Tables int
	Rows   int64
	// SchemaVersion is the bundle's schema version; MigratedTo is the
	// version after the post-restore migrations of this build.
	SchemaVersion int64
	MigratedTo    int64
	// Sequences whose value was reset to the restored maximum.
	Sequences int
}

// ErrNotEmpty means the target database already holds platform state.
var ErrNotEmpty = errors.New("platform: the target database already holds agents or Repositories (use --force to replace everything)")

// Restore replaces the platform state in the database behind pool with the
// bundle's. The target is first migrated to the bundle's schema version (a
// target at a newer version is refused), every table is restored in ONE
// transaction with session_replication_role = replica (foreign keys and the
// append-only audit triggers are bypassed for the restore only), identity
// and serial sequences are reset to the restored maxima, and finally the
// migrations of this build newer than the bundle are applied to the
// restored data.
//
// session_replication_role can only be set by a superuser (or a role granted
// SET on it, PostgreSQL 15+), so restore-platform connects as the
// PostgreSQL superuser; schema changes run as the database owner so object
// ownership stays with the platform role.
func Restore(ctx context.Context, pool *pgxpool.Pool, b *Bundle, opts RestoreOptions) (*RestoreResult, error) {
	say := opts.Progress
	if say == nil {
		say = func(string, ...any) {}
	}
	m := &b.Manifest
	res := &RestoreResult{SchemaVersion: m.SchemaVersion}

	cur, err := schemaVersion(ctx, pool)
	if err != nil {
		return nil, err
	}
	switch {
	case cur > m.SchemaVersion:
		return nil, fmt.Errorf("platform: the target database is at schema version %d, newer than the bundle's %d: "+
			"restore into a fresh, unmigrated database (start nothing before restore-platform, or set DBR2_DB_AUTO_MIGRATE=false)", cur, m.SchemaVersion)
	case cur < m.SchemaVersion:
		say("Migrating the target database from schema version %d to the bundle's %d …", cur, m.SchemaVersion)
		if err := migrate(ctx, pool, m.SchemaVersion); err != nil {
			return nil, fmt.Errorf("platform: migrate to schema version %d: %w", m.SchemaVersion, err)
		}
	}
	if !opts.Force {
		var n int64
		if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM agents) + (SELECT count(*) FROM repositories)`).Scan(&n); err != nil {
			return nil, err
		}
		if n > 0 {
			return nil, ErrNotEmpty
		}
	}
	target, err := listTables(ctx, pool)
	if err != nil {
		return nil, err
	}
	have := map[string]map[string]bool{}
	for _, t := range target {
		cols := map[string]bool{}
		for _, c := range t.Columns {
			cols[c] = true
		}
		have[t.Name] = cols
	}
	names := make([]string, 0, len(m.Tables))
	for _, t := range m.Tables {
		cols, ok := have[t.Name]
		if !ok {
			return nil, fmt.Errorf("platform: table %s from the bundle does not exist in the target database", t.Name)
		}
		for _, c := range t.Columns {
			if !cols[c] {
				return nil, fmt.Errorf("platform: column %s.%s from the bundle does not exist in the target database", t.Name, c)
			}
		}
		names = append(names, pgx.Identifier{"public", t.Name}.Sanitize())
	}

	say("Restoring %d tables in one transaction …", len(m.Tables))
	err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET LOCAL session_replication_role = replica`); err != nil {
			return fmt.Errorf("set session_replication_role (connect as the PostgreSQL superuser): %w", err)
		}
		if len(names) > 0 {
			if _, err := tx.Exec(ctx, "TRUNCATE "+strings.Join(names, ", ")); err != nil {
				return fmt.Errorf("truncate: %w", err)
			}
		}
		for _, t := range m.Tables {
			tag, err := tx.Conn().PgConn().CopyFrom(ctx, bytes.NewReader(b.Files[t.File]), copySQL(t, "FROM STDIN"))
			if err != nil {
				return fmt.Errorf("restore table %s: %w", t.Name, err)
			}
			if tag.RowsAffected() != t.Rows {
				return fmt.Errorf("restore table %s: %d rows restored, manifest says %d", t.Name, tag.RowsAffected(), t.Rows)
			}
			res.Tables++
			res.Rows += t.Rows
		}
		n, err := resetSequences(ctx, tx, m.Tables)
		res.Sequences = n
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("platform: restore: %w", err)
	}
	say("Restored %d rows.", res.Rows)

	// Migrations of this build newer than the bundle run on the restored data.
	if err := migrate(ctx, pool, 0); err != nil {
		return nil, fmt.Errorf("platform: post-restore migrations: %w", err)
	}
	if res.MigratedTo, err = schemaVersion(ctx, pool); err != nil {
		return nil, err
	}
	if res.MigratedTo > m.SchemaVersion {
		say("Applied migrations up to schema version %d.", res.MigratedTo)
	}
	return res, nil
}

// resetSequences sets every identity/serial sequence of the restored tables
// to the column's maximum (next value = max + 1).
func resetSequences(ctx context.Context, tx pgx.Tx, tables []Table) (int, error) {
	n := 0
	for _, t := range tables {
		qt := pgx.Identifier{"public", t.Name}.Sanitize()
		for _, c := range t.Columns {
			var seq *string
			if err := tx.QueryRow(ctx, `SELECT pg_get_serial_sequence($1, $2)`, qt, c).Scan(&seq); err != nil {
				return n, err
			}
			if seq == nil {
				continue
			}
			qc := pgx.Identifier{c}.Sanitize()
			if _, err := tx.Exec(ctx, fmt.Sprintf(
				`SELECT CASE WHEN max(%[1]s) IS NULL THEN setval($1::regclass, 1, false) ELSE setval($1::regclass, max(%[1]s)::bigint, true) END FROM %[2]s`,
				qc, qt), *seq); err != nil {
				return n, fmt.Errorf("reset sequence %s: %w", *seq, err)
			}
			n++
		}
	}
	return n, nil
}

// migrate applies the embedded migrations up to version (0 = latest) as the
// database owner.
func migrate(ctx context.Context, pool *pgxpool.Pool, version int64) error {
	var owner, current string
	if err := pool.QueryRow(ctx, `SELECT pg_get_userbyid(datdba), current_user FROM pg_database WHERE datname = current_database()`).
		Scan(&owner, &current); err != nil {
		return err
	}
	cfg := pool.Config().Copy()
	cfg.MaxConns = 1
	if owner != current {
		setRole := "SET ROLE " + pgx.Identifier{owner}.Sanitize()
		cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
			_, err := c.Exec(ctx, setRole)
			return err
		}
	}
	mp, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return err
	}
	defer mp.Close()
	migrations, err := fs.Sub(db.Migrations, "migrations")
	if err != nil {
		return err
	}
	sqlDB := stdlib.OpenDBFromPool(mp)
	defer sqlDB.Close()
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations)
	if err != nil {
		return err
	}
	if version > 0 {
		_, err = p.UpTo(ctx, version)
	} else {
		_, err = p.Up(ctx)
	}
	if errors.Is(err, goose.ErrNoNextVersion) {
		return nil
	}
	return err
}

// WriteSecrets writes the bundle's secrets to dir as Compose secret files
// (0600, replacing existing files atomically). It returns the files written.
func WriteSecrets(dir string, b *Bundle) ([]string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	var out []string
	for _, name := range sortedWithPrefix(b.Files, "secrets/") {
		p := filepath.Join(dir, strings.TrimPrefix(name, "secrets/"))
		if err := writeFileAtomic(p, b.Files[name], 0o600); err != nil {
			return out, err
		}
		out = append(out, p)
	}
	return out, nil
}

// WriteReposerverState writes each Repository's state archive to
// dir/<repository-id>.tar (0600).
func WriteReposerverState(dir string, b *Bundle) ([]string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	var out []string
	for _, r := range b.Manifest.Repositories {
		if r.Missing {
			continue
		}
		p := filepath.Join(dir, r.ID+".tar")
		if err := writeFileAtomic(p, b.Files[r.File], 0o600); err != nil {
			return out, err
		}
		out = append(out, p)
	}
	return out, nil
}

// StateImporter imports a reposerver state archive.
type StateImporter interface {
	StateImport(ctx context.Context, r io.Reader) error
}

// ImportResult is the outcome for one Repository.
type ImportResult struct {
	RepositoryID  string
	Name          string
	ManagementURL string
	Err           error
}

// ImportReposerverState posts each Repository's state archive to its
// reposerver (POST /v1/state-import) using the restored management URL from
// the database (pool may be nil: the URL recorded in the manifest is used)
// and the restored internal token. client may be nil (the real management
// client).
func ImportReposerverState(ctx context.Context, pool *pgxpool.Pool, b *Bundle, client func(url, token string) StateImporter) ([]ImportResult, error) {
	if client == nil {
		client = func(u, t string) StateImporter { return repoclient.New(u, t) }
	}
	token := b.Secret(InternalTokenFile)
	if token == "" {
		return nil, errors.New("platform: the bundle has no internal token")
	}
	var out []ImportResult
	for _, r := range b.Manifest.Repositories {
		if r.Missing {
			continue
		}
		url := r.ManagementURL
		var dbURL string
		if pool != nil {
			if err := pool.QueryRow(ctx, `SELECT management_url FROM repositories WHERE id = $1::uuid`, r.ID).Scan(&dbURL); err == nil && dbURL != "" {
				url = dbURL
			}
		}
		err := client(url, token).StateImport(ctx, bytes.NewReader(b.Files[r.File]))
		out = append(out, ImportResult{RepositoryID: r.ID, Name: r.Name, ManagementURL: url, Err: err})
	}
	return out, nil
}

func sortedWithPrefix(m map[string][]byte, prefix string) []string {
	var out []string
	for k := range m {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
