// Package engine is the spike's thin wrapper around Kopia's public Go packages.
//
// It is a reference for the Phase-1 `internal/engine/kopia` package (ADR-0007):
// every Kopia import used by the spike lives here, and the cmd/ programs only
// call these helpers. Only public (non-`internal/`) Kopia packages are used.
package engine

import (
	"context"
	"io"
	"math"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/pkg/errors"

	"github.com/kopia/kopia/fs"
	"github.com/kopia/kopia/fs/localfs"
	"github.com/kopia/kopia/fs/virtualfs"
	"github.com/kopia/kopia/repo"
	"github.com/kopia/kopia/repo/blob/filesystem"
	"github.com/kopia/kopia/repo/content"
	"github.com/kopia/kopia/repo/manifest"
	"github.com/kopia/kopia/repo/object"
	"github.com/kopia/kopia/snapshot"
	"github.com/kopia/kopia/snapshot/policy"
	"github.com/kopia/kopia/snapshot/restore"
	"github.com/kopia/kopia/snapshot/snapshotfs"
	"github.com/kopia/kopia/snapshot/upload"
)

// TagPrefix is the label prefix Kopia's CLI uses for user tags (`--tags k:v` => label "tag:k").
// Using the same prefix keeps DBR² tags visible to a stock `kopia snapshot list`.
const TagPrefix = "tag:"

// Tags converts DBR² tags (e.g. {"dbr2-rp": "rp_01..."}) into Kopia manifest labels.
func Tags(kv map[string]string) map[string]string {
	out := make(map[string]string, len(kv))
	for k, v := range kv {
		out[TagPrefix+k] = v
	}

	return out
}

// ---------------------------------------------------------------------------
// Repository lifecycle
// ---------------------------------------------------------------------------

// InitFilesystemRepo creates (if needed) and connects a filesystem-backed repository,
// writing the Kopia client config to configFile.
func InitFilesystemRepo(ctx context.Context, repoDir, configFile, cacheDir, password, user, host string) error {
	st, err := filesystem.New(ctx, &filesystem.Options{Path: repoDir}, true)
	if err != nil {
		return errors.Wrap(err, "filesystem storage")
	}
	defer st.Close(ctx) //nolint:errcheck

	if err := repo.Initialize(ctx, st, &repo.NewRepositoryOptions{}, password); err != nil && !errors.Is(err, repo.ErrAlreadyInitialized) {
		return errors.Wrap(err, "initialize")
	}

	return repo.Connect(ctx, configFile, st, password, &repo.ConnectOptions{
		ClientOptions:  repo.ClientOptions{Username: user, Hostname: host},
		CachingOptions: content.CachingOptions{CacheDirectory: cacheDir},
	})
}

// ConnectRepoServer connects to a Kopia Repository Server as user@host (ADR-0002).
// password is the *server user's* password, not the repository password.
func ConnectRepoServer(ctx context.Context, url, certSHA256, configFile, cacheDir, password, user, host string) error {
	return repo.ConnectAPIServer(ctx, configFile, &repo.APIServerInfo{
		BaseURL:                             url,
		TrustedServerCertificateFingerprint: certSHA256,
	}, password, &repo.ConnectOptions{
		ClientOptions:  repo.ClientOptions{Username: user, Hostname: host},
		CachingOptions: content.CachingOptions{CacheDirectory: cacheDir},
	})
}

// Open opens a previously connected repository (direct or API-server).
func Open(ctx context.Context, configFile, password string) (repo.Repository, error) {
	return repo.Open(ctx, configFile, password, &repo.Options{})
}

// ---------------------------------------------------------------------------
// Progress
// ---------------------------------------------------------------------------

// Progress is a minimal upload.Progress implementation that counts bytes and
// invokes OnTick (e.g. to feed a Temporal heartbeat) at most every Interval.
type Progress struct {
	upload.NullUploadProgress

	Interval time.Duration
	OnTick   func(hashed, uploaded, files int64)

	hashed, uploaded, files atomic.Int64
	last                    atomic.Int64
}

func (p *Progress) Enabled() bool { return true }

func (p *Progress) HashedBytes(n int64)        { p.hashed.Add(n); p.maybeTick() }
func (p *Progress) UploadedBytes(n int64)      { p.uploaded.Add(n); p.maybeTick() }
func (p *Progress) CachedFile(string, int64)   { p.files.Add(1); p.maybeTick() }
func (p *Progress) FinishedFile(string, error) { p.files.Add(1); p.maybeTick() }

// Totals returns counters so far.
func (p *Progress) Totals() (hashed, uploaded, files int64) {
	return p.hashed.Load(), p.uploaded.Load(), p.files.Load()
}

func (p *Progress) maybeTick() {
	if p.OnTick == nil {
		return
	}

	now := time.Now().UnixNano()
	last := p.last.Load()

	if now-last < int64(p.Interval) || !p.last.CompareAndSwap(last, now) {
		return
	}

	p.OnTick(p.Totals())
}

