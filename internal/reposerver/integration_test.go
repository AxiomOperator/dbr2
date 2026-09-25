// SPDX-License-Identifier: Apache-2.0

//go:build integration

// Integration test: builds the real dbr2-reposerver, runs it against a temp
// directory and drives it through the management API, with Kopia library
// clients connecting to the embedded Kopia repository server.
//
//	go test -tags integration -run TestReposerverIntegration -v ./internal/reposerver/
package reposerver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/kopia/kopia/fs"
	"github.com/kopia/kopia/fs/localfs"
	"github.com/kopia/kopia/repo"
	"github.com/kopia/kopia/repo/content"
	"github.com/kopia/kopia/repo/manifest"
	"github.com/kopia/kopia/snapshot"
	"github.com/kopia/kopia/snapshot/policy"
	"github.com/kopia/kopia/snapshot/snapshotfs"
	"github.com/kopia/kopia/snapshot/upload"

	"github.com/AxiomOperator/dbr2/internal/reposerver"
)

const (
	itToken    = "integration-token-0123456789abcdef0123456789"
	itRepoPW   = "integration-repository-password-0123456789abcdef"
	itFileBody = "hello from agent a1\n"
)

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

type rsProc struct {
	t            *testing.T
	bin, dir     string
	env          []string
	mgmt, health string
	kopiaPort    int
	cmd          *exec.Cmd
	log          *syncBuffer
}

func (p *rsProc) start() {
	p.t.Helper()
	p.log = &syncBuffer{}
	p.cmd = exec.Command(p.bin)
	p.cmd.Env = append(os.Environ(), p.env...)
	p.cmd.Stdout, p.cmd.Stderr = p.log, p.log
	if err := p.cmd.Start(); err != nil {
		p.t.Fatal(err)
	}
}

func (p *rsProc) stop() {
	if p.cmd == nil || p.cmd.Process == nil {
		return
	}
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() { _ = p.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(40 * time.Second):
		_ = p.cmd.Process.Kill()
		<-done
		p.t.Errorf("reposerver did not stop within 40s")
	}
	p.cmd = nil
}

func (p *rsProc) call(method, path, body, token string) (int, map[string]any) {
	p.t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, _ := http.NewRequest(method, "http://"+p.mgmt+path, r)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := (&http.Client{Timeout: 5 * time.Minute}).Do(req)
	if err != nil {
		p.t.Fatalf("%s %s: %v\n%s", method, path, err, p.log)
	}
	defer res.Body.Close()
	var m map[string]any
	b, _ := io.ReadAll(res.Body)
	if len(b) > 0 {
		if err := json.Unmarshal(b, &m); err != nil {
			p.t.Fatalf("%s %s: bad JSON %q", method, path, b)
		}
	}
	return res.StatusCode, m
}

