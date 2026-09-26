// SPDX-License-Identifier: Apache-2.0

package protection

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	agentv1 "github.com/AxiomOperator/dbr2/internal/agentpb/agent/v1"
	controlv1 "github.com/AxiomOperator/dbr2/internal/agentpb/control/v1"
	"github.com/AxiomOperator/dbr2/internal/audit"
	"github.com/AxiomOperator/dbr2/internal/events"
	"github.com/AxiomOperator/dbr2/internal/fleet"
	"github.com/AxiomOperator/dbr2/internal/gateway"
	"github.com/AxiomOperator/dbr2/internal/manifest"
	"github.com/AxiomOperator/dbr2/internal/rbac"
	"github.com/AxiomOperator/dbr2/internal/store"
	"github.com/AxiomOperator/dbr2/workflows/backup"
)

// Platform implements controlv1.PlatformServiceServer for dbr2-worker. It
// is registered on the internal control listener (internal token).
type Platform struct {
	controlv1.UnimplementedPlatformServiceServer
	s *Service
}

// Platform returns the gRPC service.
func (s *Service) Platform() *Platform { return &Platform{s: s} }

func grpcErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotFound), errors.Is(err, fleet.ErrNotFound), errors.Is(err, pgx.ErrNoRows):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, ErrInvalid):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, ErrConflict), errors.Is(err, gateway.ErrAgentNotActive):
		return status.Error(codes.FailedPrecondition, err.Error())
	}
	return status.Error(codes.Internal, err.Error())
}

func precondition(format string, a ...any) error {
	return status.Errorf(codes.FailedPrecondition, format, a...)
}

func repoMsg(r store.Repository) *controlv1.Repository {
	return &controlv1.Repository{Id: r.ID.String(), Name: r.Name, ServerUrl: r.ServerUrl, CertSha256: r.CertSha256,
		ManagementUrl: r.ManagementUrl, Status: r.Status, InternalServerUrl: r.InternalServerUrl}
}

// PrepareBackup validates and records a pending recovery point. Retries of
// the same workflow run return the same recovery point.
func (p *Platform) PrepareBackup(ctx context.Context, req *controlv1.PrepareBackupRequest) (*controlv1.PrepareBackupResponse, error) {
	s := p.s
	appID, err := uuid.Parse(req.ApplicationId)
	if err != nil || req.WorkflowId == "" || req.RunId == "" {
		return nil, status.Error(codes.InvalidArgument, "application_id, workflow_id and run_id are required")
	}
	app, err := s.fleet.GetApplication(ctx, appID)
	if err != nil {
		return nil, grpcErr(err)
	}
	agent, err := s.fleet.GetAgent(ctx, app.Record.AgentID)
	if err != nil {
		return nil, grpcErr(err)
	}
	if agent.Status != "active" {
		return nil, precondition("host %s is %s", agent.Hostname, agent.Status)
	}
	set, err := s.backupSettings(ctx, s.q, appID)
	if err != nil {
		return nil, grpcErr(err)
	}
	pol := s.policyFor(ctx, s.q, app.Record.PolicyID)
	var repo store.Repository
	switch {
	case set.RepositoryID != nil:
		repo, err = s.q.GetRepositoryByID(ctx, *set.RepositoryID)
	case pol != nil && pol.RepositoryID != nil:
		repo, err = s.q.GetRepositoryByID(ctx, *pol.RepositoryID)
	default:
		repo, err = s.q.GetDefaultRepository(ctx, s.opts.OrgID)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, precondition("no Repository is assigned to the application and no default Repository exists")
	}
	if err != nil {
		return nil, grpcErr(err)
	}
	if repo.Status != "ready" {
		return nil, precondition("Repository %s is %s (confirm its key escrow first)", repo.Name, strings.ReplaceAll(repo.Status, "_", " "))
	}
	mode := set.EffectiveMode()
	if set.ConsistencyMode == nil && pol != nil && pol.ConsistencyMode != nil {
		mode = *pol.ConsistencyMode
	}
	if req.ConsistencyMode != "" {
		mode = req.ConsistencyMode
	}
	pl, err := buildPlan(app, agent, set)
	if err != nil {
		return nil, precondition("%v", err)
	}
	if mode != manifest.ModeLive && len(pl.containerIDs) == 0 {
		return nil, precondition("nothing to quiesce")
	}

	rp, err := s.q.GetPendingRecoveryPointForRun(ctx, store.GetPendingRecoveryPointForRunParams{WorkflowID: req.WorkflowId, RunID: req.RunId})
	if errors.Is(err, pgx.ErrNoRows) {
		var by *uuid.UUID
		if u, err := uuid.Parse(req.RequestedBy); err == nil {
			by = &u
		}
		trigger := req.Trigger
		if trigger == "" {
			trigger = "manual"
		}
		rp, err = s.q.CreateRecoveryPoint(ctx, store.CreateRecoveryPointParams{ID: manifest.NewRecoveryPointID(s.now()), OrgID: s.opts.OrgID,
			RepositoryID: repo.ID, ApplicationID: appID, ApplicationName: displayName(app.Record), AgentID: agent.ID, Hostname: agent.Hostname,
			ConsistencyMode: mode, Trigger: trigger, RequestedBy: by, WorkflowID: req.WorkflowId, RunID: req.RunId})
	}
	if err != nil {
		return nil, grpcErr(err)
	}

	s.publish(ctx, events.BackupUpdated, rbac.BackupRead, map[string]any{"recovery_point_id": rp.ID, "application_id": appID.String(),
		"state": rp.State, "workflow_id": req.WorkflowId})
	var wait uint32
	if req.Trigger == "scheduled" {
		hs, err := s.hostSettings(ctx, s.q, agent.ID)
		if err != nil {
			return nil, grpcErr(err)
		}
		wait = uint32(hs.WaitForWindow(s.now()).Seconds())
	}
	return &controlv1.PrepareBackupResponse{
		RecoveryPointId: rp.ID, AgentId: agent.ID.String(), Repository: repoMsg(repo), ApplicationName: displayName(app.Record),
		ConsistencyMode: mode, MaxQuiesceSeconds: uint32(set.MaxQuiesceSeconds), Components: pl.components, ContainerIds: pl.containerIDs,
		PreHooks: pl.pre, PostHooks: pl.post, SeedComponents: seedComponents(mode, pl.components, s.lastManifest(ctx, appID)),
		ApplicationJson: pl.seed, HostId: agent.ID.String(), Hostname: agent.Hostname, WaitForWindowSeconds: wait,
		ContractJson: s.contractJSON(ctx, appID),
	}, nil
}

