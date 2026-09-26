// SPDX-License-Identifier: Apache-2.0

// Package restore implements the restore workflow (Phase 5; ADR-0006,
// ADR-0005, ADR-0014). It claims application/<id>, so it never overlaps a
// backup or another restore (ADR-0011). Data is restored into staging,
// verified and swapped in with the previous content kept; only a healthy
// application commits the restore, any failure rolls it back.
package restore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	agentv1 "github.com/AxiomOperator/dbr2/internal/agentpb/agent/v1"
	controlv1 "github.com/AxiomOperator/dbr2/internal/agentpb/control/v1"
	"github.com/AxiomOperator/dbr2/workflows/agentcmd"
	"github.com/AxiomOperator/dbr2/workflows/backup"
	"github.com/AxiomOperator/dbr2/workflows/saga"
)

// Input identifies the requested restore (recorded by dbr2-server).
type Input struct {
	RestoreID string
}

// Result is the result document stored with the restore.
type Result struct {
	Images     []*agentv1.ImageResult            `json:"images,omitempty"`
	Components []*agentv1.RestoreComponentResult `json:"components,omitempty"`
	Containers []*agentv1.RecreatedContainer     `json:"containers,omitempty"`
	Databases  []string                          `json:"databases,omitempty"`
	Health     *agentv1.CheckHealthResult        `json:"health,omitempty"`
	RolledBack bool                              `json:"rolled_back,omitempty"`
	Rollback   string                            `json:"rollback_error,omitempty"`
}

// ErrUnhealthy is the error type for a restored application that did not
// become healthy.
const ErrUnhealthy = "RestoredApplicationUnhealthy"

