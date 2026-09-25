// SPDX-License-Identifier: Apache-2.0

// Package ops contains cross-cutting operation workflows.
package ops

import (
	"errors"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/AxiomOperator/dbr2/internal/temporalx"
)

// TriggerInput asks the trigger to start an application operation.
type TriggerInput struct {
	ApplicationID string
	// WorkflowType is the registered name of the operation workflow.
	WorkflowType string
	// Args are passed to the operation workflow.
	Args []any
}

// Trigger outcomes.
const (
	OutcomeStarted        = "started"
	OutcomeSkippedOverlap = "skipped_overlap"
)

// TriggerResult records what a scheduled run did.
type TriggerResult struct {
	Outcome    string
	WorkflowID string
	RunID      string
}

// ScheduledOperationTrigger is what Temporal Schedules start (overlap policy
// SKIP). Schedules append a timestamp to workflow IDs, so they cannot start
// `application/{id}` directly; the trigger starts the operation as a child
// with the exclusive ID and ParentClosePolicy ABANDON, and records a skipped
// run instead of failing when another operation holds the application
// (ADR-0011, spike-verified pattern).
func ScheduledOperationTrigger(ctx workflow.Context, in TriggerInput) (TriggerResult, error) {
	id := temporalx.ApplicationWorkflowID(in.ApplicationID)
	cctx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID:            id,
		ParentClosePolicy:     enumspb.PARENT_CLOSE_POLICY_ABANDON,
		WorkflowIDReusePolicy: enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE,
	})
	child := workflow.ExecuteChildWorkflow(cctx, in.WorkflowType, in.Args...)
	var exec workflow.Execution
	err := child.GetChildWorkflowExecution().Get(ctx, &exec)
	if err == nil {
		return TriggerResult{Outcome: OutcomeStarted, WorkflowID: exec.ID, RunID: exec.RunID}, nil
	}
	if isAlreadyStarted(err) {
		workflow.GetLogger(ctx).Info("scheduled operation skipped: application busy", "workflow_id", id)
		return TriggerResult{Outcome: OutcomeSkippedOverlap, WorkflowID: id}, nil
	}
	return TriggerResult{}, err
}

func isAlreadyStarted(err error) bool {
	var started *temporal.ChildWorkflowExecutionAlreadyStartedError
	return errors.As(err, &started)
}

// ScheduleTimeout bounds the trigger itself (not the operation it starts).
const ScheduleTimeout = 5 * time.Minute
