// SPDX-License-Identifier: Apache-2.0

package kopia

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"sync"
	"sync/atomic"

	"github.com/kopia/kopia/fs"
	"github.com/kopia/kopia/repo"
	"github.com/kopia/kopia/repo/blob"
	"github.com/kopia/kopia/repo/object"
	"github.com/kopia/kopia/snapshot/snapshotfs"

	"github.com/AxiomOperator/dbr2/internal/engine"
)

// maxVerifyErrors bounds the error list of one verification.
const maxVerifyErrors = 1000

// Verify implements engine.Repository. It follows `kopia snapshot verify`:
// the snapshot tree is walked with snapshotfs.TreeWalker, every object is
// checked with VerifyObject (all its contents are in the index) and, on a
// direct connection, against the pack blob list; the sampled files are read
// completely, which decrypts and hash-checks every content.
func (r *repository) Verify(ctx context.Context, id string, o engine.VerifyOptions) (engine.VerifyStats, error) {
	var st engine.VerifyStats
	m, err := r.load(ctx, id)
	if err != nil {
		return st, err
	}
	if m.RootEntry == nil {
		return st, fmt.Errorf("snapshot %s has no root entry", id)
	}
	var blobs map[blob.ID]blob.Metadata
	if dr, ok := r.rep.(repo.DirectRepository); ok {
		if blobs, err = blob.ReadBlobMap(ctx, dr.BlobReader()); err != nil {
			return st, fmt.Errorf("list blobs: %w", err)
		}
	}
	root, err := snapshotfs.SnapshotRoot(r.rep, m)
	if err != nil {
		return st, err
	}
	par := o.Parallel
	if par <= 0 {
		par = 4
	}
	var dirs, files, filesRead, bytesRead atomic.Int64
	var mu sync.Mutex
	var extra []string // failures of the object checks, with their path
	fail := func(p string, err error) error {
		err = fmt.Errorf("%s: %w", p, err)
		mu.Lock()
		if len(extra) < maxVerifyErrors {
			extra = append(extra, err.Error())
		}
		mu.Unlock()
		return err
	}
	check := func(ctx context.Context, oid object.ID) error {
		cids, err := r.rep.VerifyObject(ctx, oid)
		if err != nil {
			return fmt.Errorf("object %v: %w", oid, err)
		}
		if blobs == nil {
			return nil
		}
		for _, cid := range cids {
			ci, err := r.rep.ContentInfo(ctx, cid)
			if err != nil {
				return fmt.Errorf("object %v: content %v: %w", oid, cid, err)
			}
			if _, ok := blobs[ci.PackBlobID]; !ok {
				return fmt.Errorf("object %v: content %v is in missing pack blob %v", oid, cid, ci.PackBlobID)
			}
		}
		return nil
	}
	tw, err := snapshotfs.NewTreeWalker(ctx, snapshotfs.TreeWalkerOptions{
		Parallelism: par,
		MaxErrors:   maxVerifyErrors,
		EntryCallback: func(ctx context.Context, e fs.Entry, oid object.ID, p string) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err := check(ctx, oid); err != nil {
				return fail(p, err) // a directory is then not descended into
			}
			if e.IsDir() {
				dirs.Add(1)
				return nil
			}
			files.Add(1)
			if !sampled(p, o.ReadPercent) {
				return nil
			}
			rd, err := r.rep.OpenObject(ctx, oid)
			if err != nil {
				return fail(p, fmt.Errorf("open object %v: %w", oid, err))
			}
			n, err := io.Copy(io.Discard, rd)
			rd.Close() //nolint:errcheck
			bytesRead.Add(n)
			if err != nil {
				return fail(p, fmt.Errorf("read object %v: %w", oid, err))
			}
			filesRead.Add(1)
			return nil
		},
	})
	if err != nil {
		return st, err
	}
	defer tw.Close(ctx)
	_ = tw.Process(ctx, root, ".") // errors are collected below
	st.Dirs, st.Files, st.FilesRead, st.BytesRead = dirs.Load(), files.Load(), filesRead.Load(), bytesRead.Load()
	if ctx.Err() != nil {
		return st, ctx.Err()
	}
	// The walker's list holds the callback errors (already in extra) and
	// its own directory-read errors.
	werrs, total := tw.GetErrors()
	st.Errors = extra
	seen := map[string]bool{}
	for _, e := range extra {
		seen[e] = true
	}
	for _, e := range werrs {
		if !seen[e.Error()] && !errors.Is(e, context.Canceled) {
			st.Errors = append(st.Errors, "directory: "+e.Error())
		}
	}
	if total > len(st.Errors) && len(st.Errors) >= maxVerifyErrors {
		st.Errors = append(st.Errors, fmt.Sprintf("… %d errors in total", total))
	}
	return st, nil
}

// sampled selects a deterministic ReadPercent sample by path.
func sampled(p string, percent float64) bool {
	switch {
	case percent >= 100:
		return true
	case percent <= 0:
		return false
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(p))
	return float64(h.Sum32()%10000) < percent*100
}
