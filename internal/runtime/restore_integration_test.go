// SPDX-License-Identifier: Apache-2.0

//go:build integration

package runtime

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func docker(t *testing.T, args ...string) string {
	t.Helper()
	// Output (stdout only): pull progress goes to stderr.
	out, err := exec.Command("docker", args...).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, ee.Stderr)
		}
		t.Fatalf("docker %s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out))
}

// TestDockerRestoreControl exercises the Phase 5 runtime operations on a
// real engine: volumes, networks, recreate from an inspect document,
// exec with stdin, copy-to-container, logs and health.
func TestDockerRestoreControl(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI not available (used only to set up the fixture)")
	}
	const (
		name = "dbr2test-restore-src"
		vol  = "dbr2test-restore-vol"
		net  = "dbr2test-restore-net"
	)
	cleanup := func() {
		_ = exec.Command("docker", "rm", "-f", name).Run()
		_ = exec.Command("docker", "volume", "rm", "-f", vol).Run()
		_ = exec.Command("docker", "network", "rm", net).Run()
	}
	cleanup()
	t.Cleanup(cleanup)
	rt, err := NewDocker("")
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// Volumes.
	if _, err := rt.InspectVolume(ctx, vol); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing volume: %v", err)
	}
	v, err := rt.CreateVolume(ctx, vol, "local", map[string]string{"dbr2.test": "1"})
	if err != nil || v.Mountpoint == "" || v.Labels["dbr2.test"] != "1" {
		t.Fatalf("create volume %+v %v", v, err)
	}
	if v2, err := rt.InspectVolume(ctx, vol); err != nil || v2.Mountpoint != v.Mountpoint {
		t.Fatalf("inspect volume %+v %v", v2, err)
	}
	// Networks.
	if ok, err := rt.NetworkExists(ctx, net); ok || err != nil {
		t.Fatalf("missing network: %v %v", ok, err)
	}
	if _, err := rt.CreateNetwork(ctx, NetworkSpec{Name: net, Driver: "bridge", Labels: map[string]string{"dbr2.test": "1"}}); err != nil {
		t.Fatal(err)
	}
	if ok, err := rt.NetworkExists(ctx, net); !ok || err != nil {
		t.Fatalf("network: %v %v", ok, err)
	}

	// A container with a bind mount, a volume, a network and a healthcheck.
	src, dst := t.TempDir(), t.TempDir()
	_ = os.WriteFile(filepath.Join(src, "f"), []byte("old-host"), 0o644)
	_ = os.WriteFile(filepath.Join(dst, "f"), []byte("new-host"), 0o644)
	id := docker(t, "run", "-d", "--name", name, "--network", net, "--network-alias", "web",
		"-v", src+":/data:z", "-v", vol+":/vol", "-e", "SECRET=real-value", // gitleaks:allow (test fixture)
		"--health-cmd", "true", "--health-interval", "1s", "--health-start-period", "0s",
		"docker.io/library/alpine:3.22", "sh", "-c", "echo hello-log; sleep 3600")
	raw, err := rt.InspectRaw(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	waitHealthy := func(id string) ContainerDetails {
		t.Helper()
		for i := 0; ; i++ {
			d, err := rt.InspectDetails(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if d.Health == "healthy" {
				return d
			}
			if i > 60 {
				t.Fatalf("not healthy: %+v", d)
			}
			time.Sleep(500 * time.Millisecond)
		}
	}
	d := waitHealthy(id)
	if d.Name != name || !d.Running || d.State != "running" || !strings.Contains(strings.Join(d.Env, " "), "SECRET=real-value") {
		t.Fatalf("details %+v", d)
	}
	if logs, err := rt.Logs(ctx, id, 50); err != nil || !strings.Contains(logs, "hello-log") {
		t.Fatalf("logs %q %v", logs, err)
	}

	// Exec with streamed stdin.
	in := bytes.Repeat([]byte("0123456789abcdef"), 1<<16) // 1 MiB
	r, err := rt.ExecInput(ctx, id, []string{"sh", "-c", "cat > /tmp/in && wc -c < /tmp/in"}, bytes.NewReader(in), 1024)
	if err != nil || r.ExitCode != 0 || strings.TrimSpace(string(r.Output)) != "1048576" {
		t.Fatalf("exec input %+v %v", r, err)
	}
	r, err = rt.ExecInput(ctx, id, []string{"sh", "-c", "cat >/dev/null; exit 3"}, strings.NewReader("x"), 1024)
	if err != nil || r.ExitCode != 3 {
		t.Fatalf("exec exit %+v %v", r, err)
	}
	// Copy into the container.
	var tb bytes.Buffer
	tw := tar.NewWriter(&tb)
	_ = tw.WriteHeader(&tar.Header{Name: "copied.txt", Mode: 0o640, Size: 6, Uid: 123, Gid: 456, Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte("copied"))
	_ = tw.Close()
	if err := rt.CopyToContainer(ctx, id, "/vol", &tb); err != nil {
		t.Fatal(err)
	}
	if r, err := rt.Exec(ctx, id, []string{"stat", "-c", "%u:%g:%a", "/vol/copied.txt"}, 256); err != nil || strings.TrimSpace(string(r.Output)) != "123:456:640" {
		t.Fatalf("copied %+v %v", r, err)
	}

	// Recreate from the inspect document with the bind remapped.
	if err := rt.RemoveContainer(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.InspectDetails(ctx, name); !errors.Is(err, ErrNotFound) {
		t.Fatalf("removed container: %v", err)
	}
	spec, err := ContainerSpecFromInspect(raw, func(p string) string {
		if p == src {
			return dst
		}
		return p
	})
	if err != nil || spec.Name != name || !spec.WasRunning || len(spec.Networks) != 1 || spec.Networks[0] != net {
		t.Fatalf("spec %+v %v", spec, err)
	}
	nid, err := rt.CreateContainer(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := rt.InspectState(ctx, nid); s.State != "created" {
		t.Fatalf("recreated state %+v", s)
	}
	if err := rt.Start(ctx, nid); err != nil {
		t.Fatal(err)
	}
	waitHealthy(nid)
	for cmd, want := range map[string]string{"cat /data/f": "new-host", "cat /vol/copied.txt": "copied", "echo $SECRET": "real-value"} {
		r, err := rt.Exec(ctx, nid, []string{"sh", "-c", cmd}, 1024)
		if err != nil || strings.TrimSpace(string(r.Output)) != want {
			t.Fatalf("%s: %q %v", cmd, r.Output, err)
		}
	}
	nraw, _ := rt.InspectRaw(ctx, nid)
	if !strings.Contains(string(nraw), `"web"`) {
		t.Fatal("network alias lost")
	}
	if err := rt.RemoveContainer(ctx, nid); err != nil {
		t.Fatal(err)
	}
	if err := rt.RemoveNetwork(ctx, net); err != nil {
		t.Fatal(err)
	}
	if err := rt.RemoveVolume(ctx, vol); err != nil {
		t.Fatal(err)
	}
	if err := rt.RemoveVolume(ctx, vol); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second remove: %v", err)
	}
}

// TestDockerImages pulls by digest, verifies and tags.
func TestDockerImages(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI not available")
	}
	const tag = "dbr2test/restore-image:pinned"
	t.Cleanup(func() { _ = exec.Command("docker", "rmi", tag).Run() })
	rt, err := NewDocker("")
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := rt.PullImage(ctx, "docker.io/library/busybox:1.37"); err != nil {
		t.Fatal(err)
	}
	info, err := rt.InspectImage(ctx, "busybox:1.37")
	if err != nil || len(info.RepoDigests) == 0 {
		t.Fatalf("inspect %+v %v", info, err)
	}
	pinned := info.RepoDigests[0]
	byDigest, err := rt.InspectImage(ctx, pinned)
	if err != nil || byDigest.ID != info.ID {
		t.Fatalf("by digest %+v %v", byDigest, err)
	}
	if err := rt.TagImage(ctx, info.ID, tag); err != nil {
		t.Fatal(err)
	}
	if tagged, err := rt.InspectImage(ctx, tag); err != nil || tagged.ID != info.ID {
		t.Fatalf("tagged %+v %v", tagged, err)
	}
	if _, err := rt.InspectImage(ctx, "dbr2test/does-not-exist:1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing image: %v", err)
	}
	if err := rt.PullImage(ctx, "docker.io/library/busybox@sha256:0000000000000000000000000000000000000000000000000000000000000000"); err == nil {
		t.Fatal("pull of an unknown digest succeeded")
	}
}
