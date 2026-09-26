// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	agentv1 "github.com/AxiomOperator/dbr2/internal/agentpb/agent/v1"
	"github.com/AxiomOperator/dbr2/internal/engine"
	"github.com/AxiomOperator/dbr2/internal/manifest"
	"github.com/AxiomOperator/dbr2/internal/runtime"
)

// dumpFake adds DumpControl to restoreFake: a scripted pg_dumpall and a
// container filesystem for CopyFromContainer.
type dumpFake struct {
	*restoreFake
	pgOut    []byte
	pgExit   int
	pgStderr string
	pgCmd    []string
	maxChunk int // largest stdout write seen
	files    map[string][]byte
	dirs     map[string]map[string][]byte
}

func (f *dumpFake) ExecOutput(ctx context.Context, id string, cmd []string, stdout io.Writer, _ int) (runtime.ExecResult, error) {
	f.mu.Lock()
	f.pgCmd = cmd
	f.mu.Unlock()
	for b := f.pgOut; len(b) > 0; {
		n := min(len(b), 32<<10)
		f.maxChunk = max(f.maxChunk, n)
		if _, err := stdout.Write(b[:n]); err != nil {
			return runtime.ExecResult{}, err
		}
		b = b[n:]
	}
	return runtime.ExecResult{ExitCode: f.pgExit, Output: []byte(f.pgStderr)}, ctx.Err()
}

func (f *dumpFake) CopyFromContainer(_ context.Context, _ string, p string) (io.ReadCloser, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if b, ok := f.files[p]; ok {
		_ = tw.WriteHeader(&tar.Header{Name: path.Base(p), Mode: 0o600, Size: int64(len(b)), Typeflag: tar.TypeReg})
		_, _ = tw.Write(b)
	} else if d, ok := f.dirs[p]; ok {
		_ = tw.WriteHeader(&tar.Header{Name: path.Base(p) + "/", Mode: 0o700, Typeflag: tar.TypeDir})
		for name, b := range d {
			_ = tw.WriteHeader(&tar.Header{Name: path.Base(p) + "/" + name, Mode: 0o600, Size: int64(len(b)), Typeflag: tar.TypeReg})
			_, _ = tw.Write(b)
		}
	} else {
		return nil, fmt.Errorf("%w: %s", runtime.ErrNotFound, p)
	}
	_ = tw.Close()
	return io.NopCloser(&buf), nil
}

// fakeRedis scripts redis-cli.
type fakeRedis struct {
	mu        sync.Mutex
	lastsave  int64
	saves     int
	pending   int // polls until the running BGSAVE finishes
	status    string
	bgsave    string // BGSAVE reply override
	noauth    bool
	noSaves   bool // old Redis: no rdb_saves field
	never     bool // BGSAVE never finishes
	config    map[string]string
	bgsaveCnt int
}

func newFakeRedis() *fakeRedis {
	return &fakeRedis{lastsave: 1_700_000_000, status: "ok",
		config: map[string]string{"dir": "/data", "dbfilename": "dump.rdb", "appendonly": "no"}}
}

