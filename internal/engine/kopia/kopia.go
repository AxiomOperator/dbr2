// SPDX-License-Identifier: Apache-2.0

// Package kopia implements engine.Repository with Kopia v0.23.1 used as a
// Go library (ADR-0007). It is the ONLY package in DBR² that imports Kopia.
// Spike pitfalls handled here (spikes/kopia-library/RESULTS.md):
//   - restores are shallow by default → RestoreDirEntryAtDepth = MaxInt32;
//   - the uploader ignores ctx cancellation → bridged to Uploader.Cancel();
//   - incomplete manifests are never saved;
//   - tags are stored as "tag:<key>" labels, keys hyphenated.
package kopia

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"
	"time"

	"github.com/kopia/kopia/fs"
	"github.com/kopia/kopia/fs/localfs"
	"github.com/kopia/kopia/fs/virtualfs"
	"github.com/kopia/kopia/repo"
	"github.com/kopia/kopia/repo/blob/filesystem"
	"github.com/kopia/kopia/repo/content"
	"github.com/kopia/kopia/repo/manifest"
	"github.com/kopia/kopia/snapshot"
	"github.com/kopia/kopia/snapshot/policy"
	"github.com/kopia/kopia/snapshot/restore"
	"github.com/kopia/kopia/snapshot/snapshotfs"
	"github.com/kopia/kopia/snapshot/upload"

	"github.com/AxiomOperator/dbr2/internal/engine"
)

// Version is the pinned Kopia version (see go.mod).
const Version = "0.23.1"

const tagPrefix = "tag:"

type repository struct {
	rep repo.Repository
}

// ConnectServer connects to a Kopia repository server as c.User@c.Host and
// opens a session. The server certificate is pinned by fingerprint.
func ConnectServer(ctx context.Context, c engine.ServerConnection) (engine.Repository, error) {
	if err := os.MkdirAll(c.StateDir, 0o700); err != nil {
		return nil, err
	}
	cfg := filepath.Join(c.StateDir, "kopia.config")
	if err := repo.ConnectAPIServer(ctx, cfg, &repo.APIServerInfo{
		BaseURL: c.URL, TrustedServerCertificateFingerprint: c.CertSHA256,
	}, c.Password, &repo.ConnectOptions{
		ClientOptions:  repo.ClientOptions{Username: c.User, Hostname: c.Host},
		CachingOptions: content.CachingOptions{CacheDirectory: filepath.Join(c.StateDir, "cache")},
	}); err != nil {
		return nil, fmt.Errorf("connect repository server %s as %s@%s: %w", c.URL, c.User, c.Host, err)
	}
	rep, err := repo.Open(ctx, cfg, c.Password, &repo.Options{})
	if err != nil {
		return nil, err
	}
	return &repository{rep: rep}, nil
}

// InitFilesystem creates (if needed) and opens a direct filesystem
// repository. Used by tests and by tooling; production agents and the worker
// always go through the repository server.
func InitFilesystem(ctx context.Context, repoDir, stateDir, password, user, host string) (engine.Repository, error) {
	st, err := filesystem.New(ctx, &filesystem.Options{Path: repoDir}, true)
	if err != nil {
		return nil, err
	}
	defer st.Close(ctx) //nolint:errcheck
	if err := repo.Initialize(ctx, st, &repo.NewRepositoryOptions{}, password); err != nil && !errors.Is(err, repo.ErrAlreadyInitialized) {
		return nil, err
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, err
	}
	cfg := filepath.Join(stateDir, "kopia.config")
	if err := repo.Connect(ctx, cfg, st, password, &repo.ConnectOptions{
		ClientOptions:  repo.ClientOptions{Username: user, Hostname: host},
		CachingOptions: content.CachingOptions{CacheDirectory: filepath.Join(stateDir, "cache")},
	}); err != nil {
		return nil, err
	}
	rep, err := repo.Open(ctx, cfg, password, &repo.Options{})
	if err != nil {
		return nil, err
	}
	return &repository{rep: rep}, nil
}

func (r *repository) Close(ctx context.Context) error { return r.rep.Close(ctx) }

type progress struct {
	upload.NullUploadProgress
	interval                time.Duration
	on                      func(engine.Progress)
	hashed, uploaded, files atomic.Int64
	last                    atomic.Int64
}

