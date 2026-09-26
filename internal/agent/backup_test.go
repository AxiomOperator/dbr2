// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	agentv1 "github.com/AxiomOperator/dbr2/internal/agentpb/agent/v1"
	"github.com/AxiomOperator/dbr2/internal/engine"
	"github.com/AxiomOperator/dbr2/internal/engine/kopia"
	"github.com/AxiomOperator/dbr2/internal/fsmeta"
	"github.com/AxiomOperator/dbr2/internal/inventory"
	"github.com/AxiomOperator/dbr2/internal/manifest"
	"github.com/AxiomOperator/dbr2/internal/runtime"
)

// fakeRuntime is an in-memory container engine with ContainerControl.
type fakeRuntime struct {
	mu         sync.Mutex
	state      map[string]string
	calls      []string
	failResume bool
	exec       func(id string, cmd []string) (runtime.ExecResult, error)
}

func newFake(states map[string]string) *fakeRuntime { return &fakeRuntime{state: states} }

func (f *fakeRuntime) Name() string                         { return "fake" }
func (f *fakeRuntime) Close() error                         { return nil }
func (f *fakeRuntime) Ping(context.Context) (string, error) { return "1", nil }
func (f *fakeRuntime) Discover(context.Context, runtime.DiscoverOptions) (*inventory.Inventory, error) {
	return &inventory.Inventory{}, nil
}

func (f *fakeRuntime) get(id string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state[id]
}

func (f *fakeRuntime) nCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeRuntime) InspectState(_ context.Context, id string) (runtime.ContainerState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.state[id]
	if !ok {
		return runtime.ContainerState{}, fmt.Errorf("no such container %s", id)
	}
	return runtime.ContainerState{ID: id, Name: "n-" + id, State: s}, nil
}

func (f *fakeRuntime) InspectRaw(_ context.Context, id string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.state[id]; !ok {
		return nil, fmt.Errorf("no such container %s", id)
	}
	return []byte(`{"Id":"` + id + `","Config":{"Env":["SECRET=real"]}}`), nil
}

func (f *fakeRuntime) transition(op, id, from, to string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, op+" "+id)
	if (op == "unpause" || op == "start") && f.failResume {
		return errors.New("engine exploded")
	}
	if f.state[id] != from {
		return fmt.Errorf("%s: container %s is %s", op, id, f.state[id])
	}
	f.state[id] = to
	return nil
}

func (f *fakeRuntime) Pause(_ context.Context, id string) error {
	return f.transition("pause", id, "running", "paused")
}
func (f *fakeRuntime) Unpause(_ context.Context, id string) error {
	return f.transition("unpause", id, "paused", "running")
}
func (f *fakeRuntime) Stop(_ context.Context, id string) error {
	return f.transition("stop", id, "running", "exited")
}
func (f *fakeRuntime) Start(_ context.Context, id string) error {
	return f.transition("start", id, "exited", "running")
}
func (f *fakeRuntime) Exec(ctx context.Context, id string, cmd []string, max int) (runtime.ExecResult, error) {
	r, err := f.exec(id, cmd)
	if len(r.Output) > max {
		r.Output, r.Truncated = r.Output[len(r.Output)-max:], true
	}
	if err == nil && ctx.Err() != nil {
		err = ctx.Err()
	}
	return r, err
}

type testAgent struct {
	*Agent
	out  *outbound
	logs *bytes.Buffer
}

func newTestAgent(t *testing.T, dir string, rt runtime.ContainerRuntime) *testAgent {
	t.Helper()
	logs := &bytes.Buffer{}
	a := &Agent{cfg: &Config{StateDir: dir}, rt: rt, log: slog.New(slog.NewTextHandler(&syncWriter{w: logs}, nil))}
	if err := a.init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.journal.Close(); a.repos.closeAll() })
	out := &outbound{ch: make(chan *agentv1.ConnectRequest, 1000), done: make(chan struct{})}
	a.cur = out
	return &testAgent{Agent: a, out: out, logs: logs}
}

