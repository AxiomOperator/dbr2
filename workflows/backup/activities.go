// SPDX-License-Identifier: Apache-2.0

package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	agentv1 "github.com/AxiomOperator/dbr2/internal/agentpb/agent/v1"
	controlv1 "github.com/AxiomOperator/dbr2/internal/agentpb/control/v1"
	"github.com/AxiomOperator/dbr2/internal/engine"
	"github.com/AxiomOperator/dbr2/internal/manifest"
	"github.com/AxiomOperator/dbr2/internal/version"
	"github.com/AxiomOperator/dbr2/workflows/agentcmd"
)

// AgentKopiaUser is the Kopia username of every agent; the Kopia hostname
// is the agent ID (ADR-0002: per-agent identity agent@<agent_id>).
const AgentKopiaUser = "agent"

// Activities implement the backup workflow steps.
type Activities struct {
	Agent    *agentcmd.Dispatcher
	Platform controlv1.PlatformServiceClient
	Token    string
	Maint    *MaintSessions
}

// SnapshotInput is the SnapshotComponents command input.
type SnapshotInput struct {
	AgentID         string
	RepositoryID    string
	RecoveryPointID string
	ApplicationID   string
	Components      []*agentv1.ComponentSpec
	Seed            bool
}

// QuiesceInput is the Quiesce command input.
type QuiesceInput struct {
	AgentID       string
	LeaseID       string
	ApplicationID string
	ContainerIDs  []string
	Mode          agentv1.QuiesceMode
	LeaseSeconds  uint32
}

// CommitInput builds the manifest.
type CommitInput struct {
	Plan             *controlv1.PrepareBackupResponse
	Results          []*agentv1.ComponentResult
	WorkflowID       string
	RunID            string
	Trigger          string
	StartedAt        time.Time
	QuiesceStartedAt *time.Time
	QuiesceEndedAt   *time.Time
}

// CommitResult describes the written manifest.
type CommitResult struct {
	ManifestJSON []byte
	SnapshotID   string
	Status       string
	Components   int
	SizeBytes    int64
}

func (a *Activities) auth(ctx context.Context) context.Context {
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+a.Token)
}

// platformErr makes permanent server answers non-retryable.
func platformErr(err error) error {
	switch status.Code(err) {
	case codes.InvalidArgument, codes.FailedPrecondition, codes.NotFound, codes.PermissionDenied, codes.AlreadyExists:
		return temporal.NewNonRetryableApplicationError(status.Convert(err).Message(), status.Code(err).String(), err)
	}
	return err
}

func deadline(ctx context.Context) int64 {
	if d, ok := ctx.Deadline(); ok {
		return d.UnixMilli()
	}
	return 0
}

// PrepareBackup records a pending recovery point and returns the plan.
func (a *Activities) PrepareBackup(ctx context.Context, in Input) (*controlv1.PrepareBackupResponse, error) {
	info := activity.GetInfo(ctx)
	resp, err := a.Platform.PrepareBackup(a.auth(ctx), &controlv1.PrepareBackupRequest{
		ApplicationId: in.ApplicationID, WorkflowId: info.WorkflowExecution.ID, RunId: info.WorkflowExecution.RunID,
		Trigger: in.Trigger, RequestedBy: in.RequestedBy, ConsistencyMode: in.ConsistencyMode})
	return resp, platformErr(err)
}

// EnsureAgentAccess makes sure the agent can write to the Repository.
func (a *Activities) EnsureAgentAccess(ctx context.Context, agentID, repositoryID string) error {
	_, err := a.Platform.EnsureAgentAccess(a.auth(ctx), &controlv1.EnsureAgentAccessRequest{
		AgentId: agentID, RepositoryId: repositoryID, CommandId: agentcmd.CommandID(ctx)})
	return platformErr(err)
}

// CompleteBackup records the outcome.
func (a *Activities) CompleteBackup(ctx context.Context, req *controlv1.CompleteBackupRequest) error {
	_, err := a.Platform.CompleteBackup(a.auth(ctx), req)
	return platformErr(err)
}

// RecordEvent records an audit event / alert.
func (a *Activities) RecordEvent(ctx context.Context, req *controlv1.RecordEventRequest) error {
	_, err := a.Platform.RecordEvent(a.auth(ctx), req)
	return platformErr(err)
}

// RunHooks runs pre or post hooks on the agent.
func (a *Activities) RunHooks(ctx context.Context, agentID, phase string, hooks []*agentv1.Hook) (*agentv1.RunHooksResult, error) {
	u, err := a.Agent.Dispatch(ctx, agentID, &agentv1.Command{CommandId: agentcmd.CommandID(ctx), DeadlineUnixMs: deadline(ctx),
		Kind: &agentv1.Command_RunHooks{RunHooks: &agentv1.RunHooksCommand{Phase: phase, Hooks: hooks}}}, nil)
	if err != nil {
		return nil, err
	}
	return u.GetRunHooks(), nil
}