func (r *fakeRedis) exec(_ string, cmd []string) (runtime.ExecResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(cmd) == 0 || cmd[0] != "redis-cli" {
		return runtime.ExecResult{ExitCode: 127}, nil
	}
	if r.noauth {
		return runtime.ExecResult{Output: []byte("NOAUTH Authentication required.\n")}, nil
	}
	out := func(s string) (runtime.ExecResult, error) { return runtime.ExecResult{Output: []byte(s)}, nil }
	args := strings.Join(cmd[1:], " ")
	switch {
	case args == "LASTSAVE":
		return out(fmt.Sprintf("%d\n", r.lastsave))
	case args == "TIME":
		return out(fmt.Sprintf("%d\n123\n", r.lastsave+5))
	case args == "INFO persistence":
		inProgress := 0
		if r.pending > 0 || r.never {
			inProgress = 1
			if !r.never {
				r.pending--
				if r.pending == 0 {
					r.saves++
					r.lastsave++
				}
			}
		}
		s := fmt.Sprintf("# Persistence\r\nloading:0\r\nrdb_bgsave_in_progress:%d\r\nrdb_last_bgsave_status:%s\r\n", inProgress, r.status)
		if !r.noSaves {
			s += fmt.Sprintf("rdb_saves:%d\r\n", r.saves)
		}
		return out(s)
	case args == "BGSAVE":
		r.bgsaveCnt++
		if r.bgsave != "" {
			return runtime.ExecResult{Output: []byte(r.bgsave + "\n"), ExitCode: 1}, nil
		}
		r.pending = 3
		return out("Background saving started\n")
	case strings.HasPrefix(args, "CONFIG GET "):
		k := strings.TrimPrefix(args, "CONFIG GET ")
		if v, ok := r.config[k]; ok {
			return out(k + "\n" + v + "\n")
		}
		return out("")
	}
	return runtime.ExecResult{Output: []byte("ERR unknown command\n"), ExitCode: 1}, nil
}

func newDumpEnv(t *testing.T) (*snapEnv, *dumpFake) {
	t.Helper()
	e := newSnapEnv(t)
	df := &dumpFake{restoreFake: newRestoreFake(t, map[string]string{"db1": "running", "down": "exited"}),
		files: map[string][]byte{}, dirs: map[string]map[string][]byte{}}
	e.Agent.rt = df
	oldI := redisPollInterval
	redisPollInterval = time.Millisecond
	t.Cleanup(func() { redisPollInterval = oldI })
	return e, df
}

func pgSpec(name, user string, required bool) *agentv1.ComponentSpec {
	return &agentv1.ComponentSpec{Name: name, Kind: agentv1.ComponentKind_COMPONENT_KIND_DATABASE, Required: required,
		Database: &agentv1.DatabaseSpec{Engine: enginePostgres, Format: formatPGDump, ContainerId: "db1", Service: "db", User: user}}
}

func redisSpec(name string, aof bool) *agentv1.ComponentSpec {
	return &agentv1.ComponentSpec{Name: name, Kind: agentv1.ComponentKind_COMPONENT_KIND_DATABASE, Required: true,
		Database: &agentv1.DatabaseSpec{Engine: engineRedis, Format: formatRDB, ContainerId: "db1", Service: "cache", IncludeAof: aof}}
}

func pgDump(size int) []byte {
	var b bytes.Buffer
	b.WriteString("--\n-- PostgreSQL database cluster dump\n--\n")
	for i := 0; b.Len() < size; i++ {
		fmt.Fprintf(&b, "INSERT INTO items VALUES (%d, 'item-%d');\n", i, i)
	}
	b.WriteString("--\n" + pgTrailer + "\n--\n\n")
	return b.Bytes()
}

