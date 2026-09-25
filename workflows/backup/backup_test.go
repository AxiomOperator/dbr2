// SPDX-License-Identifier: Apache-2.0

package backup

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"

	agentv1 "github.com/AxiomOperator/dbr2/internal/agentpb/agent/v1"
	controlv1 "github.com/AxiomOperator/dbr2/internal/agentpb/control/v1"
	"github.com/AxiomOperator/dbr2/internal/engine"
	"github.com/AxiomOperator/dbr2/internal/engine/kopia"
	"github.com/AxiomOperator/dbr2/internal/manifest"
)

const agentID = "9b2f7c1e-0000-4000-8000-000000000001"

func plan(mode string) *controlv1.PrepareBackupResponse {
	seed, _ := json.Marshal(ManifestSeed{
		Application: manifest.Application{ID: "app-1", Name: "shop"},
		Source:      manifest.Source{HostID: agentID, AgentID: agentID, Hostname: "h1"},
	})
	return &controlv1.PrepareBackupResponse{
		RecoveryPointId: manifest.NewRecoveryPointID(time.Now()), AgentId: agentID,
		Repository: &controlv1.Repository{Id: "repo-1", Name: "primary"}, ConsistencyMode: mode, MaxQuiesceSeconds: 3600,
		Components:   []*agentv1.ComponentSpec{{Name: "volume:data", Kind: agentv1.ComponentKind_COMPONENT_KIND_VOLUME, Required: true}},
		PreHooks:     []*agentv1.Hook{{ContainerId: "c1", Command: []string{"sync"}}},
		PostHooks:    []*agentv1.Hook{{ContainerId: "c1", Command: []string{"true"}}},
		ContainerIds: []string{"c1"}, ApplicationJson: seed,
	}
}

func okResults(rp string) *agentv1.SnapshotComponentsResult {
	now := time.Now().UnixMilli()
	return &agentv1.SnapshotComponentsResult{Components: []*agentv1.ComponentResult{{
		Name: "volume:data", Kind: agentv1.ComponentKind_COMPONENT_KIND_VOLUME, Required: true, Status: "succeeded",
		SnapshotId: "k1", Source: "agent@" + agentID + ":/app-1/volume:data", StartedUnixMs: now, FinishedUnixMs: now}}}
}

type calls struct {
	mu    sync.Mutex
	order []string
}

func (c *calls) add(n string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.order = append(c.order, n)
}

