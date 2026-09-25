// Command q2 exercises Kopia repository-server ACLs (ADR-0002) from library
// clients, one connection per identity, and prints an expected/actual matrix.
//
// Prerequisites: scripts/q2-setup.sh and scripts/q2-server.sh.
package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/kopia/kopia/repo"
	"github.com/kopia/kopia/repo/manifest"
	"github.com/kopia/kopia/snapshot"
	"github.com/kopia/kopia/snapshot/policy"

	"github.com/AxiomOperator/dbr2/spikes/kopia-library/internal/engine"
)

var (
	url    = flag.String("url", "https://127.0.0.1:51515", "repository server URL")
	fpFile = flag.String("fp", ".work/q2/cert.sha256", "file with server cert SHA256")
	work   = flag.String("work", ".work/q2/clients", "client state dir")
)

type identity struct{ user, host, pass string }

var (
	agentA = identity{"agent-a", "hosta", "pw-agent-a"}
	agentB = identity{"agent-b", "hostb", "pw-agent-b"}
	maint  = identity{"maint", "dbr2", "pw-maint"}
)

func (i identity) String() string { return i.user + "@" + i.host }

func connect(ctx context.Context, id identity, pass string) (repo.Repository, error) {
	fp, err := os.ReadFile(*fpFile)
	must(err)

	dir, _ := filepath.Abs(filepath.Join(*work, id.String()))
	_ = os.RemoveAll(dir)
	must(os.MkdirAll(dir, 0o700))
	cfg := filepath.Join(dir, "repository.config")

	if err := engine.ConnectRepoServer(ctx, *url, strings.TrimSpace(string(fp)), cfg, filepath.Join(dir, "cache"), pass, id.user, id.host); err != nil {
		return nil, err
	}

	return engine.Open(ctx, cfg, pass)
}

type result struct {
	who, what, expect, got, detail string
}

var results []result

// record compares expected allow/deny with the actual outcome.
func record(who identity, what string, expectAllow bool, err error, detail string) {
	exp, got := "DENY", "DENY"
	if expectAllow {
		exp = "ALLOW"
	}
	if err == nil {
		got = "ALLOW"
	} else {
		detail = strings.TrimSpace(detail + " err=" + err.Error())
	}
	results = append(results, result{who.String(), what, exp, got, detail})
	log.Printf("[%s] %-62s expect=%-5s got=%-5s %s", who, what, exp, got, detail)
}