type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// waitEvent reads outbound messages until an event of type typ arrives.
func (ta *testAgent) waitEvent(t *testing.T, typ string, timeout time.Duration) *agentv1.AgentEvent {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case m := <-ta.out.ch:
			if e := m.GetEvent(); e != nil && e.Type == typ {
				return e
			}
		case <-deadline:
			t.Fatalf("no %s event within %s", typ, timeout)
		}
	}
}

func (ta *testAgent) run(t *testing.T, c *agentv1.Command) *agentv1.CommandUpdate {
	t.Helper()
	if c.CommandId == "" {
		c.CommandId = fmt.Sprintf("cmd-%d", time.Now().UnixNano())
	}
	return ta.execute(context.Background(), c)
}

func quiesceCmd(lease, app string, mode agentv1.QuiesceMode, secs uint32, ids ...string) *agentv1.Command {
	return &agentv1.Command{Kind: &agentv1.Command_Quiesce{Quiesce: &agentv1.QuiesceCommand{
		LeaseId: lease, ApplicationId: app, ContainerIds: ids, Mode: mode, LeaseSeconds: secs}}}
}

func resumeCmd(lease string) *agentv1.Command {
	return &agentv1.Command{Kind: &agentv1.Command_Resume{Resume: &agentv1.ResumeCommand{LeaseId: lease}}}
}

func succeeded(t *testing.T, u *agentv1.CommandUpdate) {
	t.Helper()
	if u.State != agentv1.CommandState_COMMAND_STATE_SUCCEEDED {
		t.Fatalf("state %s: %s (retryable=%v)", u.State, u.Error, u.Retryable)
	}
}

func TestQuiescePauseResumePreState(t *testing.T) {
	rt := newFake(map[string]string{"c1": "running", "c2": "exited", "c3": "paused"})
	ta := newTestAgent(t, t.TempDir(), rt)

	u := ta.run(t, quiesceCmd("rp1", "app1", agentv1.QuiesceMode_QUIESCE_MODE_PAUSE, 600, "c1", "c2", "c3"))
	succeeded(t, u)
	q := u.GetQuiesce()
	if len(q.PreState) != 3 || q.PreState[0].State != "running" || q.PreState[1].State != "exited" || q.PreState[2].State != "paused" {
		t.Fatalf("pre-state %v", q.PreState)
	}
	if q.LeaseExpiresUnixMs-q.QuiescedAtUnixMs != 600_000 {
		t.Fatalf("lease %d", q.LeaseExpiresUnixMs-q.QuiescedAtUnixMs)
	}
	if rt.get("c1") != "paused" || rt.get("c2") != "exited" || rt.get("c3") != "paused" || rt.nCalls() != 1 {
		t.Fatalf("state %v calls %v", rt.state, rt.calls)
	}
	st, err := os.Stat(filepath.Join(ta.cfg.StateDir, leasesFile))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("leases.json: %v %v", st, err)
	}

	// Idempotent repeat returns the recorded pre-state, touches nothing.
	u2 := ta.run(t, quiesceCmd("rp1", "app1", agentv1.QuiesceMode_QUIESCE_MODE_PAUSE, 600, "c1", "c2", "c3"))
	succeeded(t, u2)
	if u2.GetQuiesce().PreState[0].State != "running" || u2.GetQuiesce().QuiescedAtUnixMs != q.QuiescedAtUnixMs || rt.nCalls() != 1 {
		t.Fatalf("repeat: %v calls %v", u2.GetQuiesce(), rt.calls)
	}

	// A different lease for the same application is refused, permanently.
	u3 := ta.run(t, quiesceCmd("rp2", "app1", agentv1.QuiesceMode_QUIESCE_MODE_PAUSE, 600, "c1"))
	if u3.State != agentv1.CommandState_COMMAND_STATE_FAILED || u3.Retryable || !strings.Contains(u3.Error, "application already quiesced by lease rp1") {
		t.Fatalf("conflict: %+v", u3)
	}

	r := ta.run(t, resumeCmd("rp1"))
	succeeded(t, r)
	if got := r.GetResume(); got.AlreadyResumed || got.AutoResumed || len(got.ResumedContainerIds) != 1 || got.ResumedContainerIds[0] != "c1" {
		t.Fatalf("resume %+v", got)
	}
	if rt.get("c1") != "running" || rt.get("c3") != "paused" || rt.get("c2") != "exited" {
		t.Fatalf("after resume %v", rt.state)
	}
	r2 := ta.run(t, resumeCmd("rp1"))
	succeeded(t, r2)
	if !r2.GetResume().AlreadyResumed {
		t.Fatalf("second resume %+v", r2.GetResume())
	}
	// The application can be quiesced again with a new lease.
	succeeded(t, ta.run(t, quiesceCmd("rp2", "app1", agentv1.QuiesceMode_QUIESCE_MODE_PAUSE, 600, "c1")))
}

