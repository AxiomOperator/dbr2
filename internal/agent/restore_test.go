// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	agentv1 "github.com/AxiomOperator/dbr2/internal/agentpb/agent/v1"
	"github.com/AxiomOperator/dbr2/internal/engine"
	"github.com/AxiomOperator/dbr2/internal/runtime"
)

// restoreFake adds RestoreControl to fakeRuntime.
type restoreFake struct {
	*fakeRuntime
	volRoot  string
	volumes  map[string]runtime.VolumeInfo
	networks map[string]runtime.NetworkSpec
	created  map[string]runtime.ContainerSpec
	names    map[string]string // name → id
	health   map[string]string
	restarts map[string]int
	images   map[string]runtime.ImageInfo // ref → image
	pulled   []string
	tagged   []string
	failPull bool
	// onInspect runs before every InspectDetails (health scripts).
	onInspect func(id string)
}

func newRestoreFake(t *testing.T, states map[string]string) *restoreFake {
	f := &restoreFake{fakeRuntime: newFake(states), volRoot: t.TempDir(), volumes: map[string]runtime.VolumeInfo{},
		networks: map[string]runtime.NetworkSpec{}, created: map[string]runtime.ContainerSpec{}, names: map[string]string{},
		health: map[string]string{}, restarts: map[string]int{}, images: map[string]runtime.ImageInfo{}}
	for id := range states {
		f.names["n-"+id] = id
	}
	return f
}

func (f *restoreFake) id(ref string) string {
	if id, ok := f.names[ref]; ok {
		return id
	}
	return ref
}

func (f *restoreFake) InspectDetails(_ context.Context, ref string) (runtime.ContainerDetails, error) {
	if f.onInspect != nil {
		f.onInspect(ref)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.id(ref)
	s, ok := f.state[id]
	if !ok {
		return runtime.ContainerDetails{}, fmt.Errorf("%w: no such container %s", runtime.ErrNotFound, ref)
	}
	h := f.health[id]
	if h == "" {
		h = "none"
	}
	name := "n-" + id
	for n, i := range f.names {
		if i == id {
			name = n
		}
	}
	return runtime.ContainerDetails{ID: id, Name: name, State: s, Running: s == "running" || s == "paused", Health: h,
		RestartCount: f.restarts[id], Env: []string{"POSTGRES_USER=app"}}, nil
}

func (f *restoreFake) Start(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "start "+id)
	if s := f.state[id]; s != "exited" && s != "created" {
		return fmt.Errorf("start: container %s is %s", id, s)
	}
	f.state[id] = "running"
	return nil
}

func (f *restoreFake) Logs(context.Context, string, int) (string, error) {
	return "boom: crashed\n", nil
}
func (f *restoreFake) ExecInput(context.Context, string, []string, io.Reader, int) (runtime.ExecResult, error) {
	return runtime.ExecResult{}, errors.New("not supported by the fake")
}
func (f *restoreFake) CopyToContainer(context.Context, string, string, io.Reader) error {
	return errors.New("not supported by the fake")
}

func (f *restoreFake) CreateContainer(_ context.Context, s runtime.ContainerSpec) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.names[s.Name]; ok {
		return "", errors.New("name in use")
	}
	id := "new-" + s.Name
	f.state[id], f.names[s.Name], f.created[s.Name] = "created", id, s
	return id, nil
}

func (f *restoreFake) RemoveContainer(_ context.Context, ref string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.id(ref)
	if _, ok := f.state[id]; !ok {
		return runtime.ErrNotFound
	}
	delete(f.state, id)
	for n, i := range f.names {
		if i == id {
			delete(f.names, n)
		}
	}
	f.calls = append(f.calls, "remove "+id)
	return nil
}

func (f *restoreFake) InspectVolume(_ context.Context, name string) (runtime.VolumeInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.volumes[name]
	if !ok {
		return v, runtime.ErrNotFound
	}
	return v, nil
}

func (f *restoreFake) CreateVolume(_ context.Context, name, driver string, labels map[string]string) (runtime.VolumeInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	mp := filepath.Join(f.volRoot, name, "_data")
	if err := os.MkdirAll(mp, 0o755); err != nil {
		return runtime.VolumeInfo{}, err
	}
	v := runtime.VolumeInfo{Name: name, Driver: driver, Mountpoint: mp, Labels: labels}
	f.volumes[name] = v
	return v, nil
}

func (f *restoreFake) RemoveVolume(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.volumes[name]; !ok {
		return runtime.ErrNotFound
	}
	delete(f.volumes, name)
	return os.RemoveAll(filepath.Join(f.volRoot, name))
}

func (f *restoreFake) NetworkExists(_ context.Context, name string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.networks[name]
	return ok, nil
}

func (f *restoreFake) CreateNetwork(_ context.Context, s runtime.NetworkSpec) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.networks[s.Name] = s
	return "net-" + s.Name, nil
}

func (f *restoreFake) RemoveNetwork(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.networks[name]; !ok {
		return runtime.ErrNotFound
	}
	delete(f.networks, name)
	return nil
}

