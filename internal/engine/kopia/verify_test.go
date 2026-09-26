// SPDX-License-Identifier: Apache-2.0

package kopia

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AxiomOperator/dbr2/internal/engine"
)

const verifyPW = "verify-test-password" // gitleaks:allow (test fixture)

// verifyRepo snapshots a small tree into a fresh filesystem repository and
// returns the repository directory and the snapshot ID.
func verifyRepo(t *testing.T) (repoDir, id string) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	repoDir = filepath.Join(dir, "repo")
	r, err := InitFilesystem(ctx, repoDir, filepath.Join(dir, "state"), verifyPW, "agent", "host-1")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close(ctx)
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "a", "b"), 0o750); err != nil {
		t.Fatal(err)
	}
	for i, p := range []string{"one", "a/two", "a/b/three"} {
		data := make([]byte, 64<<10*(i+1))
		_, _ = rand.Read(data)
		if err := os.WriteFile(filepath.Join(src, p), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s, err := r.SnapshotPath(ctx, src, engine.SnapshotRequest{Source: engine.Source{User: "agent", Host: "host-1", Path: "/v"}})
	if err != nil {
		t.Fatal(err)
	}
	return repoDir, s.ID
}

// reopen opens repoDir with a fresh cache, so nothing is served from a
// local copy.
func reopen(t *testing.T, repoDir string) engine.Repository {
	t.Helper()
	ctx := context.Background()
	r, err := InitFilesystem(ctx, repoDir, t.TempDir(), verifyPW, "agent", "host-1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(ctx) })
	return r
}

// packBlobs lists the data pack blob files (Kopia "p" blobs).
func packBlobs(t *testing.T, repoDir string) []string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(repoDir, func(p string, d os.DirEntry, err error) error {
		rel, _ := filepath.Rel(repoDir, p)
		if err == nil && !d.IsDir() && strings.HasPrefix(rel, "p") && strings.HasSuffix(p, ".f") {
			out = append(out, p)
		}
		return nil
	})
	if len(out) == 0 {
		t.Fatal("no pack blobs found")
	}
	return out
}

func TestVerifyClean(t *testing.T) {
	repoDir, id := verifyRepo(t)
	r := reopen(t, repoDir)
	st, err := r.Verify(context.Background(), id, engine.VerifyOptions{ReadPercent: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Errors) != 0 || st.Dirs != 3 || st.Files != 3 || st.FilesRead != 3 || st.BytesRead != 6*64<<10 {
		t.Fatalf("stats %+v", st)
	}
	st, err = r.Verify(context.Background(), id, engine.VerifyOptions{ReadPercent: 0})
	if err != nil || len(st.Errors) != 0 || st.FilesRead != 0 || st.Files != 3 {
		t.Fatalf("no read: %+v %v", st, err)
	}
	if _, err := r.Verify(context.Background(), "nope", engine.VerifyOptions{}); !errors.Is(err, engine.ErrNotFound) {
		t.Fatalf("unknown snapshot: %v", err)
	}
}

func TestVerifyCorruptPack(t *testing.T) {
	repoDir, id := verifyRepo(t)
	for _, p := range packBlobs(t, repoDir) {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for i := len(b) / 4; i < len(b)/2; i++ {
			b[i] ^= 0xff
		}
		_ = os.Chmod(p, 0o600)
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	r := reopen(t, repoDir)
	// Index-only checks cannot see corrupt content…
	st, err := r.Verify(context.Background(), id, engine.VerifyOptions{ReadPercent: 0})
	if err != nil || len(st.Errors) != 0 {
		t.Fatalf("index check: %+v %v", st, err)
	}
	// …reading every file does, and reports every bad file.
	st, err = r.Verify(context.Background(), id, engine.VerifyOptions{ReadPercent: 100})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("errors: %q", st.Errors)
	if len(st.Errors) == 0 || st.Files != 3 {
		t.Fatalf("corruption not reported: %+v", st)
	}
}

func TestVerifyMissingPack(t *testing.T) {
	repoDir, id := verifyRepo(t)
	for _, p := range packBlobs(t, repoDir) {
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
	}
	r := reopen(t, repoDir)
	st, err := r.Verify(context.Background(), id, engine.VerifyOptions{ReadPercent: 0})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Errors) != 3 || !strings.Contains(strings.Join(st.Errors, "\n"), "missing pack blob") {
		t.Fatalf("missing packs: %+v", st)
	}
}

type failingReader struct{ n int }

func (f *failingReader) Read(p []byte) (int, error) {
	if f.n > 3 {
		return 0, errors.New("dump validation failed")
	}
	f.n++
	return copy(p, bytes.Repeat([]byte("y"), len(p))), nil
}

func TestStreamReadErrorSavesNothing(t *testing.T) {
	r := open(t)
	src := engine.Source{User: "agent", Host: "host-1", Path: "/dumps/broken"}
	_, err := r.SnapshotStream(context.Background(), "dump", &failingReader{}, engine.SnapshotRequest{Source: src})
	if err == nil || !strings.Contains(err.Error(), "dump validation failed") || errors.Is(err, engine.ErrCanceled) {
		t.Fatalf("err = %v", err)
	}
	list, err := r.List(context.Background(), &src, nil)
	if err != nil || len(list) != 0 {
		t.Fatalf("broken stream saved: %v, %v", list, err)
	}
	// A clean stream still works.
	if _, err := r.SnapshotStream(context.Background(), "dump", io.LimitReader(&failingReader{}, 100), engine.SnapshotRequest{Source: src}); err != nil {
		t.Fatal(err)
	}
}
