// SPDX-License-Identifier: Apache-2.0

// Package engine is DBR²'s backup-engine abstraction (ADR-0007, ADR-0012
// terminology). The only implementation is internal/engine/kopia, which is
// the only package allowed to import Kopia; everything else uses these
// types so the engine can be replaced without touching the rest of DBR².
package engine

import (
	"context"
	"errors"
	"io"
	"os"
	"time"
)

// Tag keys (ADR-0004 amendment: hyphenated so the stock kopia CLI can
// filter on them, e.g. `kopia snapshot list --tags dbr2-rp:<id>`).
const (
	TagRP        = "dbr2-rp"
	TagApp       = "dbr2-app"
	TagComponent = "dbr2-component"
	TagKind      = "dbr2-kind"
)

// Pin is set on every DBR² snapshot so Kopia retention never deletes it
// (ADR-0002: DBR² deletes whole recovery points itself).
const Pin = "dbr2"

// Source identifies a snapshot source: user@host:path. For agents the user
// is "agent" and the host is the agent ID (per-agent Kopia identity).
type Source struct {
	User string
	Host string
	Path string
}

// String renders user@host:path.
func (s Source) String() string { return s.User + "@" + s.Host + ":" + s.Path }

// Snapshot describes a stored snapshot.
type Snapshot struct {
	ID           string
	RootObjectID string
	Source       Source
	Tags         map[string]string
	Pins         []string
	Description  string
	StartTime    time.Time
	EndTime      time.Time
	TotalBytes   int64
	Files        int64
	Dirs         int64
	Errors       int64
	Incomplete   string
}

// Progress reports upload progress.
type Progress struct {
	HashedBytes   int64
	UploadedBytes int64
	Files         int64
}

// SnapshotRequest configures one snapshot.
type SnapshotRequest struct {
	Source      Source
	Tags        map[string]string
	Pins        []string
	Description string
	// OnProgress is called at most every ProgressInterval.
	OnProgress       func(Progress)
	ProgressInterval time.Duration
	// Incremental uses the latest snapshot of the same source as the
	// hash-cache base: unchanged files (size + mtime) are not re-read.
	Incremental bool
}

// RestoreOptions tune a restore.
type RestoreOptions struct {
	// IgnorePermissionErrors must be false in DBR² (ADR-0006): a failed
	// chown/chmod fails the restore instead of silently widening access.
	IgnorePermissionErrors bool
	// WriteSparseFiles preserves sparse files (ADR-0006).
	WriteSparseFiles bool
	Parallel         int
}

// DirEntry is one entry of a snapshot directory.
type DirEntry struct {
	Name string
	// Mode carries the type bits (os.ModeDir, os.ModeSymlink, …) and the
	// permission bits including setuid/setgid/sticky.
	Mode    os.FileMode
	Size    int64
	ModTime time.Time
	UID     uint32
	GID     uint32
}

// IsDir reports whether the entry is a directory.
func (e DirEntry) IsDir() bool { return e.Mode.IsDir() }

// IsRegular reports whether the entry is a regular file.
func (e DirEntry) IsRegular() bool { return e.Mode.IsRegular() }

// VerifyOptions tune a snapshot verification.
type VerifyOptions struct {
	// ReadPercent (0–100) of the files whose content is fully read and
	// thus hash-verified. The sample is deterministic (by path); 100 reads
	// every file. Every other object is checked against the repository
	// index (and, for direct repository connections, the pack blob list).
	ReadPercent float64
	// Parallel is the number of tree walkers (0 = default).
	Parallel int
}

// VerifyStats is the outcome of a verification. Objects are counted once:
// identical files or directories (same object) are verified once.
type VerifyStats struct {
	Dirs      int64
	Files     int64
	FilesRead int64
	BytesRead int64
	// Errors lists every problem found (missing or corrupt objects,
	// unreadable directories); empty means the snapshot verified.
	Errors []string
}

// ErrCanceled is returned when a snapshot was cancelled; nothing is saved.
var ErrCanceled = errors.New("engine: snapshot canceled; nothing was saved")

// ErrNotFound is returned for unknown snapshots.
var ErrNotFound = errors.New("engine: snapshot not found")

// Repository is an open session with a backup repository.
type Repository interface {
	// SnapshotPath snapshots a directory tree.
	SnapshotPath(ctx context.Context, dir string, req SnapshotRequest) (*Snapshot, error)
	// SnapshotStream snapshots a stream as a single file named fileName. A
	// read error of r fails the snapshot and nothing is saved, so a reader
	// can abort (e.g. a failed dump validation) by returning an error.
	SnapshotStream(ctx context.Context, fileName string, r io.Reader, req SnapshotRequest) (*Snapshot, error)
	// List returns snapshots matching the source (nil = all visible) and tags.
	List(ctx context.Context, src *Source, tags map[string]string) ([]Snapshot, error)
	// Get loads one snapshot.
	Get(ctx context.Context, id string) (*Snapshot, error)
	// Delete deletes a snapshot manifest (content is reclaimed by maintenance).
	Delete(ctx context.Context, id string) error
	// RestorePath restores a snapshot into targetDir.
	RestorePath(ctx context.Context, id, targetDir string, opts RestoreOptions) error
	// OpenStream opens the single file of a stream snapshot.
	OpenStream(ctx context.Context, id, fileName string) (io.ReadCloser, error)
	// OpenFile opens the regular file at relPath (slash-separated, relative
	// to the snapshot root) of a directory snapshot.
	OpenFile(ctx context.Context, id, relPath string) (io.ReadCloser, error)
	// ListDir lists the directory at relPath ("" or "." = the root).
	ListDir(ctx context.Context, id, relPath string) ([]DirEntry, error)
	// Verify walks the whole snapshot tree, checks that every referenced
	// object exists and reads opts.ReadPercent of the files. Problems are
	// reported in VerifyStats.Errors; the error is non-nil only when the
	// verification could not run (unknown snapshot, cancellation).
	Verify(ctx context.Context, snapshotID string, opts VerifyOptions) (VerifyStats, error)
	// Close releases the session.
	Close(ctx context.Context) error
}

// ServerConnection describes a repository-server connection (ADR-0002).
type ServerConnection struct {
	// URL of the repository server, e.g. https://dbr2.example.lan:51515.
	URL string
	// CertSHA256 pins the server certificate (hex SHA-256 of its DER).
	CertSHA256 string
	// User and Host form the Kopia identity user@host.
	User string
	Host string
	// Password of the Kopia server user (not the repository password).
	Password string
	// StateDir holds the client config and cache for this connection.
	StateDir string
}