func (f *restoreFake) InspectImage(_ context.Context, ref string) (runtime.ImageInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if im, ok := f.images[ref]; ok {
		return im, nil
	}
	return runtime.ImageInfo{}, runtime.ErrNotFound
}

func (f *restoreFake) PullImage(_ context.Context, ref string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failPull {
		return errors.New("registry unreachable")
	}
	f.pulled = append(f.pulled, ref)
	repo, digest, _ := strings.Cut(ref, "@")
	f.images[ref] = runtime.ImageInfo{ID: "sha256:img-" + digest, RepoDigests: []string{repo + "@" + digest}}
	return nil
}

func (f *restoreFake) TagImage(_ context.Context, source, target string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tagged = append(f.tagged, source+" "+target)
	f.images[target] = runtime.ImageInfo{ID: source}
	return nil
}

// restoreEnv is a snapEnv whose runtime is a restoreFake.
type restoreEnv struct {
	*snapEnv
	rf *restoreFake
}

func newRestoreEnv(t *testing.T) *restoreEnv {
	t.Helper()
	e := newSnapEnv(t)
	rf := newRestoreFake(t, map[string]string{"c1": "running"})
	e.Agent.rt = rf
	e.rt = rf.fakeRuntime
	return &restoreEnv{snapEnv: e, rf: rf}
}

func restoreCmd(id string, remaps []*agentv1.PathRemap, specs ...*agentv1.RestoreSpec) *agentv1.Command {
	return &agentv1.Command{Kind: &agentv1.Command_RestoreComponents{RestoreComponents: &agentv1.RestoreComponentsCommand{
		RepositoryId: "repo1", RestoreId: id, Components: specs, PathRemaps: remaps}}}
}

func finalizeCmd(id string, action agentv1.FinalizeAction, restart ...string) *agentv1.Command {
	return &agentv1.Command{Kind: &agentv1.Command_FinalizeRestore{FinalizeRestore: &agentv1.FinalizeRestoreCommand{
		RestoreId: id, Action: action, RestartContainerIds: restart}}}
}

