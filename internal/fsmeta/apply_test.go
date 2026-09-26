// SPDX-License-Identifier: Apache-2.0

package fsmeta

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"golang.org/x/sys/unix"
)

// copyTree mimics a Kopia restore: content and modes only (hardlinks become
// separate files, no xattrs, directory mtimes = now).
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		out := filepath.Join(dst, rel)
		fi, _ := d.Info()
		switch {
		case d.IsDir():
			return os.MkdirAll(out, fi.Mode().Perm())
		case d.Type()&fs.ModeSymlink != 0:
			l, _ := os.Readlink(p)
			return os.Symlink(l, out)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(out, b, fi.Mode().Perm())
	})
	if err != nil {
		t.Fatal(err)
	}
}

func reader(t *testing.T, data []byte) *Reader {
	t.Helper()
	r, err := NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	return r
}

func TestApplyVerify(t *testing.T) {
	ctx := context.Background()
	src := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(src, "a", "b", "c"), 0o755))
	must(os.WriteFile(filepath.Join(src, "a", "f"), []byte("x"), 0o644))
	must(os.Link(filepath.Join(src, "a", "f"), filepath.Join(src, "a", "b", "g")))
	must(os.Link(filepath.Join(src, "a", "f"), filepath.Join(src, "h")))
	must(os.WriteFile(filepath.Join(src, "plain"), []byte("y"), 0o600))
	must(os.Symlink("plain", filepath.Join(src, "link")))
	xattrOK := unix.Lsetxattr(filepath.Join(src, "plain"), "user.dbr2", []byte("v\x00bin"), 0) == nil
	if xattrOK {
		must(unix.Lsetxattr(filepath.Join(src, "a", "b"), "user.dir", []byte("d"), 0))
	}
	for i, d := range []string{"a/b/c", "a/b", "a", "."} {
		mt := time.Unix(1_600_000_000+int64(i)*1000, 111)
		must(os.Chtimes(filepath.Join(src, d), mt, mt))
	}
	var rec bytes.Buffer
	if _, err := Write(ctx, &rec, src); err != nil {
		t.Fatal(err)
	}

	dst := t.TempDir()
	copyTree(t, src, dst)
	// Before: nothing matches.
	vs, err := Verify(ctx, reader(t, rec.Bytes()), dst)
	if err != nil || vs.Mismatches < 5 {
		t.Fatalf("pre-apply verify %+v %v", vs, err)
	}

	st, err := Apply(ctx, reader(t, rec.Bytes()), dst)
	if err != nil {
		t.Fatal(err)
	}
	if st.Errors != 0 || st.Links != 2 || st.DirTimes != 4 {
		t.Fatalf("apply %+v", st)
	}
	var a, b, h unix.Stat_t
	_ = unix.Lstat(filepath.Join(dst, "a", "f"), &a)
	_ = unix.Lstat(filepath.Join(dst, "a", "b", "g"), &b)
	_ = unix.Lstat(filepath.Join(dst, "h"), &h)
	if a.Ino != b.Ino || a.Ino != h.Ino || a.Nlink != 3 {
		t.Fatalf("hardlinks not recreated: %d %d %d nlink %d", a.Ino, b.Ino, h.Ino, a.Nlink)
	}
	for _, d := range []string{"a/b/c", "a/b", "a", "."} {
		var s, o unix.Stat_t
		_ = unix.Lstat(filepath.Join(dst, d), &s)
		_ = unix.Lstat(filepath.Join(src, d), &o)
		if s.Mtim != o.Mtim {
			t.Fatalf("%s mtime %v want %v", d, s.Mtim, o.Mtim)
		}
	}
	if xattrOK {
		if v, _ := getxattr(filepath.Join(dst, "plain"), "user.dbr2"); string(v) != "v\x00bin" {
			t.Fatalf("xattr %q", v)
		}
		if st.Xattrs < 2 {
			t.Fatalf("xattrs %+v", st)
		}
	} else {
		t.Log("user xattrs unsupported here; xattr assertions skipped")
	}
	vs, err = Verify(ctx, reader(t, rec.Bytes()), dst)
	if err != nil || vs.Mismatches != 0 {
		t.Fatalf("verify %+v %v", vs, err)
	}
	// Idempotent.
	if st, err := Apply(ctx, reader(t, rec.Bytes()), dst); err != nil || st.Errors != 0 || st.Links != 2 {
		t.Fatalf("re-apply %+v %v", st, err)
	}
	// A changed mtime is detected.
	must(os.Chtimes(filepath.Join(dst, "a"), time.Now(), time.Now()))
	if vs, _ := Verify(ctx, reader(t, rec.Bytes()), dst); vs.Mismatches != 1 || !strings.Contains(vs.Samples[0], "mtime") {
		t.Fatalf("verify after change %+v", vs)
	}
}

// record builds a record stream from literal records.
func record(t *testing.T, recs ...Record) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw, _ := zstd.NewWriter(&buf)
	enc := json.NewEncoder(zw)
	_ = enc.Encode(Header{FSMetaVersion: Version, Root: "/x"})
	for _, r := range recs {
		_ = enc.Encode(r)
	}
	_ = zw.Close()
	return buf.Bytes()
}

func TestApplyErrorsCounted(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "f"), nil, 0o600)
	data := record(t,
		Record{P: "../escape", X: map[string][]byte{"user.a": []byte("1")}},
		Record{P: "missing", X: map[string][]byte{"user.a": []byte("1")}},
		Record{P: "f", X: map[string][]byte{"bogus.ns": []byte("1")}},
	)
	st, err := Apply(ctx, reader(t, data), root)
	if err != nil {
		t.Fatal(err)
	}
	if st.Records != 3 || st.Errors != 3 || len(st.Samples) != 3 || !strings.Contains(st.Samples[0], "escapes") {
		t.Fatalf("apply %+v", st)
	}
	vs, err := Verify(ctx, reader(t, data), root)
	if err != nil || vs.Mismatches != 3 {
		t.Fatalf("verify %+v %v", vs, err)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := Apply(cctx, reader(t, data), root); err == nil {
		t.Fatal("want cancellation error")
	}
}

func TestApplySingleFile(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	f := filepath.Join(dir, "f")
	_ = os.WriteFile(f, []byte("z"), 0o640)
	if unix.Lsetxattr(f, "user.k", []byte("v"), 0) != nil {
		t.Skip("user xattrs unsupported here")
	}
	var rec bytes.Buffer
	if _, err := Write(ctx, &rec, f); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "g")
	_ = os.WriteFile(out, []byte("z"), 0o640)
	st, err := Apply(ctx, reader(t, rec.Bytes()), out)
	if err != nil || st.Errors != 0 || st.Xattrs < 1 {
		t.Fatalf("apply %+v %v", st, err)
	}
	if vs, err := Verify(ctx, reader(t, rec.Bytes()), out); err != nil || vs.Mismatches != 0 {
		t.Fatalf("verify %+v %v", vs, err)
	}
}
