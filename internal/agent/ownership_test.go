// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"os"
	"path/filepath"
	"testing"

	agentv1 "github.com/AxiomOperator/dbr2/internal/agentpb/agent/v1"
)

func TestEnsureParentsCreatesMissingDirectories(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o750); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "a", "b", "file")
	exact := filepath.Join(root, "a")
	lookup := func(d string) (uint32, uint32, os.FileMode, bool) {
		if d == exact {
			return uint32(os.Getuid()), uint32(os.Getgid()), 0o710, true
		}
		return 0, 0, 0, false
	}
	if err := ensureParents(target, lookup); err != nil {
		t.Fatal(err)
	}
	for d, want := range map[string]os.FileMode{exact: 0o710, filepath.Join(root, "a", "b"): 0o755, root: 0o750} {
		_, _, mode, err := ownerOf(d)
		if err != nil || mode.Perm() != want {
			t.Fatalf("%s: mode %v, want %v (%v)", d, mode.Perm(), want, err)
		}
	}
	// Existing directories are left alone.
	if err := ensureParents(target, func(string) (uint32, uint32, os.FileMode, bool) { return 0, 0, 0o700, true }); err != nil {
		t.Fatal(err)
	}
	if _, _, mode, _ := ownerOf(exact); mode.Perm() != 0o710 {
		t.Fatalf("existing directory changed: %v", mode.Perm())
	}
}

// TestConfigRestoreKeepsOwnership reproduces a project directory owned by a
// regular user that was deleted before the restore: the directory and its
// files must come back as that user, not as the (root) agent. It needs root
// to chown; CI and `go test` as a user skip it.
func TestConfigRestoreKeepsOwnership(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to chown")
	}
	const uid, gid = 1234, 2345
	e := newRestoreEnv(t)
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(parent, "planix")
	compose := filepath.Join(project, "deploy", "compose.yaml")
	env := filepath.Join(project, ".env")
	write(t, compose, "services: {}\n", 0o644)
	write(t, env, "A=1\n", 0o600)
	for _, p := range []string{project, filepath.Join(project, "deploy"), compose, env} {
		if err := os.Chown(p, uid, gid); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(project, "deploy"), 0o750); err != nil {
		t.Fatal(err)
	}

	u := e.run(t, snapCmd("rp_own", false, &agentv1.ComponentSpec{Name: "config", Kind: agentv1.ComponentKind_COMPONENT_KIND_CONFIG,
		Path: project, Required: true, Files: []string{compose, env}}))
	succeeded(t, u)
	res := u.GetSnapshotComponents().Components[0]
	if res.Status != "succeeded" {
		t.Fatalf("capture: %s", res.Error)
	}

	if err := os.RemoveAll(project); err != nil {
		t.Fatal(err)
	}
	spec := &agentv1.RestoreSpec{Name: "config", Kind: res.Kind, SnapshotId: res.SnapshotId, OwnerUid: res.OwnerUid,
		OwnerGid: res.OwnerGid, Mode: res.Mode}
	succeeded(t, e.run(t, restoreCmd("rs_own", nil, spec)))

	for p, want := range map[string]os.FileMode{project: 0o755, filepath.Join(project, "deploy"): 0o750, compose: 0o644, env: 0o600} {
		u, g, mode, err := ownerOf(p)
		if err != nil {
			t.Fatal(err)
		}
		if u != uid || g != gid || mode.Perm() != want {
			t.Fatalf("%s: %d:%d %v, want %d:%d %v", p, u, g, mode.Perm(), uid, gid, want)
		}
	}
	if _, _, mode, _ := ownerOf(parent); mode.Perm() != 0o755 {
		t.Fatalf("parent changed: %v", mode.Perm())
	}
}
