// SPDX-License-Identifier: Apache-2.0

package backup

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	controlv1 "github.com/AxiomOperator/dbr2/internal/agentpb/control/v1"
	"github.com/AxiomOperator/dbr2/internal/engine"
	"github.com/AxiomOperator/dbr2/internal/manifest"
)

// VerifyAllWorkflowID is the weekly verification schedule's workflow ID.
const VerifyAllWorkflowID = "platform/verify"

// VerifyInput selects what to verify.
type VerifyInput struct {
	RepositoryID    string
	RecoveryPointID string // "" = the least recently verified ones
	ReadPercent     float64
	Limit           uint32
}

// VerifyResult summarizes a verification run.
type VerifyResult struct {
	Verified int
	Failed   int
}

func verifyCtx(ctx workflow.Context) workflow.Context {
	return workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 6 * time.Hour, HeartbeatTimeout: 2 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{InitialInterval: 10 * time.Second, MaximumAttempts: 3},
	})
}

// VerifyRepositoryWorkflow verifies recovery points of one Repository
// (workflow ID repository/<id>/verify): every component snapshot's objects
// must exist and ReadPercent of the files are fully read (hash-verified).
func VerifyRepositoryWorkflow(ctx workflow.Context, in VerifyInput) (VerifyResult, error) {
	var a *Activities
	ctx = verifyCtx(ctx)
	if in.Limit == 0 {
		in.Limit = 50
	}
	var cands []*controlv1.VerificationCandidate
	if err := workflow.ExecuteActivity(ctx, a.VerificationCandidates, in).Get(ctx, &cands); err != nil {
		return VerifyResult{}, err
	}
	var res VerifyResult
	for _, c := range cands {
		var ok bool
		if err := workflow.ExecuteActivity(ctx, a.VerifyRecoveryPoint, in.RepositoryID, c.RecoveryPointId, c.ManifestJson, in.ReadPercent).Get(ctx, &ok); err != nil {
			res.Failed++
			continue
		}
		if ok {
			res.Verified++
		} else {
			res.Failed++
		}
	}
	return res, nil
}

// VerifyAllWorkflow verifies every ready Repository (weekly schedule).
func VerifyAllWorkflow(ctx workflow.Context, readPercent float64) (VerifyResult, error) {
	var a *Activities
	actx := verifyCtx(ctx)
	var ids []string
	if err := workflow.ExecuteActivity(actx, a.ListRepositoryIDs).Get(ctx, &ids); err != nil {
		return VerifyResult{}, err
	}
	var total VerifyResult
	for _, id := range ids {
		r, err := VerifyRepositoryWorkflow(ctx, VerifyInput{RepositoryID: id, ReadPercent: readPercent})
		if err != nil {
			workflow.GetLogger(ctx).Error("repository verification failed", "repository", id, "error", err)
			continue
		}
		total.Verified += r.Verified
		total.Failed += r.Failed
	}
	return total, nil
}

// VerificationCandidates asks dbr2-server what to verify.
func (a *Activities) VerificationCandidates(ctx context.Context, in VerifyInput) ([]*controlv1.VerificationCandidate, error) {
	r, err := a.Platform.ListVerificationCandidates(a.auth(ctx), &controlv1.ListVerificationCandidatesRequest{
		RepositoryId: in.RepositoryID, Limit: in.Limit, RecoveryPointId: in.RecoveryPointID})
	if err != nil {
		return nil, platformErr(err)
	}
	return r.Candidates, nil
}

// ComponentVerification is one component's verification result.
type ComponentVerification struct {
	Name       string   `json:"name"`
	SnapshotID string   `json:"snapshot_id"`
	Files      int64    `json:"files"`
	Dirs       int64    `json:"dirs"`
	FilesRead  int64    `json:"files_read"`
	BytesRead  int64    `json:"bytes_read"`
	Errors     []string `json:"errors,omitempty"`
}

// VerifyRecoveryPoint verifies every captured component and the manifest
// snapshot, then records the result.
func (a *Activities) VerifyRecoveryPoint(ctx context.Context, repositoryID, rpID string, manifestJSON []byte, readPercent float64) (bool, error) {
	rep, err := a.Maint.Get(ctx, repositoryID)
	if err != nil {
		return false, err
	}
	var m manifest.Manifest
	if err := json.Unmarshal(manifestJSON, &m); err != nil {
		return false, temporal.NewNonRetryableApplicationError("unreadable manifest", "ManifestInvalid", err)
	}
	results, ok, err := verifyComponents(ctx, rep, m, readPercent, func() { activity.RecordHeartbeat(ctx) })
	if err != nil {
		a.Maint.Invalidate(repositoryID)
		return false, err
	}
	details, _ := json.Marshal(map[string]any{"read_percent": readPercent, "components": results})
	if _, err := a.Platform.SetVerification(a.auth(ctx), &controlv1.SetVerificationRequest{RecoveryPointId: rpID, Ok: ok, DetailsJson: details}); err != nil {
		return ok, platformErr(err)
	}
	return ok, nil
}

func verifyComponents(ctx context.Context, rep engine.Repository, m manifest.Manifest, readPercent float64, beat func()) ([]ComponentVerification, bool, error) {
	ok := true
	var out []ComponentVerification
	for _, c := range m.Components {
		if c.Status != manifest.ComponentSucceeded || c.SnapshotID == "" {
			continue
		}
		st, err := rep.Verify(ctx, c.SnapshotID, engine.VerifyOptions{ReadPercent: readPercent})
		cv := ComponentVerification{Name: c.Name, SnapshotID: c.SnapshotID, Files: st.Files, Dirs: st.Dirs, FilesRead: st.FilesRead,
			BytesRead: st.BytesRead, Errors: st.Errors}
		switch {
		case errors.Is(err, engine.ErrNotFound):
			cv.Errors = append(cv.Errors, "snapshot is missing from the Repository")
		case err != nil:
			return out, false, err // infrastructure error: retry the activity
		}
		if len(cv.Errors) > 0 {
			ok = false
		}
		out = append(out, cv)
		beat()
	}
	return out, ok, nil
}
