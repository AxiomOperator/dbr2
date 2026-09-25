// Command q3 measures bytes on the wire between a Kopia repository-server
// client (library, as agent-a@hosta) and the server, through an in-process
// byte-counting TCP proxy (127.0.0.1:51516 -> 127.0.0.1:51515).
//
// Prerequisite: scripts/q3-setup.sh
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kopia/kopia/snapshot"
	"github.com/kopia/kopia/snapshot/policy"

	"github.com/AxiomOperator/dbr2/spikes/kopia-library/internal/engine"
	"github.com/AxiomOperator/dbr2/spikes/kopia-library/internal/proxy"
)

const (
	nFiles   = 20
	fileSize = 10 << 20 // 20 x 10 MiB = 200 MiB
	user     = "agent-a"
	host     = "hosta"
	pass     = "pw-agent-a"
)

var work, _ = filepath.Abs(".work/q3")

type row struct {
	name            string
	up, down        int64
	repoGrowth      int64
	hashed, sentApp int64
	dur             time.Duration
	note            string
}

func main() {
	log.SetFlags(log.Ltime)
	ctx := context.Background()

	p := &proxy.Proxy{Target: "127.0.0.1:51515"}
	must(p.Start("127.0.0.1:51516"))
	defer p.Close()

	fp, err := os.ReadFile(filepath.Join(work, "cert.sha256"))
	must(err)
	cfgDir := filepath.Join(work, "client")
	must(os.MkdirAll(cfgDir, 0o700))
	cfg := filepath.Join(cfgDir, "repository.config")
	must(engine.ConnectRepoServer(ctx, "https://127.0.0.1:51516", strings.TrimSpace(string(fp)), cfg,
		filepath.Join(cfgDir, "cache"), pass, user, host))

	plain := filepath.Join(work, "data/plain")
	comp := filepath.Join(work, "data/compressible")
	log.Printf("generating 2 x %d MiB datasets ...", nFiles*fileSize>>20)
	genDataset(plain, false)
	genDataset(comp, true)

	var rows []row
	var prev *snapshot.Manifest

	// run opens a FRESH client connection (no in-memory "recently written"
	// state carries over) and snapshots dir, measuring wire bytes.
	run := func(name, dir string, usePrev bool, note string) *snapshot.Manifest {
		rep, err := engine.Open(ctx, cfg, pass)
		must(err)
		defer rep.Close(ctx) //nolint:errcheck

		var previous []*snapshot.Manifest
		if usePrev && prev != nil {
			previous = []*snapshot.Manifest{prev}
		}

		repoBefore := dirSize(filepath.Join(work, "repo"))
		u0, d0 := p.Snapshot()
		prog := &engine.Progress{}
		t0 := time.Now()
		man, err := engine.SnapshotDir(ctx, rep, dir, engine.SnapshotOptions{
			Source:   snapshot.SourceInfo{UserName: user, Host: host, Path: dir},
			Previous: previous,
			Progress: prog,
		})
		must(err)
		time.Sleep(300 * time.Millisecond) // let the proxy drain
		u1, d1 := p.Snapshot()
		hashed, sent, _ := prog.Totals()
		r := row{name, u1 - u0, d1 - d0, dirSize(filepath.Join(work, "repo")) - repoBefore, hashed, sent, time.Since(t0), note}
		rows = append(rows, r)
		log.Printf("%-40s up=%7.1f MiB down=%6.2f MiB repo+=%7.1f MiB hashed=%6.1f MiB OnUpload=%6.1f MiB (%v)",
			name, mib(r.up), mib(r.down), mib(r.repoGrowth), mib(hashed), mib(sent), r.dur.Round(time.Millisecond))
		return man
	}

	// --- A. compression OFF (default policy) ---
	prev = run("A1 initial full (200 MiB, no compression)", plain, true, "")
	prev = run("A2 unchanged, with previous manifest", plain, true, "hash cache: files skipped by size+mtime")
	prev = run("A3 unchanged, NO previous manifest", plain, false, "every file re-read + re-hashed; server existence check")
	mutate(plain)
	prev = run("A4 5% changed (1 MiB in each of 10 files)", plain, true, "")

	// --- B. compression ON (zstd) for the compressible dataset ---
	{
		rep, err := engine.Open(ctx, cfg, pass)
		must(err)
		must(engine.SetPolicy(ctx, rep, snapshot.SourceInfo{UserName: user, Host: host, Path: comp},
			&policy.Policy{CompressionPolicy: policy.CompressionPolicy{CompressorName: "zstd"}}))
		rep.Close(ctx) //nolint:errcheck
	}
	prev = nil
	prev = run("B1 initial full, compressible, zstd policy", comp, true, "")
	mutate(comp)
	prev = run("B2 5% changed, zstd policy", comp, true, "")

	fmt.Println("\n| run | client→server (wire) | server→client | repo growth on server disk | hashed by client | note |\n|---|---:|---:|---:|---:|---|")
	for _, r := range rows {
		fmt.Printf("| %s | %.1f MiB | %.2f MiB | %.1f MiB | %.1f MiB | %s |\n", r.name, mib(r.up), mib(r.down), mib(r.repoGrowth), mib(r.hashed), r.note)
	}
}

func mib(n int64) float64 { return float64(n) / (1 << 20) }

// genDataset writes nFiles unique files. plain=base64 of random bytes (zstd-fastest
// could not compress it at all in practice); compressible=log-like text lines
// with random hex fields (unique, so no dedup, but compresses well).
func genDataset(dir string, compressible bool) {
	must(os.MkdirAll(dir, 0o755))
	raw := make([]byte, fileSize*3/4)
	for i := range nFiles {
		var data []byte
		if compressible {
			var b strings.Builder
			rnd := make([]byte, 8)
			for n := 0; b.Len() < fileSize; n++ {
				_, _ = rand.Read(rnd)
				fmt.Fprintf(&b, "2026-09-25T10:00:00Z level=info file=%02d seq=%09d msg=\"request served\" trace=%x\n", i, n, rnd)
			}
			data = []byte(b.String())[:fileSize]
		} else {
			_, _ = rand.Read(raw)
			data = []byte(base64.StdEncoding.EncodeToString(raw))[:fileSize]
		}
		must(os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%02d.dat", i)), data, 0o644))
	}
}

// mutate overwrites 1 MiB in the middle of every other file: 10 MiB = 5% of 200 MiB.
func mutate(dir string) {
	patch := make([]byte, 1<<20)
	for i := 0; i < nFiles; i += 2 {
		_, _ = rand.Read(patch)
		enc := []byte(base64.StdEncoding.EncodeToString(patch))[:1<<20]
		f, err := os.OpenFile(filepath.Join(dir, fmt.Sprintf("f%02d.dat", i)), os.O_WRONLY, 0)
		must(err)
		_, err = f.WriteAt(enc, fileSize/2)
		must(err)
		must(f.Close())
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

func must(err error) {
	if err != nil {
		log.Fatalf("fatal: %+v", err)
	}
}
