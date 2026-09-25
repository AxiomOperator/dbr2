// SPDX-License-Identifier: Apache-2.0

package backup

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	controlv1 "github.com/AxiomOperator/dbr2/internal/agentpb/control/v1"
	"github.com/AxiomOperator/dbr2/internal/engine"
	"github.com/AxiomOperator/dbr2/internal/manifest"
)

// DefaultOrphanGrace is how long uncommitted components (and seed passes)
// are kept before garbage collection (ADR-0004).
const DefaultOrphanGrace = 7 * 24 * time.Hour

// OrphanGCWorkflowID is the schedule's workflow ID (one run at a time).
const OrphanGCWorkflowID = "platform/orphan-gc"

// GCResult summarizes a collection.
type GCResult struct {
	Repositories int
	Deleted      int
	Kept         int
}

// OrphanGC deletes component snapshots that no manifest references after
// the grace period, in every Repository (ADR-0004 step 3).
func OrphanGC(ctx workflow.Context, grace time.Duration) (GCResult, error) {
	if grace <= 0 {
		grace = DefaultOrphanGrace
	}
	var a *Activities
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: time.Hour, HeartbeatTimeout: 2 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{InitialInterval: 10 * time.Second, MaximumAttempts: 5},
	})
	var ids []string
	if err := workflow.ExecuteActivity(ctx, a.ListRepositoryIDs).Get(ctx, &ids); err != nil {
		return GCResult{}, err
	}
	var total GCResult
	for _, id := range ids {
		var r GCResult
		if err := workflow.ExecuteActivity(ctx, a.CollectOrphans, id, grace).Get(ctx, &r); err != nil {
			workflow.GetLogger(ctx).Error("orphan GC failed", "repository", id, "error", err)
			continue
		}
		total.Repositories++
		total.Deleted += r.Deleted
		total.Kept += r.Kept
	}
	return total, nil
}

// ListRepositoryIDs returns every active Repository.
func (a *Activities) ListRepositoryIDs(ctx context.Context) ([]string, error) {
	resp, err := a.Platform.ListRepositories(a.auth(ctx), &controlv1.ListRepositoriesRequest{})
	if err != nil {
		return nil, platformErr(err)
	}
	var ids []string
	for _, r := range resp.Repositories {
		if r.Status == "ready" {
			ids = append(ids, r.Id)
		}
	}
	return ids, nil
}

// CollectOrphans deletes one Repository's orphaned components.
func (a *Activities) CollectOrphans(ctx context.Context, repositoryID string, grace time.Duration) (GCResult, error) {
	rep, err := a.Maint.Get(ctx, repositoryID)
	if err != nil {
		return GCResult{}, err
	}
	r, err := collectOrphans(ctx, rep, grace, time.Now(), func() { activity.RecordHeartbeat(ctx) })
	if err != nil {
		a.Maint.Invalidate(repositoryID)
	}
	return r, err
}

func collectOrphans(ctx context.Context, rep engine.Repository, grace time.Duration, now time.Time, beat func()) (GCResult, error) {
	all, err := rep.List(ctx, nil, nil)
	if err != nil {
		return GCResult{}, err
	}
	committed := map[string]bool{}
	for _, s := range all {
		if trustedManifest(s) {
			committed[s.Tags[engine.TagRP]] = true
		}
	}
	res := GCResult{Repositories: 1}
	for _, s := range all {
		rp := s.Tags[engine.TagRP]
		if rp == "" || s.Tags[engine.TagKind] == "manifest" {
			continue // not DBR²-managed, or a manifest (deleted only with its recovery point)
		}
		orphan := !committed[rp] || s.Tags[engine.TagKind] == "seed"
		if !orphan || now.Sub(s.StartTime) < grace {
			res.Kept++
			continue
		}
		if err := rep.Delete(ctx, s.ID); err != nil {
			return res, fmt.Errorf("delete %s: %w", s.ID, err)
		}
		res.Deleted++
		beat()
	}
	return res, nil
}

// trustedManifest reports whether a snapshot is a manifest written by
// maint@dbr2 (ADR-0004: agents can create manifest-tagged snapshots on
// their own sources; those are ignored).
func trustedManifest(s engine.Snapshot) bool {
	return s.Tags[engine.TagKind] == "manifest" && s.Tags[engine.TagRP] != "" &&
		s.Source.User == manifest.ManifestSourceUser && s.Source.Host == manifest.ManifestSourceHost
}

// ReindexResult summarizes a reindex.
type ReindexResult struct {
	Found         int
	Invalid       int
	Upserted      int
	MarkedMissing int
}

// Reindex rebuilds the recovery-point index of one Repository from its
// manifests (ADR-0003). Workflow ID: repository/<id>/reindex.
func Reindex(ctx workflow.Context, repositoryID string) (ReindexResult, error) {
	var a *Activities
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Hour, HeartbeatTimeout: 2 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{InitialInterval: 10 * time.Second, MaximumAttempts: 5},
	})
	var r ReindexResult
	err := workflow.ExecuteActivity(ctx, a.ReindexRepository, repositoryID).Get(ctx, &r)
	return r, err
}

// ReindexRepository reads every trusted manifest and replaces the index.
func (a *Activities) ReindexRepository(ctx context.Context, repositoryID string) (ReindexResult, error) {
	rep, err := a.Maint.Get(ctx, repositoryID)
	if err != nil {
		return ReindexResult{}, err
	}
	found, res, err := readManifests(ctx, rep, func() { activity.RecordHeartbeat(ctx) })
	if err != nil {
		a.Maint.Invalidate(repositoryID)
		return res, err
	}
	resp, err := a.Platform.IndexRecoveryPoints(a.auth(ctx), &controlv1.IndexRecoveryPointsRequest{
		RepositoryId: repositoryID, RecoveryPoints: found, Complete: true})
	if err != nil {
		return res, platformErr(err)
	}
	res.Upserted, res.MarkedMissing = int(resp.Upserted), int(resp.MarkedMissing)
	return res, nil
}

func readManifests(ctx context.Context, rep engine.Repository, beat func()) ([]*controlv1.IndexedRecoveryPoint, ReindexResult, error) {
	var res ReindexResult
	snaps, err := rep.List(ctx, nil, map[string]string{engine.TagKind: "manifest"})
	if err != nil {
		return nil, res, err
	}
	var out []*controlv1.IndexedRecoveryPoint
	seen := map[string]bool{}
	for _, s := range snaps {
		if !trustedManifest(s) || seen[s.Tags[engine.TagRP]] {
			continue
		}
		res.Found++
		rc, err := rep.OpenStream(ctx, s.ID, manifest.ManifestFileName)
		if err != nil {
			return nil, res, err
		}
		var buf bytes.Buffer
		_, err = buf.ReadFrom(rc)
		rc.Close()
		if err != nil {
			return nil, res, err
		}
		m, err := manifest.Parse(buf.Bytes())
		if err == nil {
			err = m.ValidateSources(AgentKopiaUser)
		}
		if err != nil || m.RecoveryPointID != s.Tags[engine.TagRP] {
			res.Invalid++
			continue
		}
		seen[m.RecoveryPointID] = true
		out = append(out, &controlv1.IndexedRecoveryPoint{ManifestJson: buf.Bytes(), ManifestSnapshotId: s.ID})
		beat()
	}
	return out, res, nil
}