func failed(t *testing.T, u *agentv1.CommandUpdate, retryable bool, contains string) {
	t.Helper()
	if u.State != agentv1.CommandState_COMMAND_STATE_FAILED || u.Retryable != retryable || !strings.Contains(u.Error, contains) {
		t.Fatalf("want failure (retryable=%v, %q), got %s retryable=%v: %s", retryable, contains, u.State, u.Retryable, u.Error)
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func write(t *testing.T, p, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func ino(t *testing.T, p string) uint64 {
	t.Helper()
	var st unix.Stat_t
	if err := unix.Lstat(p, &st); err != nil {
		t.Fatal(err)
	}
	return st.Ino
}

func leftovers(t *testing.T, dir string) []string {
	t.Helper()
	ents, _ := os.ReadDir(dir)
	var out []string
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".dbr2-") {
			out = append(out, e.Name())
		}
	}
	return out
}

// captured snapshots a volume tree, a bind directory, a single-file bind
// mount and a config component, returning the results by name.
type captured struct {
	vol, bind, file, cfgDir string
	res                     map[string]*agentv1.ComponentResult
	xattrs                  bool
	mt                      time.Time
}

func capture(t *testing.T, e *restoreEnv, rp string) *captured {
	t.Helper()
	c := &captured{vol: t.TempDir(), bind: t.TempDir(), cfgDir: t.TempDir(), mt: time.Unix(1_650_000_000, 42), res: map[string]*agentv1.ComponentResult{}}
	write(t, filepath.Join(c.vol, "db", "data"), "rows", 0o640)
	if err := os.Link(filepath.Join(c.vol, "db", "data"), filepath.Join(c.vol, "hard")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(c.vol, "secret"), "s", 0o600)
	c.xattrs = unix.Lsetxattr(filepath.Join(c.vol, "secret"), "user.dbr2", []byte("x"), 0) == nil
	_ = os.Chmod(c.vol, os.ModeSetgid|0o750)
	for _, d := range []string{filepath.Join(c.vol, "db"), c.vol} {
		if err := os.Chtimes(d, c.mt, c.mt); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(c.bind, "index.html"), "<h1>v1</h1>", 0o644)
	c.file = filepath.Join(t.TempDir(), "nginx.conf")
	write(t, c.file, "worker_processes 1;\n", 0o640)
	compose := filepath.Join(c.cfgDir, "compose.yaml")
	write(t, compose, "services: {web: {}}\n", 0o640)
	write(t, filepath.Join(c.cfgDir, ".env"), "TAG=1\n", 0o600)

	u := e.run(t, snapCmd(rp, false,
		&agentv1.ComponentSpec{Name: "config", Kind: agentv1.ComponentKind_COMPONENT_KIND_CONFIG, Path: c.cfgDir, Required: true,
			Files: []string{compose, filepath.Join(c.cfgDir, ".env")}},
		&agentv1.ComponentSpec{Name: "volume:data", Kind: agentv1.ComponentKind_COMPONENT_KIND_VOLUME, Path: c.vol, VolumeName: "data",
			Required: true, CaptureFsmeta: true},
		&agentv1.ComponentSpec{Name: "bind:web", Kind: agentv1.ComponentKind_COMPONENT_KIND_BIND_MOUNT, Path: c.bind, Required: true, CaptureFsmeta: true},
		&agentv1.ComponentSpec{Name: "bind:nginx.conf", Kind: agentv1.ComponentKind_COMPONENT_KIND_BIND_MOUNT, Path: c.file, Required: true, CaptureFsmeta: true},
	))
	succeeded(t, u)
	for _, r := range u.GetSnapshotComponents().Components {
		if r.Status != "succeeded" {
			t.Fatalf("capture %s: %s", r.Name, r.Error)
		}
		c.res[r.Name] = r
	}
	return c
}

func (c *captured) spec(name string) *agentv1.RestoreSpec {
	r := c.res[name]
	s := &agentv1.RestoreSpec{Name: name, Kind: r.Kind, SnapshotId: r.SnapshotId, FileName: r.FileName,
		OwnerUid: r.OwnerUid, OwnerGid: r.OwnerGid, Mode: r.Mode, SelinuxContext: r.SelinuxContext}
	if fm := c.res["fsmeta:"+name]; fm != nil {
		s.FsmetaSnapshotId = fm.SnapshotId
	}
	return s
}

func TestRestoreComponentsCommitAndRollback(t *testing.T) {
	e := newRestoreEnv(t)
	c := capture(t, e, "rp_r1")

	// Current (to be replaced) content on the restore host.
	vol, _ := e.rf.CreateVolume(context.Background(), "data", "local", nil)
	write(t, filepath.Join(vol.Mountpoint, "current"), "old", 0o644)
	newRoot := t.TempDir()
	bindTarget := filepath.Join(newRoot, "web")
	write(t, filepath.Join(bindTarget, "index.html"), "<h1>v2</h1>", 0o644)
	fileTarget := filepath.Join(newRoot, "nginx.conf")
	write(t, fileTarget, "broken", 0o600)

	vs := c.spec("volume:data")
	vs.VolumeName = "data"
	bs := c.spec("bind:web")
	bs.TargetPath = bindTarget
	fs := c.spec("bind:nginx.conf")
	fs.TargetPath = fileTarget
	cs := c.spec("config")
	remaps := []*agentv1.PathRemap{{From: c.cfgDir, To: filepath.Join(newRoot, "app")}}
	u := e.run(t, restoreCmd("rs1", remaps, cs, vs, bs, fs))
	succeeded(t, u)
	res := u.GetRestoreComponents().Components
	if len(res) != 4 {
		t.Fatalf("results %+v", res)
	}
	for _, r := range res {
		if r.Status != restored || r.VerifyMismatches != 0 {
			t.Fatalf("%s: %+v", r.Name, r)
		}
	}
	if res[1].TargetPath != vol.Mountpoint || res[1].PreviousPath != filepath.Join(filepath.Dir(vol.Mountpoint), ".dbr2-old-rs1") ||
		res[1].MetadataApplied < 3 || res[1].CreatedVolume || res[1].Files != 3 {
		t.Fatalf("volume %+v", res[1])
	}
	if res[0].Files != 2 {
		t.Fatalf("config %+v", res[0])
	}

	// Volume: content, hardlink, xattr, root mode, directory mtimes.
	mp := vol.Mountpoint
	if readFile(t, filepath.Join(mp, "db", "data")) != "rows" || exists(filepath.Join(mp, "current")) {
		t.Fatal("volume content")
	}
	if ino(t, filepath.Join(mp, "db", "data")) != ino(t, filepath.Join(mp, "hard")) {
		t.Fatal("hardlink not recreated")
	}
	if st, _ := os.Stat(mp); st.Mode()&os.ModeSetgid == 0 || st.Mode().Perm() != 0o750 {
		t.Fatalf("root mode %v", st.Mode())
	}
	for _, d := range []string{mp, filepath.Join(mp, "db")} {
		if st, _ := os.Stat(d); !st.ModTime().Equal(c.mt) {
			t.Fatalf("%s mtime %v", d, st.ModTime())
		}
	}
	if c.xattrs {
		var buf [8]byte
		n, err := unix.Lgetxattr(filepath.Join(mp, "secret"), "user.dbr2", buf[:])
		if err != nil || string(buf[:n]) != "x" {
			t.Fatalf("xattr %q %v", buf[:n], err)
		}
	} else {
		t.Log("user xattrs unsupported here; xattr assertions skipped")
	}
	if readFile(t, filepath.Join(filepath.Dir(mp), ".dbr2-old-rs1", "current")) != "old" {
		t.Fatal("previous content not kept")
	}
	// Bind directory, single file (mode kept), config files (remapped).
	if readFile(t, filepath.Join(bindTarget, "index.html")) != "<h1>v1</h1>" {
		t.Fatal("bind content")
	}
	if readFile(t, fileTarget) != "worker_processes 1;\n" {
		t.Fatal("single file content")
	}
	if st, _ := os.Stat(fileTarget); st.Mode().Perm() != 0o640 {
		t.Fatalf("single file mode %v", st.Mode())
	}
	if readFile(t, filepath.Join(newRoot, "app", "compose.yaml")) != "services: {web: {}}\n" {
		t.Fatal("config file not restored to the remapped path")
	}
	if st, _ := os.Stat(filepath.Join(newRoot, "app", ".env")); st.Mode().Perm() != 0o600 {
		t.Fatalf(".env mode %v", st.Mode())
	}
	if l := leftovers(t, newRoot); len(l) != 2 { // .dbr2-old for web and nginx.conf
		t.Fatalf("new root %v", l)
	}
	jpath := filepath.Join(e.cfg.StateDir, restoreDir, "rs1.json")
	if st, err := os.Stat(jpath); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("journal %v %v", st, err)
	}

	// Rollback (after an agent restart) puts everything back.
	tb := newTestAgent(t, e.cfg.StateDir, e.rf)
	tb.OpenRepository = e.OpenRepository
	tb.cleanupRestores()
	u = tb.run(t, finalizeCmd("rs1", agentv1.FinalizeAction_FINALIZE_ACTION_ROLLBACK))
	succeeded(t, u)
	if fr := u.GetFinalizeRestore(); fr.PathsRolledBack != 5 || fr.AlreadyFinalized {
		t.Fatalf("rollback %+v", fr)
	}
	if readFile(t, filepath.Join(mp, "current")) != "old" || exists(filepath.Join(mp, "db")) {
		t.Fatal("volume not rolled back")
	}
	if readFile(t, filepath.Join(bindTarget, "index.html")) != "<h1>v2</h1>" || readFile(t, fileTarget) != "broken" {
		t.Fatal("bind not rolled back")
	}
	if exists(filepath.Join(newRoot, "app", "compose.yaml")) {
		t.Fatal("config file created by the restore not removed")
	}
	if l := append(leftovers(t, newRoot), leftovers(t, filepath.Dir(mp))...); len(l) != 0 {
		t.Fatalf("leftovers %v", l)
	}
	u = tb.run(t, finalizeCmd("rs1", agentv1.FinalizeAction_FINALIZE_ACTION_ROLLBACK))
	succeeded(t, u)
	if !u.GetFinalizeRestore().AlreadyFinalized {
		t.Fatal("rollback not idempotent")
	}
	failed(t, tb.run(t, finalizeCmd("rs1", agentv1.FinalizeAction_FINALIZE_ACTION_COMMIT)), false, "already rolled_back")
	failed(t, tb.run(t, restoreCmd("rs1", nil, vs)), false, "already rolled back")

	// A new restore, committed: the previous content is deleted.
	u = tb.run(t, restoreCmd("rs2", nil, vs, bs))
	succeeded(t, u)
	u = tb.run(t, finalizeCmd("rs2", agentv1.FinalizeAction_FINALIZE_ACTION_COMMIT))
	succeeded(t, u)
	if fr := u.GetFinalizeRestore(); fr.PathsCommitted != 2 {
		t.Fatalf("commit %+v", fr)
	}
	if l := append(leftovers(t, newRoot), leftovers(t, filepath.Dir(mp))...); len(l) != 0 {
		t.Fatalf("leftovers after commit %v", l)
	}
	if readFile(t, filepath.Join(mp, "db", "data")) != "rows" {
		t.Fatal("committed content")
	}
	u = tb.run(t, finalizeCmd("rs2", agentv1.FinalizeAction_FINALIZE_ACTION_COMMIT))
	succeeded(t, u)
	if !u.GetFinalizeRestore().AlreadyFinalized {
		t.Fatal("commit not idempotent")
	}
	failed(t, tb.run(t, finalizeCmd("rs2", agentv1.FinalizeAction_FINALIZE_ACTION_ROLLBACK)), false, "already committed")
}

