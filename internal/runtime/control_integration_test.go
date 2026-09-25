// SPDX-License-Identifier: Apache-2.0

//go:build integration

package runtime

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestDockerControl exercises pause/unpause/stop/start/exec/inspect on a
// real engine (quiesce, resume and hooks, Phase 4).
func TestDockerControl(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI not available (used only to set up the fixture)")
	}
	const name = "dbr2test-control"
	_ = exec.Command("docker", "rm", "-f", name).Run()
	out, err := exec.Command("docker", "run", "-d", "--name", name, "-e", "SECRET=real-value",
		"docker.io/library/alpine:3.22", "sleep", "3600").CombinedOutput()
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
	state := func(want string) {
		t.Helper()
		s, err := rt.InspectState(ctx, id)
		if err != nil || s.State != want || s.Name != name {
			t.Fatalf("state %+v %v, want %s", s, err, want)
		}
	}
	state("running")
	if raw, err := rt.InspectRaw(ctx, id); err != nil || !strings.Contains(string(raw), "SECRET=real-value") {
		t.Fatalf("raw inspect: %v", err)
	}
	r, err := rt.Exec(ctx, id, []string{"sh", "-c", "echo out; echo err >&2; exit 7"}, 1024)
	if err != nil || r.ExitCode != 7 || !strings.Contains(string(r.Output), "out") || !strings.Contains(string(r.Output), "err") {
		t.Fatalf("exec %+v %v", r, err)
	}
	tctx, tcancel := context.WithTimeout(ctx, time.Second)
	_, err = rt.Exec(tctx, id, []string{"sleep", "30"}, 1024)
	tcancel()
	if err == nil {
		t.Fatal("exec timeout not reported")
	}
	if err := rt.Pause(ctx, id); err != nil {
		t.Fatal(err)
	}
	state("paused")
	if err := rt.Unpause(ctx, id); err != nil {
		t.Fatal(err)
	}
	state("running")
	if err := rt.Stop(ctx, id); err != nil {
		t.Fatal(err)
	}
	state("exited")
	if err := rt.Start(ctx, id); err != nil {
		t.Fatal(err)
	}
	state("running")
}