func env(t *testing.T, p *controlv1.PrepareBackupResponse, snap func() (*agentv1.SnapshotComponentsResult, error), c *calls) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	var s testsuite.WorkflowTestSuite
	e := s.NewTestWorkflowEnvironment()
	var a *Activities
	e.RegisterActivity(a)
	rec := c.add
	e.OnActivity(a.PrepareBackup, mock.Anything, mock.Anything).Return(p, nil)
	e.OnActivity(a.EnsureAgentAccess, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	e.OnActivity(a.RunHooks, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(
		func(_ context.Context, _ string, phase string, _ []*agentv1.Hook) (*agentv1.RunHooksResult, error) {
			rec("hooks:" + phase)
			return &agentv1.RunHooksResult{}, nil
		})
	e.OnActivity(a.Quiesce, mock.Anything, mock.Anything).Return(func(_ context.Context, in QuiesceInput) (*agentv1.QuiesceResult, error) {
		rec("quiesce")
		if in.LeaseSeconds != uint32((time.Hour+LeaseGrace)/time.Second) {
			t.Errorf("lease = %d", in.LeaseSeconds)
		}
		return &agentv1.QuiesceResult{QuiescedAtUnixMs: time.Now().UnixMilli()}, nil
	})
	e.OnActivity(a.Resume, mock.Anything, mock.Anything, mock.Anything).Return(func(context.Context, string, string) (*agentv1.ResumeResult, error) {
		rec("resume")
		return &agentv1.ResumeResult{}, nil
	})
	e.OnActivity(a.Snapshot, mock.Anything, mock.Anything).Return(func(context.Context, SnapshotInput) (*agentv1.SnapshotComponentsResult, error) {
		rec("snapshot")
		return snap()
	})
	e.OnActivity(a.Commit, mock.Anything, mock.Anything).Return(func(_ context.Context, in CommitInput) (CommitResult, error) {
		rec("commit")
		m, err := BuildManifest(in, time.Now())
		if err != nil {
			return CommitResult{}, err
		}
		return CommitResult{Status: m.Status, Components: len(m.Components)}, nil
	})
	e.OnActivity(a.CompleteBackup, mock.Anything, mock.Anything).Return(func(_ context.Context, r *controlv1.CompleteBackupRequest) error {
		rec("complete:" + r.Outcome)
		return nil
	})
	e.OnActivity(a.RecordEvent, mock.Anything, mock.Anything).Return(nil)
	return e
}

func TestQuiescedBackupOrder(t *testing.T) {
	p := plan(manifest.ModeQuiesced)
	c := &calls{}
	e := env(t, p, func() (*agentv1.SnapshotComponentsResult, error) { return okResults(p.RecoveryPointId), nil }, c)
	e.ExecuteWorkflow(Workflow, Input{ApplicationID: "app-1", Trigger: "manual"})
	if err := e.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
	want := "hooks:pre quiesce snapshot resume hooks:post commit complete:committed"
	if got := strings.Join(c.order, " "); got != want {
		t.Fatalf("order = %s\nwant    %s", got, want)
	}
	var r Result
	_ = e.GetWorkflowResult(&r)
	if r.Status != manifest.StatusComplete || r.RecoveryPointID != p.RecoveryPointId {
		t.Fatalf("result = %+v", r)
	}
}

func TestSeedPassRunsLiveBeforeQuiesce(t *testing.T) {
	p := plan(manifest.ModeQuiesced)
	p.SeedComponents = []string{"volume:data"}
	c := &calls{}
	var mu sync.Mutex
	var seeds []bool
	e := env(t, p, func() (*agentv1.SnapshotComponentsResult, error) { return okResults(p.RecoveryPointId), nil }, c)
	e.SetOnActivityStartedListener(func(info *activity.Info, _ context.Context, args converter.EncodedValues) {
		if info.ActivityType.Name == "Snapshot" {
			var in SnapshotInput
			_ = args.Get(&in)
			mu.Lock()
			seeds = append(seeds, in.Seed)
			mu.Unlock()
		}
	})
	e.ExecuteWorkflow(Workflow, Input{ApplicationID: "app-1"})
	if err := e.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
	want := "snapshot hooks:pre quiesce snapshot resume hooks:post commit complete:committed"
	if got := strings.Join(c.order, " "); got != want || len(seeds) != 2 || !seeds[0] || seeds[1] {
		t.Fatalf("order = %s seeds = %v", got, seeds)
	}
}

func TestSnapshotFailureStillResumes(t *testing.T) {
	p := plan(manifest.ModeQuiesced)
	c := &calls{}
	e := env(t, p, func() (*agentv1.SnapshotComponentsResult, error) {
		return nil, temporal.NewNonRetryableApplicationError("disk gone", "AgentCommandFailed", nil)
	}, c)
	e.ExecuteWorkflow(Workflow, Input{ApplicationID: "app-1"})
	if e.GetWorkflowError() == nil {
		t.Fatal("expected failure")
	}
	want := "hooks:pre quiesce snapshot resume hooks:post complete:failed"
	if got := strings.Join(c.order, " "); got != want {
		t.Fatalf("order = %s\nwant    %s", got, want)
	}
}

func TestRequiredComponentFailureWritesNoManifest(t *testing.T) {
	p := plan(manifest.ModeLive)
	c := &calls{}
	e := env(t, p, func() (*agentv1.SnapshotComponentsResult, error) {
		r := okResults(p.RecoveryPointId)
		r.Components[0].Status, r.Components[0].Error = "failed", "permission denied"
		return r, nil
	}, c)
	e.ExecuteWorkflow(Workflow, Input{ApplicationID: "app-1"})
	var ae *temporal.ApplicationError
	if err := e.GetWorkflowError(); !errors.As(err, &ae) || ae.Type() != ErrRequiredComponentFailed {
		t.Fatalf("err = %v", err)
	}
	if got := strings.Join(c.order, " "); got != "snapshot complete:failed" {
		t.Fatalf("live mode must not quiesce or commit: %s", got)
	}
}

func TestCancelDuringCaptureResumes(t *testing.T) {
	p := plan(manifest.ModeOffline)
	c := &calls{}
	e := env(t, p, func() (*agentv1.SnapshotComponentsResult, error) {
		time.Sleep(50 * time.Millisecond)
		return nil, context.Canceled
	}, c)
	e.SetOnActivityStartedListener(func(info *activity.Info, _ context.Context, _ converter.EncodedValues) {
		if info.ActivityType.Name == "Snapshot" {
			e.CancelWorkflow() // inside the quiesce window
		}
	})
	e.ExecuteWorkflow(Workflow, Input{ApplicationID: "app-1"})
	if got := strings.Join(c.order, " "); !strings.Contains(got, "quiesce snapshot resume") {
		t.Fatalf("no resume after cancel: %s", got)
	}
}

// ---- Repository-backed tests (filesystem Kopia repository) ----

func repo(t *testing.T) engine.Repository {
	t.Helper()
	d := t.TempDir()
	r, err := kopia.InitFilesystem(context.Background(), filepath.Join(d, "r"), filepath.Join(d, "s"), "pw-123456", AgentKopiaUser, agentID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(context.Background()) })
	return r
}

