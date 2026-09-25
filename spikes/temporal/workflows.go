package main

import (
	"errors"
	"fmt"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const TaskQueue = "dbr2-spike"

// ---------- BackupWorkflow (ADR-0005 Layer 1 saga) ----------

type BackupInput struct {
	AppID              string
	ProtectSeconds     int           // simulated copy duration inside the quiesce window
	FailProtect        bool          // ProtectVolumes fails permanently (non-retryable)
	StallProtect       bool          // ProtectVolumes stops heartbeating (stalled agent)
	ProtectMaxAttempts int32         // 0 = unlimited retries
	MaxQuiesce         time.Duration // ADR-0005 max quiesce duration (default 60m)
}

type BackupResult struct {
	AppID          string
	ProtectAttempt int32
	ResumedFrom    int
}

func BackupWorkflow(ctx workflow.Context, in BackupInput) (res BackupResult, err error) {
	logger := workflow.GetLogger(ctx)
	res.AppID = in.AppID
	if in.MaxQuiesce == 0 {
		in.MaxQuiesce = 60 * time.Minute
	}
	var a *Activities

	short := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: 30 * time.Second})

	// 1. Record pre-state (which containers were running) BEFORE quiescing.
	var preState []string
	if err = workflow.ExecuteActivity(short, a.RecordPreState, in.AppID).Get(ctx, &preState); err != nil {
		return res, err
	}

	// 2. Quiesce.
	if err = workflow.ExecuteActivity(short, a.Quiesce, in.AppID).Get(ctx, nil); err != nil {
		return res, err
	}

	// 3. Register Resume compensation. Runs on success-path skip, failure and cancellation
	//    (NOT on termination: the workflow code never runs again after terminate).
	resumed := false
	defer func() {
		if resumed {
			return
		}
		dctx, _ := workflow.NewDisconnectedContext(ctx)
		dctx = workflow.WithActivityOptions(dctx, workflow.ActivityOptions{
			StartToCloseTimeout: 30 * time.Second,
			RetryPolicy: &temporal.RetryPolicy{ // retry aggressively (ADR-0005)
				InitialInterval:    time.Second,
				BackoffCoefficient: 2,
				MaximumInterval:    30 * time.Second,
				MaximumAttempts:    0,
			},
			ScheduleToCloseTimeout: 30 * time.Minute, // then: flag "Needs Attention: not resumed" + critical alert
		})
		logger.Info("compensation: running Resume", "cause", err)
		if rerr := workflow.ExecuteActivity(dctx, a.Resume, in.AppID, preState).Get(dctx, nil); rerr != nil {
			logger.Error("RESUME FAILED -> Needs Attention", "err", rerr)
		}
	}()

	// 4. Protect volumes: bounded by max quiesce, stall detected via heartbeat timeout (ADR-0001).
	pctx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: in.MaxQuiesce,
		HeartbeatTimeout:    3 * time.Second,
		WaitForCancellation: true, // on cancel, wait for the agent to stop reading before Resume
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval: time.Second,
			MaximumAttempts: in.ProtectMaxAttempts,
		},
	})
	var pr ProtectResult
	if err = workflow.ExecuteActivity(pctx, a.ProtectVolumes, ProtectInput{
		AppID: in.AppID, Seconds: in.ProtectSeconds, Fail: in.FailProtect, Stall: in.StallProtect,
	}).Get(ctx, &pr); err != nil {
		return res, err
	}
	res.ProtectAttempt, res.ResumedFrom = pr.Attempt, pr.ResumedFrom

	// 5. Normal-path Resume (immediately after protect; upload/verify/commit follow later).
	if err = workflow.ExecuteActivity(short, a.Resume, in.AppID, preState).Get(ctx, nil); err != nil {
		return res, err // defer retries it in the disconnected context
	}
	resumed = true

	// 6..n Upload/verify/commit would follow here (outside the quiesce window).
	return res, nil
}

// RestoreWorkflow: a different workflow type that claims the same application ID.
func RestoreWorkflow(ctx workflow.Context, appID string) error {
	return workflow.Sleep(ctx, 2*time.Second)
}

// ---------- Schedule trigger (ADR-0011 scheduled runs) ----------

type TriggerInput struct {
	AppID string
	Mode  string // "child" | "activity"
}

type TriggerResult struct {
	Outcome        string // "started" | "skipped_overlap"
	WorkflowID     string
	RunID          string // run started, or (activity mode) the conflicting running run
	ConflictDetail string
}

// ScheduledBackupTrigger is what a Temporal Schedule starts (its own ID gets a timestamp suffix).
// It starts BackupWorkflow under the deterministic ID application/{id}; on conflict it records a skip.
func ScheduledBackupTrigger(ctx workflow.Context, in TriggerInput) (TriggerResult, error) {
	wfID := "application/" + in.AppID
	res := TriggerResult{WorkflowID: wfID}
	var a *Activities

	switch in.Mode {
	case "child":
		cctx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			WorkflowID:            wfID,
			TaskQueue:             TaskQueue,
			ParentClosePolicy:     enumspb.PARENT_CLOSE_POLICY_ABANDON, // backup outlives the trigger
			WorkflowIDReusePolicy: enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE,
		})
		f := workflow.ExecuteChildWorkflow(cctx, BackupWorkflow, BackupInput{AppID: in.AppID, ProtectSeconds: 2})
		var exec workflow.Execution
		err := f.GetChildWorkflowExecution().Get(ctx, &exec)
		var already *temporal.ChildWorkflowExecutionAlreadyStartedError
		switch {
		case errors.As(err, &already):
			res.Outcome, res.ConflictDetail = "skipped_overlap", err.Error()
		case err != nil:
			return res, err
		default:
			res.Outcome, res.RunID = "started", exec.RunID
		}
	case "activity":
		actx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: 30 * time.Second})
		if err := workflow.ExecuteActivity(actx, a.StartBackupFromActivity, in.AppID).Get(ctx, &res); err != nil {
			return res, err
		}
	default:
		return res, fmt.Errorf("unknown mode %q", in.Mode)
	}

	// Record the outcome durably (in DBR² this writes a schedule_runs row in the dbr2 database).
	actx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: 30 * time.Second})
	if err := workflow.ExecuteActivity(actx, a.RecordScheduleOutcome, in.AppID, res).Get(ctx, nil); err != nil {
		return res, err
	}
	return res, nil
}
