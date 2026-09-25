// Command q1 demonstrates Kopia v0.23.1 used as an embedded Go library (ADR-0007)
// against a filesystem repository in .work/q1. Steps a-f of the spike.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/kopia/kopia/snapshot"

	"github.com/AxiomOperator/dbr2/spikes/kopia-library/internal/engine"
)

const (
	password = "spike-repo-password"
	user     = "agent-a"
	host     = "hosta"
)

func main() {
	log.SetFlags(log.Ltime | log.Lmicroseconds)

	work, _ := filepath.Abs(".work/q1")
	must(os.RemoveAll(work))
	must(os.MkdirAll(work, 0o755))

	ctx := context.Background()
	cfg := filepath.Join(work, "repo.config")

	// (a) create + connect repository
	step("a", "initialize + connect filesystem repository")
	must(engine.InitFilesystemRepo(ctx, filepath.Join(work, "repo"), cfg, filepath.Join(work, "cache"), password, user, host))
	rep, err := engine.Open(ctx, cfg, password)
	must(err)
	defer rep.Close(ctx) //nolint:errcheck
	log.Printf("connected: %T user=%s host=%s", rep, rep.ClientOptions().Username, rep.ClientOptions().Hostname)

	rpID := "rp_01SPIKE" + fmt.Sprint(time.Now().Unix())
	appID := "app_demo"

	// (b) tagged directory snapshot + list by tag
	step("b", "snapshot a directory with DBR² tags, list by tag")
	vol := filepath.Join(work, "src/volume-data")
	makeTree(vol, 25, 64<<10)
	srcSum := treeSums(vol)

	volMan, err := engine.SnapshotDir(ctx, rep, vol, engine.SnapshotOptions{
		Source: snapshot.SourceInfo{UserName: user, Host: host, Path: vol},
		Tags: engine.Tags(map[string]string{
			"dbr2-rp": rpID, "dbr2-app": appID, "dbr2-component": "pgdata", "dbr2-kind": "volume",
		}),
	})
	must(err)
	log.Printf("volume snapshot id=%s root=%s files=%d bytes=%d", volMan.ID, volMan.RootObjectID(), volMan.Stats.TotalFileCount, volMan.Stats.TotalFileSize)

	// a second, unrelated snapshot with a different rp tag so filtering is meaningful
	_, err = engine.SnapshotDir(ctx, rep, vol, engine.SnapshotOptions{
		Source: snapshot.SourceInfo{UserName: user, Host: host, Path: vol},
		Tags:   engine.Tags(map[string]string{"dbr2-rp": "rp_OTHER", "dbr2-kind": "volume"}),
	})
	must(err)

	byRP, err := engine.ListByTags(ctx, rep, nil, engine.Tags(map[string]string{"dbr2-rp": rpID}))
	must(err)
	log.Printf("list by tag dbr2-rp=%s -> %d snapshot(s)", rpID, len(byRP))
	all, err := engine.ListByTags(ctx, rep, nil, nil)
	must(err)
	log.Printf("list all snapshots -> %d snapshot(s)", len(all))
	check(len(byRP) == 1 && byRP[0].ID == volMan.ID, "tag filter returns exactly the tagged snapshot")

	// (c) snapshot from io.Reader (simulated pg_dump stdout)
	step("c", "snapshot from io.Reader stream")
	dump := fakeDump(8 << 20)
	dumpSum := sha256.Sum256(dump)
	pr, pw := io.Pipe()
	go func() { // behave like a child process writing to a pipe in small chunks
		for off := 0; off < len(dump); off += 32 << 10 {
			end := min(off+32<<10, len(dump))
			if _, err := pw.Write(dump[off:end]); err != nil {
				return
			}
		}
		pw.Close()
	}()
	dbMan, err := engine.SnapshotStream(ctx, rep, "app.pgdump", pr, engine.SnapshotOptions{
		Source: snapshot.SourceInfo{UserName: user, Host: host, Path: "/dbr2/apps/" + appID + "/db/app"},
		Tags: engine.Tags(map[string]string{
			"dbr2-rp": rpID, "dbr2-app": appID, "dbr2-component": "db-app", "dbr2-kind": "database",
		}),
	})
	must(err)
	log.Printf("stream snapshot id=%s bytes=%d", dbMan.ID, dbMan.Stats.TotalFileSize)

	// (d) recovery manifest as its own tagged snapshot, written LAST (commit marker)
	step("d", "write recovery manifest snapshot (two-phase commit)")
	rm := map[string]any{
		"schema_version": 1, "rp_id": rpID, "application_id": appID,
		"components": []map[string]any{
			{"name": "pgdata", "kind": "volume", "snapshot_id": volMan.ID, "root_object_id": volMan.RootObjectID().String(), "required": true, "status": "ok"},
			{"name": "db-app", "kind": "database", "snapshot_id": dbMan.ID, "root_object_id": dbMan.RootObjectID().String(), "required": true, "status": "ok"},
		},
	}
	rmJSON, _ := json.MarshalIndent(rm, "", "  ")
	mMan, err := engine.SnapshotStream(ctx, rep, "recovery-manifest.json", bytes.NewReader(rmJSON), engine.SnapshotOptions{
		Source: snapshot.SourceInfo{UserName: user, Host: host, Path: "/dbr2/apps/" + appID + "/manifest"},
		Tags:   engine.Tags(map[string]string{"dbr2-rp": rpID, "dbr2-app": appID, "dbr2-kind": "manifest"}),
	})
	must(err)
	committed, err := engine.ListByTags(ctx, rep, nil, engine.Tags(map[string]string{"dbr2-rp": rpID, "dbr2-kind": "manifest"}))
	must(err)
	check(len(committed) == 1 && committed[0].ID == mMan.ID, "RP is committed: exactly one manifest snapshot found by tag")
	byRP, _ = engine.ListByTags(ctx, rep, nil, engine.Tags(map[string]string{"dbr2-rp": rpID}))
	log.Printf("snapshots in RP %s: %d (volume, database, manifest)", rpID, len(byRP))

	// (e) restore + verify
	step("e", "restore directory + stream and verify checksums")
	dst := filepath.Join(work, "restore/volume-data")
	st, err := engine.RestoreToDir(ctx, rep, volMan, dst)
	must(err)
	log.Printf("restored files=%d bytes=%d", st.RestoredFileCount, st.RestoredTotalFileSize)
	check(equalSums(srcSum, treeSums(dst)), "restored tree sha256 matches source (%d files)", len(srcSum))

	rc, err := engine.OpenStream(ctx, rep, dbMan, "app.pgdump")
	must(err)
	h := sha256.New()
	n, err := io.Copy(h, rc)
	must(err)
	rc.Close()
	check(bytes.Equal(h.Sum(nil), dumpSum[:]), "restored stream sha256 matches (%d bytes)", n)

	rc, err = engine.OpenStream(ctx, rep, mMan, "recovery-manifest.json")
	must(err)
	back, _ := io.ReadAll(rc)
	rc.Close()
	check(bytes.Equal(back, rmJSON), "recovery manifest round-trips byte-for-byte")

	// (f) progress callbacks + context cancellation
	step("f", "progress callbacks + cancel mid-snapshot")
	big := filepath.Join(work, "src/big")
	makeTree(big, 40, 8<<20) // 320 MiB of random data
	before, _ := engine.ListByTags(ctx, rep, nil, nil)
	repoBefore := dirSize(filepath.Join(work, "repo"))

	cctx, cancel := context.WithCancel(ctx)
	ticks := 0
	prog := &engine.Progress{Interval: 50 * time.Millisecond, OnTick: func(hashed, uploaded, files int64) {
		ticks++
		log.Printf("  progress: hashed=%d MiB uploaded=%d MiB files=%d", hashed>>20, uploaded>>20, files)
		if hashed > 32<<20 {
			cancel() // simulate operator / Temporal cancellation
		}
	}}
	t0 := time.Now()
	_, err = engine.SnapshotDir(cctx, rep, big, engine.SnapshotOptions{
		Source:   snapshot.SourceInfo{UserName: user, Host: host, Path: big},
		Tags:     engine.Tags(map[string]string{"dbr2-kind": "volume", "dbr2-rp": "rp_CANCELED"}),
		Progress: prog,
	})
	hashed, _, _ := prog.Totals()
	log.Printf("snapshot returned after %v, hashed=%d MiB, err=%v", time.Since(t0).Round(time.Millisecond), hashed>>20, err)
	check(errors.Is(err, engine.ErrCanceled) || errors.Is(err, context.Canceled), "cancellation surfaces as an error")
	check(ticks > 0, "progress callback fired (%d ticks)", ticks)
	after, _ := engine.ListByTags(ctx, rep, nil, nil)
	check(len(after) == len(before), "no snapshot manifest persisted for canceled run (%d before, %d after)", len(before), len(after))
	log.Printf("repo size before=%d MiB after canceled run=%d MiB (pack blobs written before cancel remain until maintenance)",
		repoBefore>>20, dirSize(filepath.Join(work, "repo"))>>20)

	// repository still healthy after cancel: snapshot the same dir fully
	prog2 := &engine.Progress{}
	full, err := engine.SnapshotDir(ctx, rep, big, engine.SnapshotOptions{
		Source:   snapshot.SourceInfo{UserName: user, Host: host, Path: big},
		Tags:     engine.Tags(map[string]string{"dbr2-kind": "volume"}),
		Progress: prog2,
	})
	must(err)
	_, up, _ := prog2.Totals()
	log.Printf("post-cancel full snapshot ok id=%s bytes=%d (uploaded this run: %d MiB) repo size now=%d MiB",
		full.ID, full.Stats.TotalFileSize, up>>20, dirSize(filepath.Join(work, "repo"))>>20)
	rest := filepath.Join(work, "restore/big")
	_, err = engine.RestoreToDir(ctx, rep, full, rest)
	must(err)
	check(equalSums(treeSums(big), treeSums(rest)), "post-cancel snapshot restores intact")

	fmt.Println("\nQ1: ALL CHECKS PASSED")
}

