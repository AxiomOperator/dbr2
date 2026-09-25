// SPDX-License-Identifier: Apache-2.0

//go:build integration

package gateway_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/AxiomOperator/dbr2/db"
	"github.com/AxiomOperator/dbr2/internal/agent"
	agentv1 "github.com/AxiomOperator/dbr2/internal/agentpb/agent/v1"
	"github.com/AxiomOperator/dbr2/internal/audit"
	"github.com/AxiomOperator/dbr2/internal/auth"
	"github.com/AxiomOperator/dbr2/internal/gateway"
	"github.com/AxiomOperator/dbr2/internal/inventory"
	"github.com/AxiomOperator/dbr2/internal/runtime"
	"github.com/AxiomOperator/dbr2/internal/store"
	"github.com/AxiomOperator/dbr2/internal/testutil"
)

type fakeRuntime struct{ discovers atomic.Int32 }

func (f *fakeRuntime) Name() string                         { return "fake" }
func (f *fakeRuntime) Close() error                         { return nil }
func (f *fakeRuntime) Ping(context.Context) (string, error) { return "29.8.1", nil }
func (f *fakeRuntime) Discover(context.Context, runtime.DiscoverOptions) (*inventory.Inventory, error) {
	f.discovers.Add(1)
	return &inventory.Inventory{SchemaVersion: 1, CollectedAt: time.Now().UTC(), Host: inventory.Host{Hostname: "host-a", RootDir: "/var/lib/docker"},
		Containers: []inventory.Container{
			{ID: "c1", Name: "shop-db-1", Image: "postgres:18", State: "running",
				Labels: map[string]string{inventory.LabelProject: "shop", inventory.LabelService: "db"},
				Env:    []inventory.EnvVar{{Key: "POSTGRES_PASSWORD", Value: "top-secret-pw"}}},
			{ID: "c2", Name: "legacy", Image: "legacy:1", State: "exited"},
		}}, nil
}

type env struct {
	t    *testing.T
	pool *pgxpool.Pool
	q    *store.Queries
	gw   *gateway.Gateway
	addr string
	org  uuid.UUID
}

func newEnv(t *testing.T) *env {
	pool, _ := testutil.Pool(t)
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, _ := auth.NewSecretBox(key)
	log := testutil.Logger()
	org := uuid.MustParse(db.DefaultOrgID)
	q := store.New(pool)
	ca, created, err := gateway.LoadOrCreateCA(context.Background(), q, box, org)
	if err != nil || !created {
		t.Fatalf("CA: created=%v err=%v", created, err)
	}
	if again, created2, err := gateway.LoadOrCreateCA(context.Background(), q, box, org); err != nil || created2 || again.Fingerprint() != ca.Fingerprint() {
		t.Fatalf("CA reload: %v %v", created2, err)
	}
	gw, err := gateway.New(gateway.Config{OrgID: org, Hostnames: []string{"localhost", "127.0.0.1"}, HeartbeatInterval: 200 * time.Millisecond},
		pool, box, ca, audit.NewRecorder(q, log), log)
	if err != nil {
		t.Fatal(err)
	}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := gw.GRPCServer()
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return &env{t: t, pool: pool, q: q, gw: gw, addr: lis.Addr().String(), org: org}
}