func (p *progress) Enabled() bool              { return true }
func (p *progress) HashedBytes(n int64)        { p.hashed.Add(n); p.tick() }
func (p *progress) UploadedBytes(n int64)      { p.uploaded.Add(n); p.tick() }
func (p *progress) CachedFile(string, int64)   { p.files.Add(1); p.tick() }
func (p *progress) FinishedFile(string, error) { p.files.Add(1); p.tick() }
func (p *progress) snapshot() engine.Progress {
	return engine.Progress{HashedBytes: p.hashed.Load(), UploadedBytes: p.uploaded.Load(), Files: p.files.Load()}
}
func (p *progress) tick() {
	if p.on == nil {
		return
	}
	now, last := time.Now().UnixNano(), p.last.Load()
	if now-last < int64(p.interval) || !p.last.CompareAndSwap(last, now) {
		return
	}
	p.on(p.snapshot())
}

func sourceInfo(s engine.Source) snapshot.SourceInfo {
	return snapshot.SourceInfo{UserName: s.User, Host: s.Host, Path: s.Path}
}

func (r *repository) snapshotEntry(ctx context.Context, entry fs.Entry, req engine.SnapshotRequest) (*engine.Snapshot, error) {
	si := sourceInfo(req.Source)
	var prev []*snapshot.Manifest
	if req.Incremental {
		if ms, err := snapshot.ListSnapshots(ctx, r.rep, si); err == nil && len(ms) > 0 {
			sort.Slice(ms, func(i, j int) bool { return ms[i].StartTime.After(ms[j].StartTime) })
			for _, m := range ms {
				if m.IncompleteReason == "" {
					prev = []*snapshot.Manifest{m}
					break
				}
			}
		}
	}
	p := &progress{interval: req.ProgressInterval, on: req.OnProgress}
	if p.interval == 0 {
		p.interval = 5 * time.Second
	}
	var result *snapshot.Manifest
	err := repo.WriteSession(ctx, r.rep, repo.WriteSessionOptions{Purpose: "dbr2-snapshot", OnUpload: p.UploadedBytes},
		func(wctx context.Context, w repo.RepositoryWriter) error {
			pt, err := policy.TreeForSource(wctx, w, si)
			if err != nil {
				return fmt.Errorf("policy tree: %w", err)
			}
			u := upload.NewUploader(w)
			u.Progress = p
			stop := context.AfterFunc(ctx, u.Cancel) // the uploader does not watch ctx
			defer stop()
			man, err := u.Upload(wctx, entry, pt, si, prev...)
			if err != nil {
				if ctx.Err() != nil {
					return fmt.Errorf("%w: %w", engine.ErrCanceled, ctx.Err())
				}
				return fmt.Errorf("upload: %w", err)
			}
			if man.IncompleteReason != "" || ctx.Err() != nil {
				return fmt.Errorf("%w (%s)", engine.ErrCanceled, man.IncompleteReason)
			}
			man.Tags = labels(req.Tags)
			man.Pins = req.Pins
			man.Description = req.Description
			if _, err := snapshot.SaveSnapshot(wctx, w, man); err != nil {
				return fmt.Errorf("save snapshot: %w", err)
			}
			result = man
			return nil
		})
	if err != nil {
		return nil, err
	}
	if req.OnProgress != nil {
		req.OnProgress(p.snapshot())
	}
	return toSnapshot(result), nil
}

func labels(tags map[string]string) map[string]string {
	out := make(map[string]string, len(tags))
	for k, v := range tags {
		out[tagPrefix+k] = v
	}
	return out
}

func toSnapshot(m *snapshot.Manifest) *engine.Snapshot {
	tags := map[string]string{}
	for k, v := range m.Tags {
		if len(k) > len(tagPrefix) && k[:len(tagPrefix)] == tagPrefix {
			tags[k[len(tagPrefix):]] = v
		}
	}
	s := &engine.Snapshot{ID: string(m.ID), Source: engine.Source{User: m.Source.UserName, Host: m.Source.Host, Path: m.Source.Path},
		Tags: tags, Pins: m.Pins, Description: m.Description, StartTime: m.StartTime.ToTime(), EndTime: m.EndTime.ToTime(),
		Incomplete: m.IncompleteReason}
	if m.RootEntry != nil {
		s.RootObjectID = m.RootEntry.ObjectID.String()
		if m.RootEntry.DirSummary != nil {
			s.TotalBytes = m.RootEntry.DirSummary.TotalFileSize
			s.Files = m.RootEntry.DirSummary.TotalFileCount
			s.Dirs = m.RootEntry.DirSummary.TotalDirCount
			s.Errors = int64(m.RootEntry.DirSummary.FatalErrorCount)
		} else {
			s.TotalBytes, s.Files = m.RootEntry.FileSize, 1
		}
	}
	return s
}