func TestRestoreFailureRollsBackSwaps(t *testing.T) {
	e := newRestoreEnv(t)
	c := capture(t, e, "rp_r2")
	root := t.TempDir()
	bindTarget := filepath.Join(root, "web")
	write(t, filepath.Join(bindTarget, "index.html"), "current", 0o644)
	bs := c.spec("bind:web")
	bs.TargetPath = bindTarget
	fs := c.spec("bind:nginx.conf")
	fs.TargetPath = filepath.Join(root, "nginx.conf")
	fs.FsmetaSnapshotId = "k0123456789abcdef0123456789abcdef" // no such snapshot
	u := e.run(t, restoreCmd("rs3", nil, bs, fs, c.spec("volume:data")))
	failed(t, u, false, "component bind:nginx.conf")
	res := u.GetRestoreComponents().Components
	if len(res) != 3 || res[0].Status != statusSkipped || !strings.Contains(res[0].Error, "rolled back") ||
		res[1].Status != statusFailed || res[2].Status != statusSkipped {
		t.Fatalf("results %+v", res)
	}
	if readFile(t, filepath.Join(bindTarget, "index.html")) != "current" || exists(filepath.Join(root, "nginx.conf")) {
		t.Fatal("swaps not rolled back")
	}
	if l := leftovers(t, root); len(l) != 0 {
		t.Fatalf("leftovers %v", l)
	}
	// Retrying the restore works (the journal allows it).
	fs.FsmetaSnapshotId = c.res["fsmeta:bind:nginx.conf"].SnapshotId
	succeeded(t, e.run(t, restoreCmd("rs3", nil, bs, fs)))
	if readFile(t, filepath.Join(root, "nginx.conf")) != "worker_processes 1;\n" {
		t.Fatal("retry")
	}
	// A second restore of the same target within the same restore is refused.
	failed(t, e.run(t, restoreCmd("rs3", nil, bs)), false, "already restored")
}