// EnsureAgentAccess creates the agent's Kopia user (agent@<agent_id>) with a
// fresh password and hands it to the agent over its mTLS session. The
// password is not stored by dbr2-server.
func (p *Platform) EnsureAgentAccess(ctx context.Context, req *controlv1.EnsureAgentAccessRequest) (*controlv1.EnsureAgentAccessResponse, error) {
	s := p.s
	agentID, err1 := uuid.Parse(req.AgentId)
	repoID, err2 := uuid.Parse(req.RepositoryId)
	if err1 != nil || err2 != nil || req.CommandId == "" {
		return nil, status.Error(codes.InvalidArgument, "agent_id, repository_id and command_id are required")
	}
	out := &controlv1.EnsureAgentAccessResponse{Username: backup.AgentKopiaUser, Hostname: agentID.String()}
	if _, err := s.q.GetAgentRepositoryAccess(ctx, store.GetAgentRepositoryAccessParams{AgentID: agentID, RepositoryID: repoID}); err == nil {
		return out, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, grpcErr(err)
	}
	repo, err := s.q.GetRepositoryByID(ctx, repoID)
	if err != nil {
		return nil, grpcErr(err)
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, grpcErr(err)
	}
	pw := base64.RawURLEncoding.EncodeToString(b)
	user := out.Username + "@" + out.Hostname
	if err := s.repoClient(repo.ManagementUrl).SetUser(ctx, user, pw); err != nil {
		return nil, status.Errorf(codes.Unavailable, "reposerver %s: %v", repo.Name, err)
	}
	// A fresh command ID per attempt: a retried attempt uses a new password,
	// so the agent must not answer with a journaled earlier result.
	cmd := &agentv1.Command{CommandId: req.CommandId + "/" + uuid.NewString()[:8], Kind: &agentv1.Command_ConfigureRepository{
		ConfigureRepository: &agentv1.ConfigureRepositoryCommand{RepositoryId: repo.ID.String(), ServerUrl: repo.ServerUrl,
			CertSha256: repo.CertSha256, Username: out.Username, Hostname: out.Hostname, Password: pw}}}
	final, err := s.gw.Dispatch(ctx, agentID.String(), cmd, nil)
	if err != nil {
		if errors.Is(err, gateway.ErrAgentNotActive) {
			return nil, grpcErr(err)
		}
		return nil, status.Errorf(codes.Unavailable, "configure agent: %v", err)
	}
	if final.State != agentv1.CommandState_COMMAND_STATE_SUCCEEDED {
		code := codes.Unavailable
		if !final.Retryable {
			code = codes.FailedPrecondition
		}
		return nil, status.Errorf(code, "agent could not connect to Repository %s: %s", repo.Name, final.Error)
	}
	if err := s.q.UpsertAgentRepositoryAccess(ctx, store.UpsertAgentRepositoryAccessParams{AgentID: agentID, RepositoryID: repoID,
		Username: out.Username, Hostname: out.Hostname}); err != nil {
		return nil, grpcErr(err)
	}
	return out, nil
}