func snapsOf(t *testing.T, e *snapEnv, rp string) []engine.Snapshot {
	t.Helper()
	s, err := e.repo.List(context.Background(), nil, map[string]string{engine.TagRP: rp})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestDumpPostgresStreams(t *testing.T) {
	e, df := newDumpEnv(t)
	df.pgOut = pgDump(4 << 20)
	u := e.run(t, snapCmd("rp_pg1", false, pgSpec("database:db", "", true)))
	succeeded(t, u)
	r := u.GetSnapshotComponents().Components[0]
	if r.Status != manifest.ComponentSucceeded || r.FileName != pgDumpFile || r.Database.GetEngine() != enginePostgres ||
		r.Database.GetService() != "db" || !strings.Contains(r.Validation, "trailer present") ||
		!strings.Contains(r.Validation, "MiB uncompressed") || r.SizeBytes <= 0 || r.SizeBytes >= int64(len(df.pgOut)) {
		t.Fatalf("result %+v", r)
	}
	if strings.Join(df.pgCmd, " ") != "pg_dumpall --clean --if-exists -U app" {
		t.Fatalf("command %q", df.pgCmd)
	}
	if df.maxChunk > 32<<10 {
		t.Fatalf("stdout written in %d byte chunks", df.maxChunk)
	}
	rc, err := e.repo.OpenStream(context.Background(), r.SnapshotId, pgDumpFile)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	zr, err := zstd.NewReader(rc)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	got, err := io.ReadAll(zr)
	if err != nil || !bytes.Equal(got, df.pgOut) {
		t.Fatalf("dump round trip: %d bytes, %v", len(got), err)
	}
	snaps := snapsOf(t, e, "rp_pg1")
	if len(snaps) != 1 || snaps[0].Tags[engine.TagKind] != manifest.KindDatabase || snaps[0].Tags[engine.TagComponent] != "database:db" {
		t.Fatalf("snapshots %+v", snaps)
	}
	// An explicit user wins over POSTGRES_USER.
	succeeded(t, e.run(t, snapCmd("rp_pg2", false, pgSpec("database:db", "backup", true))))
	if df.pgCmd[len(df.pgCmd)-1] != "backup" {
		t.Fatalf("command %q", df.pgCmd)
	}
}

func TestDumpPostgresValidationFailures(t *testing.T) {
	e, df := newDumpEnv(t)
	vol := t.TempDir()
	cases := []struct {
		out    []byte
		exit   int
		stderr string
		want   string
	}{
		{out: pgDump(1 << 20)[:1<<19], want: "trailer"},
		{out: pgDump(1 << 20), exit: 1, stderr: "pg_dumpall: error: connection failed", want: "exited with code 1: pg_dumpall: error: connection failed"},
		{out: nil, want: "trailer"},
	}
	for i, c := range cases {
		df.pgOut, df.pgExit, df.pgStderr = c.out, c.exit, c.stderr
		rp := fmt.Sprintf("rp_bad%d", i)
		u := e.run(t, snapCmd(rp, false, pgSpec("database:db", "", true),
			&agentv1.ComponentSpec{Name: "volume:v", Kind: agentv1.ComponentKind_COMPONENT_KIND_VOLUME, Path: vol, Required: true}))
		succeeded(t, u)
		res := u.GetSnapshotComponents().Components
		if res[0].Status != manifest.ComponentFailed || !strings.Contains(res[0].Error, c.want) || res[0].SnapshotId != "" ||
			res[0].Database == nil {
			t.Fatalf("%d: %+v", i, res[0])
		}
		if res[1].Status != manifest.ComponentSkipped {
			t.Fatalf("%d: required failure did not skip the rest: %+v", i, res[1])
		}
		if s := snapsOf(t, e, rp); len(s) != 0 {
			t.Fatalf("%d: failed dump saved: %+v", i, s)
		}
	}
	// Optional component: failure does not stop the rest.
	df.pgOut = nil
	u := e.run(t, snapCmd("rp_opt", false, pgSpec("database:db", "", false),
		&agentv1.ComponentSpec{Name: "volume:v", Kind: agentv1.ComponentKind_COMPONENT_KIND_VOLUME, Path: vol, Required: true}))
	if res := u.GetSnapshotComponents().Components; res[0].Status != manifest.ComponentFailed || res[1].Status != manifest.ComponentSucceeded {
		t.Fatalf("optional: %+v", res)
	}
	// Spec problems.
	for _, s := range []*agentv1.ComponentSpec{
		{Name: "database:x", Kind: agentv1.ComponentKind_COMPONENT_KIND_DATABASE},
		{Name: "database:x", Kind: agentv1.ComponentKind_COMPONENT_KIND_DATABASE, Database: &agentv1.DatabaseSpec{Engine: "mysql", Format: "sql", ContainerId: "db1"}},
		{Name: "database:x", Kind: agentv1.ComponentKind_COMPONENT_KIND_DATABASE, Database: &agentv1.DatabaseSpec{Engine: enginePostgres, Format: formatPGDump, ContainerId: "down"}},
	} {
		u := e.run(t, snapCmd("rp_spec", false, s))
		if r := u.GetSnapshotComponents().Components[0]; r.Status != manifest.ComponentFailed {
			t.Fatalf("spec %+v: %+v", s, r)
		}
	}
}

func rdb(size int) []byte {
	return append([]byte("REDIS0012"), bytes.Repeat([]byte{0xfa}, size)...)
}

func TestDumpRedis(t *testing.T) {
	e, df := newDumpEnv(t)
	fr := newFakeRedis()
	df.exec = fr.exec
	df.files["/data/dump.rdb"] = rdb(1000)
	u := e.run(t, snapCmd("rp_r1", false, redisSpec("database:cache", false)))
	succeeded(t, u)
	r := u.GetSnapshotComponents().Components[0]
	if r.Status != manifest.ComponentSucceeded || r.FileName != rdbDumpFile || r.Database.GetEngine() != engineRedis ||
		!strings.Contains(r.Validation, "RDB magic REDIS0012") || r.SizeBytes != 1009 || fr.bgsaveCnt != 1 || fr.saves != 1 {
		t.Fatalf("result %+v (bgsave %d, saves %d)", r, fr.bgsaveCnt, fr.saves)
	}
	rc, err := e.repo.OpenStream(context.Background(), r.SnapshotId, rdbDumpFile)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if !bytes.Equal(b, df.files["/data/dump.rdb"]) {
		t.Fatal("rdb content")
	}

	// A save already running is waited for; old Redis uses LASTSAVE.
	fr.bgsave, fr.pending, fr.noSaves = "ERR Background save already in progress", 2, true
	succeeded(t, e.run(t, snapCmd("rp_r2", false, redisSpec("database:cache", false))))
	if r := snapsOf(t, e, "rp_r2"); len(r) != 1 {
		t.Fatalf("in progress: %+v", r)
	}

	fail := func(rp, want string) {
		t.Helper()
		u := e.run(t, snapCmd(rp, false, redisSpec("database:cache", false)))
		succeeded(t, u)
		if r := u.GetSnapshotComponents().Components[0]; r.Status != manifest.ComponentFailed || !strings.Contains(r.Error, want) {
			t.Fatalf("%s: %+v", rp, r)
		}
		if s := snapsOf(t, e, rp); len(s) != 0 {
			t.Fatalf("%s: saved %+v", rp, s)
		}
	}
	// A failed save.
	fr.bgsave, fr.noSaves, fr.status = "", false, "err"
	fail("rp_r3", "BGSAVE failed")
	fr.status = ""
	fr.status = "ok"
	// Timeout.
	old := redisSaveTimeout
	redisSaveTimeout = 20 * time.Millisecond
	fr.never = true
	fail("rp_r4", "did not complete")
	fr.never, redisSaveTimeout = false, old
	// Unexpected BGSAVE reply.
	fr.bgsave = "ERR something else"
	fail("rp_r5", "something else")
	fr.bgsave = ""
	// Bad RDB magic, empty and truncated files.
	df.files["/data/dump.rdb"] = append([]byte("NOTREDIS!"), make([]byte, 100)...)
	fail("rp_r6", "not an RDB file")
	df.files["/data/dump.rdb"] = nil
	fail("rp_r7", "empty")
	df.files["/data/dump.rdb"] = []byte("REDIS")
	fail("rp_r8", "truncated")
	delete(df.files, "/data/dump.rdb")
	fail("rp_r9", "not found")
	df.files["/data/dump.rdb"] = rdb(10)
	// Password-protected Redis.
	fr.noauth = true
	fail("rp_r10", "Redis AUTH is not supported yet")
}

func TestDumpRedisAOF(t *testing.T) {
	e, df := newDumpEnv(t)
	fr := newFakeRedis()
	df.exec = fr.exec
	df.files["/data/dump.rdb"] = rdb(100)
	// AOF requested but disabled: RDB only, as a stream.
	u := e.run(t, snapCmd("rp_a1", false, redisSpec("database:cache", true)))
	succeeded(t, u)
	if r := u.GetSnapshotComponents().Components[0]; r.Status != manifest.ComponentSucceeded || !strings.Contains(r.Validation, "AOF disabled") {
		t.Fatalf("aof off: %+v", r)
	}
	// Redis 7+ appendonlydir.
	fr.config["appendonly"], fr.config["appenddirname"] = "yes", "appendonlydir"
	df.dirs["/data/appendonlydir"] = map[string][]byte{
		"appendonly.aof.1.base.rdb": rdb(10), "appendonly.aof.1.incr.aof": []byte("*1\r\n$4\r\nPING\r\n"), "appendonly.aof.manifest": []byte("file x\n")}
	u = e.run(t, snapCmd("rp_a2", false, redisSpec("database:cache", true)))
	succeeded(t, u)
	r := u.GetSnapshotComponents().Components[0]
	if r.Status != manifest.ComponentSucceeded || r.FileName != rdbDumpFile || !strings.Contains(r.Validation, "AOF appendonlydir (3 files") || r.Files != 4 {
		t.Fatalf("aof: %+v", r)
	}
	ctx := context.Background()
	ents, err := e.repo.ListDir(ctx, r.SnapshotId, "")
	if err != nil || len(ents) != 2 || ents[0].Name != "appendonlydir" || ents[1].Name != rdbDumpFile {
		t.Fatalf("root %+v %v", ents, err)
	}
	// The restore side opens dump.rdb by name.
	rc, err := e.repo.OpenStream(ctx, r.SnapshotId, rdbDumpFile)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if !bytes.Equal(b, df.files["/data/dump.rdb"]) {
		t.Fatal("rdb content")
	}
	if ents, _ := e.repo.ListDir(ctx, r.SnapshotId, "appendonlydir"); len(ents) != 3 {
		t.Fatalf("aof dir %+v", ents)
	}
	// Legacy appendonly.aof.
	delete(fr.config, "appenddirname")
	fr.config["appendfilename"] = "appendonly.aof"
	df.files["/data/appendonly.aof"] = []byte("*1\r\n$4\r\nPING\r\n")
	u = e.run(t, snapCmd("rp_a3", false, redisSpec("database:cache", true)))
	succeeded(t, u)
	if r := u.GetSnapshotComponents().Components[0]; r.Status != manifest.ComponentSucceeded || !strings.Contains(r.Validation, "AOF appendonly.aof (1 files") {
		t.Fatalf("legacy aof: %+v", r)
	}
	if ents, _ := e.repo.ListDir(ctx, u.GetSnapshotComponents().Components[0].SnapshotId, ""); len(ents) != 2 {
		t.Fatalf("legacy root %+v", ents)
	}
	// A corrupt RDB fails the directory capture too, and staging is removed.
	df.files["/data/dump.rdb"] = []byte("garbage-data")
	u = e.run(t, snapCmd("rp_a4", false, redisSpec("database:cache", true)))
	if r := u.GetSnapshotComponents().Components[0]; r.Status != manifest.ComponentFailed || !strings.Contains(r.Error, "not an RDB file") {
		t.Fatalf("corrupt: %+v", r)
	}
	if s := snapsOf(t, e, "rp_a4"); len(s) != 0 {
		t.Fatalf("saved %+v", s)
	}
}

func TestRDBReaderAndCopyTreeRejects(t *testing.T) {
	for in, want := range map[string]string{"": "empty", "REDIS00": "truncated", "REDISabcd": "not an RDB", "REDIS0011": ""} {
		_, err := io.ReadAll(&rdbReader{r: strings.NewReader(in)})
		if (want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), want)) {
			t.Fatalf("%q: %v", in, err)
		}
	}
	df := &dumpFake{restoreFake: newRestoreFake(t, nil), dirs: map[string]map[string][]byte{"/data/appendonlydir": {"../../evil": []byte("x")}}}
	if _, _, err := copyTree(context.Background(), df, "c", "/data/appendonlydir", t.TempDir()); err == nil || !strings.Contains(err.Error(), "unexpected archive entry") {
		t.Fatalf("traversal: %v", err)
	}
	if !errors.Is(errRedisAuth, errRedisAuth) || humanBytes(1536) != "1.5 KiB" || humanBytes(12) != "12 B" {
		t.Fatal("helpers")
	}
}
