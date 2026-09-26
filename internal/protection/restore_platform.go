// SPDX-License-Identifier: Apache-2.0

package protection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	agentv1 "github.com/AxiomOperator/dbr2/internal/agentpb/agent/v1"
	controlv1 "github.com/AxiomOperator/dbr2/internal/agentpb/control/v1"
	"github.com/AxiomOperator/dbr2/internal/audit"
	"github.com/AxiomOperator/dbr2/internal/events"
	"github.com/AxiomOperator/dbr2/internal/manifest"
	"github.com/AxiomOperator/dbr2/internal/rbac"
	"github.com/AxiomOperator/dbr2/internal/repoclient"
	"github.com/AxiomOperator/dbr2/internal/store"
	"github.com/AxiomOperator/dbr2/workflows/backup"
)

// RestoreLease is the dead-man lease for containers stopped by a restore:
// long enough for large restores; the workflow's saga resumes them first.
const RestoreLease = 24 * 60 * 60

var componentKinds = map[string]agentv1.ComponentKind{
	manifest.KindConfig: agentv1.ComponentKind_COMPONENT_KIND_CONFIG, manifest.KindVolume: agentv1.ComponentKind_COMPONENT_KIND_VOLUME,
	manifest.KindBindMount: agentv1.ComponentKind_COMPONENT_KIND_BIND_MOUNT, manifest.KindDatabase: agentv1.ComponentKind_COMPONENT_KIND_DATABASE,
}

// PrepareRestore re-validates the requested restore against the target's
// current state and returns the plan.
func (p *Platform) PrepareRestore(ctx context.Context, req *controlv1.PrepareRestoreRequest) (*controlv1.PrepareRestoreResponse, error) {
	s := p.s
	run, err := s.q.GetRestoreRunByID(ctx, req.RestoreId)
	if err != nil {
		return nil, grpcErr(err)
	}
	if run.State != "requested" && run.State != "running" {
		return nil, precondition("restore %s is already %s", run.ID, run.State)
	}
	var remaps []PathRemap
	_ = json.Unmarshal(run.PathRemaps, &remaps)
	target := run.TargetAgentID
	rc, err := s.resolveRestore(ctx, s.q, RestoreRequest{RecoveryPointID: run.RecoveryPointID, TargetAgentID: &target,
		Components: run.Components, PathRemaps: remaps})
	if err != nil {
		return nil, grpcErr(err)
	}
	if rc.preview.Blocked {
		b, _ := json.Marshal(rc.preview.Collisions)
		return nil, precondition("the target changed since the restore was requested and it is now blocked: %s", b)
	}
	if _, err := s.q.StartRestoreRun(ctx, store.StartRestoreRunParams{ID: run.ID, WorkflowID: &req.WorkflowId, RunID: &req.RunId}); err != nil {
		return nil, grpcErr(err)
	}
	repo, err := s.q.GetRepositoryByID(ctx, run.RepositoryID)
	if err != nil {
		return nil, grpcErr(err)
	}
	if repo.Status != "ready" {
		return nil, precondition("Repository %s is %s", repo.Name, repo.Status)
	}
	m := rc.m
	out := &controlv1.PrepareRestoreResponse{RestoreId: run.ID, RecoveryPointId: run.RecoveryPointID, Repository: repoMsg(repo),
		SourceAgentId: run.SourceAgentID.String(), TargetAgentId: run.TargetAgentID.String(), CrossHost: run.SourceAgentID != run.TargetAgentID,
		PathRemaps: agentRemaps(remaps), Networks: rc.preview.networkSpecs, StopContainerIds: rc.preview.stopIDs,
		RecreateContainers: len(rc.preview.CreateContainers) > 0, HealthTimeoutSeconds: 300, LeaseSeconds: RestoreLease,
		TargetApplicationId: run.SourceApplicationID.String()}
	if run.TargetApplicationID != nil {
		out.TargetApplicationId = run.TargetApplicationID.String()
	}
	for _, c := range m.Components {
		if c.Kind == manifest.KindConfig && c.Status == manifest.ComponentSucceeded {
			out.ConfigSnapshotId = c.SnapshotID
		}
	}
	vols := map[string]manifest.TopologyVolume{}
	if m.Topology != nil {
		for _, v := range m.Topology.Volumes {
			vols[v.Name] = v
		}
		for _, c := range m.Topology.Containers {
			if c.State == "running" || c.State == "paused" || c.State == "restarting" {
				out.RunningAtCapture = append(out.RunningAtCapture, c.Name)
			}
		}
	}
	for _, c := range rc.selected {
		if c.Kind == manifest.KindDatabase {
			if c.Database == nil {
				return nil, precondition("database component %s has no engine information", c.Name)
			}
			out.Databases = append(out.Databases, &agentv1.RestoreDatabaseCommand{Name: c.Name, SnapshotId: c.SnapshotID, FileName: c.FileName,
				Engine: c.Database.Engine, Format: c.Database.Format, ContainerId: firstNonEmpty(c.Database.Container, c.Database.Service)})
			continue
		}
		spec := &agentv1.RestoreSpec{Name: c.Name, Kind: componentKinds[c.Kind], SnapshotId: c.SnapshotID, FileName: c.FileName,
			Mode: c.Mode, SelinuxContext: c.SELinuxContext}
		if c.OwnerUID != nil {
			spec.OwnerUid = *c.OwnerUID
		}
		if c.OwnerGID != nil {
			spec.OwnerGid = *c.OwnerGID
		}
		if f := fsmetaFor(m, c.Name); f != nil {
			spec.FsmetaSnapshotId = f.SnapshotID
		}
		switch c.Kind {
		case manifest.KindVolume:
			spec.VolumeName = c.VolumeName
			v := vols[c.VolumeName]
			spec.VolumeDriver, spec.VolumeLabels = firstNonEmpty(v.Driver, "local"), v.Labels
		case manifest.KindBindMount:
			spec.TargetPath = Remap(c.Path, remaps)
		}
		out.Components = append(out.Components, spec)
	}
	for _, im := range m.Images {
		d := digestOf(im.Digest)
		out.Images = append(out.Images, &agentv1.ImageSpec{Ref: im.Ref, Digest: d})
	}
	return out, nil
}