func main() {
	flag.Parse()
	log.SetFlags(log.Ltime)
	ctx := context.Background()

	srcRoot, _ := filepath.Abs(".work/q2/src")
	mkData := func(name string) string {
		d := filepath.Join(srcRoot, name)
		must(os.MkdirAll(d, 0o755))
		b := make([]byte, 256<<10)
		_, _ = rand.Read(b)
		must(os.WriteFile(filepath.Join(d, "data.bin"), b, 0o644))
		return d
	}
	dirA, dirB := mkData("hosta-vol"), mkData("hostb-vol")

	// --- authentication --------------------------------------------------
	_, err := connect(ctx, agentA, "wrong-password")
	record(agentA, "connect with wrong password", false, err, "")

	ra, err := connect(ctx, agentA, agentA.pass)
	record(agentA, "connect with own server-user password (no repo password)", true, err, "")
	must(err)
	rb, err := connect(ctx, agentB, agentB.pass)
	must(err)
	rm, err := connect(ctx, maint, maint.pass)
	must(err)
	record(agentA, "connection is NOT direct (cannot run maintenance)", false, boolErr(engine.IsDirect(ra)), fmt.Sprintf("type=%T", ra))

	// --- create own snapshots --------------------------------------------
	tags := engine.Tags(map[string]string{"dbr2-kind": "volume", "dbr2-rp": "rp_Q2"})
	srcA := snapshot.SourceInfo{UserName: agentA.user, Host: agentA.host, Path: dirA}
	srcB := snapshot.SourceInfo{UserName: agentB.user, Host: agentB.host, Path: dirB}

	manA, err := engine.SnapshotDir(ctx, ra, dirA, engine.SnapshotOptions{Source: srcA, Tags: tags})
	record(agentA, "create snapshot of own source", true, err, idOf(manA))
	manB, err := engine.SnapshotDir(ctx, rb, dirB, engine.SnapshotOptions{Source: srcB, Tags: tags})
	record(agentB, "create snapshot of own source", true, err, idOf(manB))
	must(err)

	// --- visibility ------------------------------------------------------
	lst, err := engine.ListByTags(ctx, ra, nil, nil)
	record(agentA, "list snapshots: sees own", true, errIf(err == nil && !containsSrc(lst, srcA), "own snapshot missing"), fmt.Sprintf("visible=%s", sources(lst)))
	record(agentA, "list snapshots: sees agent-b's", false, errIf(err != nil || !containsSrc(lst, srcB), "not visible"), "")

	_, err = engine.LoadSnapshot(ctx, ra, manB.ID)
	record(agentA, "load agent-b snapshot manifest by ID", false, err, "")

	_, err = engine.RestoreToDir(ctx, ra, manA, filepath.Join(srcRoot, "restore-a"))
	record(agentA, "restore own snapshot", true, err, "")

	// Content-level access is global in Kopia (type=content has no user/host labels).
	data, err := engine.ReadObject(ctx, ra, manB.RootObjectID().String())
	record(agentA, "read agent-b root dir object by ID (bypassing manifests)", false, err, fmt.Sprintf("bytes=%d", len(data)))
	var dirList struct {
		Entries []struct{ Name, Obj string } `json:"entries"`
	}
	_ = json.Unmarshal(data, &dirList)
	if len(dirList.Entries) > 0 {
		fileData, err := engine.ReadObject(ctx, ra, dirList.Entries[0].Obj)
		record(agentA, "read agent-b FILE content by object ID", false, err, fmt.Sprintf("file=%s bytes=%d", dirList.Entries[0].Name, len(fileData)))
	}
	_, err = engine.ReadObject(ctx, ra, "k0123456789abcdef0123456789abcdef")
	record(agentA, "read random/unknown object ID", false, err, "")

	// --- deletion / tampering --------------------------------------------
	err = engine.DeleteSnapshot(ctx, ra, manA.ID)
	record(agentA, "delete own snapshot", false, err, "")
	err = engine.DeleteSnapshot(ctx, ra, manB.ID)
	record(agentA, "delete agent-b snapshot", false, err, "")

	forged := *manA
	forged.Source = srcB
	err = engine.PutRawSnapshotManifest(ctx, ra, &forged)
	record(agentA, "forge snapshot manifest for agent-b@hostb source", false, err, "")

	err = engine.SetPolicy(ctx, ra, srcA, &policy.Policy{RetentionPolicy: policy.RetentionPolicy{KeepLatest: ptr(policy.OptionalInt(1))}})
	record(agentA, "set retention policy on own source", false, err, "")
	_, err = policy.GetDefinedPolicy(ctx, ra, policy.GlobalPolicySourceInfo)
	record(agentA, "read global policy (needed by uploader)", true, ignoreNotFound(err), "")

	users, err := engine.FindManifests(ctx, ra, map[string]string{manifest.TypeLabelKey: "user"})
	record(agentA, "enumerate server users", false, errIf(err != nil || len(users) == 0, "none visible"), fmt.Sprintf("visible=%d", len(users)))
	err = engine.PutRawManifest(ctx, ra, map[string]string{manifest.TypeLabelKey: "acl"}, map[string]any{"user": "*@*", "target": map[string]string{"type": "snapshot"}, "access": "FULL"})
	record(agentA, "write an ACL manifest granting itself FULL", false, err, "")

	// --- server-side retention bypass probe ------------------------------
	// Kopia's server exposes ApplyRetentionPolicy to anyone with APPEND on the
	// source; deletion is then executed with the SERVER's privileges.
	dirR := mkData("hosta-retention")
	srcR := snapshot.SourceInfo{UserName: agentA.user, Host: agentA.host, Path: dirR}
	for range 12 {
		_, err := engine.SnapshotDir(ctx, ra, dirR, engine.SnapshotOptions{Source: srcR, Tags: tags})
		must(err)
	}
	del, err := engine.ServerSideRetention(ctx, ra, dirR, true)
	left, _ := engine.ListByTags(ctx, ra, &srcR, nil)
	record(agentA, "trigger server-side retention on own source (12 unpinned)", false, errIf(err != nil || len(del) == 0, "nothing deleted"), fmt.Sprintf("deleted=%d remaining=%d (default global policy keep-latest=10)", len(del), len(left)))

	dirP := mkData("hosta-pinned")
	srcP := snapshot.SourceInfo{UserName: agentA.user, Host: agentA.host, Path: dirP}
	for range 12 {
		_, err := engine.SnapshotDir(ctx, ra, dirP, engine.SnapshotOptions{Source: srcP, Tags: tags, Pins: []string{"dbr2"}})
		must(err)
	}
	del, err = engine.ServerSideRetention(ctx, ra, dirP, true)
	left, _ = engine.ListByTags(ctx, ra, &srcP, nil)
	record(agentA, "trigger server-side retention on own source (12 PINNED)", false, errIf(err != nil || len(del) == 0, "nothing deleted"), fmt.Sprintf("deleted=%d remaining=%d", len(del), len(left)))

	// agent-b has 12 unpinned snapshots of dirB2; agent-a asks for retention on that path.
	dirB2 := mkData("hostb-retention")
	srcB2 := snapshot.SourceInfo{UserName: agentB.user, Host: agentB.host, Path: dirB2}
	for range 12 {
		_, err := engine.SnapshotDir(ctx, rb, dirB2, engine.SnapshotOptions{Source: srcB2, Tags: tags})
		must(err)
	}
	beforeB, _ := engine.ListByTags(ctx, rb, &srcB2, nil)
	del, err = engine.ServerSideRetention(ctx, ra, dirB2, true)
	leftB, _ := engine.ListByTags(ctx, rb, &srcB2, nil)
	record(agentA, "trigger server-side retention on agent-b's path", false, errIf(len(leftB) == len(beforeB), "agent-b snapshots untouched"),
		fmt.Sprintf("rpcErr=%v deleted=%d agent-b remaining=%d (server binds request to caller's user@host)", err, len(del), len(leftB)))

	// --- maintenance identity --------------------------------------------
	all, err := engine.ListByTags(ctx, rm, nil, nil)
	record(maint, "list snapshots of all agents", true, errIf(err != nil || !containsSrc(all, srcA) || !containsSrc(all, srcB), "not all visible"), fmt.Sprintf("visible=%s", sources(all)))
	_, err = engine.RestoreToDir(ctx, rm, manB, filepath.Join(srcRoot, "restore-b-by-maint"))
	record(maint, "restore agent-b snapshot (staged cross-host restore)", true, err, "")
	err = engine.SetPolicy(ctx, rm, srcA, &policy.Policy{RetentionPolicy: policy.RetentionPolicy{KeepLatest: ptr(policy.OptionalInt(100))}})
	record(maint, "set policy on agent-a source", true, err, "")
	err = engine.DeleteSnapshot(ctx, rm, manB.ID)
	record(maint, "delete agent-b snapshot", true, err, "")
	lb, _ := engine.ListByTags(ctx, rb, &srcB, nil)
	record(agentB, "own snapshot gone after maint delete", true, errIf(len(lb) != 0, "still there"), fmt.Sprintf("remaining=%d", len(lb)))
	record(maint, "connection is NOT direct (cannot run maintenance/GC)", false, boolErr(engine.IsDirect(rm)), fmt.Sprintf("type=%T", rm))

	// --- summary ---------------------------------------------------------
	fmt.Println("\n| identity | operation | expected (DBR² intent) | actual | detail |\n|---|---|---|---|---|")
	mismatch := 0
	for _, r := range results {
		mark := r.got
		if r.got != r.expect {
			mark = "**" + r.got + "** (mismatch)"
			mismatch++
		}
		fmt.Printf("| %s | %s | %s | %s | %s |\n", r.who, r.what, r.expect, mark, strings.ReplaceAll(r.detail, "|", "/"))
	}
	fmt.Printf("\n%d checks, %d mismatches vs DBR² intent\n", len(results), mismatch)
}

// ---------------------------------------------------------------------------

func must(err error) {
	if err != nil {
		log.Fatalf("fatal: %+v", err)
	}
}

func ptr[T any](v T) *T { return &v }

func idOf(m *snapshot.Manifest) string {
	if m == nil {
		return ""
	}
	return "id=" + string(m.ID)
}

// errIf turns a boolean "operation had no effect" into an error so record()
// can treat it as DENY.
func errIf(cond bool, msg string) error {
	if cond {
		return errors.New(msg)
	}
	return nil
}

func boolErr(ok bool) error { return errIf(!ok, "false") }

func ignoreNotFound(err error) error {
	if errors.Is(err, policy.ErrPolicyNotFound) {
		return nil
	}
	return err
}

func containsSrc(ms []*snapshot.Manifest, si snapshot.SourceInfo) bool {
	for _, m := range ms {
		if m.Source == si {
			return true
		}
	}
	return false
}

func sources(ms []*snapshot.Manifest) string {
	seen := map[string]bool{}
	var out []string
	for _, m := range ms {
		k := m.Source.UserName + "@" + m.Source.Host
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return "[" + strings.Join(out, " ") + "]"
}
