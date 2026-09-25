package main

import (
	"context"
	"errors"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
)

type Activities struct {
	Client client.Client // used by StartBackupFromActivity
}

func (a *Activities) RecordPreState(ctx context.Context, appID string) ([]string, error) {
	activity.GetLogger(ctx).Info("RecordPreState", "app", appID)
	return []string{appID + "-web", appID + "-worker"}, nil // containers running before quiesce
}

func (a *Activities) Quiesce(ctx context.Context, appID string) error {
	activity.GetLogger(ctx).Info("Quiesce", "app", appID)
	return nil
}

func (a *Activities) Resume(ctx context.Context, appID string, preState []string) error {
	activity.GetLogger(ctx).Info("Resume", "app", appID, "containers", preState)
	return nil
}

type ProtectInput struct {
	AppID   string
	Seconds int
	Fail    bool
	Stall   bool
}

type ProtectResult struct {
	Attempt     int32
	ResumedFrom int
}

// ProtectVolumes simulates the agent copying data; it relays progress as heartbeats (ADR-0001).
func (a *Activities) ProtectVolumes(ctx context.Context, in ProtectInput) (ProtectResult, error) {
	info := activity.GetInfo(ctx)
	log := activity.GetLogger(ctx)
	start := 0
	if activity.HasHeartbeatDetails(ctx) {
		_ = activity.GetHeartbeatDetails(ctx, &start) // resume from last reported progress (idempotent retry)
	}
	log.Info("ProtectVolumes start", "attempt", info.Attempt, "from", start)
	for i := start; i < in.Seconds; i++ {
		if in.Fail && i == 1 {
			return ProtectResult{}, temporal.NewNonRetryableApplicationError("disk full on repository", "ProtectFailed", nil)
		}
		if in.Stall && info.Attempt == 1 && i == 1 {
			log.Info("ProtectVolumes stalling (no heartbeats)")
			select { // stalled agent: no heartbeats
			case <-ctx.Done():
				return ProtectResult{}, ctx.Err()
			case <-time.After(20 * time.Second):
			}
		}
		select {
		case <-ctx.Done():
			log.Info("ProtectVolumes cancelled", "err", ctx.Err())
			return ProtectResult{}, ctx.Err()
		case <-time.After(time.Second):
		}
		activity.RecordHeartbeat(ctx, i+1)
	}
	return ProtectResult{Attempt: info.Attempt, ResumedFrom: start}, nil
}

// StartBackupFromActivity is the "activity" variant of the schedule trigger.
func (a *Activities) StartBackupFromActivity(ctx context.Context, appID string) (TriggerResult, error) {
	wfID := "application/" + appID
	res := TriggerResult{WorkflowID: wfID}
	run, err := a.Client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:                                       wfID,
		TaskQueue:                                TaskQueue,
		WorkflowIDConflictPolicy:                 enumspb.WORKFLOW_ID_CONFLICT_POLICY_FAIL,
		WorkflowIDReusePolicy:                    enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE,
		WorkflowExecutionErrorWhenAlreadyStarted: true,
		// NOTE: not idempotent across activity retries (SDK exposes no RequestID): if attempt 1 started the
		// run but its completion was lost, attempt 2 sees AlreadyStarted and misreports a skip.
	}, BackupWorkflow, BackupInput{AppID: appID, ProtectSeconds: 2})
	var already *serviceerror.WorkflowExecutionAlreadyStarted
	switch {
	case errors.As(err, &already):
		res.Outcome, res.RunID, res.ConflictDetail = "skipped_overlap", already.RunId, err.Error()
		return res, nil
	case err != nil:
		return res, err
	}
	res.Outcome, res.RunID = "started", run.GetRunID()
	return res, nil
}

func (a *Activities) RecordScheduleOutcome(ctx context.Context, appID string, r TriggerResult) error {
	activity.GetLogger(ctx).Info("schedule run outcome", "app", appID, "outcome", r.Outcome, "runID", r.RunID)
	return nil
}