func TestQuiesceStopMode(t *testing.T) {
	rt := newFake(map[string]string{"c1": "running", "c2": "exited"})
	ta := newTestAgent(t, t.TempDir(), rt)
	succeeded(t, ta.run(t, quiesceCmd("rp1", "app1", agentv1.QuiesceMode_QUIESCE_MODE_STOP, 600, "c1", "c2")))
	if rt.get("c1") != "exited" {
		t.Fatal("c1 not stopped")
	}
	r := ta.run(t, resumeCmd("rp1"))
	succeeded(t, r)
	if rt.get("c1") != "running" || rt.get("c2") != "exited" {
		t.Fatalf("after resume %v", rt.state)
	}
}

func TestResumeFailureKeepsLease(t *testing.T) {
	rt := newFake(map[string]string{"c1": "running"})
	ta := newTestAgent(t, t.TempDir(), rt)
	succeeded(t, ta.run(t, quiesceCmd("rp1", "app1", agentv1.QuiesceMode_QUIESCE_MODE_PAUSE, 600, "c1")))
	rt.mu.Lock()
	rt.failResume = true
	rt.mu.Unlock()
	r := ta.run(t, resumeCmd("rp1"))
	if r.State != agentv1.CommandState_COMMAND_STATE_FAILED || !r.Retryable {
		t.Fatalf("resume failure: %+v", r)
	}
	e := ta.waitEvent(t, evResumeFailed, time.Second)
	if e.Severity != sevCritical || e.LeaseId != "rp1" {
		t.Fatalf("event %+v", e)
	}
	rt.mu.Lock()
	rt.failResume = false
	rt.mu.Unlock()
	succeeded(t, ta.run(t, resumeCmd("rp1")))
	if rt.get("c1") != "running" {
		t.Fatal("not resumed")
	}
}

func TestDeadManAutoResume(t *testing.T) {
	rt := newFake(map[string]string{"c1": "running", "c2": "paused"})
	ta := newTestAgent(t, t.TempDir(), rt)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ta.leases.start(ctx)
	succeeded(t, ta.run(t, quiesceCmd("rp1", "app1", agentv1.QuiesceMode_QUIESCE_MODE_PAUSE, 1, "c1", "c2")))
	w := ta.waitEvent(t, evLeaseWarning, 3*time.Second)
	if w.Severity != sevWarning || w.ApplicationId != "app1" {
		t.Fatalf("warning %+v", w)
	}
	e := ta.waitEvent(t, evAutoResumed, 3*time.Second)
	if e.Severity != sevCritical || e.LeaseId != "rp1" || e.OccurredUnixMs == 0 {
		t.Fatalf("event %+v", e)
	}
	if rt.get("c1") != "running" || rt.get("c2") != "paused" {
		t.Fatalf("after auto-resume %v", rt.state)
	}
	// A late Quiesce retry with the same lease must not re-quiesce.
	if u := ta.run(t, quiesceCmd("rp1", "app1", agentv1.QuiesceMode_QUIESCE_MODE_PAUSE, 1, "c1")); u.State != agentv1.CommandState_COMMAND_STATE_FAILED || u.Retryable {
		t.Fatalf("late quiesce %+v", u)
	}
	r := ta.run(t, resumeCmd("rp1"))
	succeeded(t, r)
	if got := r.GetResume(); !got.AutoResumed || !got.AlreadyResumed || len(got.ResumedContainerIds) != 1 {
		t.Fatalf("resume %+v", got)
	}
	if r := ta.run(t, resumeCmd("rp1")); r.GetResume().AutoResumed {
		t.Fatal("lease not removed")
	}
}

