// SPDX-License-Identifier: Apache-2.0

//go:build integration

package runtime

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"
)

type failWriter struct{ n int }

func (f *failWriter) Write(p []byte) (int, error) {
	if f.n += len(p); f.n > 1<<20 {
		return 0, errors.New("sink full")
	}
	return len(p), nil
}

// TestDockerDumpControl exercises ExecOutput and CopyFromContainer (Phase 8).
func TestDockerDumpControl(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI not available (used only to set up the fixture)")
	}
	const name = "dbr2test-dumpctl"
	_ = exec.Command("docker", "rm", "-f", name).Run()
	out, err := exec.Command("docker", "run", "-d", "--name", name, "docker.io/library/alpine:3.22", "sh", "-c",
		"mkdir -p /d/sub && echo one > /d/a && echo two > /d/sub/b && sleep 3600").Output()
	if err != nil {
		t.Fatalf("docker run: %v\n%s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() })
	id := strings.TrimSpace(string(out))
	rt, err := NewDocker("")
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// stdout is streamed separately from stderr; exit codes are reported.
	var stdout bytes.Buffer
	r, err := rt.ExecOutput(ctx, id, []string{"sh", "-c", "head -c 3000000 /dev/zero; echo oops >&2; exit 3"}, &stdout, 1024)
	if err != nil || r.ExitCode != 3 || stdout.Len() != 3000000 || strings.TrimSpace(string(r.Output)) != "oops" {
		t.Fatalf("exec output %d bytes, %+v, %v", stdout.Len(), r, err)
	}
	// A failing sink aborts the exec.
	if _, err := rt.ExecOutput(ctx, id, []string{"sh", "-c", "head -c 10000000 /dev/zero"}, &failWriter{}, 1024); err == nil {
		t.Fatal("sink error not reported")
	}

	// A directory copy holds the tree under its base name.
	for {
		if res, _ := rt.Exec(ctx, id, []string{"test", "-f", "/d/sub/b"}, 64); res.ExitCode == 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	rc, err := rt.CopyFromContainer(ctx, id, "/d")
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	tr := tar.NewReader(rc)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		files[h.Name] = string(b)
	}
	rc.Close()
	if files["d/a"] != "one\n" || files["d/sub/b"] != "two\n" {
		t.Fatalf("copied %v", files)
	}
	if _, err := rt.CopyFromContainer(ctx, id, "/nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing path: %v", err)
	}
}
