// SPDX-License-Identifier: Apache-2.0

// Package fsmeta captures the filesystem metadata Kopia does not keep
// (ADR-0006): extended attributes (user.*, security.* incl. the full SELinux
// context, system.posix_acl_*, trusted.* when root), hardlink groups and
// directory mtimes. The record is zstd-compressed JSON Lines: a Header line,
// then one Record per path that has something to restore.
package fsmeta

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
	"golang.org/x/sys/unix"
)

// Version is the record format version.
const Version = 1

// FileName is the stream file name inside the fsmeta snapshot.
const FileName = "fsmeta.jsonl.zst"

// Header is the first line of a record.
type Header struct {
	FSMetaVersion int       `json:"fsmeta_version"`
	Root          string    `json:"root"`
	CreatedAt     time.Time `json:"created_at"`
}

// Record is the metadata of one path, relative to the root ("." = root).
type Record struct {
	P string `json:"p"`
	// X maps extended attribute names to raw values (base64 in JSON).
	X map[string][]byte `json:"x,omitempty"`
	// H is the hardlink group ("dev:ino") of a non-directory with nlink > 1.
	H string `json:"h,omitempty"`
	// DM is a directory's mtime in Unix nanoseconds.
	DM *int64 `json:"dm,omitempty"`
	// E lists errors reading this path's metadata (not fatal).
	E string `json:"e,omitempty"`
}

// Stats summarizes a Write.
type Stats struct {
	Paths   int64 // paths visited
	Records int64 // records written
	Errors  int64 // records carrying an error
}

// Write walks root (no symlink following, one filesystem) and writes the
// compressed record to w. root may also be a single regular file (a
// file bind mount): the record then holds just that file as ".".
func Write(ctx context.Context, w io.Writer, root string) (Stats, error) {
	var st Stats
	root, err := filepath.Abs(root)
	if err != nil {
		return st, err
	}
	var rs unix.Stat_t
	if err := unix.Lstat(root, &rs); err != nil {
		return st, fmt.Errorf("stat %s: %w", root, err)
	}
	switch rs.Mode & unix.S_IFMT {
	case unix.S_IFDIR, unix.S_IFREG:
	default:
		return st, fmt.Errorf("%s is neither a directory nor a regular file", root)
	}
	zw, err := zstd.NewWriter(w)
	if err != nil {
		return st, err
	}
	enc := json.NewEncoder(zw)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(Header{FSMetaVersion: Version, Root: root, CreatedAt: time.Now().UTC()}); err != nil {
		zw.Close()
		return st, err
	}
	werr := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		st.Paths++
		rec := Record{P: rel}
		var errs []string
		if walkErr != nil {
			errs = append(errs, walkErr.Error())
		}
		var s unix.Stat_t
		if err := unix.Lstat(path, &s); err != nil {
			errs = append(errs, "lstat: "+err.Error())
		} else {
			isDir := s.Mode&unix.S_IFMT == unix.S_IFDIR
			if isDir && s.Dev != rs.Dev {
				st.Paths--
				return fs.SkipDir // another filesystem
			}
			if isDir {
				dm := s.Mtim.Nano()
				rec.DM = &dm
			} else if s.Nlink > 1 {
				rec.H = fmt.Sprintf("%d:%d", s.Dev, s.Ino)
			}
			x, xerrs := xattrs(path)
			rec.X = x
			errs = append(errs, xerrs...)
		}
		if len(errs) > 0 {
			rec.E = strings.Join(errs, "; ")
			st.Errors++
		}
		if rec.DM != nil || rec.H != "" || len(rec.X) > 0 || rec.E != "" {
			if err := enc.Encode(rec); err != nil {
				return err
			}
			st.Records++
		}
		if walkErr != nil && d != nil && d.IsDir() {
			return fs.SkipDir
		}
		return nil
	})
	if werr != nil {
		zw.Close()
		return st, werr
	}
	return st, zw.Close()
}

// xattrs reads every readable extended attribute of path (not following a
// symlink). Unsupported filesystems yield no attributes and no error.
func xattrs(path string) (map[string][]byte, []string) {
	names, err := listxattr(path)
	if err != nil {
		if errors.Is(err, unix.ENOTSUP) {
			return nil, nil
		}
		return nil, []string{"listxattr: " + err.Error()}
	}
	var out map[string][]byte
	var errs []string
	for _, n := range names {
		v, err := getxattr(path, n)
		if err != nil {
			if errors.Is(err, unix.ENODATA) {
				continue // removed meanwhile
			}
			errs = append(errs, n+": "+err.Error())
			continue
		}
		if out == nil {
			out = map[string][]byte{}
		}
		out[n] = v
	}
	return out, errs
}

func listxattr(path string) ([]string, error) {
	for {
		n, err := unix.Llistxattr(path, nil)
		if err != nil || n == 0 {
			return nil, err
		}
		buf := make([]byte, n)
		n, err = unix.Llistxattr(path, buf)
		if errors.Is(err, unix.ERANGE) {
			continue // grew meanwhile
		}
		if err != nil {
			return nil, err
		}
		var names []string
		for _, s := range strings.Split(string(buf[:n]), "\x00") {
			if s != "" {
				names = append(names, s)
			}
		}
		return names, nil
	}
}

func getxattr(path, name string) ([]byte, error) {
	for {
		n, err := unix.Lgetxattr(path, name, nil)
		if err != nil {
			return nil, err
		}
		buf := make([]byte, n)
		n, err = unix.Lgetxattr(path, name, buf)
		if errors.Is(err, unix.ERANGE) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return buf[:n], nil
	}
}

// SELinuxContext returns path's security.selinux label without the trailing
// NUL, or "" when the filesystem or host has none.
func SELinuxContext(path string) string {
	v, err := getxattr(path, "security.selinux")
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(v), "\x00")
}

// Reader iterates a record written by Write.
type Reader struct {
	zr  *zstd.Decoder
	dec *json.Decoder
	hdr Header
}

// NewReader reads and checks the header.
func NewReader(r io.Reader) (*Reader, error) {
	zr, err := zstd.NewReader(r)
	if err != nil {
		return nil, err
	}
	rd := &Reader{zr: zr, dec: json.NewDecoder(zr)}
	if err := rd.dec.Decode(&rd.hdr); err != nil {
		zr.Close()
		return nil, fmt.Errorf("fsmeta header: %w", err)
	}
	if rd.hdr.FSMetaVersion < 1 || rd.hdr.FSMetaVersion > Version {
		zr.Close()
		return nil, fmt.Errorf("unsupported fsmeta_version %d", rd.hdr.FSMetaVersion)
	}
	return rd, nil
}

// Header returns the record header.
func (r *Reader) Header() Header { return r.hdr }

// Next returns the next record, or io.EOF.
func (r *Reader) Next() (Record, error) {
	var rec Record
	if err := r.dec.Decode(&rec); err != nil {
		return Record{}, err
	}
	return rec, nil
}

// Close releases the decoder.
func (r *Reader) Close() { r.zr.Close() }