// RestoreWorkflow restores a recovery point. No workflow-level timeouts: they skip
// compensation (ADR-0005).
func RestoreWorkflow(ctx workflow.Context, in Input) (res Result, err error) {
	var a *Activities
	var ba *backup.Activities
	short := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{InitialInterval: 2 * time.Second, MaximumAttempts: 10,
			NonRetryableErrorTypes: []string{"InvalidArgument", "FailedPrecondition", "NotFound"}},
	})
	step := func(name string) {
		_ = workflow.ExecuteActivity(short, a.UpdateRestore, &controlv1.UpdateRestoreRequest{RestoreId: in.RestoreID, State: "running", Step: name}).Get(ctx, nil)
	}
	var plan *controlv1.PrepareRestoreResponse
	if err := workflow.ExecuteActivity(short, a.PrepareRestore, in.RestoreID).Get(ctx, &plan); err != nil {
		finish(ctx, a, in.RestoreID, "failed", res, err)
		return res, err
	}

	agentOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 15 * time.Minute, HeartbeatTimeout: 2 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{InitialInterval: 5 * time.Second, MaximumAttempts: 5,
			NonRetryableErrorTypes: []string{agentcmd.ErrAgentNotActive, agentcmd.ErrAgentCommandFailed}},
	}
	actx := workflow.WithActivityOptions(ctx, agentOpts)
	long := workflow.WithActivityOptions(ctx, withTimeout(agentOpts, 24*time.Hour))
	target := plan.TargetAgentId

	var sg saga.Saga
	committed, dataTouched := false, false
	var restart []string
	defer func() {
		if cerr := sg.Run(ctx); cerr != nil {
			err = errors.Join(err, cerr)
			res.Rollback = cerr.Error()
		}
		switch {
		case committed:
			finish(ctx, a, in.RestoreID, "succeeded", res, nil)
		case dataTouched && res.Rollback == "":
			res.RolledBack = true
			finish(ctx, a, in.RestoreID, "rolled_back", res, err)
		default:
			finish(ctx, a, in.RestoreID, "failed", res, err)
		}
	}()

	if plan.CrossHost {
		step("grant-access")
		var grant string
		if err := workflow.ExecuteActivity(short, a.GrantRestoreAccess, in.RestoreID).Get(ctx, &grant); err != nil {
			return res, err
		}
		sg.Always("revoke-access", func(c workflow.Context) error {
			return workflow.ExecuteActivity(workflow.WithActivityOptions(c, workflow.ActivityOptions{StartToCloseTimeout: time.Minute,
				RetryPolicy: &temporal.RetryPolicy{MaximumAttempts: 20}}), a.RevokeRestoreAccess, in.RestoreID, grant).Get(c, nil)
		})
	}
	step("agent-access")
	if err := workflow.ExecuteActivity(actx, ba.EnsureAgentAccess, target, plan.Repository.Id).Get(ctx, nil); err != nil {
		return res, err
	}
	if len(plan.Images) > 0 {
		step("images")
		if err := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, withTimeout(agentOpts, 2*time.Hour)), a.EnsureImages, target, plan.Images).Get(ctx, &res.Images); err != nil {
			return res, fmt.Errorf("images: %w", err)
		}
	}

	resumed := true
	if len(plan.StopContainerIds) > 0 {
		step("stop-application")
		resumed = false
		var q *agentv1.QuiesceResult
		sg.Always("resume", func(c workflow.Context) error {
			if resumed {
				return nil
			}
			resumed = true
			return workflow.ExecuteActivity(workflow.WithActivityOptions(c, workflow.ActivityOptions{StartToCloseTimeout: 5 * time.Minute,
				HeartbeatTimeout: 2 * time.Minute, ScheduleToCloseTimeout: 30 * time.Minute,
				RetryPolicy: &temporal.RetryPolicy{InitialInterval: 2 * time.Second, MaximumInterval: time.Minute}}),
				ba.Resume, target, in.RestoreID).Get(c, nil)
		})
		if err := workflow.ExecuteActivity(actx, ba.Quiesce, backup.QuiesceInput{AgentID: target, LeaseID: in.RestoreID,
			ApplicationID: plan.TargetApplicationId, ContainerIDs: plan.StopContainerIds, Mode: agentv1.QuiesceMode_QUIESCE_MODE_STOP,
			LeaseSeconds: plan.LeaseSeconds}).Get(ctx, &q); err != nil {
			return res, fmt.Errorf("stop application: %w", err)
		}
		for _, cs := range q.GetPreState() {
			if cs.State == "running" || cs.State == "paused" || cs.State == "restarting" {
				restart = append(restart, cs.Id)
			}
		}
	}

	// From here on the target may change: roll back unless committed.
	dataTouched = true
	sg.Add("rollback", func(c workflow.Context) error {
		if committed {
			return nil
		}
		// The lease is still released by the resume compensation below
		// (it runs after this one; starting running containers is a no-op).
		return workflow.ExecuteActivity(workflow.WithActivityOptions(c, workflow.ActivityOptions{StartToCloseTimeout: 2 * time.Hour,
			HeartbeatTimeout: 2 * time.Minute, RetryPolicy: &temporal.RetryPolicy{InitialInterval: 5 * time.Second, MaximumAttempts: 10}}),
			a.FinalizeRestore, target, in.RestoreID, agentv1.FinalizeAction_FINALIZE_ACTION_ROLLBACK, restart).Get(c, nil)
	})

	if len(plan.Components) > 0 {
		step("restore-data")
		if err := workflow.ExecuteActivity(long, a.RestoreComponents, target, &agentv1.RestoreComponentsCommand{RepositoryId: plan.Repository.Id,
			RestoreId: in.RestoreID, Components: plan.Components, PathRemaps: plan.PathRemaps, FreshSession: plan.CrossHost}).Get(ctx, &res.Components); err != nil {
			return res, fmt.Errorf("restore data: %w", err)
		}
	}
	var start []string
	if plan.RecreateContainers && plan.ConfigSnapshotId != "" {
		step("recreate-containers")
		var rc *agentv1.RecreateContainersResult
		if err := workflow.ExecuteActivity(actx, a.RecreateContainers, target, &agentv1.RecreateContainersCommand{RepositoryId: plan.Repository.Id,
			RestoreId: in.RestoreID, ConfigSnapshotId: plan.ConfigSnapshotId, Networks: plan.Networks, PathRemaps: plan.PathRemaps,
			FreshSession: plan.CrossHost}).Get(ctx, &rc); err != nil {
			return res, fmt.Errorf("recreate containers: %w", err)
		}
		res.Containers = rc.GetContainers()
		wasRunning := map[string]bool{}
		for _, n := range plan.RunningAtCapture {
			wasRunning[n] = true
		}
		for _, c := range rc.GetContainers() {
			// The topology state predates any quiesce; the inspect state in the
			// config component may say "exited" after an Offline backup.
			if c.Status == "created" && (wasRunning[c.Name] || (len(plan.RunningAtCapture) == 0 && c.WasRunning)) {
				start = append(start, c.ContainerId)
			}
		}
	}
	step("start-application")
	if !resumed {
		resumed = true
		if err := workflow.ExecuteActivity(actx, ba.Resume, target, in.RestoreID).Get(ctx, nil); err != nil {
			return res, fmt.Errorf("start application: %w", err)
		}
	}
	if len(start) > 0 {
		if err := workflow.ExecuteActivity(actx, a.StartContainers, target, in.RestoreID, start).Get(ctx, nil); err != nil {
			return res, fmt.Errorf("start containers: %w", err)
		}
	}
	for _, db := range plan.Databases {
		step("restore-database")
		db.RestoreId, db.RepositoryId, db.FreshSession = in.RestoreID, plan.Repository.Id, plan.CrossHost
		if err := workflow.ExecuteActivity(long, a.RestoreDatabase, target, db).Get(ctx, nil); err != nil {
			return res, fmt.Errorf("restore database %s: %w", db.Name, err)
		}
		res.Databases = append(res.Databases, db.Name)
	}
	if ids := append(append([]string{}, restart...), start...); len(ids) > 0 {
		step("health-check")
		hs := plan.HealthTimeoutSeconds
		if hs == 0 {
			hs = 300
		}
		if err := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, withTimeout(agentOpts, time.Duration(hs+120)*time.Second)),
			a.CheckHealth, target, ids, hs).Get(ctx, &res.Health); err != nil {
			return res, fmt.Errorf("health check: %w", err)
		}
		if !res.Health.GetOk() {
			return res, temporal.NewNonRetryableApplicationError("the restored application did not become healthy: "+unhealthy(res.Health), ErrUnhealthy, nil)
		}
	}
	step("commit")
	if err := workflow.ExecuteActivity(actx, a.FinalizeRestore, target, in.RestoreID, agentv1.FinalizeAction_FINALIZE_ACTION_COMMIT, []string(nil)).Get(ctx, nil); err != nil {
		return res, fmt.Errorf("commit: %w", err)
	}
	committed = true
	return res, nil
}

