// SPDX-License-Identifier: Apache-2.0

//go:build integration

package agent

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	agentv1 "github.com/AxiomOperator/dbr2/internal/agentpb/agent/v1"
	"github.com/AxiomOperator/dbr2/internal/engine"
	"github.com/AxiomOperator/dbr2/internal/runtime"
)

func dockerOut(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("docker", args...).Output() // stdout only: pull progress is on stderr
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, ee.Stderr)
		}
		t.Fatalf("docker %s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out))
}

// dockerEnv is a snapEnv on the real Docker engine.
func dockerEnv(t *testing.T) *snapEnv {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI not available (used only to set up fixtures)")
	}
	e := newSnapEnv(t)
	rt, err := runtime.NewDocker("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Close() })
	e.Agent.rt = rt
	return e
}

func rmContainer(t *testing.T, name string) {
	_ = exec.Command("docker", "rm", "-f", name).Run()
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() })
}

func TestIntegrationEnsureImagesByDigest(t *testing.T) {
	e := dockerEnv(t)
	const ref = "dbr2test/ensure:restored"
	t.Cleanup(func() { _ = exec.Command("docker", "rmi", ref).Run() })
	_ = exec.Command("docker", "rmi", ref).Run()
	dockerOut(t, "pull", "-q", "docker.io/library/busybox:1.37")
	digest := dockerOut(t, "image", "inspect", "busybox:1.37", "--format", "{{index .RepoDigests 0}}")
	_, digest, _ = strings.Cut(digest, "@")
	cmd := &agentv1.Command{Kind: &agentv1.Command_EnsureImages{EnsureImages: &agentv1.EnsureImagesCommand{Images: []*agentv1.ImageSpec{
		{Ref: "busybox:1.37", Digest: digest}, {Ref: "docker.io/library/busybox:1.37"}}}}}
	u := e.run(t, cmd)
	succeeded(t, u)
	for _, r := range u.GetEnsureImages().Images {
		if r.Status != "present" {
			t.Fatalf("image %+v", r)
		}
	}
	// A captured digest under a local reference that does not exist yet:
	// resolved by digest (pulled when absent) and tagged as ref.
	u = e.run(t, &agentv1.Command{Kind: &agentv1.Command_EnsureImages{EnsureImages: &agentv1.EnsureImagesCommand{
		Images: []*agentv1.ImageSpec{{Ref: "busybox:dbr2test", Digest: digest}}}}})
	succeeded(t, u)
	t.Cleanup(func() { _ = exec.Command("docker", "rmi", "busybox:dbr2test").Run() })
	if got := dockerOut(t, "image", "inspect", "busybox:dbr2test", "--format", "{{.Id}}"); got != dockerOut(t, "image", "inspect", "busybox:1.37", "--format", "{{.Id}}") {
		t.Fatalf("tag points at %s", got)
	}
	// An unknown digest fails, retryably.
	u = e.run(t, &agentv1.Command{Kind: &agentv1.Command_EnsureImages{EnsureImages: &agentv1.EnsureImagesCommand{
		Images: []*agentv1.ImageSpec{{Ref: ref, Digest: "sha256:" + strings.Repeat("0", 64)}}}}})
	failed(t, u, true, ref)
}

