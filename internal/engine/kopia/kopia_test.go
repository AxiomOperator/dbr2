// SPDX-License-Identifier: Apache-2.0

package kopia

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AxiomOperator/dbr2/internal/engine"
)

func open(t *testing.T) engine.Repository {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	r, err := InitFilesystem(ctx, filepath.Join(dir, "repo"), filepath.Join(dir, "state"), "test-password-123", "agent", "host-1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(ctx) })
	return r
}

func TestSnapshotRestoreDeepTreeAndTags(t *testing.T) {
	ctx := context.Background()
	r := open(t)
	src := t.TempDir()
	deep := filepath.Join(src, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deep, "f.txt"), []byte("deep"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "top.txt"), []byte("top"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := r.SnapshotPath(ctx, src, engine.SnapshotRequest{
		Source: engine.Source{User: "agent", Host: "host-1", Path: "/volumes/data"},
		Tags:   map[string]string{engine.TagRP: "rp1", engine.TagComponent: "volume:data"},
		Pins:   []string{engine.Pin},
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.Files != 2 || s.Tags[engine.TagRP] != "rp1" || len(s.Pins) != 1 {
		t.Fatalf("snapshot = %+v", s)
	}
	list, err := r.List(ctx, nil, map[string]string{engine.TagRP: "rp1"})
	if err != nil || len(list) != 1 || list[0].ID != s.ID {
		t.Fatalf("list by tag = %v, %v", list, err)
	}
	if none, _ := r.List(ctx, nil, map[string]string{engine.TagRP: "other"}); len(none) != 0 {
		t.Fatalf("unexpected match: %v", none)
	}
	out := t.TempDir()
	if err := r.RestorePath(ctx, s.ID, out, engine.RestoreOptions{}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(out, "a", "b", "c", "f.txt")) // deep restore, not shallow
	if err != nil || string(b) != "deep" {
		t.Fatalf("deep file = %q, %v", b, err)
	}
	fi, err := os.Stat(filepath.Join(out, "top.txt"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, %v", fi, err)
	}
	if err := r.Delete(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Get(ctx, s.ID); !errors.Is(err, engine.ErrNotFound) {
		t.Fatalf("get after delete = %v", err)
	}
}

func TestStreamRoundTrip(t *testing.T) {
	ctx := context.Background()
	r := open(t)
	data := bytes.Repeat([]byte("dump-line\n"), 10000)
	var calls int
	s, err := r.SnapshotStream(ctx, "dump.sql", bytes.NewReader(data), engine.SnapshotRequest{
		Source:     engine.Source{User: "agent", Host: "host-1", Path: "/dumps/db"},
		OnProgress: func(engine.Progress) { calls++ },
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.TotalBytes != int64(len(data)) || calls == 0 {
		t.Fatalf("bytes = %d, progress calls = %d", s.TotalBytes, calls)
	}
	rc, err := r.OpenStream(ctx, s.ID, "dump.sql")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if !bytes.Equal(got, data) {
		t.Fatal("stream content mismatch")
	}
}

type slowReader struct{ n int }

func (s *slowReader) Read(p []byte) (int, error) {
	time.Sleep(10 * time.Millisecond)
	s.n++
	return copy(p, strings.Repeat("x", len(p))), nil
}

func TestCancelSavesNothing(t *testing.T) {
	r := open(t)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	src := engine.Source{User: "agent", Host: "host-1", Path: "/dumps/endless"}
	_, err := r.SnapshotStream(ctx, "endless", &slowReader{}, engine.SnapshotRequest{Source: src})
	if !errors.Is(err, engine.ErrCanceled) {
		t.Fatalf("err = %v, want ErrCanceled", err)
	}
	list, err := r.List(context.Background(), &src, nil)
	if err != nil || len(list) != 0 {
		t.Fatalf("incomplete snapshot saved: %v, %v", list, err)
	}
}

func TestIncrementalUsesPrevious(t *testing.T) {
	ctx := context.Background()
	r := open(t)
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "big"), bytes.Repeat([]byte{1}, 1<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	req := engine.SnapshotRequest{Source: engine.Source{User: "agent", Host: "host-1", Path: "/v"}, Incremental: true}
	if _, err := r.SnapshotPath(ctx, src, req); err != nil {
		t.Fatal(err)
	}
	var last engine.Progress
	req.OnProgress = func(p engine.Progress) { last = p }
	if _, err := r.SnapshotPath(ctx, src, req); err != nil {
		t.Fatal(err)
	}
	if last.HashedBytes != 0 {
		t.Fatalf("second snapshot re-hashed %d bytes; want 0 (hash cache)", last.HashedBytes)
	}
}

func TestOpenFileAndListDir(t *testing.T) {
	ctx := context.Background()
	r := open(t)
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "files", "etc"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "files", "etc", "app.env"), []byte("A=1\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "top"), []byte("t"), 0o600); err != nil {
		t.Fatal(err)
	}
	mt := time.Unix(1_700_000_000, 0)
	_ = os.Chtimes(filepath.Join(src, "top"), mt, mt)
	s, err := r.SnapshotPath(ctx, src, engine.SnapshotRequest{Source: engine.Source{User: "agent", Host: "host-1", Path: "/cfg"}})
	if err != nil {
		t.Fatal(err)
	}
	ents, err := r.ListDir(ctx, s.ID, "")
	if err != nil || len(ents) != 2 || ents[0].Name != "files" || !ents[0].IsDir() || ents[1].Name != "top" ||
		!ents[1].IsRegular() || ents[1].Mode.Perm() != 0o600 || ents[1].Size != 1 || !ents[1].ModTime.Equal(mt) ||
		ents[1].UID != uint32(os.Getuid()) {
		t.Fatalf("root = %+v, %v", ents, err)
	}
	ents, err = r.ListDir(ctx, s.ID, "files/etc")
	if err != nil || len(ents) != 1 || ents[0].Name != "app.env" || ents[0].Mode.Perm() != 0o640 {
		t.Fatalf("files/etc = %+v, %v", ents, err)
	}
	rc, err := r.OpenFile(ctx, s.ID, "files/etc/app.env")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if string(b) != "A=1\n" {
		t.Fatalf("content %q", b)
	}
	if _, err := r.OpenFile(ctx, s.ID, "files/missing"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing: %v", err)
	}
	if _, err := r.OpenFile(ctx, s.ID, "files"); err == nil {
		t.Fatal("opened a directory")
	}
	if _, err := r.ListDir(ctx, s.ID, "top/x"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("below a file: %v", err)
	}
	if _, err := r.ListDir(ctx, "nope", ""); !errors.Is(err, engine.ErrNotFound) {
		t.Fatalf("unknown snapshot: %v", err)
	}
}