func (p *rsProc) waitRunning() map[string]any {
	p.t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if res, err := http.Get("http://" + p.mgmt + "/v1/status"); err == nil {
			res.Body.Close()
			code, st := p.call("GET", "/v1/status", "", itToken)
			if code == 200 && st["server_running"] == true {
				return st
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	p.t.Fatalf("kopia server did not come up\n%s", p.log)
	return nil
}

// kopiaChildren lists live `kopia` children using this test's state dir.
func kopiaChildren(stateDir string) []string {
	var out []string
	ents, _ := os.ReadDir("/proc")
	for _, e := range ents {
		b, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if err != nil {
			continue
		}
		cl := strings.ReplaceAll(string(b), "\x00", " ")
		if strings.Contains(cl, " kopia ") && strings.Contains(cl, stateDir) {
			out = append(out, e.Name()+": "+cl)
		}
	}
	return out
}

type client struct {
	user, host string
	rep        repo.Repository
}

func connect(ctx context.Context, t *testing.T, dir, url, fp, user, host, pw string) (*client, error) {
	t.Helper()
	cfgDir, err := os.MkdirTemp(dir, "client-"+user+"-"+host+"-*")
	if err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(cfgDir, "repository.config")
	err = repo.ConnectAPIServer(ctx, cfg, &repo.APIServerInfo{BaseURL: url, TrustedServerCertificateFingerprint: fp}, pw,
		&repo.ConnectOptions{
			ClientOptions:  repo.ClientOptions{Username: user, Hostname: host},
			CachingOptions: content.CachingOptions{CacheDirectory: filepath.Join(cfgDir, "cache")},
		})
	if err != nil {
		return nil, err
	}
	rep, err := repo.Open(ctx, cfg, pw, &repo.Options{})
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { _ = rep.Close(context.Background()) })
	return &client{user: user, host: host, rep: rep}, nil
}

func mustConnect(ctx context.Context, t *testing.T, dir, url, fp, user, host, pw string) *client {
	t.Helper()
	c, err := connect(ctx, t, dir, url, fp, user, host, pw)
	if err != nil {
		t.Fatalf("connect %s@%s: %v", user, host, err)
	}
	return c
}

func snapshotDir(ctx context.Context, rep repo.Repository, src snapshot.SourceInfo, dir string, tags map[string]string, pins []string) (*snapshot.Manifest, error) {
	entry, err := localfs.Directory(dir)
	if err != nil {
		return nil, err
	}
	var man *snapshot.Manifest
	err = repo.WriteSession(ctx, rep, repo.WriteSessionOptions{Purpose: "it"}, func(wctx context.Context, w repo.RepositoryWriter) error {
		pt, err := policy.TreeForSource(wctx, w, src)
		if err != nil {
			return err
		}
		m, err := upload.NewUploader(w).Upload(wctx, entry, pt, src)
		if err != nil {
			return err
		}
		m.Tags, m.Pins = tags, pins
		if _, err := snapshot.SaveSnapshot(wctx, w, m); err != nil {
			return err
		}
		man = m
		return nil
	})
	return man, err
}

func deleteManifest(ctx context.Context, rep repo.Repository, id manifest.ID) error {
	return repo.WriteSession(ctx, rep, repo.WriteSessionOptions{Purpose: "it-delete"}, func(wctx context.Context, w repo.RepositoryWriter) error {
		return w.DeleteManifest(wctx, id)
	})
}

func readFile(ctx context.Context, rep repo.Repository, man *snapshot.Manifest, name string) (string, error) {
	root, err := snapshotfs.SnapshotRoot(rep, man)
	if err != nil {
		return "", err
	}
	ch, err := root.(fs.Directory).Child(ctx, name)
	if err != nil {
		return "", err
	}
	r, err := ch.(fs.File).Open(ctx)
	if err != nil {
		return "", err
	}
	defer r.Close()
	b, err := io.ReadAll(r)
	return string(b), err
}

func TestReposerverIntegration(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	bin := filepath.Join(dir, "dbr2-reposerver")
	build := exec.Command("go", "build", "-o", bin, "github.com/AxiomOperator/dbr2/cmd/reposerver")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	repoDir, stateDir := filepath.Join(dir, "repo"), filepath.Join(dir, "state")
	if err := os.MkdirAll(repoDir, 0o700); err != nil {
		t.Fatal(err)
	}
	kport := freePort(t)
	p := &rsProc{t: t, bin: bin, dir: dir,
		mgmt: fmt.Sprintf("127.0.0.1:%d", freePort(t)), health: fmt.Sprintf("127.0.0.1:%d", freePort(t)), kopiaPort: kport}
	p.env = []string{
		"DBR2_REPOSITORY_ID=it-repo", "DBR2_REPOSERVER_PATH=" + repoDir,
		"DBR2_REPOSERVER_EXPECT_FSTYPE=any", "DBR2_REPOSERVER_REQUIRE_MOUNTPOINT=false", "DBR2_REPOSERVER_INIT_IF_EMPTY=true",
		"DBR2_REPOSERVER_STATE_DIR=" + stateDir, "DBR2_INTERNAL_TOKEN=" + itToken,
		"DBR2_HEALTH_ADDR=" + p.health, "DBR2_REPOSERVER_MGMT_ADDR=" + p.mgmt,
		fmt.Sprintf("DBR2_REPOSERVER_KOPIA_ADDR=127.0.0.1:%d", kport),
		"DBR2_REPOSERVER_TLS_NAMES=dbr2.example.lan", "DBR2_REPOSERVER_WATCHDOG_INTERVAL=1s",
	}
	p.start()
	t.Cleanup(p.stop)
	for i := 0; ; i++ {
		if c, err := net.Dial("tcp", p.mgmt); err == nil {
			c.Close()
			break
		}
		if i > 100 {
			t.Fatalf("mgmt API did not start\n%s", p.log)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// --- management API: auth and pre-initialize state
	if code, m := p.call("GET", "/v1/status", "", ""); code != 401 || m["code"] != "unauthorized" {
		t.Fatalf("unauthenticated status: %d %v", code, m)
	}
	if code, _ := p.call("POST", "/v1/initialize", `{"password":"`+itRepoPW+`"}`, "wrong-token"); code != 401 {
		t.Fatalf("wrong token initialize: %d", code)
	}
	code, st := p.call("GET", "/v1/status", "", itToken)
	if code != 200 || st["initialized"] != false || st["server_running"] != false || st["repository_id"] != "it-repo" {
		t.Fatalf("status before initialize: %d %v", code, st)
	}
	if code, m := p.call("PUT", "/v1/users/agent@a1", `{"password":"agent-a1-password-0123"}`, itToken); code != 409 || m["code"] != "not_initialized" {
		t.Fatalf("PUT user before initialize: %d %v", code, m)
	}

	// --- initialize
	code, st = p.call("POST", "/v1/initialize", `{"password":"`+itRepoPW+`","splitter":"DYNAMIC-1M-BUZHASH"}`, itToken)
	if code != 200 || st["initialized"] != true || st["splitter"] != "DYNAMIC-1M-BUZHASH" {
		t.Fatalf("initialize: %d %v\n%s", code, st, p.log)
	}
	if code, m := p.call("POST", "/v1/initialize", `{"password":"`+itRepoPW+`"}`, itToken); code != 409 || m["code"] != "already_initialized" {
		t.Fatalf("second initialize: %d %v", code, m)
	}
	st = p.waitRunning()
	fp, _ := st["cert_sha256"].(string)
	if len(fp) != 64 || st["kopia_version"] != "0.23.1" ||
		st["storage_healthy"] != true || st["storage_path"] != repoDir ||
		st["storage_total_bytes"].(float64) <= 0 || st["storage_free_bytes"].(float64) <= 0 || st["storage_used_bytes"].(float64) <= 0 ||
		st["kopia_address"] != fmt.Sprintf("127.0.0.1:%d", kport) {
		t.Fatalf("status after initialize: %v", st)
	}
	if b, err := os.ReadFile(filepath.Join(stateDir, "repository-password")); err != nil || strings.TrimSpace(string(b)) != itRepoPW {
		t.Fatalf("repository-password: %v", err)
	}
	for _, f := range []string{"repository-password", "control-password", "tls.key"} {
		if fi, err := os.Stat(filepath.Join(stateDir, f)); err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v %v", f, fi.Mode(), err)
		}
	}
	if _, err := os.Stat(filepath.Join(stateDir, "kopia", "repository.config.kopia-password")); err == nil {
		t.Fatal("Kopia persisted the repository password next to its config")
	}
	if strings.Contains(p.log.String(), itRepoPW) {
		t.Fatal("repository password appears in the reposerver log")
	}
	if res, err := http.Get("http://" + p.health + "/healthz"); err != nil || res.StatusCode != 200 {
		t.Fatalf("healthz: %v", err)
	} else {
		res.Body.Close()
	}

	// --- users
	pw := map[string]string{"agent@a1": "agent-a1-password-0123", "agent@b2": "agent-b2-password-4567", "maint@dbr2": "maint-password-89abcdef"}
	for _, u := range []string{"agent@a1", "agent@b2", "maint@dbr2"} {
		if code, m := p.call("PUT", "/v1/users/"+u, `{"password":"`+pw[u]+`"}`, itToken); code != 204 {
			t.Fatalf("PUT %s: %d %v\n%s", u, code, m, p.log)
		}
	}
	for u, secret := range pw {
		if strings.Contains(p.log.String(), secret) {
			t.Fatalf("password of %s appears in the log", u)
		}
	}
	url := fmt.Sprintf("https://127.0.0.1:%d", kport)

	// --- wrong fingerprint / wrong password are refused
	if _, err := connect(ctx, t, dir, url, strings.Repeat("0", 64), "agent", "a1", pw["agent@a1"]); err == nil {
		t.Fatal("connect with a wrong fingerprint succeeded")
	}
	if _, err := connect(ctx, t, dir, url, fp, "agent", "a1", "wrong-password-xxxxxxxx"); err == nil {
		t.Fatal("connect with a wrong password succeeded")
	}

	a1 := mustConnect(ctx, t, dir, url, fp, "agent", "a1", pw["agent@a1"])
	b2 := mustConnect(ctx, t, dir, url, fp, "agent", "b2", pw["agent@b2"])
	maint := mustConnect(ctx, t, dir, url, fp, "maint", "dbr2", pw["maint@dbr2"])

	// --- agent@a1 snapshots a small dir with tags + pins
	data := filepath.Join(dir, "data")
	_ = os.MkdirAll(data, 0o700)
	if err := os.WriteFile(filepath.Join(data, "hello.txt"), []byte(itFileBody), 0o600); err != nil {
		t.Fatal(err)
	}
	src := snapshot.SourceInfo{UserName: "agent", Host: "a1", Path: "/data"}
	tags := map[string]string{"tag:dbr2-rp": "rp_it1", "tag:dbr2-kind": "component"}
	man, err := snapshotDir(ctx, a1.rep, src, data, tags, []string{"dbr2"})
	if err != nil {
		t.Fatalf("a1 snapshot: %v", err)
	}
	if got, err := readFile(ctx, a1.rep, man, "hello.txt"); err != nil || got != itFileBody {
		t.Fatalf("a1 reads own snapshot: %q %v", got, err)
	}

	// --- agent@a1 cannot delete it
	if err := deleteManifest(ctx, a1.rep, man.ID); err == nil || !strings.Contains(err.Error(), "access denied") {
		t.Fatalf("a1 deleted its own snapshot: %v", err)
	}
	// --- agent@b2 cannot list or load it
	ids, err := snapshot.ListSnapshotManifests(ctx, b2.rep, nil, nil)
	if err != nil || slices.Contains(ids, man.ID) {
		t.Fatalf("b2 lists a1's snapshot: %v %v", ids, err)
	}
	if _, err := snapshot.LoadSnapshot(ctx, b2.rep, man.ID); err == nil {
		t.Fatal("b2 loaded a1's snapshot")
	}

	// --- read grant: b2 may load a1's snapshots, then revoked
	code, m := p.call("POST", "/v1/acl/read-grants", `{"user":"agent@b2","source_user":"agent","source_host":"a1"}`, itToken)
	gid, _ := m["id"].(string)
	if code != 201 || gid == "" {
		t.Fatalf("add read grant: %d %v", code, m)
	}
	// Kopia evaluates ACLs per session: reconnect.
	b2g := mustConnect(ctx, t, dir, url, fp, "agent", "b2", pw["agent@b2"])
	gm, err := snapshot.LoadSnapshot(ctx, b2g.rep, man.ID)
	if err != nil {
		t.Fatalf("b2 with grant cannot load a1's snapshot: %v", err)
	}
	if got, err := readFile(ctx, b2g.rep, gm, "hello.txt"); err != nil || got != itFileBody {
		t.Fatalf("b2 with grant reads: %q %v", got, err)
	}
	if err := deleteManifest(ctx, b2g.rep, man.ID); err == nil {
		t.Fatal("read grant allowed deletion")
	}
	if code, _ := p.call("DELETE", "/v1/acl/read-grants/"+gid, "", itToken); code != 204 {
		t.Fatalf("revoke: %d", code)
	}
	if code, _ := p.call("DELETE", "/v1/acl/read-grants/"+gid, "", itToken); code != 404 {
		t.Fatalf("revoke again: %d", code)
	}
	b2r := mustConnect(ctx, t, dir, url, fp, "agent", "b2", pw["agent@b2"])
	if _, err := snapshot.LoadSnapshot(ctx, b2r.rep, man.ID); err == nil {
		t.Fatal("b2 still loads a1's snapshot after revocation")
	}

	// --- maint sees everything; pins and the global policy are intact
	mm, err := snapshot.LoadSnapshot(ctx, maint.rep, man.ID)
	if err != nil {
		t.Fatalf("maint load: %v", err)
	}
	if !slices.Equal(mm.Pins, []string{"dbr2"}) || mm.Tags["tag:dbr2-rp"] != "rp_it1" {
		t.Fatalf("pins/tags not preserved: pins=%v tags=%v", mm.Pins, mm.Tags)
	}
	gp, err := policy.GetDefinedPolicy(ctx, maint.rep, policy.GlobalPolicySourceInfo)
	if err != nil {
		t.Fatalf("read global policy: %v", err)
	}
	if gp.CompressionPolicy.CompressorName != reposerver.GlobalCompression {
		t.Fatalf("global compression = %q", gp.CompressionPolicy.CompressorName)
	}
	rp := gp.RetentionPolicy
	for name, v := range map[string]*policy.OptionalInt{"latest": rp.KeepLatest, "hourly": rp.KeepHourly, "daily": rp.KeepDaily,
		"weekly": rp.KeepWeekly, "monthly": rp.KeepMonthly, "annual": rp.KeepAnnual} {
		if v == nil || int(*v) != reposerver.KeepForever {
			t.Fatalf("global keep-%s = %v", name, v)
		}
	}
	if gp.SchedulingPolicy.IntervalSeconds != 0 || len(gp.SchedulingPolicy.TimesOfDay) != 0 || gp.SchedulingPolicy.Cron != nil {
		t.Fatalf("global policy schedules snapshots: %+v", gp.SchedulingPolicy)
	}

	// --- restart: the Kopia server comes back automatically, data intact
	p.stop()
	if kids := kopiaChildren(stateDir); len(kids) != 0 {
		t.Fatalf("kopia children left after shutdown: %v", kids)
	}
	p.start()
	st = p.waitRunning()
	if st["initialized"] != true || st["cert_sha256"] != fp || st["splitter"] != "DYNAMIC-1M-BUZHASH" {
		t.Fatalf("status after restart: %v", st)
	}
	a1r := mustConnect(ctx, t, dir, url, fp, "agent", "a1", pw["agent@a1"])
	rm, err := snapshot.LoadSnapshot(ctx, a1r.rep, man.ID)
	if err != nil {
		t.Fatalf("a1 load after restart: %v", err)
	}
	if got, err := readFile(ctx, a1r.rep, rm, "hello.txt"); err != nil || got != itFileBody {
		t.Fatalf("data after restart: %q %v", got, err)
	}
	if code, m := p.call("POST", "/v1/initialize", `{"password":"`+itRepoPW+`"}`, itToken); code != 409 || m["code"] != "already_initialized" {
		t.Fatalf("initialize after restart: %d %v", code, m)
	}

	// --- maint lists all agents' snapshots and deletes
	m2 := mustConnect(ctx, t, dir, url, fp, "maint", "dbr2", pw["maint@dbr2"])
	all, err := snapshot.ListSnapshotManifests(ctx, m2.rep, nil, nil)
	if err != nil || !slices.Contains(all, man.ID) {
		t.Fatalf("maint list: %v %v", all, err)
	}
	if err := deleteManifest(ctx, m2.rep, man.ID); err != nil {
		t.Fatalf("maint delete: %v", err)
	}
	if _, err := snapshot.LoadSnapshot(ctx, m2.rep, man.ID); err == nil {
		t.Fatal("snapshot still loadable after maint delete")
	}

	// --- user deletion
	if code, _ := p.call("DELETE", "/v1/users/agent@b2", "", itToken); code != 204 {
		t.Fatalf("delete user: %d", code)
	}
	if code, _ := p.call("DELETE", "/v1/users/agent@b2", "", itToken); code != 404 {
		t.Fatalf("delete absent user: %d", code)
	}
	if _, err := connect(ctx, t, dir, url, fp, "agent", "b2", pw["agent@b2"]); err == nil {
		t.Fatal("deleted user can still connect")
	}
	if strings.Contains(p.log.String(), itRepoPW) {
		t.Fatal("repository password appears in the reposerver log")
	}
}

// syncBuffer is a bytes.Buffer safe for the child's output copier and the
// test reading it concurrently.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}