// Quiesce quiesces the application and arms the agent's dead-man switch.
func (a *Activities) Quiesce(ctx context.Context, in QuiesceInput) (*agentv1.QuiesceResult, error) {
	u, err := a.Agent.Dispatch(ctx, in.AgentID, &agentv1.Command{CommandId: agentcmd.CommandID(ctx), DeadlineUnixMs: deadline(ctx),
		Kind: &agentv1.Command_Quiesce{Quiesce: &agentv1.QuiesceCommand{LeaseId: in.LeaseID, ApplicationId: in.ApplicationID,
			ContainerIds: in.ContainerIDs, Mode: in.Mode, LeaseSeconds: in.LeaseSeconds}}}, nil)
	if err != nil {
		return nil, err
	}
	return u.GetQuiesce(), nil
}

// Resume restores the pre-quiesce state (idempotent on the agent).
func (a *Activities) Resume(ctx context.Context, agentID, leaseID string) (*agentv1.ResumeResult, error) {
	u, err := a.Agent.Dispatch(ctx, agentID, &agentv1.Command{CommandId: agentcmd.CommandID(ctx), DeadlineUnixMs: deadline(ctx),
		Kind: &agentv1.Command_Resume{Resume: &agentv1.ResumeCommand{LeaseId: leaseID}}}, nil)
	if err != nil {
		return nil, err
	}
	return u.GetResume(), nil
}

// Snapshot captures components on the agent.
func (a *Activities) Snapshot(ctx context.Context, in SnapshotInput) (*agentv1.SnapshotComponentsResult, error) {
	u, err := a.Agent.Dispatch(ctx, in.AgentID, &agentv1.Command{CommandId: agentcmd.CommandID(ctx), DeadlineUnixMs: deadline(ctx),
		Kind: &agentv1.Command_SnapshotComponents{SnapshotComponents: &agentv1.SnapshotComponentsCommand{
			RepositoryId: in.RepositoryID, RecoveryPointId: in.RecoveryPointID, ApplicationId: in.ApplicationID,
			Components: in.Components, Seed: in.Seed}}},
		func(u *agentv1.CommandUpdate) { activity.RecordHeartbeat(ctx, json.RawMessage(u.Progress)) })
	if err != nil {
		return nil, err
	}
	return u.GetSnapshotComponents(), nil
}

// ManifestSeed is PrepareBackupResponse.application_json: the parts of the
// manifest the server knows.
type ManifestSeed struct {
	Application manifest.Application `json:"application"`
	Source      manifest.Source      `json:"source"`
	Images      []manifest.Image     `json:"images,omitempty"`
}

// BuildManifest assembles the manifest from the plan and component results.
func BuildManifest(in CommitInput, now time.Time) (*manifest.Manifest, error) {
	var seed ManifestSeed
	if err := json.Unmarshal(in.Plan.ApplicationJson, &seed); err != nil {
		return nil, fmt.Errorf("plan application_json: %w", err)
	}
	m := &manifest.Manifest{
		SchemaVersion: manifest.SchemaVersion, RecoveryPointID: in.Plan.RecoveryPointId, CreatedAt: now.UTC(),
		ConsistencyMode: in.Plan.ConsistencyMode, QuiesceStartedAt: in.QuiesceStartedAt, QuiesceEndedAt: in.QuiesceEndedAt,
		Application: seed.Application, Source: seed.Source, Images: seed.Images,
		Repository: manifest.RepositoryRef{ID: in.Plan.Repository.GetId(), Name: in.Plan.Repository.GetName()},
		Workflow:   manifest.Workflow{WorkflowID: in.WorkflowID, RunID: in.RunID, Trigger: in.Trigger},
		Producer:   manifest.Producer{Component: string(version.Worker), Version: version.Of(version.Worker)},
	}
	m.ConsistencyPoint = in.StartedAt.UTC()
	if in.Plan.ConsistencyMode == manifest.ModeLive {
		m.CrashConsistentOnly = true
	} else if in.QuiesceStartedAt != nil {
		m.ConsistencyPoint = in.QuiesceStartedAt.UTC()
	}
	for _, r := range in.Results {
		c := manifest.Component{Name: r.Name, Kind: kindName(r.Kind), Required: r.Required, Status: r.Status, Error: r.Error,
			SnapshotID: r.SnapshotId, RootObjectID: r.RootObjectId, SnapshotSource: r.Source, SizeBytes: r.SizeBytes, Files: r.Files,
			StartedAt: time.UnixMilli(r.StartedUnixMs).UTC(), FinishedAt: time.UnixMilli(r.FinishedUnixMs).UTC(),
			Path: r.Path, VolumeName: r.VolumeName, Mode: r.Mode, SELinuxContext: r.SelinuxContext, Parent: r.Parent, CaptureMethod: r.CaptureMethod}
		if c.Kind == manifest.KindVolume || c.Kind == manifest.KindBindMount {
			uid, gid := r.OwnerUid, r.OwnerGid
			c.OwnerUID, c.OwnerGID = &uid, &gid
		}
		m.Components = append(m.Components, c)
	}
	st, ok := manifest.StatusFor(m.Components)
	if !ok {
		return nil, errors.New("a required component did not succeed")
	}
	m.Status = st
	if err := m.Validate(); err != nil {
		return nil, err
	}
	if err := m.ValidateSources(AgentKopiaUser); err != nil {
		return nil, err
	}
	return m, nil
}

