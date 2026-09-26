// SPDX-License-Identifier: Apache-2.0

package platform

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AxiomOperator/dbr2/internal/escrow"
	"github.com/AxiomOperator/dbr2/internal/repoclient"
	"github.com/AxiomOperator/dbr2/internal/version"
)

// Secrets are the platform secrets from the dbr2-server configuration.
type Secrets struct {
	// SecretKey is DBR2_SECRET_KEY (32 raw bytes).
	SecretKey []byte
	// InternalToken is DBR2_INTERNAL_TOKEN.
	InternalToken string
	// EntraClientSecret is DBR2_ENTRA_CLIENT_SECRET ("" = not configured).
	EntraClientSecret string
}

// StateClient exports a reposerver's state.
type StateClient interface {
	StateExport(ctx context.Context) (io.ReadCloser, error)
}

// Exporter produces Platform Recovery Bundles (runs inside dbr2-server).
type Exporter struct {
	Pool    *pgxpool.Pool
	OrgID   uuid.UUID
	Secrets Secrets
	Log     *slog.Logger
	// Reposerver builds a management client (replaceable in tests).
	Reposerver func(managementURL string) StateClient
	// Now is replaceable in tests.
	Now func() time.Time
	// StateTimeout bounds each reposerver state export.
	StateTimeout time.Duration
}

// NewExporter returns an exporter that reaches reposervers with the
// internal token.
func NewExporter(pool *pgxpool.Pool, orgID uuid.UUID, s Secrets, log *slog.Logger) *Exporter {
	if log == nil {
		log = slog.Default()
	}
	return &Exporter{Pool: pool, OrgID: orgID, Secrets: s, Log: log,
		Reposerver: func(u string) StateClient { return repoclient.New(u, s.InternalToken) }}
}

// Result describes an exported bundle.
type Result struct {
	Manifest     *Manifest
	ManifestJSON []byte
}

// Export writes one encrypted bundle to w.
func (e *Exporter) Export(ctx context.Context, w io.Writer) (*Result, error) {
	if len(e.Secrets.SecretKey) == 0 {
		return nil, fmt.Errorf("platform: DBR2_SECRET_KEY is not available to the exporter")
	}
	now := time.Now
	if e.Now != nil {
		now = e.Now
	}
	tx, err := e.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	recipients, keys, err := loadRecipients(ctx, tx, e.OrgID)
	if err != nil {
		return nil, err
	}
	schema, err := schemaVersion(ctx, tx)
	if err != nil {
		return nil, err
	}
	tables, err := listTables(ctx, tx)
	if err != nil {
		return nil, err
	}
	repos, err := listRepositories(ctx, tx, e.OrgID)
	if err != nil {
		return nil, err
	}
	m := &Manifest{FormatVersion: FormatVersion, Kind: Kind, CreatedAt: now().UTC(), OrgID: e.OrgID.String(),
		PlatformVersion: version.Of(version.Platform), Components: version.All(), SchemaVersion: schema,
		EscrowRecipients: keys, Temporal: TemporalInfo{Included: false, Note: TemporalNote}, Tables: []Table{},
		Secrets: []string{}, Repositories: []RepositoryState{}}
	bw, err := NewWriter(w, recipients, m)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	for _, t := range tables {
		buf.Reset()
		tag, err := tx.Conn().PgConn().CopyTo(ctx, &buf, copySQL(t, "TO STDOUT"))
		if err != nil {
			return nil, fmt.Errorf("platform: export table %s: %w", t.Name, err)
		}
		t.Rows = tag.RowsAffected()
		t.File = "db/" + t.Name + ".copy"
		if err := bw.Add(t.File, buf.Bytes()); err != nil {
			return nil, err
		}
		m.Tables = append(m.Tables, t)
	}
	// The snapshot is complete; release it before talking to reposervers.
	_ = tx.Rollback(ctx)

	secrets := []struct {
		name, value string
	}{
		{SecretKeyFile, base64.StdEncoding.EncodeToString(e.Secrets.SecretKey)},
		{InternalTokenFile, e.Secrets.InternalToken},
		{EntraClientSecretFile, e.Secrets.EntraClientSecret},
	}
	for _, s := range secrets {
		if s.value == "" {
			continue
		}
		if err := bw.Add("secrets/"+s.name, []byte(s.value)); err != nil {
			return nil, err
		}
		m.Secrets = append(m.Secrets, s.name)
	}

	timeout := e.StateTimeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	for _, r := range repos {
		st := RepositoryState{ID: r.id, Name: r.name, ManagementURL: r.managementURL}
		data, err := e.exportState(ctx, r.managementURL, timeout)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			e.Log.WarnContext(ctx, "platform backup: reposerver state unavailable", "repository", r.name, "err", err)
			st.Missing, st.Error = true, err.Error()
		} else {
			st.File = "reposerver/" + r.id + ".tar"
			if err := bw.Add(st.File, data); err != nil {
				return nil, err
			}
		}
		m.Repositories = append(m.Repositories, st)
	}
	mj, err := bw.Close()
	if err != nil {
		return nil, err
	}
	return &Result{Manifest: m, ManifestJSON: mj}, nil
}

