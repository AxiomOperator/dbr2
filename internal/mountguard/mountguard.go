// SPDX-License-Identifier: Apache-2.0

// Package mountguard protects DBR² Repositories stored on NFS (ADR-0002
// storage-safety amendment). The Kopia spike showed that creating a
// repository on an unmounted mount point silently writes to the local disk,
// and that a `hard` NFS mount blocks forever when the server disappears. The
// guard therefore refuses to use a path unless it is an active mount of the
// expected type holding the matching sentinel file, and a watchdog reports a
// stalled mount instead of hanging the process.
package mountguard

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// SentinelName is the file identifying the Repository stored at a path.
const SentinelName = ".dbr2-repository-id"

// Errors reported by Check.
var (
	ErrNotMountPoint = errors.New("path is not a mount point (is the NFS share mounted?)")
	ErrWrongFSType   = errors.New("mount has an unexpected filesystem type")
	ErrNoSentinel    = errors.New("repository sentinel file missing")
	ErrWrongSentinel = errors.New("repository sentinel does not match the configured repository ID")
	ErrNotEmpty      = errors.New("refusing to initialize: directory is not empty and has no sentinel")
	ErrStalled       = errors.New("storage did not respond before the watchdog timeout (stalled mount?)")
)

// Mount is one entry of /proc/self/mountinfo.
type Mount struct {
	MountPoint string
	FSType     string
	Source     string
}

// ParseMountInfo parses the mountinfo format (proc(5)).
func ParseMountInfo(r io.Reader) ([]Mount, error) {
	var out []Mount
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		sep := -1
		for i, f := range fields {
			if f == "-" {
				sep = i
				break
			}
		}
		if sep < 5 || len(fields) < sep+3 {
			continue
		}
		out = append(out, Mount{MountPoint: unescape(fields[4]), FSType: fields[sep+1], Source: unescape(fields[sep+2])})
	}
	return out, sc.Err()
}

// unescape decodes the octal escapes mountinfo uses for spaces etc.
func unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			var v int
			if _, err := fmt.Sscanf(s[i+1:i+4], "%03o", &v); err == nil {
				b.WriteByte(byte(v))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// Guard validates a repository path.
type Guard struct {
	Path              string
	ExpectFSType      string // "" or "any" = any filesystem (dev mock)
	RequireMountPoint bool
	RepositoryID      string
	// MountInfo returns the mount table (defaults to /proc/self/mountinfo).
	MountInfo func() ([]Mount, error)
}

func (g *Guard) mounts() ([]Mount, error) {
	if g.MountInfo != nil {
		return g.MountInfo()
	}
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ParseMountInfo(f)
}

// CheckMount verifies the mount point and filesystem type.
func (g *Guard) CheckMount() (*Mount, error) {
	path := filepath.Clean(g.Path)
	ms, err := g.mounts()
	if err != nil {
		return nil, err
	}
	var hit *Mount
	for i := range ms {
		if ms[i].MountPoint == path {
			hit = &ms[i] // last entry wins (overmounts)
		}
	}
	if hit == nil {
		if g.RequireMountPoint {
			return nil, fmt.Errorf("%s: %w", path, ErrNotMountPoint)
		}
		return nil, nil
	}
	if g.ExpectFSType != "" && g.ExpectFSType != "any" && hit.FSType != g.ExpectFSType {
		return hit, fmt.Errorf("%s is %s, want %s: %w", path, hit.FSType, g.ExpectFSType, ErrWrongFSType)
	}
	return hit, nil
}

// Check verifies mount, type and sentinel. It is the gate before any
// repository create/connect.
func (g *Guard) Check() error {
	if _, err := g.CheckMount(); err != nil {
		return err
	}
	b, err := os.ReadFile(filepath.Join(g.Path, SentinelName))
	if errors.Is(err, os.ErrNotExist) {
		return ErrNoSentinel
	}
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(b)) != g.RepositoryID {
		return fmt.Errorf("found %q: %w", strings.TrimSpace(string(b)), ErrWrongSentinel)
	}
	return nil
}

// Initialize writes the sentinel on an empty, correctly mounted path. It
// never overwrites an existing sentinel and never initializes a non-empty
// directory.
func (g *Guard) Initialize() error {
	if _, err := g.CheckMount(); err != nil {
		return err
	}
	switch err := g.Check(); {
	case err == nil:
		return nil // already initialized for this repository
	case !errors.Is(err, ErrNoSentinel):
		return err
	}
	entries, err := os.ReadDir(g.Path)
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		return ErrNotEmpty
	}
	f, err := os.OpenFile(filepath.Join(g.Path, SentinelName), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(f, g.RepositoryID); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Watchdog periodically re-checks the path with a timeout, in a separate
// goroutine, because a stalled `hard` NFS mount blocks instead of erroring.
type Watchdog struct {
	Guard    *Guard
	Interval time.Duration
	Timeout  time.Duration
	// OnChange is called when health changes (nil error = healthy).
	OnChange func(err error)

	healthy atomic.Bool
	lastErr atomic.Pointer[string]
}

// Healthy reports the last observed state.
func (w *Watchdog) Healthy() (bool, string) {
	msg := ""
	if p := w.lastErr.Load(); p != nil {
		msg = *p
	}
	return w.healthy.Load(), msg
}

// Probe runs one check bounded by Timeout.
func (w *Watchdog) Probe() error {
	done := make(chan error, 1)
	go func() { done <- w.Guard.Check() }() // may leak while the mount is stalled; bounded by one in flight below
	select {
	case err := <-done:
		return err
	case <-time.After(w.Timeout):
		return ErrStalled
	}
}

// Run probes until ctx is done. Only one probe is in flight at a time so a
// stalled mount does not accumulate blocked goroutines.
func (w *Watchdog) Run(ctx context.Context) {
	var inFlight atomic.Bool
	t := time.NewTicker(w.Interval)
	defer t.Stop()
	for {
		if inFlight.CompareAndSwap(false, true) {
			err := w.Probe()
			if !errors.Is(err, ErrStalled) {
				inFlight.Store(false)
			} else {
				go func() { _ = w.Guard.Check(); inFlight.Store(false) }()
			}
			w.record(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (w *Watchdog) record(err error) {
	was := w.healthy.Load()
	now := err == nil
	w.healthy.Store(now)
	if err != nil {
		s := err.Error()
		w.lastErr.Store(&s)
	} else {
		w.lastErr.Store(nil)
	}
	if was != now && w.OnChange != nil {
		w.OnChange(err)
	}
}