// ---------------------------------------------------------------------------

func step(id, s string) { log.Printf("==== (%s) %s", id, s) }

func check(ok bool, f string, a ...any) {
	if !ok {
		log.Fatalf("FAIL: "+f, a...)
	}
	log.Printf("PASS: "+f, a...)
}

func must(err error) {
	if err != nil {
		log.Fatalf("error: %+v", err)
	}
}

func makeTree(root string, n, size int) {
	must(os.MkdirAll(filepath.Join(root, "sub"), 0o755))
	buf := make([]byte, size)
	for i := range n {
		_, _ = rand.Read(buf)
		dir := root
		if i%2 == 1 {
			dir = filepath.Join(root, "sub")
		}
		must(os.WriteFile(filepath.Join(dir, fmt.Sprintf("file-%03d.bin", i)), buf, 0o644))
	}
}

func dirSize(root string) int64 {
	var n int64
	_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if fi, e := d.Info(); e == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n
}

func fakeDump(n int) []byte {
	var b bytes.Buffer
	for i := 0; b.Len() < n; i++ {
		fmt.Fprintf(&b, "INSERT INTO t VALUES (%d, 'row-%d', now());\n", i, i*7919)
	}
	return b.Bytes()[:n]
}

func treeSums(root string) map[string]string {
	out := map[string]string{}
	must(filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		s := sha256.Sum256(b)
		rel, _ := filepath.Rel(root, p)
		out[rel] = hex.EncodeToString(s[:])
		return nil
	}))
	return out
}

func equalSums(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	keys := make([]string, 0, len(a))
	for k := range a {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if a[k] != b[k] {
			return false
		}
	}
	return true
}