func agentRemaps(r []PathRemap) []*agentv1.PathRemap {
	out := make([]*agentv1.PathRemap, 0, len(r))
	for _, x := range r {
		out = append(out, &agentv1.PathRemap{From: x.From, To: x.To})
	}
	return out
}

// GrantRestoreAccess lets the target agent read the source agent's
// snapshots for the duration of a cross-host restore.
func (p *Platform) GrantRestoreAccess(ctx context.Context, req *controlv1.GrantRestoreAccessRequest) (*controlv1.GrantRestoreAccessResponse, error) {
	s := p.s
	run, err := s.q.GetRestoreRunByID(ctx, req.RestoreId)
	if err != nil {
		return nil, grpcErr(err)
	}
	if run.SourceAgentID == run.TargetAgentID {
		return nil, status.Error(codes.InvalidArgument, "not a cross-host restore")
	}
	repo, err := s.q.GetRepositoryByID(ctx, run.RepositoryID)
	if err != nil {
		return nil, grpcErr(err)
	}
	id, err := s.repoClient(repo.ManagementUrl).GrantRead(ctx, backup.AgentKopiaUser+"@"+run.TargetAgentID.String(), backup.AgentKopiaUser, run.SourceAgentID.String())
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "grant read access: %v", err)
	}
	if err := s.q.SetRestoreGrant(ctx, store.SetRestoreGrantParams{ID: run.ID, GrantID: &id}); err != nil {
		return nil, grpcErr(err)
	}
	return &controlv1.GrantRestoreAccessResponse{GrantId: id}, nil
}