func TestIntegrationRecreateAndCheckHealth(t *testing.T) {
	e := dockerEnv(t)
	const name, net = "dbr2test-recreate", "dbr2test-recreate-net"
	_ = exec.Command("docker", "network", "rm", net).Run()
	t.Cleanup(func() { _ = exec.Command("docker", "network", "rm", net).Run() }) // runs after the container removal
	rmContainer(t, name)
	dockerOut(t, "network", "create", net)
	id := dockerOut(t, "run", "-d", "--name", name, "--network", net, "-e", "TOKEN=abc", // gitleaks:allow (test fixture)
		"--health-cmd", "test -f /tmp/ready", "--health-interval", "1s", "--health-start-period", "0s",
		"docker.io/library/alpine:3.22", "sh", "-c", "sleep 2; touch /tmp/ready; sleep 3600")
	// Capture the config component with the real inspect document.
	u := e.run(t, snapCmd("rp_it1", false, &agentv1.ComponentSpec{Name: "config", Kind: agentv1.ComponentKind_COMPONENT_KIND_CONFIG,
		Required: true, MetadataJson: []byte(`{}`), ContainerIds: []string{id}}))
	succeeded(t, u)
	cfgSnap := u.GetSnapshotComponents().Components[0].SnapshotId
	dockerOut(t, "rm", "-f", name)
	dockerOut(t, "network", "rm", net)

	u = e.run(t, &agentv1.Command{Kind: &agentv1.Command_RecreateContainers{RecreateContainers: &agentv1.RecreateContainersCommand{
		RepositoryId: "repo1", RestoreId: "rsit1", ConfigSnapshotId: cfgSnap,
		Networks: []*agentv1.NetworkSpec{{Name: net, Driver: "bridge"}}}}})
	succeeded(t, u)
	rc := u.GetRecreateContainers()
	if len(rc.Containers) != 1 || rc.Containers[0].Status != "created" || !rc.Containers[0].WasRunning || len(rc.CreatedNetworks) != 1 {
		t.Fatalf("recreate %+v", rc)
	}
	nid := rc.Containers[0].ContainerId
	succeeded(t, e.run(t, &agentv1.Command{Kind: &agentv1.Command_StartContainers{StartContainers: &agentv1.StartContainersCommand{
		RestoreId: "rsit1", ContainerIds: []string{nid}}}}))
	u = e.run(t, &agentv1.Command{Kind: &agentv1.Command_CheckHealth{CheckHealth: &agentv1.CheckHealthCommand{
		ContainerIds: []string{nid}, TimeoutSeconds: 60, StableSeconds: 2}}})
	succeeded(t, u)
	if h := u.GetCheckHealth().Containers[0]; h.Health != "healthy" || h.Name != name || !h.Ok {
		t.Fatalf("health %+v", h)
	}
	if env := dockerOut(t, "exec", nid, "sh", "-c", "echo $TOKEN"); env != "abc" {
		t.Fatalf("env %q", env)
	}
	// A crashing container fails the check early with its logs.
	const crash = "dbr2test-crash"
	rmContainer(t, crash)
	cid := dockerOut(t, "run", "-d", "--name", crash, "docker.io/library/alpine:3.22", "sh", "-c", "echo fatal-error-here; exit 4")
	dockerOut(t, "wait", cid)
	u = e.run(t, &agentv1.Command{Kind: &agentv1.Command_CheckHealth{CheckHealth: &agentv1.CheckHealthCommand{
		ContainerIds: []string{cid}, TimeoutSeconds: 30}}})
	failed(t, u, false, "exited")
	if h := u.GetCheckHealth().Containers[0]; h.ExitCode != 4 || !strings.Contains(h.LogTail, "fatal-error-here") {
		t.Fatalf("crash %+v", h)
	}
	// Rollback removes the recreated container and network.
	u = e.run(t, finalizeCmd("rsit1", agentv1.FinalizeAction_FINALIZE_ACTION_ROLLBACK))
	succeeded(t, u)
	if err := exec.Command("docker", "inspect", nid).Run(); err == nil {
		t.Fatal("recreated container not removed")
	}
	if err := exec.Command("docker", "network", "inspect", net).Run(); err == nil {
		t.Fatal("created network not removed")
	}
}

func snapshotStream(t *testing.T, e *snapEnv, name, file string, data []byte) string {
	t.Helper()
	s, err := e.repo.SnapshotStream(context.Background(), file, bytes.NewReader(data), engine.SnapshotRequest{
		Source: engine.Source{User: "agent", Host: "agent-1", Path: "/app1/" + name}})
	if err != nil {
		t.Fatal(err)
	}
	return s.ID
}