// ---------------------------------------------------------------------------
// Snapshot creation
// ---------------------------------------------------------------------------

// SnapshotOptions configures one component snapshot.
type SnapshotOptions struct {
	Source snapshot.SourceInfo // user@host:path identity of the snapshot source
	Tags   map[string]string   // already label-prefixed (see Tags)
	// Pins protect a snapshot from Kopia's own retention (policy.ApplyRetentionPolicy
	// skips pinned snapshots). Removing a pin needs FULL access on the manifest.
	Pins     []string
	Progress upload.Progress // optional
	// Previous enables Kopia's "hash cache": unchanged files (same size+mtime)
	// are not re-read. Pass nil to force a full read/hash.
	Previous []*snapshot.Manifest
}

// ErrCanceled is returned when a snapshot was aborted via context cancellation.
var ErrCanceled = errors.New("snapshot canceled")

// snapshotEntry uploads an fs.Entry inside a write session and saves the manifest.
// Context cancellation is bridged to Uploader.Cancel(): the uploader itself only
// polls its own cancel flag, it does not watch ctx.Done().
func snapshotEntry(ctx context.Context, rep repo.Repository, entry fs.Entry, opt SnapshotOptions) (*snapshot.Manifest, error) {
	var result *snapshot.Manifest

	wso := repo.WriteSessionOptions{Purpose: "dbr2-snapshot"}
	if opt.Progress != nil {
		// Bytes actually written to the repository (pack blobs in direct
		// mode; WriteContent payloads sent to the server in API-server mode).
		wso.OnUpload = opt.Progress.UploadedBytes
	}

	err := repo.WriteSession(ctx, rep, wso,
		func(wctx context.Context, w repo.RepositoryWriter) error {
			pt, err := policy.TreeForSource(wctx, w, opt.Source)
			if err != nil {
				return errors.Wrap(err, "policy tree")
			}

			u := upload.NewUploader(w)
			if opt.Progress != nil {
				u.Progress = opt.Progress
			}

			stop := context.AfterFunc(ctx, u.Cancel)
			defer stop()

			man, err := u.Upload(wctx, entry, pt, opt.Source, opt.Previous...)
			if err != nil {
				return errors.Wrap(err, "upload")
			}

			if man.IncompleteReason != "" || ctx.Err() != nil {
				// Do not persist an incomplete snapshot; returning an error
				// makes WriteSession skip the flush.
				return errors.Wrapf(ErrCanceled, "incomplete: %q", man.IncompleteReason)
			}

			man.Tags = opt.Tags
			man.Pins = opt.Pins

			if _, err := snapshot.SaveSnapshot(wctx, w, man); err != nil {
				return errors.Wrap(err, "save snapshot")
			}

			result = man

			return nil
		})

	return result, err
}

// SnapshotDir snapshots a local directory (volume, bind mount, staged config).
func SnapshotDir(ctx context.Context, rep repo.Repository, dir string, opt SnapshotOptions) (*snapshot.Manifest, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}

	entry, err := localfs.Directory(abs)
	if err != nil {
		return nil, errors.Wrap(err, "localfs")
	}

	return snapshotEntry(ctx, rep, entry, opt)
}

// SnapshotStream snapshots an io.Reader (e.g. pg_dump stdout) as a single file
// named fileName inside a virtual root directory - exactly what
// `kopia snapshot create --stdin-file` does.
func SnapshotStream(ctx context.Context, rep repo.Repository, fileName string, r io.Reader, opt SnapshotOptions) (*snapshot.Manifest, error) {
	root := virtualfs.NewStaticDirectory("stream", []fs.Entry{
		virtualfs.StreamingFileFromReader(fileName, io.NopCloser(r)),
	})

	return snapshotEntry(ctx, rep, root, opt)
}

// ---------------------------------------------------------------------------
// Listing / deletion
// ---------------------------------------------------------------------------

// ListByTags returns snapshot manifests whose labels include all given labels
// (use Tags() to build them). src may be nil for "all sources I can see".
func ListByTags(ctx context.Context, rep repo.Repository, src *snapshot.SourceInfo, labels map[string]string) ([]*snapshot.Manifest, error) {
	ids, err := snapshot.ListSnapshotManifests(ctx, rep, src, labels)
	if err != nil {
		return nil, err
	}

	return snapshot.LoadSnapshots(ctx, rep, ids)
}

// DeleteSnapshot deletes a snapshot manifest (content is reclaimed later by maintenance/GC).
func DeleteSnapshot(ctx context.Context, rep repo.Repository, id manifest.ID) error {
	return repo.WriteSession(ctx, rep, repo.WriteSessionOptions{Purpose: "dbr2-delete"},
		func(wctx context.Context, w repo.RepositoryWriter) error {
			return w.DeleteManifest(wctx, id)
		})
}

// ---------------------------------------------------------------------------
// Restore
// ---------------------------------------------------------------------------