func component(t *testing.T, r engine.Repository, rp, name, kind, host string) *engine.Snapshot {
	t.Helper()
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "f"), []byte(name), 0o600)
	s, err := r.SnapshotPath(context.Background(), dir, engine.SnapshotRequest{
		Source: engine.Source{User: AgentKopiaUser, Host: host, Path: "/app-1/" + name}, Pins: []string{engine.Pin},
		Tags: map[string]string{engine.TagRP: rp, engine.TagApp: "app-1", engine.TagComponent: name, engine.TagKind: kind}})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func commitInput(p *controlv1.PrepareBackupResponse, s *engine.Snapshot) CommitInput {
	res := okResults(p.RecoveryPointId)
	res.Components[0].SnapshotId, res.Components[0].Source = s.ID, s.Source.String()
	return CommitInput{Plan: p, Results: res.Components, WorkflowID: "application/app-1", RunID: "r1", StartedAt: time.Now()}
}

func TestCommitWritesTrustedManifestIdempotently(t *testing.T) {
	ctx := context.Background()
	r := repo(t)
	p := plan(manifest.ModeLive)
	s := component(t, r, p.RecoveryPointId, "volume:data", "volume", agentID)
	m, err := BuildManifest(commitInput(p, s), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	first, err := commitWith(ctx, r, m)
	if err != nil {
		t.Fatal(err)
	}
	second, err := commitWith(ctx, r, m)
	if err != nil || second.SnapshotID != first.SnapshotID {
		t.Fatalf("retry wrote a second manifest: %v %v", second.SnapshotID, err)
	}
	found, res, err := readManifests(ctx, r, func() {})
	if err != nil || len(found) != 1 || res.Found != 1 {
		t.Fatalf("reindex found %d (%+v), %v", len(found), res, err)
	}
	parsed, err := manifest.Parse(found[0].ManifestJson)
	if err != nil || parsed.RecoveryPointID != p.RecoveryPointId || !parsed.CrashConsistentOnly {
		t.Fatalf("parsed %+v %v", parsed, err)
	}
}

func TestCommitRejectsForeignComponent(t *testing.T) {
	r := repo(t)
	p := plan(manifest.ModeLive)
	s := component(t, r, p.RecoveryPointId, "volume:data", "volume", "another-agent")
	in := commitInput(p, s)
	in.Results[0].Source = "agent@" + agentID + ":/app-1/volume:data" // lie about the source
	m, err := BuildManifest(in, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var ae *temporal.ApplicationError
	if _, err := commitWith(context.Background(), r, m); !errors.As(err, &ae) || ae.Type() != ErrSourceMismatch {
		t.Fatalf("err = %v", err)
	}
}

func TestReindexIgnoresAgentWrittenManifest(t *testing.T) {
	ctx := context.Background()
	r := repo(t)
	// An agent tagging its own snapshot as a manifest is not trusted.
	component(t, r, manifest.NewRecoveryPointID(time.Now()), "fake", "manifest", agentID)
	found, _, err := readManifests(ctx, r, func() {})
	if err != nil || len(found) != 0 {
		t.Fatalf("trusted an agent manifest: %d %v", len(found), err)
	}
}

func TestOrphanGC(t *testing.T) {
	ctx := context.Background()
	r := repo(t)
	p := plan(manifest.ModeLive)
	kept := component(t, r, p.RecoveryPointId, "volume:data", "volume", agentID)
	m, _ := BuildManifest(commitInput(p, kept), time.Now())
	if _, err := commitWith(ctx, r, m); err != nil {
		t.Fatal(err)
	}
	orphan := component(t, r, manifest.NewRecoveryPointID(time.Now()), "volume:data", "volume", agentID)
	seed := component(t, r, p.RecoveryPointId, "volume:data", "seed", agentID)

	if res, err := collectOrphans(ctx, r, time.Hour, time.Now(), func() {}); err != nil || res.Deleted != 0 {
		t.Fatalf("within grace: %+v %v", res, err)
	}
	res, err := collectOrphans(ctx, r, time.Hour, time.Now().Add(2*time.Hour), func() {})
	if err != nil || res.Deleted != 2 {
		t.Fatalf("after grace: %+v %v", res, err)
	}
	for id, want := range map[string]bool{kept.ID: true, orphan.ID: false, seed.ID: false} {
		_, err := r.Get(ctx, id)
		if exists := err == nil; exists != want {
			t.Errorf("snapshot %s exists=%v want %v", id, exists, want)
		}
	}
	if found, _, _ := readManifests(ctx, r, func() {}); len(found) != 1 {
		t.Fatal("GC deleted the manifest")
	}
}