func (e *env) token() string {
	tok, _ := auth.NewToken(gateway.RegistrationTokenPrefix)
	if _, err := e.q.CreateRegistrationToken(context.Background(), store.CreateRegistrationTokenParams{
		OrgID: e.org, TokenHash: auth.HashToken(tok), Prefix: tok[:14], Description: "test", ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		e.t.Fatal(err)
	}
	return tok
}

func (e *env) status(id, s string) {
	if _, err := e.q.SetAgentStatus(context.Background(), store.SetAgentStatusParams{ID: uuid.MustParse(id), Status: s, OrgID: e.org}); err != nil {
		e.t.Fatal(err)
	}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func echo(id string, d time.Duration) *agentv1.Command {
	return &agentv1.Command{CommandId: id, DeadlineUnixMs: time.Now().Add(time.Minute).UnixMilli(),
		Kind: &agentv1.Command_Echo{Echo: &agentv1.EchoCommand{Message: "pong:" + id, DurationMs: uint32(d / time.Millisecond)}}}
}

func TestEnrollApproveDispatchResumeLifecycle(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	dir := t.TempDir()
	opts := agent.EnrollOptions{Server: e.addr, Token: e.token(), CASHA256: e.gw.CA().Fingerprint(),
		ConfigPath: filepath.Join(dir, "agent.yaml"), StateDir: filepath.Join(dir, "state"), Hostname: "host-a"}

	// A wrong CA pin is refused before any credential is sent.
	bad := opts
	bad.CASHA256 = strings.Repeat("0", 64)
	if _, err := agent.Enroll(ctx, bad); err == nil || !strings.Contains(err.Error(), "fingerprint mismatch") {
		t.Fatalf("wrong pin: %v", err)
	}
	agentID, err := agent.Enroll(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(filepath.Join(dir, "state", "agent.key")); st.Mode().Perm() != 0o600 {
		t.Fatalf("key mode %v", st.Mode())
	}
	if st, _ := os.Stat(opts.ConfigPath); st.Mode().Perm() != 0o600 {
		t.Fatalf("config mode %v", st.Mode())
	}
	// Single-use token.
	again := opts
	again.StateDir, again.ConfigPath = filepath.Join(dir, "s2"), filepath.Join(dir, "a2.yaml")
	if _, err := agent.Enroll(ctx, again); err == nil || !strings.Contains(err.Error(), "already used") {
		t.Fatalf("token reuse: %v", err)
	}

	cfg, err := agent.LoadConfig(opts.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	rt := &fakeRuntime{}
	ag, err := agent.New(cfg, rt, testutil.Logger())
	if err != nil {
		t.Fatal(err)
	}
	ag.MaxBackoff = 300 * time.Millisecond
	ag.FirstInventoryDelay = time.Hour
	actx, stop := context.WithCancel(ctx)
	defer stop()
	go func() { _ = ag.Run(actx) }()

	// Pending: rejected until approved.
	time.Sleep(700 * time.Millisecond)
	if e.gw.Connected(agentID) {
		t.Fatal("pending agent must not get a session")
	}
	if _, err := e.gw.Dispatch(ctx, agentID, echo("x", 0), nil); err == nil {
		t.Fatal("dispatch to a pending agent must fail")
	}
	e.status(agentID, "active")
	eventually(t, "session after approval", func() bool { return e.gw.Connected(agentID) })

	res, err := e.gw.Dispatch(ctx, agentID, echo("cmd-1", 50*time.Millisecond), nil)
	if err != nil || res.State != agentv1.CommandState_COMMAND_STATE_SUCCEEDED || res.GetEcho().Message != "pong:cmd-1" {
		t.Fatalf("dispatch: %v %v", res, err)
	}

	// Disconnect mid-command: the command keeps running on the agent, the
	// dispatcher re-sends it on the new session, and it completes once.
	done := make(chan *agentv1.CommandUpdate, 1)
	go func() {
		u, err := e.gw.Dispatch(ctx, agentID, echo("cmd-2", 1500*time.Millisecond), nil)
		if err != nil {
			t.Error(err)
		}
		done <- u
	}()
	time.Sleep(300 * time.Millisecond)
	if !e.gw.ForceReconnect(agentID) {
		t.Fatal("no session to drop")
	}
	select {
	case u := <-done:
		if u.State != agentv1.CommandState_COMMAND_STATE_SUCCEEDED || u.GetEcho().Message != "pong:cmd-2" {
			t.Fatalf("resumed result: %v", u)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("command did not complete after reconnect")
	}
	journal, _ := os.ReadFile(filepath.Join(dir, "state", "journal.jsonl"))
	if n := bytes.Count(journal, []byte(`"command_id":"cmd-2","kind":"echo","state":"accepted"`)); n != 1 {
		t.Fatalf("cmd-2 accepted %d times (must execute exactly once)\n%s", n, journal)
	}
	// Re-dispatching a finished command returns the journaled result.
	if u, err := e.gw.Dispatch(ctx, agentID, echo("cmd-2", time.Hour), nil); err != nil || u.GetEcho().Message != "pong:cmd-2" {
		t.Fatalf("idempotent re-dispatch: %v %v", u, err)
	}

	// Discovery: inventory ingested, secrets sealed, applications created.
	du, err := e.gw.Dispatch(ctx, agentID, &agentv1.Command{CommandId: "disc-1", Kind: &agentv1.Command_Discover{Discover: &agentv1.DiscoverCommand{IncludeFilesystemChanges: true}}}, nil)
	if err != nil || du.State != agentv1.CommandState_COMMAND_STATE_SUCCEEDED {
		t.Fatalf("discover: %v %v", du, err)
	}
	snap, err := e.q.GetInventorySnapshot(ctx, uuid.MustParse(agentID))
	if err != nil || bytes.Contains(snap.Data, []byte("top-secret-pw")) {
		t.Fatalf("snapshot missing or secret stored in clear: %v", err)
	}
	apps, _ := e.q.ListAgentApplications(ctx, uuid.MustParse(agentID))
	keys := map[string]bool{}
	for _, a := range apps {
		keys[a.Key] = true
	}
	if !keys["compose:shop"] || !keys["container:legacy"] {
		t.Fatalf("applications: %v", keys)
	}

	// Health, latency and lease are recorded.
	eventually(t, "latency and health", func() bool {
		a, _ := e.q.GetAgent(ctx, uuid.MustParse(agentID))
		return a.LastLatencyMs != nil && a.DockerReachable != nil && *a.DockerReachable
	})
	sessions, _ := e.q.ListAgentSessions(ctx)
	if len(sessions) != 1 || sessions[0].AgentID.String() != agentID {
		t.Fatalf("lease: %+v", sessions)
	}

	// Renewal: new certificate, old one revoked.
	before, _ := e.q.GetAgent(ctx, uuid.MustParse(agentID))
	ag.RenewAfter = time.Nanosecond
	e.gw.ForceReconnect(agentID)
	eventually(t, "renewed certificate", func() bool {
		a, _ := e.q.GetAgent(ctx, uuid.MustParse(agentID))
		return a.CertSerial != nil && *a.CertSerial != *before.CertSerial && e.gw.Connected(agentID)
	})
	ag.RenewAfter = 0
	old, _ := e.q.GetAgentCertificate(ctx, *before.CertSerial)
	if old.RevokedAt == nil {
		t.Fatal("previous certificate not revoked after renewal")
	}

	// Suspend: session ended, dispatch refused, reconnects rejected.
	e.status(agentID, "suspended")
	e.gw.Disconnect(agentID, gateway.RejectSuspended, "suspended by test")
	eventually(t, "suspended agent disconnected", func() bool { return !e.gw.Connected(agentID) })
	if _, err := e.gw.Dispatch(ctx, agentID, echo("cmd-3", 0), nil); err == nil {
		t.Fatal("dispatch to suspended agent must fail")
	}
	time.Sleep(700 * time.Millisecond)
	if e.gw.Connected(agentID) {
		t.Fatal("suspended agent reconnected")
	}
	e.status(agentID, "active")
	eventually(t, "resumed agent reconnects", func() bool { return e.gw.Connected(agentID) })

	// Revoke: certificates revoked, never reconnects.
	_ = e.q.RevokeAgentCertificates(ctx, uuid.MustParse(agentID))
	e.status(agentID, "revoked")
	e.gw.Disconnect(agentID, gateway.RejectRevoked, "revoked by test")
	time.Sleep(time.Second)
	if e.gw.Connected(agentID) {
		t.Fatal("revoked agent reconnected")
	}
}

// TestProtocolMismatchAndAnonymousRejected checks the gateway refuses
// sessions without a client certificate and agents speaking another
// protocol MAJOR (ADR-0015).
func TestProtocolMismatchAndAnonymousRejected(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	dir := t.TempDir()
	opts := agent.EnrollOptions{Server: e.addr, Token: e.token(), CASHA256: e.gw.CA().Fingerprint(),
		ConfigPath: filepath.Join(dir, "agent.yaml"), StateDir: dir, Hostname: "host-b"}
	agentID, err := agent.Enroll(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	e.status(agentID, "active")

	pool := x509.NewCertPool()
	pool.AddCert(e.gw.CA().Cert)
	anon, _ := grpc.NewClient(e.addr, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{RootCAs: pool, ServerName: "localhost"})))
	defer anon.Close()
	st, err := agentv1.NewAgentServiceClient(anon).Connect(ctx)
	if err == nil {
		_ = st.Send(&agentv1.ConnectRequest{Body: &agentv1.ConnectRequest_Hello{Hello: &agentv1.Hello{ProtocolVersion: "0.2.0.0"}}})
		_, err = st.Recv()
	}
	if err == nil || !strings.Contains(err.Error(), "client certificate") {
		t.Fatalf("anonymous session: %v", err)
	}

	cert, err := tls.LoadX509KeyPair(filepath.Join(dir, "agent.crt"), filepath.Join(dir, "agent.key"))
	if err != nil {
		t.Fatal(err)
	}
	conn, _ := grpc.NewClient(e.addr, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{RootCAs: pool, ServerName: "localhost", Certificates: []tls.Certificate{cert}})))
	defer conn.Close()
	st, err = agentv1.NewAgentServiceClient(conn).Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_ = st.Send(&agentv1.ConnectRequest{Body: &agentv1.ConnectRequest_Hello{Hello: &agentv1.Hello{AgentVersion: "9.0.0.1", ProtocolVersion: "9.0.0.0", Hostname: "host-b"}}})
	m, err := st.Recv()
	if err != nil || m.GetReject().GetCode() != gateway.RejectProtocolVersion {
		t.Fatalf("protocol mismatch not rejected: %v %v", m, err)
	}
}