// RevokeRestoreAccess removes a grant (idempotent).
func (p *Platform) RevokeRestoreAccess(ctx context.Context, req *controlv1.RevokeRestoreAccessRequest) (*controlv1.RevokeRestoreAccessResponse, error) {
	s := p.s
	run, err := s.q.GetRestoreRunByID(ctx, req.RestoreId)
	if err != nil {
		return nil, grpcErr(err)
	}
	repo, err := s.q.GetRepositoryByID(ctx, run.RepositoryID)
	if err != nil {
		return nil, grpcErr(err)
	}
	if err := s.repoClient(repo.ManagementUrl).RevokeRead(ctx, req.GrantId); err != nil && !isNotFound(err) {
		return nil, status.Errorf(codes.Unavailable, "revoke read access: %v", err)
	}
	return &controlv1.RevokeRestoreAccessResponse{}, nil
}

func isNotFound(err error) bool {
	var e *repoclient.Error
	return errors.As(err, &e) && e.StatusCode == http.StatusNotFound
}

var restoreEvents = map[string]string{"succeeded": audit.RestoreSucceeded, "failed": audit.RestoreFailed, "rolled_back": audit.RestoreRolledBack}

// UpdateRestore records a step or the outcome (audit + alert).
func (p *Platform) UpdateRestore(ctx context.Context, req *controlv1.UpdateRestoreRequest) (*controlv1.UpdateRestoreResponse, error) {
	s := p.s
	defer s.publish(ctx, events.RestoreUpdated, rbac.RestoreRead, map[string]any{"restore_id": req.RestoreId, "state": req.State,
		"step": req.Step, "error": req.Error})
	if req.State == "running" {
		if err := s.q.SetRestoreStep(ctx, store.SetRestoreStepParams{ID: req.RestoreId, Step: &req.Step}); err != nil {
			return nil, grpcErr(err)
		}
		return &controlv1.UpdateRestoreResponse{}, nil
	}
	typ, ok := restoreEvents[req.State]
	if !ok {
		return nil, status.Errorf(codes.InvalidArgument, "state %q", req.State)
	}
	var result []byte
	if len(req.ResultJson) > 0 && json.Valid(req.ResultJson) {
		result = req.ResultJson
	}
	err := s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		run, err := q.FinishRestoreRun(ctx, store.FinishRestoreRunParams{ID: req.RestoreId, State: req.State, Result: result, Error: strPtr(req.Error)})
		if errors.Is(err, errNoRows) {
			return nil // already finished (retried activity)
		}
		if err != nil {
			return err
		}
		outcome := audit.Success
		if req.State != "succeeded" {
			outcome = audit.Failure
		}
		details := map[string]any{"restore_id": run.ID, "recovery_point_id": run.RecoveryPointID, "target_host": run.TargetHostname,
			"mode": run.Mode, "error": req.Error, "duration_seconds": durationSeconds(run)}
		if _, err := rec.Record(ctx, s.systemEvent(typ, "application", run.SourceApplicationID.String(), outcome, details)); err != nil {
			return err
		}
		switch req.State {
		case "failed":
			return s.alert(ctx, q, "critical", typ, "application", run.SourceApplicationID.String(),
				fmt.Sprintf("Restore of %s to %s failed: %s", run.ApplicationName, run.TargetHostname, req.Error), details)
		case "rolled_back":
			return s.alert(ctx, q, "warning", typ, "application", run.SourceApplicationID.String(),
				fmt.Sprintf("Restore of %s to %s was rolled back (the previous data is back in place): %s", run.ApplicationName, run.TargetHostname, req.Error), details)
		}
		return s.alert(ctx, q, "info", typ, "application", run.SourceApplicationID.String(),
			fmt.Sprintf("Restore of %s to %s succeeded", run.ApplicationName, run.TargetHostname), details)
	})
	if err != nil {
		return nil, grpcErr(err)
	}
	return &controlv1.UpdateRestoreResponse{}, nil
}

func durationSeconds(r store.RestoreRun) string {
	if r.StartedAt == nil || r.FinishedAt == nil {
		return ""
	}
	return strconv.FormatFloat(r.FinishedAt.Sub(*r.StartedAt).Seconds(), 'f', 0, 64)
}