func waitExec(t *testing.T, name string, args ...string) {
	t.Helper()
	for i := 0; ; i++ {
		if exec.Command("docker", append([]string{"exec", name}, args...)...).Run() == nil {
			return
		}
		if i > 120 {
			t.Fatalf("%s: %v never succeeded", name, args)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func dbCmd(name, snap, file, engineName, format, container string) *agentv1.Command {
	return &agentv1.Command{Kind: &agentv1.Command_RestoreDatabase{RestoreDatabase: &agentv1.RestoreDatabaseCommand{
		RepositoryId: "repo1", RestoreId: "rsdb", Name: name, SnapshotId: snap, FileName: file, Engine: engineName,
		Format: format, ContainerId: container}}}
}

func TestIntegrationRestorePostgres(t *testing.T) {
	e := dockerEnv(t)
	const name = "dbr2test-restore-pg"
	rmContainer(t, name)
	dockerOut(t, "run", "-d", "--name", name, "-e", "POSTGRES_PASSWORD=pw-test", "-e", "POSTGRES_USER=app", // gitleaks:allow (test fixture)
		"docker.io/library/postgres:18-alpine")
	// TCP only answers once initdb's temporary (socket-only) server is gone.
	waitExec(t, name, "psql", "-h", "127.0.0.1", "-U", "app", "-d", "postgres", "-c", "select 1")
	psql := func(sql string) string {
		return dockerOut(t, "exec", name, "psql", "-X", "-At", "-U", "app", "-d", "postgres", "-c", sql)
	}
	psql("create table items (id int primary key, name text); insert into items select g, 'item-' || g from generate_series(1, 500) g")
	dump, err := exec.Command("docker", "exec", name, "pg_dumpall", "-U", "app").Output()
	if err != nil || !bytes.Contains(dump, []byte("CREATE TABLE public.items")) {
		t.Fatalf("pg_dumpall: %v", err)
	}
	var zbuf bytes.Buffer
	zw, _ := zstd.NewWriter(&zbuf)
	_, _ = zw.Write(dump)
	_ = zw.Close()
	snap := snapshotStream(t, e, "database:pg", "dumpall.sql.zst", zbuf.Bytes())
	psql("drop table items")

	u := e.run(t, dbCmd("database:pg", snap, "dumpall.sql.zst", enginePostgres, formatPGDump, name))
	succeeded(t, u)
	if r := u.GetRestoreDatabase(); r.Bytes != int64(len(dump)) || r.Output == "" {
		t.Fatalf("result bytes=%d (want %d) output=%q", r.Bytes, len(dump), r.Output)
	}
	if n := psql("select count(*), max(name) from items"); n != "500|item-99" {
		t.Fatalf("rows after restore: %q", n)
	}
	// A dump psql cannot run fails the command.
	var bad bytes.Buffer
	zw, _ = zstd.NewWriter(&bad)
	_, _ = zw.Write([]byte("\\connect nonexistent_db\n"))
	_ = zw.Close()
	snap = snapshotStream(t, e, "database:pg-bad", "bad.sql.zst", bad.Bytes())
	failed(t, e.run(t, dbCmd("database:pg", snap, "bad.sql.zst", enginePostgres, formatPGDump, name)), false, "psql exited")
	// Corrupt compression fails too.
	snap = snapshotStream(t, e, "database:pg-corrupt", "c.sql.zst", []byte("not zstd at all"))
	failed(t, e.run(t, dbCmd("database:pg", snap, "c.sql.zst", enginePostgres, formatPGDump, name)), false, "")
}

func TestIntegrationRestoreRedis(t *testing.T) {
	e := dockerEnv(t)
	const name = "dbr2test-restore-redis"
	rmContainer(t, name)
	dockerOut(t, "run", "-d", "--name", name, "docker.io/library/redis:8-alpine")
	waitExec(t, name, "redis-cli", "PING")
	cli := func(args ...string) string {
		return dockerOut(t, append([]string{"exec", name, "redis-cli"}, args...)...)
	}
	cli("SET", "k1", "v1")
	cli("SET", "k2", "v2")
	cli("SAVE")
	rdb, err := exec.Command("docker", "exec", name, "cat", "/data/dump.rdb").Output()
	if err != nil || !bytes.HasPrefix(rdb, []byte("REDIS")) {
		t.Fatalf("rdb: %v", err)
	}
	snap := snapshotStream(t, e, "database:redis", "dump.rdb", rdb)
	cli("FLUSHALL")
	cli("SET", "other", "x")

	u := e.run(t, dbCmd("database:redis", snap, "dump.rdb", engineRedis, formatRDB, name))
	succeeded(t, u)
	if r := u.GetRestoreDatabase(); r.Bytes != int64(len(rdb)) || r.Output != "PONG" {
		t.Fatalf("result %+v", r)
	}
	if cli("GET", "k1") != "v1" || cli("GET", "k2") != "v2" || cli("EXISTS", "other") != "0" {
		t.Fatal("keys not restored")
	}
	if owner := dockerOut(t, "exec", name, "stat", "-c", "%U", "/data/dump.rdb"); owner != "redis" {
		t.Fatalf("dump owner %q", owner)
	}
	// AOF enabled: refused.
	cli("CONFIG", "SET", "appendonly", "yes")
	failed(t, e.run(t, dbCmd("database:redis", snap, "dump.rdb", engineRedis, formatRDB, name)), false, "AOF enabled")
}