func unhealthy(h *agentv1.CheckHealthResult) string {
	var bad []string
	for _, c := range h.GetContainers() {
		if !c.Ok {
			bad = append(bad, fmt.Sprintf("%s (%s/%s)", c.Name, c.State, c.Health))
		}
	}
	return strings.Join(bad, ", ")
}

func finish(ctx workflow.Context, a *Activities, id, state string, res Result, err error) {
	dctx, cancel := workflow.NewDisconnectedContext(ctx)
	defer cancel()
	dctx = workflow.WithActivityOptions(dctx, workflow.ActivityOptions{StartToCloseTimeout: time.Minute,
		RetryPolicy: &temporal.RetryPolicy{InitialInterval: 2 * time.Second, MaximumAttempts: 30}})
	b, _ := json.Marshal(res)
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	_ = workflow.ExecuteActivity(dctx, a.UpdateRestore, &controlv1.UpdateRestoreRequest{RestoreId: id, State: state, ResultJson: b, Error: msg}).Get(dctx, nil)
}

func withTimeout(o workflow.ActivityOptions, d time.Duration) workflow.ActivityOptions {
	o.StartToCloseTimeout = d
	return o
}

// ---- Activities -----------------------------------------------------------------

// Activities implement restore steps (agent access, quiesce and resume are
// the backup activities).
type Activities struct {
	Agent    *agentcmd.Dispatcher
	Platform controlv1.PlatformServiceClient
	Token    string
}

func (a *Activities) auth(ctx context.Context) context.Context {
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+a.Token)
}

