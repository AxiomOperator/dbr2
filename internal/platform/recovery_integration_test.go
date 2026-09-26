// SPDX-License-Identifier: Apache-2.0

//go:build integration

package platform

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AxiomOperator/dbr2/db"
	"github.com/AxiomOperator/dbr2/internal/audit"
	"github.com/AxiomOperator/dbr2/internal/auth"
	"github.com/AxiomOperator/dbr2/internal/escrow"
	"github.com/AxiomOperator/dbr2/internal/gateway"
	"github.com/AxiomOperator/dbr2/internal/store"
	"github.com/AxiomOperator/dbr2/internal/testutil"
)

// fakeReposerver serves the reposerver state endpoints.
type fakeReposerver struct {
	*httptest.Server
	state    []byte
	mu       sync.Mutex
	imported []byte
}

func newFakeReposerver(t *testing.T, token string, state []byte) *fakeReposerver {
	f := &fakeReposerver{state: state}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, `{"code":"unauthorized","message":"bad token"}`, http.StatusUnauthorized)
			return
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/state-export":
			w.Header().Set("Content-Type", "application/x-tar")
			_, _ = w.Write(f.state)
		case "POST /v1/state-import":
			b, _ := io.ReadAll(r.Body)
			f.mu.Lock()
			f.imported = b
			f.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

type seeded struct {
	caFingerprint string
	escrowSecret  string
	repoID        uuid.UUID
}