func TestRestoreRefusedAfterLeaseAutoResume(t *testing.T) {
	e := newRestoreEnv(t)
	c := capture(t, e, "rp_r3")
	root := t.TempDir()
	bs := c.spec("bind:web")
	bs.TargetPath = filepath.Join(root, "web")
	write(t, filepath.Join(bs.TargetPath, "index.html"), "current", 0o644)
	now := time.Now()
	e.leases.mu.Lock()
	e.leases.leases["rs4"] = &leaseRecord{LeaseID: "rs4", ApplicationID: "app1", Mode: "stop", AutoResumedAt: &now}
	e.leases.mu.Unlock()
	u := e.run(t, restoreCmd("rs4", nil, bs))
	failed(t, u, false, "stop lease expired")
	if readFile(t, filepath.Join(bs.TargetPath, "index.html")) != "current" || len(leftovers(t, root)) != 0 {
		t.Fatal("swapped despite the expired lease")
	}
}

func TestRestoreCrashRecovery(t *testing.T) {
	e := newRestoreEnv(t)
	root := t.TempDir()
	target := filepath.Join(root, "data")
	// Crash 1: while writing staging. Crash 2: between the two renames.
	write(t, filepath.Join(root, ".dbr2-staging-rs5-x", "f"), "partial", 0o644)
	write(t, filepath.Join(root, ".dbr2-old-rs5-data", "f"), "original", 0o644)
	write(t, filepath.Join(root, ".dbr2-staging-rs5-data", "f"), "restored", 0o644)
	j := &restoreJournal{RestoreID: "rs5", State: restoreActive, Swaps: []*swapRecord{
		{Component: "bind:x", Kind: "dir", Target: filepath.Join(root, "x"), Staging: filepath.Join(root, ".dbr2-staging-rs5-x"),
			Previous: filepath.Join(root, ".dbr2-old-rs5-x"), Discard: filepath.Join(root, ".dbr2-discard-rs5-x"), State: swapStaging},
		{Component: "bind:data", Kind: "dir", Target: target, Staging: filepath.Join(root, ".dbr2-staging-rs5-data"),
			Previous: filepath.Join(root, ".dbr2-old-rs5-data"), Discard: filepath.Join(root, ".dbr2-discard-rs5-data"),
			HadPrevious: true, State: swapSwapping},
	}}
	if err := e.restores.save(j); err != nil {
		t.Fatal(err)
	}
	tb := newTestAgent(t, e.cfg.StateDir, e.rf)
	tb.cleanupRestores()
	if exists(filepath.Join(root, ".dbr2-staging-rs5-x")) {
		t.Fatal("staging of an interrupted restore not cleaned at start")
	}
	if !exists(filepath.Join(root, ".dbr2-staging-rs5-data")) {
		t.Fatal("swapping entry must be left to FinalizeRestore")
	}
	failed(t, tb.run(t, finalizeCmd("rs5", agentv1.FinalizeAction_FINALIZE_ACTION_COMMIT)), false, "interrupted swap")
	u := tb.run(t, finalizeCmd("rs5", agentv1.FinalizeAction_FINALIZE_ACTION_ROLLBACK))
	succeeded(t, u)
	if readFile(t, filepath.Join(target, "f")) != "original" || len(leftovers(t, root)) != 0 {
		t.Fatalf("crash rollback: leftovers %v", leftovers(t, root))
	}
	// No journal at all: rollback still restarts the application.
	e.rf.state["c1"] = "exited"
	u = tb.run(t, finalizeCmd("never", agentv1.FinalizeAction_FINALIZE_ACTION_ROLLBACK, "c1"))
	succeeded(t, u)
	if e.rf.get("c1") != "running" {
		t.Fatal("c1 not restarted")
	}
}

// inspectDoc is a trimmed real `docker inspect` document.
const inspectDoc = `{
  "Id": "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
  "Name": "/shop-web-1",
  "State": {"Status": "running", "Running": true, "Pid": 42},
  "Config": {"Hostname": "abcdef123456", "Image": "nginx:1.27", "Env": ["SECRET=real"],
    "Labels": {"com.docker.compose.project": "shop"}},
  "HostConfig": {"Binds": ["/srv/shop/html:/usr/share/nginx/html:ro", "shop_cache:/cache"],
    "Mounts": [{"Type": "bind", "Source": "/srv/shop/conf/nginx.conf", "Target": "/etc/nginx/nginx.conf"},
               {"Type": "volume", "Source": "shop_data", "Target": "/data"}],
    "NetworkMode": "shop_net", "ContainerIDFile": "/tmp/cid", "Links": ["/shop-db-1:/shop-web-1/db"],
    "RestartPolicy": {"Name": "unless-stopped"}},
  "NetworkSettings": {"Networks": {"shop_net": {"IPAddress": "172.20.0.5", "MacAddress": "02:42:ac:14:00:05",
    "Aliases": ["web", "abcdef123456"], "NetworkID": "n1", "EndpointID": "e1", "Gateway": "172.20.0.1"},
    "static_net": {"IPAMConfig": {"IPv4Address": "10.9.0.7"}, "IPAddress": "10.9.0.7"}}}
}`