func (e *Exporter) exportState(ctx context.Context, url string, timeout time.Duration) ([]byte, error) {
	if e.Reposerver == nil {
		return nil, fmt.Errorf("no reposerver client")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	rc, err := e.Reposerver(url).StateExport(ctx)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	const limit = 256 << 20
	b, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, err
	}
	if len(b) > limit {
		return nil, fmt.Errorf("state archive larger than %d bytes", limit)
	}
	if len(b) == 0 {
		return nil, fmt.Errorf("empty state archive")
	}
	return b, nil
}

func loadRecipients(ctx context.Context, tx pgx.Tx, org uuid.UUID) ([]age.Recipient, []string, error) {
	rows, err := tx.Query(ctx, `SELECT public_key FROM escrow_recipients WHERE org_id = $1 AND removed_at IS NULL ORDER BY created_at`, org)
	if err != nil {
		return nil, nil, err
	}
	keys, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, nil, err
	}
	var rs []age.Recipient
	for _, k := range keys {
		r, err := escrow.ParseRecipient(k)
		if err != nil {
			return nil, nil, fmt.Errorf("platform: escrow recipient %q: %w", k, err)
		}
		rs = append(rs, r)
	}
	if len(rs) == 0 {
		return nil, nil, fmt.Errorf("platform: no escrow recipients configured; add them before the platform can be backed up")
	}
	return rs, keys, nil
}

// querier is satisfied by pgx.Tx, *pgx.Conn and *pgxpool.Pool.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// schemaVersion returns the highest applied goose migration (0 = none).
func schemaVersion(ctx context.Context, q querier) (int64, error) {
	var exists bool
	if err := q.QueryRow(ctx, `SELECT to_regclass('public.`+GooseTable+`') IS NOT NULL`).Scan(&exists); err != nil || !exists {
		return 0, err
	}
	var v int64
	err := q.QueryRow(ctx, `SELECT coalesce(max(version_id), 0) FROM public.`+GooseTable+` WHERE is_applied`).Scan(&v)
	return v, err
}

// listTables returns every ordinary table of the public schema except the
// goose table, with its non-generated columns in attribute order.
func listTables(ctx context.Context, q querier) ([]Table, error) {
	rows, err := q.Query(ctx, `
SELECT c.relname,
       array_agg(a.attname::text ORDER BY a.attnum) FILTER (WHERE a.attnum > 0 AND NOT a.attisdropped AND a.attgenerated = '')
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
JOIN pg_attribute a ON a.attrelid = c.oid
WHERE n.nspname = 'public' AND c.relkind = 'r' AND c.relname <> $1
GROUP BY c.relname
ORDER BY c.relname`, GooseTable)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Table, error) {
		var t Table
		err := r.Scan(&t.Name, &t.Columns)
		return t, err
	})
}

type repoRow struct{ id, name, managementURL string }

func listRepositories(ctx context.Context, q querier, org uuid.UUID) ([]repoRow, error) {
	rows, err := q.Query(ctx, `SELECT id::text, name, management_url FROM repositories
WHERE org_id = $1 AND status <> 'retired' ORDER BY name`, org)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (repoRow, error) {
		var x repoRow
		err := r.Scan(&x.id, &x.name, &x.managementURL)
		return x, err
	})
}

// copySQL builds COPY <table> (<columns>) <dir> (FORMAT binary).
func copySQL(t Table, dir string) string {
	cols := ""
	if len(t.Columns) > 0 {
		cols = " (" + columnList(t.Columns) + ")"
	}
	return "COPY " + pgx.Identifier{"public", t.Name}.Sanitize() + cols + " " + dir + " (FORMAT binary)"
}

func columnList(cols []string) string {
	var b bytes.Buffer
	for i, c := range cols {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(pgx.Identifier{c}.Sanitize())
	}
	return b.String()
}
