// SPDX-License-Identifier: Apache-2.0

package fsmeta

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/sys/unix"
)

// maxSamples bounds the error/mismatch messages kept in stats.
const maxSamples = 20

// ApplyStats summarizes an Apply.
type ApplyStats struct {
	Records  int64 // records read
	Links    int64 // hardlinks recreated
	Xattrs   int64 // extended attributes set
	DirTimes int64 // directory mtimes set
	// Errors counts entries that could not be applied (unsupported xattr
	// namespaces, missing paths, …); Samples keeps the first messages.
	Errors  int64
	Samples []string
}

func (s *ApplyStats) fail(msg string) {
	s.Errors++
	if len(s.Samples) < maxSamples {
		s.Samples = append(s.Samples, msg)
	}
}

// localPath maps a record path to a path under root, refusing anything
// that would escape it.
func localPath(root, p string) (string, error) {
	if p == "." {
		return root, nil
	}
	if !filepath.IsLocal(p) {
		return "", fmt.Errorf("record path %q escapes the root", p)
	}
	return filepath.Join(root, p), nil
}

// Apply restores the record read from r onto root (ADR-0006 restore
// sequence steps 2-5): hardlink groups are recreated (the first path of a
// group is kept, later ones are replaced by links to it), extended
// attributes (ACLs, SELinux, user.*, trusted.*) are set without following
// symlinks, and directory mtimes are applied last, deepest first. Per-entry
// failures are counted in Errors; the error is non-nil only for a broken
// record stream or cancellation.
func Apply(ctx context.Context, r *Reader, root string) (ApplyStats, error) {
	var st ApplyStats
	groups := map[string]string{} // hardlink group → first path
	type dirTime struct {
		path string
		ns   int64
	}
	var dirs []dirTime
	for {
		if err := ctx.Err(); err != nil {
			return st, err
		}
		rec, err := r.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return st, fmt.Errorf("fsmeta record: %w", err)
		}
		st.Records++
		path, err := localPath(root, rec.P)
		if err != nil {
			st.fail(err.Error())
			continue
		}
		if rec.H != "" {
			if first, ok := groups[rec.H]; !ok {
				groups[rec.H] = path
			} else if err := relink(first, path); err != nil {
				st.fail(fmt.Sprintf("%s: hardlink: %v", rec.P, err))
			} else {
				st.Links++
			}
		}
		for _, name := range sortedNames(rec.X) {
			if err := unix.Lsetxattr(path, name, rec.X[name], 0); err != nil {
				st.fail(fmt.Sprintf("%s: set %s: %v", rec.P, name, err))
				continue
			}
			st.Xattrs++
		}
		if rec.DM != nil {
			dirs = append(dirs, dirTime{path, *rec.DM})
		}
	}
	// Deepest first, so setting a parent's time is the last change to it.
	sort.SliceStable(dirs, func(i, j int) bool {
		return strings.Count(dirs[i].path, string(filepath.Separator)) > strings.Count(dirs[j].path, string(filepath.Separator))
	})
	for _, d := range dirs {
		ts := unix.NsecToTimespec(d.ns)
		if err := unix.UtimesNanoAt(unix.AT_FDCWD, d.path, []unix.Timespec{ts, ts}, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			st.fail(fmt.Sprintf("%s: set mtime: %v", d.path, err))
			continue
		}
		st.DirTimes++
	}
	return st, nil
}

// relink replaces path with a hardlink to first (atomically via rename).
func relink(first, path string) error {
	var a, b unix.Stat_t
	if err := unix.Lstat(first, &a); err != nil {
		return err
	}
	if err := unix.Lstat(path, &b); err == nil && a.Dev == b.Dev && a.Ino == b.Ino {
		return nil // already linked
	}
	if a.Mode&unix.S_IFMT == unix.S_IFDIR {
		return errors.New("group leader is a directory")
	}
	tmp := filepath.Join(filepath.Dir(path), ".dbr2-link-"+filepath.Base(path))
	_ = os.Remove(tmp)
	if err := os.Link(first, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func sortedNames(x map[string][]byte) []string {
	names := make([]string, 0, len(x))
	for n := range x {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// VerifyStats summarizes a Verify.
type VerifyStats struct {
	Records    int64
	Mismatches int64
	Samples    []string
}

func (s *VerifyStats) mismatch(msg string) {
	s.Mismatches++
	if len(s.Samples) < maxSamples {
		s.Samples = append(s.Samples, msg)
	}
}

// Verify re-reads root and counts every recorded attribute that does not
// match: extended attributes (value equality), hardlink groups (same
// inode as the group's first path) and directory mtimes. Attributes present
// on disk but absent from the record are not mismatches.
func Verify(ctx context.Context, r *Reader, root string) (VerifyStats, error) {
	var st VerifyStats
	groups := map[string]unix.Stat_t{}
	for {
		if err := ctx.Err(); err != nil {
			return st, err
		}
		rec, err := r.Next()
		if errors.Is(err, io.EOF) {
			return st, nil
		}
		if err != nil {
			return st, fmt.Errorf("fsmeta record: %w", err)
		}
		st.Records++
		path, err := localPath(root, rec.P)
		if err != nil {
			st.mismatch(err.Error())
			continue
		}
		var s unix.Stat_t
		if err := unix.Lstat(path, &s); err != nil {
			st.mismatch(fmt.Sprintf("%s: %v", rec.P, err))
			continue
		}
		if rec.H != "" {
			if first, ok := groups[rec.H]; !ok {
				groups[rec.H] = s
			} else if first.Dev != s.Dev || first.Ino != s.Ino {
				st.mismatch(rec.P + ": hardlink not restored")
			}
		}
		for _, name := range sortedNames(rec.X) {
			v, err := getxattr(path, name)
			if err != nil {
				st.mismatch(fmt.Sprintf("%s: %s: %v", rec.P, name, err))
				continue
			}
			if !bytes.Equal(trimNUL(name, v), trimNUL(name, rec.X[name])) {
				st.mismatch(fmt.Sprintf("%s: %s differs", rec.P, name))
			}
		}
		if rec.DM != nil && s.Mtim.Nano() != *rec.DM {
			st.mismatch(rec.P + ": directory mtime differs")
		}
	}
}

// trimNUL ignores the optional trailing NUL of an SELinux label.
func trimNUL(name string, v []byte) []byte {
	if name == "security.selinux" {
		return bytes.TrimRight(v, "\x00")
	}
	return v
}