func TestRecreateContainers(t *testing.T) {
	e := newRestoreEnv(t)
	stage := t.TempDir()
	write(t, filepath.Join(stage, "containers", "abcdef.json"), inspectDoc, 0o600)
	write(t, filepath.Join(stage, "containers", "c1.json"), `{"Id":"c1","Name":"/n-c1","Config":{"Image":"redis:8"},"State":{"Running":false}}`, 0o600)
	write(t, filepath.Join(stage, "containers", "errors.json"), `{"gone":"no such container"}`, 0o600)
	snap, err := e.repo.SnapshotPath(context.Background(), stage, engine.SnapshotRequest{
		Source: engine.Source{User: "agent", Host: "agent-1", Path: "/app1/config"}})
	if err != nil {
		t.Fatal(err)
	}
	e.rf.networks["ext_net"] = runtime.NetworkSpec{Name: "ext_net"}
	cmd := func(nets ...*agentv1.NetworkSpec) *agentv1.Command {
		return &agentv1.Command{Kind: &agentv1.Command_RecreateContainers{RecreateContainers: &agentv1.RecreateContainersCommand{
			RepositoryId: "repo1", RestoreId: "rs6", ConfigSnapshotId: snap.ID, Networks: nets,
			PathRemaps: []*agentv1.PathRemap{{From: "/srv/shop", To: "/mnt/restore/shop"}}}}}
	}
	// A missing external network fails before anything is created.
	failed(t, e.run(t, cmd(&agentv1.NetworkSpec{Name: "gone_net", External: true})), false, "external network gone_net")
	if len(e.rf.created) != 0 {
		t.Fatal("created despite the missing network")
	}
	u := e.run(t, cmd(&agentv1.NetworkSpec{Name: "shop_net", Driver: "bridge", Labels: map[string]string{"a": "b"}, Internal: true},
		&agentv1.NetworkSpec{Name: "ext_net", External: true}, &agentv1.NetworkSpec{Name: "bridge"},
		&agentv1.NetworkSpec{Name: "static_net", Driver: "bridge"}))
	succeeded(t, u)
	res := u.GetRecreateContainers()
	if len(res.Containers) != 2 || len(res.CreatedNetworks) != 2 {
		t.Fatalf("result %+v", res)
	}
	existing, web := res.Containers[0], res.Containers[1]
	if existing.Name != "n-c1" || existing.Status != "existing" || existing.ContainerId != "c1" || existing.WasRunning {
		t.Fatalf("existing %+v", existing)
	}
	if web.Name != "shop-web-1" || web.Status != "created" || web.ContainerId != "new-shop-web-1" || !web.WasRunning {
		t.Fatalf("web %+v", web)
	}
	if n := e.rf.networks["shop_net"]; !n.Internal || n.Labels["a"] != "b" || n.Driver != "bridge" {
		t.Fatalf("network %+v", n)
	}
	var body struct {
		Config struct {
			Hostname string
			Image    string
			Env      []string
		}
		HostConfig struct {
			Binds           []string
			Mounts          []struct{ Type, Source, Target string }
			ContainerIDFile string
			Links           []string
			NetworkMode     string
		}
		NetworkingConfig struct {
			EndpointsConfig map[string]struct {
				IPAddress  string
				MacAddress string
				Aliases    []string
				IPAMConfig *struct{ IPv4Address string }
			}
		}
	}
	if err := json.Unmarshal(e.rf.created["shop-web-1"].Create, &body); err != nil {
		t.Fatal(err)
	}
	hc := body.HostConfig
	if hc.Binds[0] != "/mnt/restore/shop/html:/usr/share/nginx/html:ro" || hc.Binds[1] != "shop_cache:/cache" {
		t.Fatalf("binds %v", hc.Binds)
	}
	if hc.Mounts[0].Source != "/mnt/restore/shop/conf/nginx.conf" || hc.Mounts[1].Source != "shop_data" {
		t.Fatalf("mounts %+v", hc.Mounts)
	}
	if hc.ContainerIDFile != "" || hc.Links[0] != "shop-db-1:db" || hc.NetworkMode != "shop_net" {
		t.Fatalf("host config %+v", hc)
	}
	if body.Config.Hostname != "" || body.Config.Image != "nginx:1.27" || body.Config.Env[0] != "SECRET=real" {
		t.Fatalf("config %+v", body.Config)
	}
	ep := body.NetworkingConfig.EndpointsConfig["shop_net"]
	if ep.IPAddress != "" || ep.MacAddress != "" || len(ep.Aliases) != 1 || ep.Aliases[0] != "web" || ep.IPAMConfig != nil {
		t.Fatalf("endpoint %+v", ep)
	}
	if st := body.NetworkingConfig.EndpointsConfig["static_net"]; st.IPAMConfig == nil || st.IPAMConfig.IPv4Address != "10.9.0.7" {
		t.Fatalf("static endpoint %+v", st)
	}
	// Secrets never reach the logs.
	if strings.Contains(e.logs.String(), "SECRET=real") {
		t.Fatal("environment logged")
	}
	// Idempotent: a repeat sees the created container as existing.
	u = e.run(t, cmd(&agentv1.NetworkSpec{Name: "shop_net"}))
	succeeded(t, u)
	if c := u.GetRecreateContainers().Containers[1]; c.Status != "existing" || len(u.GetRecreateContainers().CreatedNetworks) != 0 {
		t.Fatalf("repeat %+v", u.GetRecreateContainers())
	}

	// Start, then roll back: the created container and networks go away.
	u = e.run(t, &agentv1.Command{Kind: &agentv1.Command_StartContainers{StartContainers: &agentv1.StartContainersCommand{
		RestoreId: "rs6", ContainerIds: []string{"new-shop-web-1", "c1"}}}})
	succeeded(t, u)
	if s := u.GetStartContainers().Started; len(s) != 1 || s[0] != "new-shop-web-1" {
		t.Fatalf("started %v", s)
	}
	u = e.run(t, finalizeCmd("rs6", agentv1.FinalizeAction_FINALIZE_ACTION_ROLLBACK, "c1"))
	succeeded(t, u)
	if rc := u.GetFinalizeRestore().RemovedContainers; len(rc) != 1 || rc[0] != "shop-web-1" {
		t.Fatalf("removed %v", rc)
	}
	if _, ok := e.rf.networks["shop_net"]; ok || e.rf.get("c1") != "running" {
		t.Fatalf("after rollback: networks %v c1 %s", e.rf.networks, e.rf.get("c1"))
	}
	if _, ok := e.rf.networks["ext_net"]; !ok {
		t.Fatal("external network removed")
	}
}

