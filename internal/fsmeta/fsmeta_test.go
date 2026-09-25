// SPDX-License-Identifier: Apache-2.0

package fsmeta

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"golang.org/x/sys/unix"
)

func readAll(t *testing.T, data []byte) (Header, map[string]Record) {
	t.Helper()
	r, err := NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	out := map[string]Record{}
	for {
		rec, err := r.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		out[rec.P] = rec
	}
	return r.Header(), out
}

func TestRoundTrip(t *testing.T) {
	root := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(root, "a", "b"), 0o755))
	must(os.WriteFile(filepath.Join(root, "a", "f"), []byte("x"), 0o644))
	must(os.Link(filepath.Join(root, "a", "f"), filepath.Join(root, "a", "b", "g")))
	must(os.WriteFile(filepath.Join(root, "plain"), []byte("y"), 0o644))
	must(os.Symlink("/etc", filepath.Join(root, "link")))
	mt := time.Unix(1_700_000_000, 123456789)
	must(os.Chtimes(filepath.Join(root, "a", "b"), mt, mt))

	xattrOK := unix.Lsetxattr(filepath.Join(root, "plain"), "user.dbr2", []byte("v\x00bin"), 0) == nil

	var buf bytes.Buffer
	st, err := Write(context.Background(), &buf, root)
	must(err)
	hdr, recs := readAll(t, buf.Bytes())
	if hdr.FSMetaVersion != 1 || hdr.Root != root || hdr.CreatedAt.IsZero() {
		t.Fatalf("header %+v", hdr)
	}
	for _, d := range []string{".", "a", "a/b"} {
		if recs[d].DM == nil {
			t.Fatalf("no dir mtime for %q: %+v", d, recs)
		}
	}
	if *recs["a/b"].DM != mt.UnixNano() {
		t.Fatalf("dm = %d, want %d", *recs["a/b"].DM, mt.UnixNano())
	}
	if h := recs["a/f"].H; h == "" || h != recs["a/b/g"].H {
		t.Fatalf("hardlink group %q vs %q", h, recs["a/b/g"].H)
	}
	if _, ok := recs["link"]; ok && recs["link"].DM != nil {
		t.Fatal("symlink followed")
	}
	if xattrOK {
		if string(recs["plain"].X["user.dbr2"]) != "v\x00bin" {
			t.Fatalf("xattr %+v", recs["plain"])
		}
	} else if _, ok := recs["plain"]; ok && len(recs["plain"].X["user.dbr2"]) > 0 {
		t.Fatal("unexpected xattr")
	}
	if st.Records != int64(len(recs)) || st.Paths < 6 {
		t.Fatalf("stats %+v, %d records", st, len(recs))
	}
	if !xattrOK {
		t.Log("user xattrs unsupported here; xattr assertions skipped")
	}
}

func TestSingleFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "f")
	_ = os.WriteFile(f, nil, 0o600)
	_ = os.Link(f, filepath.Join(dir, "g"))
	var buf bytes.Buffer
	if _, err := Write(context.Background(), &buf, f); err != nil {
		t.Fatal(err)
	}
	_, recs := readAll(t, buf.Bytes())
	if len(recs) != 1 || recs["."].H == "" || recs["."].DM != nil {
		t.Fatalf("records %+v", recs)
	}
	sock := filepath.Join(dir, "s")
	if err := unix.Mknod(sock, unix.S_IFIFO|0o600, 0); err != nil {
		t.Skip("mknod fifo:", err)
	}
	if _, err := Write(context.Background(), io.Discard, sock); err == nil {
		t.Fatal("want error for a fifo")
	}
}

func TestCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Write(ctx, io.Discard, t.TempDir()); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestRejectsFutureVersion(t *testing.T) {
	var buf bytes.Buffer
	zw, _ := zstd.NewWriter(&buf)
	_, _ = zw.Write([]byte(`{"fsmeta_version":2,"root":"/x"}` + "\n"))
	_ = zw.Close()
	if _, err := NewReader(&buf); err == nil {
		t.Fatal("want error for version 2")
	}
	if _, err := NewReader(bytes.NewReader([]byte("not zstd"))); err == nil {
		t.Fatal("want error for garbage")
	}
}
