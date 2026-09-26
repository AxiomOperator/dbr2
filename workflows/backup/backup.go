// SPDX-License-Identifier: Apache-2.0

// Package backup implements the application backup workflow (final stack →
// Backup workflow; ADR-0004, ADR-0005): claim the application (workflow ID
// application/<id>) → pre hooks → quiesce → protect → resume → post hooks →
// commit. Resuming the application is guaranteed by saga compensation
// (layer 1) and the agent's dead-man switch (layer 2).
package backup

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	agentv1 "github.com/AxiomOperator/dbr2/internal/agentpb/agent/v1"
	controlv1 "github.com/AxiomOperator/dbr2/internal/agentpb/control/v1"
	"github.com/AxiomOperator/dbr2/internal/manifest"
	"github.com/AxiomOperator/dbr2/workflows/agentcmd"
	"github.com/AxiomOperator/dbr2/workflows/saga"
)

// Input starts a backup.
type Input struct {
	ApplicationID string
	// manual | scheduled
	Trigger     string
	RequestedBy string
	// Optional override: live | quiesced | offline.
	ConsistencyMode string
}

// Result summarizes a committed recovery point.
type Result struct {
	RecoveryPointID string
	Status          string
	Components      int
	SizeBytes       int64
}

// LeaseGrace is added to the maximum quiesce duration to form the agent's
// dead-man lease: the workflow's own bound fires first (ADR-0005).
const LeaseGrace = 10 * time.Minute

// Error types.
const (
	ErrRequiredComponentFailed = "RequiredComponentFailed"
	ErrAutoResumed             = "QuiesceAutoResumed"
	ErrSourceMismatch          = "ComponentSourceMismatch"
)