func TestRollbackRemovesCreatedVolume(t *testing.T) {
	e := newRestoreEnv(t)
	c := capture(t, e, "rp_r7")
	vs := c.spec("volume:data")
	vs.VolumeName, vs.VolumeDriver, vs.VolumeLabels = "fresh", "local", map[string]string{"k": "v"}
	u := e.run(t, restoreCmd("rs7", nil, vs))
	succeeded(t, u)
	if r := u.GetRestoreComponents().Components[0]; !r.CreatedVolume || r.Status != restored {
		t.Fatalf("result %+v", r)
	}
	if v := e.rf.volumes["fresh"]; v.Labels["k"] != "v" || readFile(t, filepath.Join(v.Mountpoint, "db", "data")) != "rows" {
		t.Fatalf("volume %+v", v)
	}
	succeeded(t, e.run(t, finalizeCmd("rs7", agentv1.FinalizeAction_FINALIZE_ACTION_ROLLBACK)))
	if _, ok := e.rf.volumes["fresh"]; ok {
		t.Fatal("created volume not removed")
	}
}

func TestRestoreFreeSpacePreflight(t *testing.T) {
	var st unix.Statfs_t
	dir := t.TempDir()
	if err := unix.Statfs(dir, &st); err != nil {
		t.Fatal(err)
	}
	if err := freeSpace(dir, 1<<62); err == nil || !isPermanent(err) || !strings.Contains(err.Error(), "insufficient free space") {
		t.Fatalf("err = %v", err)
	}
	if err := freeSpace(dir, 1); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(engineErr(fmt.Errorf("x: %w", syscall.ENOSPC)), syscall.ENOSPC) || !isPermanent(engineErr(syscall.ENOSPC)) ||
		isPermanent(engineErr(errors.New("connection reset"))) {
		t.Fatal("engineErr classification")
	}
}

func TestRemapper(t *testing.T) {
	m := remapper([]*agentv1.PathRemap{{From: "/srv", To: "/mnt/a"}, {From: "/srv/app/", To: "/opt/app"}, {From: "", To: "/x"}})
	for in, want := range map[string]string{"/srv/app/x": "/opt/app/x", "/srv/app": "/opt/app", "/srv/other": "/mnt/a/other",
		"/srvx/y": "/srvx/y", "/etc/z": "/etc/z"} {
		if got := m(in); got != want {
			t.Fatalf("%s → %s, want %s", in, got, want)
		}
	}
}

