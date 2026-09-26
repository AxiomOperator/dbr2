// SPDX-License-Identifier: Apache-2.0

//go:build integration

package agent

import (
	"context"
	"strings"
	"testing"

	agentv1 "github.com/AxiomOperator/dbr2/internal/agentpb/agent/v1"
	"github.com/AxiomOperator/dbr2/internal/manifest"
)

func dumpSpec(name, container string, d *agentv1.DatabaseSpec) *agentv1.ComponentSpec {
	d.ContainerId = container
	return &agentv1.ComponentSpec{Name: name, Kind: agentv1.ComponentKind_COMPONENT_KIND_DATABASE, Required: true, Database: d}
}

func captureDB(t *testing.T, e *snapEnv, rp string, spec *agentv1.ComponentSpec) *agentv1.ComponentResult {
	t.Helper()
	u := e.run(t, snapCmd(rp, false, spec))
	succeeded(t, u)
	r := u.GetSnapshotComponents().Components[0]
	if r.Status != manifest.ComponentSucceeded {
		t.Fatalf("capture %s: %s", r.Name, r.Error)
	}
	return r
}

// Full round trip: pg_dumpall capture → RestoreDatabase into a fresh
// container.
func TestIntegrationDumpPostgresRoundTrip(t *testing.T) {
	e := dockerEnv(t)
	const src, dst = "dbr2test-dump-pg", "dbr2test-dump-pg-new"
	rmContainer(t, src)
	rmContainer(t, dst)
	run := func(name string) {
		dockerOut(t, "run", "-d", "--name", name, "-e", "POSTGRES_PASSWORD=pw-test", "-e", "POSTGRES_USER=app", // gitleaks:allow (test fixture)
			"docker.io/library/postgres:18-alpine")
		waitExec(t, name, "psql", "-h", "127.0.0.1", "-U", "app", "-d", "postgres", "-c", "select 1")
	}
	run(src)
	psql := func(name, db, sql string) string {
		return dockerOut(t, "exec", name, "psql", "-X", "-At", "-U", "app", "-d", db, "-c", sql)
	}
	psql(src, "postgres", "create table items (id int primary key, name text); insert into items select g, 'item-' || g from generate_series(1, 500) g")
	psql(src, "postgres", "create database shop")
	psql(src, "shop", "create table orders (id int); insert into orders select generate_series(1, 42)")

	r := captureDB(t, e, "rp_itpg", dumpSpec("database:db", src, &agentv1.DatabaseSpec{Engine: enginePostgres, Format: formatPGDump, Service: "db"}))
	if r.FileName != pgDumpFile || !strings.Contains(r.Validation, "pg_dumpall exit 0, trailer present") || r.Database.GetService() != "db" {
		t.Fatalf("result %+v", r)
	}
	t.Logf("validation: %s (stored %d bytes)", r.Validation, r.SizeBytes)

	// A wrong role fails validation (exit code) and saves nothing.
	u := e.run(t, snapCmd("rp_itpg_bad", false, dumpSpec("database:db", src,
		&agentv1.DatabaseSpec{Engine: enginePostgres, Format: formatPGDump, User: "nobody"})))
	if b := u.GetSnapshotComponents().Components[0]; b.Status != manifest.ComponentFailed || !strings.Contains(b.Error, "exited with code") {
		t.Fatalf("bad role: %+v", b)
	}
	if s := snapsOf(t, e, "rp_itpg_bad"); len(s) != 0 {
		t.Fatalf("failed dump saved: %+v", s)
	}

	run(dst)
	succeeded(t, e.run(t, dbCmd("database:db", r.SnapshotId, r.FileName, enginePostgres, formatPGDump, dst)))
	if n := psql(dst, "postgres", "select count(*), max(name) from items"); n != "500|item-99" {
		t.Fatalf("items after restore: %q", n)
	}
	if n := psql(dst, "shop", "select count(*) from orders"); n != "42" {
		t.Fatalf("orders after restore: %q", n)
	}
}

