// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// configFormatFile marks config snapshots whose staged files and ancestor
// directories carry the source's ownership and mode (format 2). Older
// snapshots recorded the agent (root) as owner of every staged file.
const configFormatFile = "format.json"

type configFormat struct {
	Format         int  `json:"format"`
	FilesOwnership bool `json:"files_ownership"`
}

func writeConfigFormat(stage string) error {
	b, _ := json.Marshal(configFormat{Format: 2, FilesOwnership: true})
	return os.WriteFile(filepath.Join(stage, configFormatFile), b, 0o600)
}

// ownerOf returns a path's numeric owner, group and permission bits.
func ownerOf(p string) (uid, gid uint32, mode os.FileMode, err error) {
	fi, err := os.Lstat(p)
	if err != nil {
		return 0, 0, 0, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, 0, fmt.Errorf("no ownership information for %s", p)
	}
	return st.Uid, st.Gid, fi.Mode(), nil
}

// chownIfRoot applies ownership when the agent can (it runs as root in
// production); an unprivileged agent (tests, development) keeps its own.
func chownIfRoot(p string, uid, gid uint32) error {
	if os.Geteuid() != 0 {
		return nil
	}
	return os.Lchown(p, int(uid), int(gid))
}

// stageAncestors mirrors the ownership, mode and mtime of every ancestor
// directory of src (below /) into <stage>/files, so a restore can recreate
// missing directories as their real owners.
func stageAncestors(stage, src string) error {
	rel := strings.TrimPrefix(filepath.Clean(filepath.Dir(src)), "/")
	if rel == "" || rel == "." {
		return nil
	}
	cur := "/"
	for _, part := range strings.Split(rel, "/") {
		cur = filepath.Join(cur, part)
		uid, gid, mode, err := ownerOf(cur)
		if err != nil {
			return err
		}
		dst := filepath.Join(stage, "files", cur)
		if err := chownIfRoot(dst, uid, gid); err != nil {
			return err
		}
		if err := os.Chmod(dst, mode.Perm()); err != nil {
			return err
		}
	}
	return nil
}

// ensureParents creates the missing directories above target. Each new
// directory gets, in order of preference: the owner and mode recorded in
// the snapshot (exact, format 2 config snapshots; lookup returns ok), or
// the owner of the nearest existing ancestor with mode 0755. It never
// changes existing directories.
func ensureParents(target string, lookup func(dir string) (uid, gid uint32, mode os.FileMode, ok bool)) error {
	dir := filepath.Dir(filepath.Clean(target))
	var missing []string
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Lstat(d); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		missing = append(missing, d)
		if d == "/" {
			break
		}
	}
	for i := len(missing) - 1; i >= 0; i-- {
		d := missing[i]
		puid, pgid, _, err := ownerOf(filepath.Dir(d))
		if err != nil {
			return err
		}
		uid, gid, mode := puid, pgid, os.FileMode(0o755)
		if lookup != nil {
			if u, g, m, ok := lookup(d); ok {
				uid, gid, mode = u, g, m.Perm()
			}
		}
		if err := os.Mkdir(d, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		if err := chownIfRoot(d, uid, gid); err != nil {
			return fmt.Errorf("chown %s: %w", d, err)
		}
		if err := os.Chmod(d, mode); err != nil { // explicit: the agent's umask is 0077
			return err
		}
	}
	return nil
}

func readConfigFormat(rc io.ReadCloser, err error) configFormat {
	var f configFormat
	if err != nil {
		return f
	}
	defer rc.Close()
	_ = json.NewDecoder(rc).Decode(&f)
	return f
}