func TestEnsureImages(t *testing.T) {
	e := newRestoreEnv(t)
	const d1, d2 = "sha256:1111", "sha256:2222"
	e.rf.images["postgres@"+d1] = runtime.ImageInfo{ID: "sha256:pg", RepoDigests: []string{"postgres@" + d1}}
	e.rf.images["postgres:18"] = runtime.ImageInfo{ID: "sha256:pg"}
	e.rf.images["busybox"] = runtime.ImageInfo{ID: "sha256:bb"}
	cmd := func(imgs ...*agentv1.ImageSpec) *agentv1.Command {
		return &agentv1.Command{Kind: &agentv1.Command_EnsureImages{EnsureImages: &agentv1.EnsureImagesCommand{Images: imgs}}}
	}
	u := e.run(t, cmd(&agentv1.ImageSpec{Ref: "postgres:18", Digest: d1}, &agentv1.ImageSpec{Ref: "ghcr.io/acme/web:2", Digest: d2},
		&agentv1.ImageSpec{Ref: "busybox"}, &agentv1.ImageSpec{Ref: "alpine:3"}))
	succeeded(t, u)
	res := u.GetEnsureImages().Images
	want := []string{"present", "pulled", "present", "pulled"}
	for i, r := range res {
		if r.Status != want[i] {
			t.Fatalf("%d: %+v", i, r)
		}
	}
	if len(e.rf.pulled) != 2 || e.rf.pulled[0] != "ghcr.io/acme/web@"+d2 || e.rf.pulled[1] != "alpine:3" {
		t.Fatalf("pulled %v", e.rf.pulled)
	}
	if len(e.rf.tagged) != 1 || e.rf.tagged[0] != "sha256:img-"+d2+" ghcr.io/acme/web:2" {
		t.Fatalf("tagged %v", e.rf.tagged)
	}
	e.rf.failPull = true
	u = e.run(t, cmd(&agentv1.ImageSpec{Ref: "nginx:1", Digest: "sha256:3333"}))
	failed(t, u, true, "nginx:1")
	if r := u.GetEnsureImages().Images[0]; r.Status != "failed" || !strings.Contains(r.Error, "registry unreachable") {
		t.Fatalf("failed image %+v", r)
	}
	if repoOf("registry:5000/a/b:1@sha256:x") != "registry:5000/a/b" || repoOf("registry:5000/a") != "registry:5000/a" ||
		normalizeRepo("postgres") != "docker.io/library/postgres" || normalizeRepo("acme/web") != "docker.io/acme/web" ||
		normalizeRepo("docker.io/postgres") != "docker.io/library/postgres" || normalizeRepo("localhost/x") != "localhost/x" {
		t.Fatal("reference helpers")
	}
}

func TestCheckHealth(t *testing.T) {
	e := newRestoreEnv(t)
	e.healthPoll = 10 * time.Millisecond
	rf := e.rf
	rf.state["h1"], rf.state["h2"] = "running", "running"
	check := func(timeout, stable uint32, ids ...string) *agentv1.CommandUpdate {
		return e.run(t, &agentv1.Command{Kind: &agentv1.Command_CheckHealth{CheckHealth: &agentv1.CheckHealthCommand{
			ContainerIds: ids, TimeoutSeconds: timeout, StableSeconds: stable}}})
	}
	// Healthy after a few polls, then stable.
	var n int
	rf.health["h2"] = "starting"
	rf.onInspect = func(id string) {
		if id == "h2" {
			rf.mu.Lock()
			if n++; n > 3 {
				rf.health["h2"] = "healthy"
			}
			rf.mu.Unlock()
		}
	}
	start := time.Now()
	u := check(10, 1, "h1", "h2")
	succeeded(t, u)
	if r := u.GetCheckHealth(); !r.Ok || r.Containers[1].Health != "healthy" || time.Since(start) < time.Second {
		t.Fatalf("healthy %+v after %s", r, time.Since(start))
	}
	rf.onInspect = nil

	// Unhealthy until the timeout: failed, with logs.
	rf.health["h2"] = "unhealthy"
	u = check(1, 0, "h1", "h2")
	failed(t, u, false, "not healthy within")
	if r := u.GetCheckHealth(); r.Ok || r.Containers[0].LogTail != "" || !strings.Contains(r.Containers[1].LogTail, "crashed") {
		t.Fatalf("unhealthy %+v", r)
	}

	// Sustained "unhealthy" (Docker already applied the healthcheck's
	// retries) fails early instead of waiting for the timeout.
	e.unhealthyGrace = 50 * time.Millisecond
	start = time.Now()
	failed(t, check(30, 0, "h1", "h2"), false, "unhealthy for")
	if time.Since(start) > 5*time.Second {
		t.Fatal("unhealthy did not fail early")
	}
	e.unhealthyGrace = 0

	// An exited container fails early.
	rf.state["h2"] = "exited"
	start = time.Now()
	failed(t, check(30, 0, "h1", "h2"), false, "n-h2 exited")
	if time.Since(start) > 5*time.Second {
		t.Fatal("did not fail early")
	}

	// A restart loop fails early.
	rf.state["h2"], rf.health["h2"] = "running", ""
	rf.onInspect = func(id string) {
		if id == "h2" {
			rf.mu.Lock()
			rf.restarts["h2"]++
			rf.mu.Unlock()
		}
	}
	failed(t, check(30, 20, "h1", "h2"), false, "restarted")
	rf.onInspect = nil

	// A vanished container fails early.
	failed(t, check(30, 0, "gone"), false, "no longer exists")
}
