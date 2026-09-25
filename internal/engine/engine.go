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

// ErrCanceled is returned when a snapshot was cancelled; nothing is saved.
var ErrCanceled = errors.New("engine: snapshot canceled; nothing was saved")

// ErrNotFound is returned for unknown snapshots.
var ErrNotFound = errors.New("engine: snapshot not found")

// Repository is an open session with a backup repository.
type Repository interface {
	// SnapshotPath snapshots a directory tree.
	SnapshotPath(ctx context.Context, dir string, req SnapshotRequest) (*Snapshot, error)
	// SnapshotStream snapshots a stream as a single file named fileName.
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