func platformErr(err error) error {
	switch status.Code(err) {
	case codes.InvalidArgument, codes.FailedPrecondition, codes.NotFound, codes.PermissionDenied:
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

func (a *Activities) run(ctx context.Context, agentID string, cmd *agentv1.Command) (*agentv1.CommandUpdate, error) {
	cmd.CommandId, cmd.DeadlineUnixMs = agentcmd.CommandID(ctx), deadline(ctx)
	return a.Agent.Dispatch(ctx, agentID, cmd, func(u *agentv1.CommandUpdate) { activity.RecordHeartbeat(ctx, json.RawMessage(u.Progress)) })
}

// PrepareRestore marks the restore running and returns its plan.
func (a *Activities) PrepareRestore(ctx context.Context, id string) (*controlv1.PrepareRestoreResponse, error) {
	info := activity.GetInfo(ctx)
	r, err := a.Platform.PrepareRestore(a.auth(ctx), &controlv1.PrepareRestoreRequest{RestoreId: id,
		WorkflowId: info.WorkflowExecution.ID, RunId: info.WorkflowExecution.RunID})
	return r, platformErr(err)
}

// UpdateRestore records progress or the outcome.
func (a *Activities) UpdateRestore(ctx context.Context, req *controlv1.UpdateRestoreRequest) error {
	_, err := a.Platform.UpdateRestore(a.auth(ctx), req)
	return platformErr(err)
}

// GrantRestoreAccess grants cross-host READ.
func (a *Activities) GrantRestoreAccess(ctx context.Context, id string) (string, error) {
	r, err := a.Platform.GrantRestoreAccess(a.auth(ctx), &controlv1.GrantRestoreAccessRequest{RestoreId: id})
	if err != nil {
		return "", platformErr(err)
	}
	return r.GrantId, nil
}

// RevokeRestoreAccess removes the grant.
func (a *Activities) RevokeRestoreAccess(ctx context.Context, id, grant string) error {
	_, err := a.Platform.RevokeRestoreAccess(a.auth(ctx), &controlv1.RevokeRestoreAccessRequest{RestoreId: id, GrantId: grant})
	return platformErr(err)
}

// EnsureImages pulls missing images by digest.
func (a *Activities) EnsureImages(ctx context.Context, agentID string, images []*agentv1.ImageSpec) ([]*agentv1.ImageResult, error) {
	u, err := a.run(ctx, agentID, &agentv1.Command{Kind: &agentv1.Command_EnsureImages{EnsureImages: &agentv1.EnsureImagesCommand{Images: images}}})
	if err != nil {
		return nil, err
	}
	return u.GetEnsureImages().GetImages(), nil
}

// RestoreComponents stages, verifies and swaps in the data.
func (a *Activities) RestoreComponents(ctx context.Context, agentID string, cmd *agentv1.RestoreComponentsCommand) ([]*agentv1.RestoreComponentResult, error) {
	u, err := a.run(ctx, agentID, &agentv1.Command{Kind: &agentv1.Command_RestoreComponents{RestoreComponents: cmd}})
	if err != nil {
		return nil, err
	}
	return u.GetRestoreComponents().GetComponents(), nil
}

// RecreateContainers recreates missing containers and networks.
func (a *Activities) RecreateContainers(ctx context.Context, agentID string, cmd *agentv1.RecreateContainersCommand) (*agentv1.RecreateContainersResult, error) {
	u, err := a.run(ctx, agentID, &agentv1.Command{Kind: &agentv1.Command_RecreateContainers{RecreateContainers: cmd}})
	if err != nil {
		return nil, err
	}
	return u.GetRecreateContainers(), nil
}

// StartContainers starts containers.
func (a *Activities) StartContainers(ctx context.Context, agentID, restoreID string, ids []string) error {
	_, err := a.run(ctx, agentID, &agentv1.Command{Kind: &agentv1.Command_StartContainers{StartContainers: &agentv1.StartContainersCommand{
		RestoreId: restoreID, ContainerIds: ids}}})
	return err
}

// RestoreDatabase loads a logical dump.
func (a *Activities) RestoreDatabase(ctx context.Context, agentID string, cmd *agentv1.RestoreDatabaseCommand) error {
	_, err := a.run(ctx, agentID, &agentv1.Command{Kind: &agentv1.Command_RestoreDatabase{RestoreDatabase: cmd}})
	return err
}

// CheckHealth waits for the application to be healthy.
func (a *Activities) CheckHealth(ctx context.Context, agentID string, ids []string, timeoutSeconds uint32) (*agentv1.CheckHealthResult, error) {
	u, err := a.run(ctx, agentID, &agentv1.Command{Kind: &agentv1.Command_CheckHealth{CheckHealth: &agentv1.CheckHealthCommand{
		ContainerIds: ids, TimeoutSeconds: timeoutSeconds, StableSeconds: 15}}})
	if err != nil {
		return nil, err
	}
	return u.GetCheckHealth(), nil
}

// FinalizeRestore commits or rolls back.
func (a *Activities) FinalizeRestore(ctx context.Context, agentID, restoreID string, action agentv1.FinalizeAction, restart []string) error {
	_, err := a.run(ctx, agentID, &agentv1.Command{Kind: &agentv1.Command_FinalizeRestore{FinalizeRestore: &agentv1.FinalizeRestoreCommand{
		RestoreId: restoreID, Action: action, RestartContainerIds: restart}}})
	return err
}