func (r *repository) SnapshotPath(ctx context.Context, dir string, req engine.SnapshotRequest) (*engine.Snapshot, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	entry, err := localfs.Directory(abs)
	if err != nil {
		return nil, err
	}
	return r.snapshotEntry(ctx, entry, req)
}

func (r *repository) SnapshotStream(ctx context.Context, fileName string, rd io.Reader, req engine.SnapshotRequest) (*engine.Snapshot, error) {
	root := virtualfs.NewStaticDirectory("stream", []fs.Entry{virtualfs.StreamingFileFromReader(fileName, io.NopCloser(rd))})
	return r.snapshotEntry(ctx, root, req)
}

func (r *repository) List(ctx context.Context, src *engine.Source, tags map[string]string) ([]engine.Snapshot, error) {
	var si *snapshot.SourceInfo
	if src != nil {
		s := sourceInfo(*src)
		si = &s
	}
	ids, err := snapshot.ListSnapshotManifests(ctx, r.rep, si, labels(tags))
	if err != nil {
		return nil, err
	}
	ms, err := snapshot.LoadSnapshots(ctx, r.rep, ids)
	if err != nil {
		return nil, err
	}
	out := make([]engine.Snapshot, 0, len(ms))
	for _, m := range ms {
		out = append(out, *toSnapshot(m))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartTime.Before(out[j].StartTime) })
	return out, nil
}

func (r *repository) load(ctx context.Context, id string) (*snapshot.Manifest, error) {
	m, err := snapshot.LoadSnapshot(ctx, r.rep, manifest.ID(id))
	if errors.Is(err, snapshot.ErrSnapshotNotFound) {
		return nil, engine.ErrNotFound
	}
	return m, err
}

func (r *repository) Get(ctx context.Context, id string) (*engine.Snapshot, error) {
	m, err := r.load(ctx, id)
	if err != nil {
		return nil, err
	}
	return toSnapshot(m), nil
}

func (r *repository) Delete(ctx context.Context, id string) error {
	return repo.WriteSession(ctx, r.rep, repo.WriteSessionOptions{Purpose: "dbr2-delete"},
		func(wctx context.Context, w repo.RepositoryWriter) error {
			return w.DeleteManifest(wctx, manifest.ID(id))
		})
}

func (r *repository) RestorePath(ctx context.Context, id, targetDir string, o engine.RestoreOptions) error {
	m, err := r.load(ctx, id)
	if err != nil {
		return err
	}
	root, err := snapshotfs.SnapshotRoot(r.rep, m)
	if err != nil {
		return err
	}
	out := &restore.FilesystemOutput{TargetPath: targetDir, OverwriteDirectories: true, OverwriteFiles: true,
		IgnorePermissionErrors: o.IgnorePermissionErrors, WriteSparseFiles: o.WriteSparseFiles}
	if err := out.Init(ctx); err != nil {
		return err
	}
	cancel := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { close(cancel) })
	defer stop()
	par := o.Parallel
	if par <= 0 {
		par = 4
	}
	st, err := restore.Entry(ctx, r.rep, out, root, restore.Options{
		Parallel: par, Cancel: cancel,
		RestoreDirEntryAtDepth: math.MaxInt32, // default 0 = shallow restore (spike pitfall)
	})
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	_ = st
	return nil
}

func (r *repository) OpenStream(ctx context.Context, id, fileName string) (io.ReadCloser, error) {
	m, err := r.load(ctx, id)
	if err != nil {
		return nil, err
	}
	root, err := snapshotfs.SnapshotRoot(r.rep, m)
	if err != nil {
		return nil, err
	}
	dir, ok := root.(fs.Directory)
	if !ok {
		return nil, errors.New("stream snapshot root is not a directory")
	}
	child, err := dir.Child(ctx, fileName)
	if err != nil {
		return nil, err
	}
	f, ok := child.(fs.File)
	if !ok {
		return nil, fmt.Errorf("%s is not a file", fileName)
	}
	return f.Open(ctx)
}
