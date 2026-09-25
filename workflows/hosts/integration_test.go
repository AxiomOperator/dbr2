// SPDX-License-Identifier: Apache-2.0

//go:build integration

package hosts_test

import (
	"context"
	"crypto/rand"
	"net"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/AxiomOperator/dbr2/db"
	"github.com/AxiomOperator/dbr2/internal/agent"
	controlv1 "github.com/AxiomOperator/dbr2/internal/agentpb/control/v1"
	"github.com/AxiomOperator/dbr2/internal/audit"
	"github.com/AxiomOperator/dbr2/internal/auth"
	"github.com/AxiomOperator/dbr2/internal/gateway"
	"github.com/AxiomOperator/dbr2/internal/inventory"
	"github.com/AxiomOperator/dbr2/internal/runtime"
	"github.com/AxiomOperator/dbr2/internal/store"
	"github.com/AxiomOperator/dbr2/internal/temporalx"
	"github.com/AxiomOperator/dbr2/internal/testutil"
	"github.com/AxiomOperator/dbr2/workflows"
	"github.com/AxiomOperator/dbr2/workflows/diag"
	"github.com/AxiomOperator/dbr2/workflows/hosts"
)

// slowRuntime takes a while to discover so the test can drop the session
// mid-command.
type slowRuntime struct{ runs atomic.Int32 }

func (s *slowRuntime) Name() string                         { return "fake" }
func (s *slowRuntime) Close() error                         { return nil }
func (s *slowRuntime) Ping(context.Context) (string, error) { return "29.8.1", nil }
func (s *slowRuntime) Discover(ctx context.Context, _ runtime.DiscoverOptions) (*inventory.Inventory, error) {
	s.runs.Add(1)
	select {
	case <-time.After(2 * time.Second):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &inventory.Inventory{SchemaVersion: 1, CollectedAt: time.Now().UTC(),
		Containers: []inventory.Container{{ID: "c1", Name: "web-1", Image: "nginx", State: "running",
			Labels: map[string]string{inventory.LabelProject: "web", inventory.LabelService: "web"}}}}, nil
}

func TestDiscoverHostThroughGatewayWithResume(t *testing.T) {
	ctx := context.Background()
	pool, _ := testutil.Pool(t)
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, _ := auth.NewSecretBox(key)
	log := testutil.Logger()
	org := uuid.MustParse(db.DefaultOrgID)
	q := store.New(pool)
	ca, _, err := gateway.LoadOrCreateCA(ctx, q, box, org)
	if err != nil {
		t.Fatal(err)
	}
	gw, err := gateway.New(gateway.Config{OrgID: org, Hostnames: []string{"localhost"}, HeartbeatInterval: 200 * time.Millisecond},
		pool, box, ca, audit.NewRecorder(q, log), log)
	if err != nil {
		t.Fatal(err)
	}
	agentLis, _ := net.Listen("tcp", "127.0.0.1:0")
	agentSrv := gw.GRPCServer()
	go func() { _ = agentSrv.Serve(agentLis) }()
	t.Cleanup(agentSrv.Stop)
	const token = "internal-token-for-tests-0123456789abcdef"
	ctrlLis, _ := net.Listen("tcp", "127.0.0.1:0")
	ctrlSrv := gw.ControlServer(token)
	go func() { _ = ctrlSrv.Serve(ctrlLis) }()
	t.Cleanup(ctrlSrv.Stop)

	// Enroll, approve and run an agent.
	tok, _ := auth.NewToken(gateway.RegistrationTokenPrefix)
	_, _ = q.CreateRegistrationToken(ctx, store.CreateRegistrationTokenParams{OrgID: org, TokenHash: auth.HashToken(tok), Prefix: "p",
		Description: "t", ExpiresAt: time.Now().Add(time.Hour)})
	dir := t.TempDir()
	_, port, _ := net.SplitHostPort(agentLis.Addr().String())
	agentID, err := agent.Enroll(ctx, agent.EnrollOptions{Server: "localhost:" + port, Token: tok, CASHA256: ca.Fingerprint(),
		ConfigPath: filepath.Join(dir, "a.yaml"), StateDir: dir, Hostname: "h"})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = q.SetAgentStatus(ctx, store.SetAgentStatusParams{ID: uuid.MustParse(agentID), Status: "active", OrgID: org})
	cfg, _ := agent.LoadConfig(filepath.Join(dir, "a.yaml"))
	rt := &slowRuntime{}
	ag, err := agent.New(cfg, rt, log)
	if err != nil {
		t.Fatal(err)
	}
	ag.MaxBackoff = 200 * time.Millisecond
	// Keep the periodic inventory push out of the execution count: the dev
	// server can take >10 s to start on CI runners.
	ag.FirstInventoryDelay = time.Hour
	actx, stop := context.WithCancel(ctx)
	defer stop()
	go func() { _ = ag.Run(actx) }()

	// Temporal dev server + worker with the gateway-dispatching activities.
	srv, err := testsuite.StartDevServer(ctx, testsuite.DevServerOptions{ClientOptions: &client.Options{Namespace: "default"}})
	if err != nil {
		t.Skipf("temporal dev server unavailable: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop() })
	c := srv.Client()
	conn, _ := grpc.NewClient(ctrlLis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	defer conn.Close()
	w := worker.New(c, "dbr2-test", worker.Options{})
	workflows.Register(w, &diag.Activities{}, &hosts.Activities{Control: controlv1.NewGatewayControlServiceClient(conn), Token: token, WaitForAgent: time.Minute}, nil)
	if err := w.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Stop)

	for !gw.Connected(agentID) {
		time.Sleep(50 * time.Millisecond)
	}
	run, err := temporalx.StartHostOperation(ctx, c, "dbr2-test", agentID, "discover", hosts.DiscoverHost, hosts.DiscoverInput{AgentID: agentID})
	if err != nil {
		t.Fatal(err)
	}
	// A second discovery for the same host is refused while one runs.
	if _, err := temporalx.StartHostOperation(ctx, c, "dbr2-test", agentID, "discover", hosts.DiscoverHost, hosts.DiscoverInput{AgentID: agentID}); err == nil {
		t.Fatal("concurrent discovery of the same host must be refused")
	}
	// Drop the agent's session mid-discovery.
	time.Sleep(700 * time.Millisecond)
	gw.ForceReconnect(agentID)

	var res hosts.DiscoverResult
	if err := run.Get(ctx, &res); err != nil {
		t.Fatalf("workflow failed: %v", err)
	}
	if res.InventoryBytes == 0 || !strings.HasPrefix(res.CommandID, "host/"+agentID+"/discover/") {
		t.Fatalf("result: %+v", res)
	}
	if n := rt.runs.Load(); n != 1 {
		t.Fatalf("discovery executed %d times; the resumed command must run exactly once", n)
	}
	if _, err := q.GetInventorySnapshot(ctx, uuid.MustParse(agentID)); err != nil {
		t.Fatalf("inventory not ingested: %v", err)
	}

	// A suspended agent fails fast (non-retryable), without waiting.
	_, _ = q.SetAgentStatus(ctx, store.SetAgentStatusParams{ID: uuid.MustParse(agentID), Status: "suspended", OrgID: org})
	run2, err := temporalx.StartHostOperation(ctx, c, "dbr2-test", agentID, "discover", hosts.DiscoverHost, hosts.DiscoverInput{AgentID: agentID})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err = run2.Get(ctx, nil)
	if err == nil || !strings.Contains(err.Error(), "not active") || time.Since(start) > 30*time.Second {
		t.Fatalf("suspended agent: err=%v after %s", err, time.Since(start))
	}
}
