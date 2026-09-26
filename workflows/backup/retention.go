// SPDX-License-Identifier: Apache-2.0

package backup

import (
	"context"
	"fmt"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	controlv1 "github.com/AxiomOperator/dbr2/internal/agentpb/control/v1"
	"github.com/AxiomOperator/dbr2/internal/engine"
)

// RetentionWorkflowID is the retention schedule's workflow ID.
const RetentionWorkflowID = "platform/retention"

// RetentionResult summarizes a retention run.
type RetentionResult struct {
	Candidates int
	Deleted    int
	Failed     int
}

// RetentionWorkflow deletes recovery points outside their policy's
// retention and manual deletions past their grace period. Each deletion
// removes the manifest first (un-commit), then the components (ADR-0004).
func RetentionWorkflow(ctx workflow.Context) (RetentionResult, error) {
	var a *Activities
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Minute, HeartbeatTimeout: 2 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{InitialInterval: 10 * time.Second, MaximumAttempts: 5},
	})
	var cands []*controlv1.RetentionCandidate
	if err := workflow.ExecuteActivity(ctx, a.RetentionCandidates).Get(ctx, &cands); err != nil {
		return RetentionResult{}, err
	}
	res := RetentionResult{Candidates: len(cands)}
	for _, c := range cands {
		if err := workflow.ExecuteActivity(ctx, a.DeleteRecoveryPoint, c.RecoveryPointId, c.Reason).Get(ctx, nil); err != nil {
			workflow.GetLogger(ctx).Error("deleting recovery point failed", "recovery_point_id", c.RecoveryPointId, "error", err)
			res.Failed++
			continue
		}
		res.Deleted++
	}
	return res, nil
}

// RetentionCandidates asks dbr2-server what to delete now.
func (a *Activities) RetentionCandidates(ctx context.Context) ([]*controlv1.RetentionCandidate, error) {
	r, err := a.Platform.ListRetentionCandidates(a.auth(ctx), &controlv1.ListRetentionCandidatesRequest{})
	if err != nil {
		return nil, platformErr(err)
	}
	return r.Candidates, nil
}

// DeleteRecoveryPoint deletes one recovery point as maint@dbr2.
func (a *Activities) DeleteRecoveryPoint(ctx context.Context, rpID, reason string) error {
	b, err := a.Platform.BeginRecoveryPointDeletion(a.auth(ctx), &controlv1.BeginRecoveryPointDeletionRequest{RecoveryPointId: rpID})
	if err != nil {
		return platformErr(err)
	}
	if !b.Proceed {
		return nil
	}
	finish := func(n int, derr error) error {
		req := &controlv1.FinishRecoveryPointDeletionRequest{RecoveryPointId: rpID, Reason: reason, SnapshotsDeleted: int32(n)}
		if derr != nil {
			req.Error = derr.Error()
		}
		if _, err := a.Platform.FinishRecoveryPointDeletion(a.auth(ctx), req); err != nil {
			return platformErr(err)
		}
		return derr
	}
	rep, err := a.Maint.Get(ctx, b.RepositoryId)
	if err != nil {
		return finish(0, err)
	}
	n, err := deleteRecoveryPoint(ctx, rep, rpID, func() { activity.RecordHeartbeat(ctx) })
	if err != nil {
		a.Maint.Invalidate(b.RepositoryId)
	}
	return finish(n, err)
}

// deleteRecoveryPoint removes the trusted manifest(s) first, then every
// snapshot tagged with the recovery point (components, fsmeta, seeds).
func deleteRecoveryPoint(ctx context.Context, rep engine.Repository, rpID string, beat func()) (int, error) {
	snaps, err := rep.List(ctx, nil, map[string]string{engine.TagRP: rpID})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, pass := range []bool{true, false} { // manifests first
		for _, s := range snaps {
			if (s.Tags[engine.TagKind] == "manifest") != pass {
				continue
			}
			if err := rep.Delete(ctx, s.ID); err != nil {
				return n, fmt.Errorf("delete snapshot %s: %w", s.ID, err)
			}
			n++
			beat()
		}
	}
	return n, nil
}

// RecordScheduledSkip records a scheduled backup skipped because another
// operation held the application (ADR-0011: skipped and recorded). Called
// by ops.ScheduledOperationTrigger by activity name.
func (a *Activities) RecordScheduledSkip(ctx context.Context, applicationID string) error {
	return a.RecordEvent(ctx, &controlv1.RecordEventRequest{Type: "backup.skipped", TargetType: "application", TargetId: applicationID,
		Outcome: "failure", AlertSeverity: "warning", Details: jsonDetails(map[string]any{"reason": "another operation held the application"})})
}