func TestIntegrationDumpRedis(t *testing.T) {
	e := dockerEnv(t)
	const name = "dbr2test-dump-redis"
	rmContainer(t, name)
	dockerOut(t, "run", "-d", "--name", name, "docker.io/library/redis:8-alpine")
	waitExec(t, name, "redis-cli", "PING")
	cli := func(c string, args ...string) string {
		return dockerOut(t, append([]string{"exec", c, "redis-cli"}, args...)...)
	}
	cli(name, "SET", "k1", "v1")
	cli(name, "SET", "k2", "v2")
	r := captureDB(t, e, "rp_itr", dumpSpec("database:cache", name, &agentv1.DatabaseSpec{Engine: engineRedis, Format: formatRDB, IncludeAof: true}))
	if r.FileName != rdbDumpFile || !strings.Contains(r.Validation, "RDB magic REDIS") || !strings.Contains(r.Validation, "AOF disabled") {
		t.Fatalf("result %+v", r)
	}
	// A second capture right after the first (same LASTSAVE second) works.
	captureDB(t, e, "rp_itr2", dumpSpec("database:cache", name, &agentv1.DatabaseSpec{Engine: engineRedis, Format: formatRDB}))

	cli(name, "FLUSHALL")
	cli(name, "SET", "other", "x")
	succeeded(t, e.run(t, dbCmd("database:cache", r.SnapshotId, r.FileName, engineRedis, formatRDB, name)))
	if cli(name, "GET", "k1") != "v1" || cli(name, "GET", "k2") != "v2" || cli(name, "EXISTS", "other") != "0" {
		t.Fatal("keys not restored")
	}

	// Password-protected Redis is refused clearly.
	cli(name, "CONFIG", "SET", "requirepass", "pw-test-redis") // gitleaks:allow (test fixture)
	u := e.run(t, snapCmd("rp_itr_auth", false, dumpSpec("database:cache", name, &agentv1.DatabaseSpec{Engine: engineRedis, Format: formatRDB})))
	if b := u.GetSnapshotComponents().Components[0]; b.Status != manifest.ComponentFailed || !strings.Contains(b.Error, "Redis AUTH is not supported yet") {
		t.Fatalf("auth: %+v", b)
	}
}

func TestIntegrationDumpRedisAOF(t *testing.T) {
	e := dockerEnv(t)
	const name = "dbr2test-dump-redis-aof"
	rmContainer(t, name)
	dockerOut(t, "run", "-d", "--name", name, "docker.io/library/redis:8-alpine", "redis-server", "--appendonly", "yes")
	waitExec(t, name, "redis-cli", "PING")
	dockerOut(t, "exec", name, "redis-cli", "SET", "k1", "v1")
	r := captureDB(t, e, "rp_itaof", dumpSpec("database:cache", name, &agentv1.DatabaseSpec{Engine: engineRedis, Format: formatRDB, IncludeAof: true}))
	if !strings.Contains(r.Validation, "AOF appendonlydir") {
		t.Fatalf("result %+v", r)
	}
	ctx := context.Background()
	ents, err := e.repo.ListDir(ctx, r.SnapshotId, "appendonlydir")
	if err != nil || len(ents) < 2 {
		t.Fatalf("aof files %+v %v", ents, err)
	}
	var names []string
	for _, x := range ents {
		names = append(names, x.Name)
	}
	if !strings.Contains(strings.Join(names, " "), ".manifest") {
		t.Fatalf("no AOF manifest in %v", names)
	}
	rc, err := e.repo.OpenStream(ctx, r.SnapshotId, rdbDumpFile)
	if err != nil {
		t.Fatal(err)
	}
	head := make([]byte, 5)
	_, _ = rc.Read(head)
	rc.Close()
	if string(head) != "REDIS" {
		t.Fatalf("rdb head %q", head)
	}
}