// RestoreToDir restores a snapshot into targetDir.
func RestoreToDir(ctx context.Context, rep repo.Repository, man *snapshot.Manifest, targetDir string) (restore.Stats, error) {
	root, err := snapshotfs.SnapshotRoot(rep, man)
	if err != nil {
		return restore.Stats{}, err
	}

	out := &restore.FilesystemOutput{
		TargetPath:           targetDir,
		OverwriteDirectories: true,
		OverwriteFiles:       true,
		SkipOwners:           true, // spike runs unprivileged
	}
	if err := out.Init(ctx); err != nil {
		return restore.Stats{}, err
	}

	cancel := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { close(cancel) })
	defer stop()

	return restore.Entry(ctx, rep, out, root, restore.Options{
		Parallel: 4,
		Cancel:   cancel,
		// Zero value means "shallow restore at depth 0" (writes .kopia-entry
		// placeholders)! The CLI default is MaxInt32 = full restore.
		RestoreDirEntryAtDepth: math.MaxInt32,
	})
}

// OpenStream returns a reader over the single file stored by SnapshotStream.
func OpenStream(ctx context.Context, rep repo.Repository, man *snapshot.Manifest, fileName string) (io.ReadCloser, error) {
	root, err := snapshotfs.SnapshotRoot(rep, man)
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
		return nil, errors.Errorf("%s is not a file", fileName)
	}

	return f.Open(ctx)
}

// LoadSnapshot loads one snapshot manifest by ID.
func LoadSnapshot(ctx context.Context, rep repo.Repository, id manifest.ID) (*snapshot.Manifest, error) {
	return snapshot.LoadSnapshot(ctx, rep, id)
}

// ReadObject reads a whole object (file or directory listing) by object ID,
// bypassing snapshot manifests entirely.
func ReadObject(ctx context.Context, rep repo.Repository, oid string) ([]byte, error) {
	id, err := object.ParseID(oid)
	if err != nil {
		return nil, err
	}

	r, err := rep.OpenObject(ctx, id)
	if err != nil {
		return nil, err
	}
	defer r.Close() //nolint:errcheck

	return io.ReadAll(r)
}

// PutRawSnapshotManifest writes a snapshot manifest as-is (used to probe
// whether a client can forge a manifest for someone else's source).
func PutRawSnapshotManifest(ctx context.Context, rep repo.Repository, man *snapshot.Manifest) error {
	return repo.WriteSession(ctx, rep, repo.WriteSessionOptions{Purpose: "probe"},
		func(wctx context.Context, w repo.RepositoryWriter) error {
			_, err := snapshot.SaveSnapshot(wctx, w, man)
			return err
		})
}

// SetPolicy writes a policy for a source.
func SetPolicy(ctx context.Context, rep repo.Repository, si snapshot.SourceInfo, pol *policy.Policy) error {
	return repo.WriteSession(ctx, rep, repo.WriteSessionOptions{Purpose: "set-policy"},
		func(wctx context.Context, w repo.RepositoryWriter) error {
			return policy.SetPolicy(wctx, w, si, pol)
		})
}

// ServerSideRetention asks a repository *server* to apply the retention policy
// for one of the caller's own sources (what `kopia snapshot create` does after
// every snapshot). The server executes deletions with its own privileges.
func ServerSideRetention(ctx context.Context, rep repo.Repository, path string, reallyDelete bool) ([]manifest.ID, error) {
	var ids []manifest.ID

	err := repo.WriteSession(ctx, rep, repo.WriteSessionOptions{Purpose: "retention"},
		func(wctx context.Context, w repo.RepositoryWriter) error {
			rr, ok := w.(repo.RemoteRetentionPolicy)
			if !ok {
				return errors.New("not a repository-server client")
			}

			var err error
			ids, err = rr.ApplyRetentionPolicy(wctx, path, reallyDelete)

			return err
		})

	return ids, err
}

// FindManifests exposes raw label search (e.g. type=user, type=acl probes).
func FindManifests(ctx context.Context, rep repo.Repository, labels map[string]string) ([]*manifest.EntryMetadata, error) {
	return rep.FindManifests(ctx, labels)
}

// PutRawManifest writes an arbitrary manifest (probe for acl/user tampering).
func PutRawManifest(ctx context.Context, rep repo.Repository, labels map[string]string, payload any) error {
	return repo.WriteSession(ctx, rep, repo.WriteSessionOptions{Purpose: "probe"},
		func(wctx context.Context, w repo.RepositoryWriter) error {
			_, err := w.PutManifest(wctx, labels, payload)
			return err
		})
}

// IsDirect reports whether rep is a direct (storage-level) connection. Only
// direct connections can run maintenance (snapshotmaintenance.Run takes a
// repo.DirectRepositoryWriter).
func IsDirect(rep repo.Repository) bool {
	_, ok := rep.(repo.DirectRepository)
	return ok
}