// CompleteBackup records a committed or failed backup.
func (p *Platform) CompleteBackup(ctx context.Context, req *controlv1.CompleteBackupRequest) (*controlv1.CompleteBackupResponse, error) {
	s := p.s
	rp, err := s.q.GetRecoveryPointByID(ctx, req.RecoveryPointId)
	if err != nil {
		return nil, grpcErr(err)
	}
	switch req.Outcome {
	case "committed":
		m, err := manifest.Parse(req.ManifestJson)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		if m.RecoveryPointID != rp.ID || m.Source.AgentID != rp.AgentID.String() {
			return nil, status.Error(codes.InvalidArgument, "manifest does not match the recovery point")
		}
		err = s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
			if _, err := q.CommitRecoveryPoint(ctx, commitParams(rp.ID, m, req.ManifestJson, req.ManifestSnapshotId)); err != nil {
				return err
			}
			_, err := rec.Record(ctx, s.systemEvent(audit.BackupCompleted, "application", rp.ApplicationID.String(), audit.Success,
				map[string]any{"recovery_point_id": rp.ID, "status": m.Status, "consistency_mode": m.ConsistencyMode,
					"components": len(m.Components), "size_bytes": sizeOf(m), "manifest_snapshot_id": req.ManifestSnapshotId}))
			if err == nil && m.Status == manifest.StatusPartial {
				err = s.alert(ctx, q, "warning", audit.BackupCompleted, "application", rp.ApplicationID.String(),
					fmt.Sprintf("Backup of %s committed as Partial: optional components failed", rp.ApplicationName), map[string]any{"recovery_point_id": rp.ID})
			}
			return err
		})
	case "failed":
		err = s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
			if _, err := q.FailRecoveryPoint(ctx, store.FailRecoveryPointParams{ID: rp.ID, Error: &req.Error}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			if _, err := rec.Record(ctx, s.systemEvent(audit.BackupFailed, "application", rp.ApplicationID.String(), audit.Failure,
				map[string]any{"recovery_point_id": rp.ID, "error": req.Error})); err != nil {
				return err
			}
			return s.alert(ctx, q, "critical", audit.BackupFailed, "application", rp.ApplicationID.String(),
				fmt.Sprintf("Backup of %s failed: %s", rp.ApplicationName, req.Error), map[string]any{"recovery_point_id": rp.ID})
		})
	default:
		return nil, status.Error(codes.InvalidArgument, "outcome must be committed or failed")
	}
	if err != nil {
		return nil, grpcErr(err)
	}
	state := "committed"
	if req.Outcome == "failed" {
		state = "failed"
	}
	s.publish(ctx, events.BackupUpdated, rbac.BackupRead, map[string]any{"recovery_point_id": rp.ID, "application_id": rp.ApplicationID.String(),
		"state": state, "error": req.Error})
	return &controlv1.CompleteBackupResponse{}, nil
}

func sizeOf(m *manifest.Manifest) int64 {
	var n int64
	for _, c := range m.Components {
		n += c.SizeBytes
	}
	return n
}

func commitParams(id string, m *manifest.Manifest, raw []byte, snapID string) store.CommitRecoveryPointParams {
	cp := m.ConsistencyPoint
	return store.CommitRecoveryPointParams{ID: id, Status: &m.Status, ConsistencyMode: m.ConsistencyMode, ConsistencyPoint: &cp,
		CrashConsistentOnly: m.CrashConsistentOnly, SizeBytes: sizeOf(m), ComponentCount: int32(len(m.Components)),
		Manifest: raw, ManifestSnapshotID: &snapID}
}

// GetRepository returns connection details.
func (p *Platform) GetRepository(ctx context.Context, req *controlv1.GetRepositoryRequest) (*controlv1.GetRepositoryResponse, error) {
	id, err := uuid.Parse(req.RepositoryId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "repository_id")
	}
	r, err := p.s.q.GetRepositoryByID(ctx, id)
	if err != nil {
		return nil, grpcErr(err)
	}
	return &controlv1.GetRepositoryResponse{Repository: repoMsg(r)}, nil
}

// ListRepositories returns every Repository that is not retired.
func (p *Platform) ListRepositories(ctx context.Context, _ *controlv1.ListRepositoriesRequest) (*controlv1.ListRepositoriesResponse, error) {
	rows, err := p.s.q.ListRepositories(ctx, p.s.opts.OrgID)
	if err != nil {
		return nil, grpcErr(err)
	}
	out := &controlv1.ListRepositoriesResponse{}
	for _, r := range rows {
		if r.Status != "retired" {
			out.Repositories = append(out.Repositories, repoMsg(r))
		}
	}
	return out, nil
}