func kindName(k agentv1.ComponentKind) string {
	switch k {
	case agentv1.ComponentKind_COMPONENT_KIND_CONFIG:
		return manifest.KindConfig
	case agentv1.ComponentKind_COMPONENT_KIND_VOLUME:
		return manifest.KindVolume
	case agentv1.ComponentKind_COMPONENT_KIND_BIND_MOUNT:
		return manifest.KindBindMount
	case agentv1.ComponentKind_COMPONENT_KIND_DATABASE:
		return manifest.KindDatabase
	case agentv1.ComponentKind_COMPONENT_KIND_IMAGE:
		return manifest.KindImage
	case agentv1.ComponentKind_COMPONENT_KIND_FSMETA:
		return manifest.KindFSMeta
	}
	return "unknown"
}

// Commit validates every component against the Repository and writes the
// manifest last, as maint@dbr2 (ADR-0004). Idempotent: a manifest already
// written for the recovery point is returned.
func (a *Activities) Commit(ctx context.Context, in CommitInput) (CommitResult, error) {
	m, err := BuildManifest(in, time.Now())
	if err != nil {
		return CommitResult{}, temporal.NewNonRetryableApplicationError(err.Error(), "ManifestInvalid", err)
	}
	rep, err := a.Maint.Get(ctx, in.Plan.Repository.GetId())
	if err != nil {
		return CommitResult{}, err
	}
	res, err := commitWith(ctx, rep, m)
	if err != nil && !isPermanent(err) {
		a.Maint.Invalidate(in.Plan.Repository.GetId())
	}
	return res, err
}

func isPermanent(err error) bool {
	var ae *temporal.ApplicationError
	return errors.As(err, &ae) && ae.NonRetryable()
}

func commitWith(ctx context.Context, rep engine.Repository, m *manifest.Manifest) (CommitResult, error) {
	src := engine.Source{User: manifest.ManifestSourceUser, Host: manifest.ManifestSourceHost, Path: manifest.ManifestPath(m.Application.ID)}
	existing, err := rep.List(ctx, &src, map[string]string{engine.TagRP: m.RecoveryPointID, engine.TagKind: "manifest"})
	if err != nil {
		return CommitResult{}, err
	}
	var size int64
	for _, c := range m.Components {
		size += c.SizeBytes
		if c.Status != manifest.ComponentSucceeded {
			continue
		}
		s, err := rep.Get(ctx, c.SnapshotID)
		if err != nil {
			return CommitResult{}, fmt.Errorf("component %s: snapshot %s: %w", c.Name, c.SnapshotID, err)
		}
		if s.Source.User != AgentKopiaUser || s.Source.Host != m.Source.AgentID || s.Source.String() != c.SnapshotSource ||
			s.Tags[engine.TagRP] != m.RecoveryPointID || s.Tags[engine.TagComponent] != c.Name || s.Incomplete != "" {
			e := fmt.Errorf("component %s: snapshot %s (source %s, tags %v) does not belong to recovery point %s of agent %s",
				c.Name, c.SnapshotID, s.Source, s.Tags, m.RecoveryPointID, m.Source.AgentID)
			return CommitResult{}, temporal.NewNonRetryableApplicationError(e.Error(), ErrSourceMismatch, e)
		}
	}
	out := CommitResult{Status: m.Status, Components: len(m.Components), SizeBytes: size}
	if len(existing) > 0 {
		rc, err := rep.OpenStream(ctx, existing[0].ID, manifest.ManifestFileName)
		if err != nil {
			return CommitResult{}, err
		}
		defer rc.Close()
		var buf bytes.Buffer
		if _, err := buf.ReadFrom(rc); err != nil {
			return CommitResult{}, err
		}
		prev, err := manifest.Parse(buf.Bytes())
		if err != nil {
			return CommitResult{}, err
		}
		out.ManifestJSON, out.SnapshotID, out.Status = buf.Bytes(), existing[0].ID, prev.Status
		return out, nil
	}
	b, err := m.Marshal()
	if err != nil {
		return CommitResult{}, err
	}
	s, err := rep.SnapshotStream(ctx, manifest.ManifestFileName, bytes.NewReader(b), engine.SnapshotRequest{
		Source: src, Pins: []string{engine.Pin}, Description: "DBR² recovery manifest " + m.RecoveryPointID,
		Tags: map[string]string{engine.TagRP: m.RecoveryPointID, engine.TagApp: m.Application.ID, engine.TagKind: "manifest"},
	})
	if err != nil {
		return CommitResult{}, err
	}
	out.ManifestJSON, out.SnapshotID = b, s.ID
	return out, nil
}

func jsonDetails(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