func TestLeaseRecoveryAfterRestart(t *testing.T) {
	dir := t.TempDir()
	rt := newFake(map[string]string{"c1": "running", "c2": "running"})
	ta := newTestAgent(t, dir, rt)
	ctx1, cancel1 := context.WithCancel(context.Background())
	ta.leases.start(ctx1)
	succeeded(t, ta.run(t, quiesceCmd("short", "app1", agentv1.QuiesceMode_QUIESCE_MODE_STOP, 1, "c1")))
	succeeded(t, ta.run(t, quiesceCmd("long", "app2", agentv1.QuiesceMode_QUIESCE_MODE_PAUSE, 3600, "c2")))
	cancel1() // "agent stops": timers disarmed
	time.Sleep(1200 * time.Millisecond)
	if rt.get("c1") != "exited" {
		t.Fatal("switch fired after shutdown")
	}

	tb := newTestAgent(t, dir, rt)
	tb.cur = nil // disconnected at start: events are buffered
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	tb.leases.start(ctx2)
	if rt.get("c1") != "running" {
		t.Fatal("expired lease not resumed on start")
	}
	if rt.get("c2") != "paused" {
		t.Fatal("unexpired lease resumed")
	}
	tb.leases.mu.Lock()
	armed := len(tb.leases.timers["long"])
	tb.leases.mu.Unlock()
	if armed == 0 {
		t.Fatal("unexpired lease not re-armed")
	}
	tb.mu.Lock()
	pending := tb.pending
	tb.mu.Unlock()
	if len(pending) != 1 || pending[0].Type != evAutoResumed || pending[0].LeaseId != "short" {
		t.Fatalf("pending events %v", pending)
	}
	tb.flushEvents(tb.out)
	tb.waitEvent(t, evAutoResumed, time.Second)
	r := tb.run(t, resumeCmd("short"))
	if !r.GetResume().AutoResumed {
		t.Fatalf("resume %+v", r.GetResume())
	}
}

func TestRunHooks(t *testing.T) {
	rt := newFake(map[string]string{"c1": "running"})
	rt.exec = func(id string, cmd []string) (runtime.ExecResult, error) {
		switch cmd[0] {
		case "ok":
			return runtime.ExecResult{Output: []byte("fine")}, nil
		case "big":
			return runtime.ExecResult{Output: bytes.Repeat([]byte("x"), 20000)}, nil
		case "slow":
			time.Sleep(1500 * time.Millisecond)
			return runtime.ExecResult{}, nil
		}
		return runtime.ExecResult{ExitCode: 3, Output: []byte("boom")}, nil
	}
	ta := newTestAgent(t, t.TempDir(), rt)
	hooks := func(h ...*agentv1.Hook) *agentv1.Command {
		return &agentv1.Command{Kind: &agentv1.Command_RunHooks{RunHooks: &agentv1.RunHooksCommand{Phase: "pre", Hooks: h}}}
	}
	u := ta.run(t, hooks(
		&agentv1.Hook{ContainerId: "c1", Command: []string{"ok"}},
		&agentv1.Hook{ContainerId: "c1", Command: []string{"fail"}, Optional: true},
		&agentv1.Hook{ContainerId: "c1", Command: []string{"big"}},
		&agentv1.Hook{ContainerId: "c1", Command: []string{"slow"}, TimeoutSeconds: 1, Optional: true},
	))
	succeeded(t, u)
	res := u.GetRunHooks().Results
	if len(res) != 4 || res[0].ExitCode != 0 || res[0].Output != "fine" || res[1].ExitCode != 3 || res[1].Error == "" {
		t.Fatalf("results %+v", res)
	}
	if len(res[2].Output) > maxHookOutput+32 || !strings.HasPrefix(res[2].Output, "[output truncated]") {
		t.Fatalf("output not truncated: %d", len(res[2].Output))
	}
	if !strings.Contains(res[3].Error, "timed out") {
		t.Fatalf("timeout: %+v", res[3])
	}

	u = ta.run(t, hooks(
		&agentv1.Hook{ContainerId: "c1", Command: []string{"ok"}},
		&agentv1.Hook{ContainerId: "c1", Command: []string{"fail"}},
		&agentv1.Hook{ContainerId: "c1", Command: []string{"ok"}},
	))
	if u.State != agentv1.CommandState_COMMAND_STATE_FAILED || u.Retryable || len(u.GetRunHooks().Results) != 2 {
		t.Fatalf("required failure: %+v", u)
	}
}

