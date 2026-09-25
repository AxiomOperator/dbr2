// SPDX-License-Identifier: Apache-2.0

package mountguard

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const sample = `22 1 0:21 / / rw,relatime shared:1 - btrfs /dev/nvme0n1p3 rw
36 22 0:33 / /mnt/dbr2-repo rw,relatime shared:9 - nfs4 nas.example.lan:/export/dbr2 rw,vers=4.2,hard,timeo=600
37 22 0:34 / /mnt/with\040space rw - ext4 /dev/sdb1 rw
`

func TestParseMountInfo(t *testing.T) {
	ms, err := ParseMountInfo(strings.NewReader(sample))
	if err != nil || len(ms) != 3 {
		t.Fatalf("parse: %v %+v", err, ms)
	}
	if ms[1].MountPoint != "/mnt/dbr2-repo" || ms[1].FSType != "nfs4" || ms[1].Source != "nas.example.lan:/export/dbr2" {
		t.Errorf("nfs entry: %+v", ms[1])
	}
	if ms[2].MountPoint != "/mnt/with space" {
		t.Errorf("escape: %q", ms[2].MountPoint)
	}
}

func guardAt(t *testing.T, fstype string, mounted bool) *Guard {
	dir := t.TempDir()
	return &Guard{Path: dir, ExpectFSType: "nfs4", RequireMountPoint: true, RepositoryID: "repo-1",
		MountInfo: func() ([]Mount, error) {
			if !mounted {
				return []Mount{{MountPoint: "/", FSType: "btrfs"}}, nil
			}
			return []Mount{{MountPoint: dir, FSType: fstype}}, nil
		}}
}

func TestRefusesUnmountedPath(t *testing.T) {
	// The spike's failure mode: an unmounted mount point is a plain local dir.
	g := guardAt(t, "", false)
	if err := g.Initialize(); !errors.Is(err, ErrNotMountPoint) {
		t.Fatalf("Initialize on unmounted path: %v", err)
	}
	if err := g.Check(); !errors.Is(err, ErrNotMountPoint) {
		t.Fatalf("Check: %v", err)
	}
}

func TestRefusesWrongFSType(t *testing.T) {
	g := guardAt(t, "xfs", true)
	if err := g.Initialize(); !errors.Is(err, ErrWrongFSType) {
		t.Fatalf("got %v", err)
	}
}

func TestInitializeAndSentinel(t *testing.T) {
	g := guardAt(t, "nfs4", true)
	if err := g.Check(); !errors.Is(err, ErrNoSentinel) {
		t.Fatalf("fresh: %v", err)
	}
	if err := g.Initialize(); err != nil {
		t.Fatal(err)
	}
	if err := g.Check(); err != nil {
		t.Fatalf("after init: %v", err)
	}
	if err := g.Initialize(); err != nil {
		t.Fatalf("idempotent init: %v", err)
	}
	other := *g
	other.RepositoryID = "repo-2"
	if err := other.Check(); !errors.Is(err, ErrWrongSentinel) {
		t.Fatalf("wrong repo: %v", err)
	}
	if err := other.Initialize(); !errors.Is(err, ErrWrongSentinel) {
		t.Fatalf("must not re-initialize another repository's storage: %v", err)
	}
}

func TestRefusesNonEmptyDirWithoutSentinel(t *testing.T) {
	g := guardAt(t, "nfs4", true)
	_ = os.WriteFile(filepath.Join(g.Path, "existing-data"), []byte("x"), 0o600)
	if err := g.Initialize(); !errors.Is(err, ErrNotEmpty) {
		t.Fatalf("got %v", err)
	}
}

func TestDevMockAcceptsAnyFilesystem(t *testing.T) {
	dir := t.TempDir()
	g := &Guard{Path: dir, RepositoryID: "dev", RequireMountPoint: false,
		MountInfo: func() ([]Mount, error) { return nil, nil }}
	if err := g.Initialize(); err != nil {
		t.Fatal(err)
	}
	if err := g.Check(); err != nil {
		t.Fatal(err)
	}
}

func TestWatchdogReportsStall(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	g := &Guard{Path: t.TempDir(), RepositoryID: "r", MountInfo: func() ([]Mount, error) {
		<-block // simulate a hung hard NFS mount
		return nil, nil
	}}
	w := &Watchdog{Guard: g, Interval: time.Hour, Timeout: 50 * time.Millisecond}
	if err := w.Probe(); !errors.Is(err, ErrStalled) {
		t.Fatalf("expected stall, got %v", err)
	}
}