// IndexRecoveryPoints upserts reindexed recovery points (the Repository
// wins; ADR-0003).
func (p *Platform) IndexRecoveryPoints(ctx context.Context, req *controlv1.IndexRecoveryPointsRequest) (*controlv1.IndexRecoveryPointsResponse, error) {
	s := p.s
	repoID, err := uuid.Parse(req.RepositoryId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "repository_id")
	}
	out := &controlv1.IndexRecoveryPointsResponse{}
	err = s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		var ids []string
		for _, irp := range req.RecoveryPoints {
			m, err := manifest.Parse(irp.ManifestJson)
			if err != nil {
				return fmt.Errorf("%w: %v", ErrInvalid, err)
			}
			if err := m.ValidateSources(backup.AgentKopiaUser); err != nil {
				return fmt.Errorf("%w: %v", ErrInvalid, err)
			}
			appID, err1 := uuid.Parse(m.Application.ID)
			agentID, err2 := uuid.Parse(m.Source.AgentID)
			if err1 != nil || err2 != nil {
				return fmt.Errorf("%w: manifest %s has non-UUID identities", ErrInvalid, m.RecoveryPointID)
			}
			cp := m.ConsistencyPoint
			snap := irp.ManifestSnapshotId
			if err := q.UpsertIndexedRecoveryPoint(ctx, store.UpsertIndexedRecoveryPointParams{ID: m.RecoveryPointID, OrgID: s.opts.OrgID,
				RepositoryID: repoID, ApplicationID: appID, ApplicationName: m.Application.Name, AgentID: agentID, Hostname: m.Source.Hostname,
				Status: &m.Status, ConsistencyMode: m.ConsistencyMode, ConsistencyPoint: &cp, CrashConsistentOnly: m.CrashConsistentOnly,
				Trigger: firstNonEmpty(m.Workflow.Trigger, "manual"), WorkflowID: m.Workflow.WorkflowID, RunID: m.Workflow.RunID,
				SizeBytes: sizeOf(m), ComponentCount: int32(len(m.Components)), Manifest: irp.ManifestJson, ManifestSnapshotID: &snap,
				CreatedAt: m.CreatedAt}); err != nil {
				return err
			}
			ids = append(ids, m.RecoveryPointID)
			out.Upserted++
		}
		if req.Complete {
			if ids == nil {
				ids = []string{}
			}
			n, err := q.MarkMissingRecoveryPoints(ctx, store.MarkMissingRecoveryPointsParams{RepositoryID: repoID, PresentIds: ids})
			if err != nil {
				return err
			}
			out.MarkedMissing = uint32(n)
			if err := q.SetRepositoryReindexed(ctx, repoID); err != nil {
				return err
			}
		}
		_, err := rec.Record(ctx, s.systemEvent(audit.RepositoryReindexed, "repository", repoID.String(), audit.Success,
			map[string]any{"upserted": out.Upserted, "marked_missing": out.MarkedMissing, "complete": req.Complete}))
		if err == nil && out.MarkedMissing > 0 {
			err = s.alert(ctx, q, "critical", audit.RepositoryReindexed, "repository", repoID.String(),
				fmt.Sprintf("%d indexed recovery points have no manifest in the Repository and were marked missing", out.MarkedMissing), nil)
		}
		return err
	})
	if err != nil {
		return nil, grpcErr(err)
	}
	return out, nil
}

var workerEvents = map[string]bool{audit.ApplicationNotResumed: true, audit.BackupSkipped: true}

// RecordEvent records an audit event (and alert) for the worker.
func (p *Platform) RecordEvent(ctx context.Context, req *controlv1.RecordEventRequest) (*controlv1.RecordEventResponse, error) {
	s := p.s
	if !workerEvents[req.Type] {
		return nil, status.Errorf(codes.InvalidArgument, "event type %q is not accepted from the worker", req.Type)
	}
	var details map[string]any
	_ = json.Unmarshal(req.Details, &details)
	result := audit.Success
	if req.Outcome == "failure" {
		result = audit.Failure
	}
	err := s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		if _, err := rec.Record(ctx, s.systemEvent(req.Type, req.TargetType, req.TargetId, result, details)); err != nil {
			return err
		}
		if req.AlertSeverity == "" {
			return nil
		}
		msg := req.Type
		if req.Type == audit.BackupSkipped {
			msg = "Scheduled backup skipped: another backup or restore of the application was still running"
		}
		if req.Type == audit.ApplicationNotResumed {
			msg = "Needs attention: the application was not resumed after a backup. Check its containers now; the agent's quiesce lease resumes it when it expires."
		}
		return s.alert(ctx, q, req.AlertSeverity, req.Type, req.TargetType, req.TargetId, msg, details)
	})
	if err != nil {
		return nil, grpcErr(err)
	}
	return &controlv1.RecordEventResponse{}, nil
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}