// BackupWorkflow is the backup workflow. It has no workflow-level timeouts:
// termination and workflow timeouts skip compensation (ADR-0005).
func BackupWorkflow(ctx workflow.Context, in Input) (res Result, err error) {
	log := workflow.GetLogger(ctx)
	var a *Activities
	info := workflow.GetInfo(ctx)
	started := workflow.Now(ctx)

	short := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{InitialInterval: 2 * time.Second, MaximumAttempts: 10, NonRetryableErrorTypes: []string{"InvalidArgument", "FailedPrecondition"}},
	})
	var plan *controlv1.PrepareBackupResponse
	if err := workflow.ExecuteActivity(short, a.PrepareBackup, in).Get(ctx, &plan); err != nil {
		return res, err
	}
	res.RecoveryPointID = plan.RecoveryPointId
	committed := false
	defer func() {
		if committed {
			return
		}
		dctx, cancel := workflow.NewDisconnectedContext(short)
		defer cancel()
		msg := "backup did not complete"
		if err != nil {
			msg = err.Error()
		}
		_ = workflow.ExecuteActivity(dctx, a.CompleteBackup, &controlv1.CompleteBackupRequest{
			RecoveryPointId: plan.RecoveryPointId, Outcome: "failed", Error: msg}).Get(dctx, nil)
	}()

	if plan.WaitForWindowSeconds > 0 {
		log.Info("waiting for the host backup window", "seconds", plan.WaitForWindowSeconds)
		if err := workflow.Sleep(ctx, time.Duration(plan.WaitForWindowSeconds)*time.Second); err != nil {
			return res, err
		}
	}

	agentOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 15 * time.Minute,
		HeartbeatTimeout:    2 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{InitialInterval: 5 * time.Second, MaximumAttempts: 5,
			NonRetryableErrorTypes: []string{agentcmd.ErrAgentNotActive, agentcmd.ErrAgentCommandFailed}},
	}
	actx := workflow.WithActivityOptions(ctx, agentOpts)
	if err := workflow.ExecuteActivity(actx, a.EnsureAgentAccess, plan.AgentId, plan.Repository.Id).Get(ctx, nil); err != nil {
		return res, err
	}

	// Database dumps are online (Phase 8): they run first, outside the
	// quiesce window; the filesystem components follow.
	var dbComps, fsComps []*agentv1.ComponentSpec
	for _, c := range plan.Components {
		if c.Kind == agentv1.ComponentKind_COMPONENT_KIND_DATABASE {
			dbComps = append(dbComps, c)
		} else {
			fsComps = append(fsComps, c)
		}
	}
	snapIn := SnapshotInput{AgentID: plan.AgentId, RepositoryID: plan.Repository.Id, RecoveryPointID: plan.RecoveryPointId,
		ApplicationID: in.ApplicationID, Components: fsComps}

	// Seed pass (ADR-0005): live, never committed; failures only warn.
	if len(plan.SeedComponents) > 0 {
		seed := snapIn
		seed.Seed = true
		seed.Components = filterComponents(plan.Components, plan.SeedComponents)
		sctx := workflow.WithActivityOptions(ctx, withTimeout(agentOpts, 24*time.Hour))
		if err := workflow.ExecuteActivity(sctx, a.Snapshot, seed).Get(ctx, nil); err != nil {
			log.Warn("seed pass failed; continuing with the consistent pass", "error", err)
		}
	}

	var dbResults []*agentv1.ComponentResult
	if len(dbComps) > 0 {
		dbIn := snapIn
		dbIn.Components = dbComps
		var r *agentv1.SnapshotComponentsResult
		if err := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, withTimeout(agentOpts, 12*time.Hour)), a.Snapshot, dbIn).Get(ctx, &r); err != nil {
			return res, fmt.Errorf("database dumps: %w", err)
		}
		dbResults = r.GetComponents()
		if failed := failedRequired(dbResults); len(failed) > 0 {
			return res, temporal.NewNonRetryableApplicationError("required database dumps failed: "+strings.Join(failed, "; "), ErrRequiredComponentFailed, nil)
		}
	}

	mode := plan.ConsistencyMode
	maxQuiesce := time.Duration(plan.MaxQuiesceSeconds) * time.Second
	var sg saga.Saga
	defer func() {
		if cerr := sg.Run(ctx); cerr != nil {
			err = errors.Join(err, cerr)
		}
	}()

	var quiesce *agentv1.QuiesceResult
	resumed, postHooksDone := false, false
	var resume *agentv1.ResumeResult
	runPost := func(c workflow.Context) error {
		if postHooksDone || len(plan.PostHooks) == 0 {
			return nil
		}
		postHooksDone = true
		return workflow.ExecuteActivity(workflow.WithActivityOptions(c, agentOpts), a.RunHooks, plan.AgentId, "post", plan.PostHooks).Get(c, nil)
	}
	doResume := func(c workflow.Context) error {
		if resumed {
			return nil
		}
		rctx := workflow.WithActivityOptions(c, workflow.ActivityOptions{
			StartToCloseTimeout: 5 * time.Minute, HeartbeatTimeout: 2 * time.Minute, ScheduleToCloseTimeout: 30 * time.Minute,
			RetryPolicy: &temporal.RetryPolicy{InitialInterval: 2 * time.Second, BackoffCoefficient: 1.5, MaximumInterval: time.Minute,
				NonRetryableErrorTypes: []string{agentcmd.ErrAgentNotActive}},
		})
		if err := workflow.ExecuteActivity(rctx, a.Resume, plan.AgentId, plan.RecoveryPointId).Get(c, &resume); err != nil {
			// Needs Attention: not resumed (the agent's lease still fires).
			_ = workflow.ExecuteActivity(workflow.WithActivityOptions(c, workflow.ActivityOptions{StartToCloseTimeout: time.Minute}),
				a.RecordEvent, &controlv1.RecordEventRequest{Type: "application.not_resumed", TargetType: "application", TargetId: in.ApplicationID,
					Outcome: "failure", AlertSeverity: "critical", Details: jsonDetails(map[string]any{"recovery_point_id": plan.RecoveryPointId, "error": err.Error()})}).Get(c, nil)
			return err
		}
		resumed = true
		return nil
	}

	if mode != manifest.ModeLive {
		if len(plan.PreHooks) > 0 {
			sg.Always("post-hooks", runPost) // post hooks undo pre hooks, even on failure
			if err := workflow.ExecuteActivity(actx, a.RunHooks, plan.AgentId, "pre", plan.PreHooks).Get(ctx, nil); err != nil {
				return res, fmt.Errorf("pre-backup hooks: %w", err)
			}
		}
		qmode := agentv1.QuiesceMode_QUIESCE_MODE_PAUSE
		if mode == manifest.ModeOffline {
			qmode = agentv1.QuiesceMode_QUIESCE_MODE_STOP
		}
		// Registered before Quiesce is attempted: a timed-out Quiesce may
		// still have paused containers. Resume of an unknown lease is a no-op.
		sg.Always("resume", doResume)
		qctx := workflow.WithActivityOptions(ctx, withTimeout(agentOpts, 10*time.Minute))
		if err := workflow.ExecuteActivity(qctx, a.Quiesce, QuiesceInput{AgentID: plan.AgentId, LeaseID: plan.RecoveryPointId,
			ApplicationID: in.ApplicationID, ContainerIDs: plan.ContainerIds, Mode: qmode,
			LeaseSeconds: uint32((maxQuiesce + LeaseGrace) / time.Second)}).Get(ctx, &quiesce); err != nil {
			return res, fmt.Errorf("quiesce: %w", err)
		}
	}

	// Protect. Inside the quiesce window the activity is bounded by the
	// maximum quiesce duration and waits for the agent to stop reading
	// before compensation resumes the application.
	pOpts := withTimeout(agentOpts, 24*time.Hour)
	if mode != manifest.ModeLive {
		pOpts = withTimeout(agentOpts, maxQuiesce)
		pOpts.WaitForCancellation = true
		pOpts.RetryPolicy = &temporal.RetryPolicy{InitialInterval: 5 * time.Second, MaximumAttempts: 2,
			NonRetryableErrorTypes: []string{agentcmd.ErrAgentNotActive, agentcmd.ErrAgentCommandFailed}}
	}
	var snap *agentv1.SnapshotComponentsResult
	snapErr := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, pOpts), a.Snapshot, snapIn).Get(ctx, &snap)

	var qStart, qEnd *time.Time
	if mode != manifest.ModeLive {
		if err := doResume(ctx); err != nil {
			return res, errors.Join(snapErr, fmt.Errorf("resume: %w", err))
		}
		t := workflow.Now(ctx)
		qEnd = &t
		if quiesce != nil {
			s := time.UnixMilli(quiesce.QuiescedAtUnixMs).UTC()
			qStart = &s
		}
		if err := runPost(ctx); err != nil {
			log.Warn("post-backup hooks failed", "error", err)
		}
	}
	if snapErr != nil {
		return res, fmt.Errorf("protect: %w", snapErr)
	}
	if resume != nil && resume.AutoResumed {
		return res, temporal.NewNonRetryableApplicationError(
			"the agent dead-man switch resumed the application during capture; consistency is not guaranteed", ErrAutoResumed, nil)
	}

	results := append(dbResults, snap.GetComponents()...)
	if failed := failedRequired(results); len(failed) > 0 {
		return res, temporal.NewNonRetryableApplicationError("required components failed: "+strings.Join(failed, "; "), ErrRequiredComponentFailed, nil)
	}

	commitCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Minute, HeartbeatTimeout: 2 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{InitialInterval: 5 * time.Second, MaximumAttempts: 10, NonRetryableErrorTypes: []string{ErrSourceMismatch, "ManifestInvalid"}},
	})
	var cr CommitResult
	if err := workflow.ExecuteActivity(commitCtx, a.Commit, CommitInput{
		Plan: plan, Results: results, WorkflowID: info.WorkflowExecution.ID, RunID: info.WorkflowExecution.RunID,
		Trigger: in.Trigger, StartedAt: started, QuiesceStartedAt: qStart, QuiesceEndedAt: qEnd,
	}).Get(ctx, &cr); err != nil {
		return res, fmt.Errorf("commit: %w", err)
	}
	if err := workflow.ExecuteActivity(short, a.CompleteBackup, &controlv1.CompleteBackupRequest{
		RecoveryPointId: plan.RecoveryPointId, Outcome: "committed", ManifestJson: cr.ManifestJSON, ManifestSnapshotId: cr.SnapshotID,
	}).Get(ctx, nil); err != nil {
		// The recovery point exists (manifest written); reindexing repairs the index.
		log.Error("recording the committed recovery point failed; run reindex", "error", err)
	}
	committed = true
	return Result{RecoveryPointID: plan.RecoveryPointId, Status: cr.Status, Components: cr.Components, SizeBytes: cr.SizeBytes}, nil
}

func withTimeout(o workflow.ActivityOptions, d time.Duration) workflow.ActivityOptions {
	o.StartToCloseTimeout = d
	return o
}

func filterComponents(all []*agentv1.ComponentSpec, names []string) []*agentv1.ComponentSpec {
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	var out []*agentv1.ComponentSpec
	for _, c := range all {
		if want[c.Name] {
			out = append(out, c)
		}
	}
	return out
}

func failedRequired(rs []*agentv1.ComponentResult) []string {
	var out []string
	for _, r := range rs {
		if r.Required && r.Status != manifest.ComponentSucceeded {
			out = append(out, r.Name+": "+firstNonEmpty(r.Error, r.Status))
		}
	}
	return out
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}