// seed builds realistic platform state through the real services.
func seed(t *testing.T, pool *pgxpool.Pool, secretKey []byte, recipient string, repoURL string) seeded {
	t.Helper()
	ctx := context.Background()
	org := uuid.MustParse(db.DefaultOrgID)
	q := store.New(pool)
	log := testutil.Logger()
	rec := audit.NewRecorder(q, log)

	svc, err := auth.NewService(pool, rec, log, auth.Options{OrgID: org, MasterAdminUsername: "dbr2-admin",
		SessionTTL: time.Hour, SessionIdle: time.Hour, SecretKey: secretKey})
	if err != nil {
		t.Fatal(err)
	}
	if created, err := svc.EnsureMasterAdmin(ctx, func(string, string) error { return nil }); err != nil || !created {
		t.Fatalf("master admin: %v %v", created, err)
	}
	prov, iss, sub, email := "entra", "https://login.example/tenant/v2.0", "subject-1", "ops@example.com"
	user, err := q.CreateOIDCUser(ctx, store.CreateOIDCUserParams{OrgID: org, Username: "ops", DisplayName: "Ops",
		Email: &email, OidcProvider: &prov, OidcIssuer: &iss, OidcSubject: &sub})
	if err != nil {
		t.Fatal(err)
	}

	box, err := auth.NewSecretBox(secretKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, created, err := gateway.LoadOrCreateCA(ctx, q, box, org)
	if err != nil || !created {
		t.Fatalf("CA: %v %v", created, err)
	}
	agent, err := q.CreateAgent(ctx, store.CreateAgentParams{OrgID: org, Hostname: "host-a", AgentVersion: "0.1.0.0", ProtocolVersion: "1"})
	if err != nil {
		t.Fatal(err)
	}

	er, err := q.CreateEscrowRecipient(ctx, store.CreateEscrowRecipientParams{OrgID: org, Name: "safe A", PublicKey: recipient, CreatedBy: &user.ID})
	if err != nil {
		t.Fatal(err)
	}
	code, _ := escrow.NewConfirmationCode()
	secret := "repository-password-" + uuid.NewString()
	pkg, err := escrow.Seal(escrow.Payload{Kind: escrow.KindRepositoryPassword, CreatedAt: time.Now(), RepositoryName: "primary",
		Secret: secret, ConfirmationCode: code, Instructions: escrow.RepositoryInstructions}, []string{recipient})
	if err != nil {
		t.Fatal(err)
	}
	repo, err := q.CreateRepository(ctx, store.CreateRepositoryParams{OrgID: org, Name: "primary", Backend: "nfs", ManagementUrl: repoURL,
		ServerUrl: "https://nas:51515", CertSha256: "ab", IsDefault: true, EscrowPackage: pkg, EscrowRecipientIds: []uuid.UUID{er.ID},
		EscrowConfirmHash: escrow.HashCode(code), CreatedBy: &user.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.ConfirmRepositoryEscrow(ctx, store.ConfirmRepositoryEscrowParams{ID: repo.ID, OrgID: org, EscrowConfirmedBy: &user.ID}); err != nil {
		t.Fatal(err)
	}
	// A second Repository whose reposerver is unreachable (missing state).
	if _, err := q.CreateRepository(ctx, store.CreateRepositoryParams{OrgID: org, Name: "offline", Backend: "nfs",
		ManagementUrl: "http://127.0.0.1:1", ServerUrl: "https://nas2:51515", EscrowRecipientIds: []uuid.UUID{}}); err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		if _, err := q.CreateRecoveryPoint(ctx, store.CreateRecoveryPointParams{ID: "rp_" + uuid.NewString(), OrgID: org, RepositoryID: repo.ID,
			ApplicationID: uuid.New(), ApplicationName: "app", AgentID: agent.ID, Hostname: "host-a", ConsistencyMode: "live",
			Trigger: "manual", WorkflowID: "application/x", RunID: string(rune('a' + i))}); err != nil {
			t.Fatal(err)
		}
	}
	for _, typ := range []string{audit.RepositoryCreated, audit.RepositoryEscrowConfirm, audit.AgentApproved} {
		if _, err := rec.Record(ctx, audit.Event{OrgID: org, Type: typ, ActorKind: audit.ActorSystem, ActorDisplay: "test", Result: audit.Success,
			TargetType: "repository", TargetID: repo.ID.String()}); err != nil {
			t.Fatal(err)
		}
	}
	tt, tid := "repository", repo.ID.String()
	if err := q.InsertNotification(ctx, store.InsertNotificationParams{OrgID: org, Severity: "warning", EventType: "test.alert",
		TargetType: &tt, TargetID: &tid, Message: "seeded", Payload: []byte(`{"k":"v"}`)}); err != nil {
		t.Fatal(err)
	}
	return seeded{caFingerprint: ca.Fingerprint(), escrowSecret: secret, repoID: repo.ID}
}

func rowCounts(t *testing.T, pool *pgxpool.Pool) map[string]int64 {
	t.Helper()
	ctx := context.Background()
	tables, err := listTables(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int64{}
	for _, tb := range tables {
		var n int64
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM public."+`"`+tb.Name+`"`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		out[tb.Name] = n
	}
	return out
}

// TestPlatformRecovery is the platform recovery test (ADR-0008 §4): seed a
// source platform, export a bundle, restore it into a fresh database and
// check that the restored platform is usable.
func TestPlatformRecovery(t *testing.T) {
	ctx := context.Background()
	src, _ := testutil.Pool(t)
	dst, _ := testutil.Pool(t)

	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	secretKey := make([]byte, 32)
	_, _ = rand.Read(secretKey)
	token := "internal-token-" + uuid.NewString()
	state := []byte("reposerver-state-tar")
	rs := newFakeReposerver(t, token, state)
	s := seed(t, src, secretKey, id.Recipient().String(), rs.URL)
	org := uuid.MustParse(db.DefaultOrgID)

	exp := NewExporter(src, org, Secrets{SecretKey: secretKey, InternalToken: token, EntraClientSecret: "entra-secret"}, testutil.Logger())
	exp.StateTimeout = 5 * time.Second
	var bundle bytes.Buffer
	res, err := exp.Export(ctx, &bundle)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(res.ManifestJSON, []byte(token)) || bytes.Contains(res.ManifestJSON, []byte("entra-secret")) {
		t.Fatal("manifest contains secret values")
	}
	if missing := res.Manifest.MissingRepositories(); len(missing) != 1 {
		t.Fatalf("missing repositories: %v", missing)
	}
	var mj map[string]any
	if err := json.Unmarshal(res.ManifestJSON, &mj); err != nil || mj["temporal"] == nil {
		t.Fatalf("manifest: %v %v", err, mj)
	}

	// A flipped byte anywhere is rejected (age authentication).
	tampered := bytes.Clone(bundle.Bytes())
	tampered[len(tampered)/2] ^= 0x40
	if _, err := Open(bytes.NewReader(tampered), id); err == nil {
		t.Fatal("tampered bundle accepted")
	}

	b, err := Open(bytes.NewReader(bundle.Bytes()), id)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b.Files["reposerver/"+s.repoID.String()+".tar"], state) {
		t.Fatal("reposerver state not in the bundle")
	}
	// The source database is not empty: refused without --force.
	if _, err := Restore(ctx, src, b, RestoreOptions{}); !errors.Is(err, ErrNotEmpty) {
		t.Fatalf("restore over a populated database: %v", err)
	}
	r, err := Restore(ctx, dst, b, RestoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Tables != len(b.Manifest.Tables) || r.Rows == 0 {
		t.Fatalf("restore result %+v", r)
	}

	// Row counts match table by table.
	want, got := rowCounts(t, src), rowCounts(t, dst)
	for name, n := range want {
		if got[name] != n {
			t.Errorf("table %s: %d rows restored, source has %d", name, got[name], n)
		}
	}

	// The append-only audit trigger is enforced again after the restore.
	if _, err := dst.Exec(ctx, `UPDATE audit_events SET reason = 'tamper'`); err == nil {
		t.Fatal("audit_events UPDATE allowed after restore")
	}

	// Sequences were reset: new rows get fresh identities.
	q := store.New(dst)
	if _, err := audit.NewRecorder(q, testutil.Logger()).Record(ctx, audit.Event{OrgID: org, Type: audit.AgentApproved,
		ActorKind: audit.ActorSystem, ActorDisplay: "test", Result: audit.Success}); err != nil {
		t.Fatalf("new audit event after restore: %v", err)
	}
	tt := "x"
	if err := q.InsertNotification(ctx, store.InsertNotificationParams{OrgID: org, Severity: "info", EventType: "t", TargetType: &tt,
		TargetID: &tt, Message: "m", Payload: []byte(`{}`)}); err != nil {
		t.Fatalf("new notification after restore: %v", err)
	}

	// The CA private key decrypts with the restored secret key.
	key, err := base64.StdEncoding.DecodeString(b.Secret(SecretKeyFile))
	if err != nil || !bytes.Equal(key, secretKey) {
		t.Fatal("restored secret key differs")
	}
	box, _ := auth.NewSecretBox(key)
	ca, created, err := gateway.LoadOrCreateCA(ctx, q, box, org)
	if err != nil || created || ca.Fingerprint() != s.caFingerprint {
		t.Fatalf("restored CA: created=%v err=%v", created, err)
	}

	// The escrow package decrypts with the escrow identity.
	var pkg []byte
	if err := dst.QueryRow(ctx, `SELECT escrow_package FROM repositories WHERE id = $1`, s.repoID).Scan(&pkg); err != nil {
		t.Fatal(err)
	}
	p, err := escrow.Open(pkg, id)
	if err != nil || p.Secret != s.escrowSecret {
		t.Fatalf("escrow package: %v", err)
	}

	// Reposerver state goes back to the (restored) management URL.
	results, err := ImportReposerverState(ctx, dst, b, nil)
	if err != nil || len(results) != 1 || results[0].Err != nil {
		t.Fatalf("import: %+v %v", results, err)
	}
	rs.mu.Lock()
	imported := rs.imported
	rs.mu.Unlock()
	if !bytes.Equal(imported, state) {
		t.Fatal("reposerver state not imported")
	}

	// --force replaces a populated database.
	if _, err := Restore(ctx, dst, b, RestoreOptions{Force: true}); err != nil {
		t.Fatalf("forced restore: %v", err)
	}
	if got := rowCounts(t, dst); got["audit_events"] != want["audit_events"] {
		t.Fatalf("forced restore audit rows %d, want %d", got["audit_events"], want["audit_events"])
	}
}

// TestRestoreMigratesEmptyDatabase restores into a database with no schema.
func TestRestoreMigratesEmptyDatabase(t *testing.T) {
	ctx := context.Background()
	src, _ := testutil.Pool(t)
	dst, _ := testutil.Pool(t)
	// Drop the schema: restore-platform must migrate to the bundle's version.
	if _, err := dst.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
		t.Fatal(err)
	}
	id, _ := age.GenerateX25519Identity()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	org := uuid.MustParse(db.DefaultOrgID)
	if _, err := store.New(src).CreateEscrowRecipient(ctx, store.CreateEscrowRecipientParams{OrgID: org, Name: "a", PublicKey: id.Recipient().String()}); err != nil {
		t.Fatal(err)
	}
	var bundle bytes.Buffer
	if _, err := NewExporter(src, org, Secrets{SecretKey: key, InternalToken: "t"}, nil).Export(ctx, &bundle); err != nil {
		t.Fatal(err)
	}
	b, err := Open(&bundle, id)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Restore(ctx, dst, b, RestoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if r.MigratedTo != b.Manifest.SchemaVersion {
		t.Fatalf("schema %d, want %d", r.MigratedTo, b.Manifest.SchemaVersion)
	}
	var n int
	if err := dst.QueryRow(ctx, `SELECT count(*) FROM escrow_recipients`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("escrow recipients restored: %d %v", n, err)
	}
}