// snapEnv is an agent with a configured filesystem Kopia repository.
type snapEnv struct {
	*testAgent
	rt   *fakeRuntime
	repo engine.Repository
}

const testPassword = "s3cret-kopia-pw" // gitleaks:allow (test fixture)

func newSnapEnv(t *testing.T) *snapEnv {
	t.Helper()
	rt := newFake(map[string]string{"c1": "running"})
	ta := newTestAgent(t, t.TempDir(), rt)
	repoDir := t.TempDir()
	ta.OpenRepository = func(ctx context.Context, c engine.ServerConnection) (engine.Repository, error) {
		if c.Password != testPassword || c.User != "agent" || c.Host != "agent-1" {
			return nil, errors.New("bad connection " + c.User + "@" + c.Host)
		}
		return kopia.InitFilesystem(ctx, repoDir, c.StateDir, "repo-pw", c.User, c.Host)
	}
	u := ta.run(t, &agentv1.Command{Kind: &agentv1.Command_ConfigureRepository{ConfigureRepository: &agentv1.ConfigureRepositoryCommand{
		RepositoryId: "repo1", ServerUrl: "https://dbr2:51515", CertSha256: "ab", Username: "agent", Hostname: "agent-1", Password: testPassword}}})
	succeeded(t, u)
	conn := filepath.Join(ta.cfg.StateDir, reposDir, "repo1", "connection.json")
	if st, err := os.Stat(conn); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("connection.json %v %v", st, err)
	}
	if st, _ := os.Stat(filepath.Dir(conn)); st.Mode().Perm() != 0o700 {
		t.Fatalf("repo dir mode %v", st.Mode())
	}
	h, err := ta.openRepo(context.Background(), "repo1")
	if err != nil {
		t.Fatal(err)
	}
	h.release()
	return &snapEnv{testAgent: ta, rt: rt, repo: h.repo}
}

func snapCmd(rp string, seed bool, comps ...*agentv1.ComponentSpec) *agentv1.Command {
	return &agentv1.Command{Kind: &agentv1.Command_SnapshotComponents{SnapshotComponents: &agentv1.SnapshotComponentsCommand{
		RepositoryId: "repo1", RecoveryPointId: rp, ApplicationId: "app1", Components: comps, Seed: seed}}}
}

func TestSnapshotComponents(t *testing.T) {
	e := newSnapEnv(t)
	ctx := context.Background()

	vol := t.TempDir()
	if err := os.MkdirAll(filepath.Join(vol, "sub"), 0o750); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(vol, "sub", "data"), []byte("hello"), 0o640)
	_ = os.Link(filepath.Join(vol, "sub", "data"), filepath.Join(vol, "hard"))
	_ = os.Chmod(vol, os.ModeSetgid|0o750)

	cfgDir := t.TempDir()
	compose := filepath.Join(cfgDir, "compose.yaml")
	_ = os.WriteFile(compose, []byte("services: {}\n"), 0o640)

	u := e.run(t, snapCmd("rp_1", false,
		&agentv1.ComponentSpec{Name: "config", Kind: agentv1.ComponentKind_COMPONENT_KIND_CONFIG, Path: cfgDir, Required: true,
			Files: []string{compose, filepath.Join(cfgDir, "missing.env")}, MetadataJson: []byte(`{"redacted":true}`),
			ContainerIds: []string{"c1", "gone"}},
		&agentv1.ComponentSpec{Name: "volume:data", Kind: agentv1.ComponentKind_COMPONENT_KIND_VOLUME, Path: vol, VolumeName: "data",
			Required: true, CaptureFsmeta: true},
		&agentv1.ComponentSpec{Name: "database:db", Kind: agentv1.ComponentKind_COMPONENT_KIND_DATABASE, Required: false},
	))
	succeeded(t, u)
	res := u.GetSnapshotComponents().Components
	if len(res) != 4 {
		t.Fatalf("results %v", res)
	}
	cfg, v, fm, db := res[0], res[1], res[2], res[3]
	if cfg.Status != manifest.ComponentSucceeded || !strings.Contains(cfg.Error, "missing.env") || cfg.SnapshotId == "" {
		t.Fatalf("config %+v", cfg)
	}
	if v.Status != manifest.ComponentSucceeded || v.Mode != "2750" || v.OwnerUid != uint32(os.Getuid()) || v.CaptureMethod != "live" ||
		v.Source != "agent@agent-1:/app1/volume:data" || v.Files < 2 || v.VolumeName != "data" {
		t.Fatalf("volume %+v", v)
	}
	if fm.Name != "fsmeta:volume:data" || fm.Kind != agentv1.ComponentKind_COMPONENT_KIND_FSMETA || fm.Parent != "volume:data" ||
		!fm.Required || fm.Status != manifest.ComponentSucceeded || fm.FileName != fsmeta.FileName || v.FileName != "" {
		t.Fatalf("fsmeta %+v", fm)
	}
	if db.Status != manifest.ComponentFailed || !strings.Contains(db.Error, "no database spec") {
		t.Fatalf("database %+v", db)
	}

	// Tags, pins and sources as the manifest writer expects them.
	snaps, err := e.repo.List(ctx, nil, map[string]string{engine.TagRP: "rp_1"})
	if err != nil || len(snaps) != 3 {
		t.Fatalf("list %d %v", len(snaps), err)
	}
	for _, s := range snaps {
		name := s.Tags[engine.TagComponent]
		wantKind := map[string]string{"config": "config", "volume:data": "volume", "fsmeta:volume:data": "fsmeta"}[name]
		if s.Tags[engine.TagKind] != wantKind || s.Tags[engine.TagApp] != "app1" || len(s.Pins) != 1 || s.Pins[0] != engine.Pin ||
			s.Source != (engine.Source{User: "agent", Host: "agent-1", Path: "/app1/" + name}) {
			t.Fatalf("snapshot %+v", s)
		}
	}

	// Config staging layout.
	out := t.TempDir()
	if err := e.repo.RestorePath(ctx, cfg.SnapshotId, out, engine.RestoreOptions{}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(out, "files", compose)); string(b) != "services: {}\n" {
		t.Fatalf("staged compose %q", b)
	}
	if st, _ := os.Stat(filepath.Join(out, "files", compose)); st.Mode().Perm() != 0o640 {
		t.Fatalf("staged mode %v", st.Mode())
	}
	if b, _ := os.ReadFile(filepath.Join(out, "discovery.json")); string(b) != `{"redacted":true}` {
		t.Fatalf("discovery.json %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(out, "containers", "c1.json")); !strings.Contains(string(b), "SECRET=real") {
		t.Fatalf("inspect %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(out, "containers", "errors.json")); !strings.Contains(string(b), "gone") {
		t.Fatalf("errors.json %q", b)
	}
	if ents, _ := os.ReadDir(e.cfg.path(tmpDir)); len(ents) != 0 {
		t.Fatalf("staging not removed: %v", ents)
	}

	// fsmeta round trip through the repository.
	rc, err := e.repo.OpenStream(ctx, fm.SnapshotId, fsmeta.FileName)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	rd, err := fsmeta.NewReader(rc)
	if err != nil {
		t.Fatal(err)
	}
	defer rd.Close()
	if rd.Header().Root != vol {
		t.Fatalf("root %q", rd.Header().Root)
	}
	groups := map[string]string{}
	for {
		rec, err := rd.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		groups[rec.P] = rec.H
	}
	if groups["hard"] == "" || groups["hard"] != groups["sub/data"] {
		t.Fatalf("hardlinks %v", groups)
	}

	// Progress was reported and the password never logged.
	if strings.Contains(e.logs.String(), testPassword) {
		t.Fatal("password logged")
	}
	b, _ := os.ReadFile(e.cfg.path(jrnlFile))
	if strings.Contains(string(b), testPassword) {
		t.Fatal("password journaled")
	}
}

func TestSnapshotRequiredFailureSkipsRest(t *testing.T) {
	e := newSnapEnv(t)
	vol := t.TempDir()
	u := e.run(t, snapCmd("rp_2", false,
		&agentv1.ComponentSpec{Name: "bind:gone", Kind: agentv1.ComponentKind_COMPONENT_KIND_BIND_MOUNT, Path: "/nonexistent/dbr2", Required: true, CaptureFsmeta: true},
		&agentv1.ComponentSpec{Name: "volume:v", Kind: agentv1.ComponentKind_COMPONENT_KIND_VOLUME, Path: vol, Required: true, CaptureFsmeta: true},
	))
	succeeded(t, u)
	res := u.GetSnapshotComponents().Components
	want := []struct{ name, status string }{
		{"bind:gone", manifest.ComponentFailed}, {"fsmeta:bind:gone", manifest.ComponentSkipped},
		{"volume:v", manifest.ComponentSkipped}, {"fsmeta:volume:v", manifest.ComponentSkipped},
	}
	if len(res) != len(want) {
		t.Fatalf("results %v", res)
	}
	for i, w := range want {
		if res[i].Name != w.name || res[i].Status != w.status {
			t.Fatalf("%d: %s %s (%s), want %s %s", i, res[i].Name, res[i].Status, res[i].Error, w.name, w.status)
		}
	}
	snaps, _ := e.repo.List(context.Background(), nil, map[string]string{engine.TagRP: "rp_2"})
	if len(snaps) != 0 {
		t.Fatalf("uploaded after required failure: %v", snaps)
	}
}

func TestSnapshotSingleFileBindMount(t *testing.T) {
	e := newSnapEnv(t)
	dir := t.TempDir()
	conf := filepath.Join(dir, "nginx.conf")
	_ = os.WriteFile(conf, []byte("worker_processes 1;\n"), 0o644)
	fifo := filepath.Join(dir, "fifo")
	fifoOK := unixMkfifo(fifo) == nil
	comps := []*agentv1.ComponentSpec{{Name: "bind:nginx.conf", Kind: agentv1.ComponentKind_COMPONENT_KIND_BIND_MOUNT, Path: conf,
		Required: true, CaptureFsmeta: true}}
	if fifoOK {
		comps = append(comps, &agentv1.ComponentSpec{Name: "bind:fifo", Kind: agentv1.ComponentKind_COMPONENT_KIND_BIND_MOUNT, Path: fifo})
	}
	u := e.run(t, snapCmd("rp_6", false, comps...))
	succeeded(t, u)
	res := u.GetSnapshotComponents().Components
	if res[0].Status != manifest.ComponentSucceeded || res[0].Mode != "0644" || res[0].Files != 1 || res[0].FileName != "nginx.conf" ||
		res[1].Name != "fsmeta:bind:nginx.conf" || res[1].Status != manifest.ComponentSucceeded || res[1].FileName != fsmeta.FileName {
		t.Fatalf("results %+v", res)
	}
	rc, err := e.repo.OpenStream(context.Background(), res[0].SnapshotId, "nginx.conf")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if string(b) != "worker_processes 1;\n" {
		t.Fatalf("content %q", b)
	}
	if fifoOK && (res[2].Status != manifest.ComponentFailed || !strings.Contains(res[2].Error, "named pipe")) {
		t.Fatalf("fifo %+v", res[2])
	}
}

func TestSnapshotSeedTagging(t *testing.T) {
	e := newSnapEnv(t)
	vol := t.TempDir()
	_ = os.WriteFile(filepath.Join(vol, "f"), []byte("x"), 0o600)
	succeeded(t, e.run(t, snapCmd("rp_3", true,
		&agentv1.ComponentSpec{Name: "volume:v", Kind: agentv1.ComponentKind_COMPONENT_KIND_VOLUME, Path: vol, Required: true})))
	snaps, _ := e.repo.List(context.Background(), nil, map[string]string{engine.TagRP: "rp_3"})
	if len(snaps) != 1 || snaps[0].Tags[engine.TagKind] != "seed" || snaps[0].Tags[engine.TagComponent] != "volume:v" {
		t.Fatalf("seed %+v", snaps)
	}
}

func TestSnapshotInfrastructureErrors(t *testing.T) {
	ta := newTestAgent(t, t.TempDir(), newFake(nil))
	u := ta.run(t, &agentv1.Command{Kind: &agentv1.Command_SnapshotComponents{SnapshotComponents: &agentv1.SnapshotComponentsCommand{
		RepositoryId: "nope", RecoveryPointId: "rp", ApplicationId: "app"}}})
	if u.State != agentv1.CommandState_COMMAND_STATE_FAILED || u.Retryable || !strings.Contains(u.Error, "repository not configured") {
		t.Fatalf("not configured: %+v", u)
	}

	e := newSnapEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	u = e.execute(ctx, &agentv1.Command{CommandId: "c", Kind: snapCmd("rp_4", false,
		&agentv1.ComponentSpec{Name: "volume:v", Kind: agentv1.ComponentKind_COMPONENT_KIND_VOLUME, Path: t.TempDir(), Required: true}).Kind})
	if u.State != agentv1.CommandState_COMMAND_STATE_FAILED || !u.Retryable {
		t.Fatalf("cancelled: %+v", u)
	}
}

func TestSnapshotQueuedByLimit(t *testing.T) {
	e := newSnapEnv(t)
	e.jobs.setLimit(1)
	if !e.jobs.tryAcquire() {
		t.Fatal("slot")
	}
	vol := t.TempDir()
	done := make(chan *agentv1.CommandUpdate, 1)
	go func() {
		done <- e.execute(context.Background(), &agentv1.Command{CommandId: "q1", Kind: snapCmd("rp_5", false,
			&agentv1.ComponentSpec{Name: "volume:v", Kind: agentv1.ComponentKind_COMPONENT_KIND_VOLUME, Path: vol}).Kind})
	}()
	deadline := time.After(5 * time.Second)
	for queued := false; !queued; {
		select {
		case m := <-e.out.ch:
			if cu := m.GetCommandUpdate(); cu != nil && cu.CommandId == "q1" && cu.State == agentv1.CommandState_COMMAND_STATE_RUNNING {
				var p map[string]any
				_ = json.Unmarshal(cu.Progress, &p)
				queued = p["queued"] == true
			}
		case <-deadline:
			t.Fatal("no queued progress")
		}
	}
	select {
	case <-done:
		t.Fatal("ran despite the limit")
	case <-time.After(200 * time.Millisecond):
	}
	e.jobs.release()
	succeeded(t, <-done)
}

func unixMkfifo(path string) error { return syscall.Mkfifo(path, 0o600) }
