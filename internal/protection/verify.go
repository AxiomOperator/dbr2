// SPDX-License-Identifier: Apache-2.0

package protection

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	controlv1 "github.com/AxiomOperator/dbr2/internal/agentpb/control/v1"
	"github.com/AxiomOperator/dbr2/internal/audit"
	"github.com/AxiomOperator/dbr2/internal/auth"
	"github.com/AxiomOperator/dbr2/internal/events"
	"github.com/AxiomOperator/dbr2/internal/rbac"
	"github.com/AxiomOperator/dbr2/internal/store"
	"github.com/AxiomOperator/dbr2/internal/temporalx"
	"github.com/AxiomOperator/dbr2/workflows/backup"
)

// DefaultVerifyReadPercent is the share of files fully read by on-demand
// verification.
const DefaultVerifyReadPercent = 10

// StartVerification starts repository/<id>/verify for a Repository or a
// single recovery point.
func (s *Service) StartVerification(ctx context.Context, p *auth.Principal, repoID uuid.UUID, rpID string, readPercent float64, m auth.RequestMeta) (string, error) {
	if _, err := s.q.GetRepository(ctx, store.GetRepositoryParams{ID: repoID, OrgID: s.opts.OrgID}); err != nil {
		return "", notFound(err)
	}
	if rpID != "" {
		rp, err := s.q.GetRecoveryPoint(ctx, store.GetRecoveryPointParams{ID: rpID, OrgID: s.opts.OrgID})
		if err != nil {
			return "", notFound(err)
		}
		if rp.State != "committed" {
			return "", fmt.Errorf("%w: recovery point is %s", ErrConflict, rp.State)
		}
	}
	if readPercent <= 0 || readPercent > 100 {
		readPercent = DefaultVerifyReadPercent
	}
	run, err := temporalx.StartRepositoryOperation(ctx, s.temporal, s.opts.TaskQueue, repoID.String(), "verify", backup.VerifyRepositoryWorkflow,
		backup.VerifyInput{RepositoryID: repoID.String(), RecoveryPointID: rpID, ReadPercent: readPercent})
	if err != nil {
		return "", err
	}
	ev := s.event(p, m, audit.VerificationRequested)
	ev.TargetType, ev.TargetID = "repository", repoID.String()
	ev.Details = map[string]any{"recovery_point_id": rpID, "read_percent": readPercent, "workflow_id": run.GetID()}
	if _, err := s.audit.Record(ctx, ev); err != nil {
		return "", err
	}
	return run.GetID(), nil
}

// ListVerificationCandidates implements the RPC.
func (p *Platform) ListVerificationCandidates(ctx context.Context, req *controlv1.ListVerificationCandidatesRequest) (*controlv1.ListVerificationCandidatesResponse, error) {
	s := p.s
	out := &controlv1.ListVerificationCandidatesResponse{}
	if req.RecoveryPointId != "" {
		rp, err := s.q.GetRecoveryPointByID(ctx, req.RecoveryPointId)
		if err != nil {
			return nil, grpcErr(err)
		}
		out.Candidates = append(out.Candidates, &controlv1.VerificationCandidate{RecoveryPointId: rp.ID, ManifestJson: rp.Manifest})
		return out, nil
	}
	id, err := uuid.Parse(req.RepositoryId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "repository_id")
	}
	limit := int32(req.Limit)
	if limit <= 0 || limit > 1000 {
		limit = 50
	}
	rows, err := s.q.ListVerificationCandidates(ctx, store.ListVerificationCandidatesParams{RepositoryID: id, Limit: limit})
	if err != nil {
		return nil, grpcErr(err)
	}
	for _, r := range rows {
		out.Candidates = append(out.Candidates, &controlv1.VerificationCandidate{RecoveryPointId: r.ID, ManifestJson: r.Manifest})
	}
	return out, nil
}

// SetVerification implements the RPC: verified or verification_failed,
// with an alert on failure.
func (p *Platform) SetVerification(ctx context.Context, req *controlv1.SetVerificationRequest) (*controlv1.SetVerificationResponse, error) {
	s := p.s
	rp, err := s.q.GetRecoveryPointByID(ctx, req.RecoveryPointId)
	if err != nil {
		return nil, grpcErr(err)
	}
	state, typ, outcome := "verified", audit.VerificationSucceeded, audit.Success
	if !req.Ok {
		state, typ, outcome = "verification_failed", audit.VerificationFailed, audit.Failure
	}
	details := json.RawMessage(req.DetailsJson)
	if !json.Valid(details) {
		details = json.RawMessage(`{}`)
	}
	err = s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		if err := q.SetRecoveryPointVerification(ctx, store.SetRecoveryPointVerificationParams{ID: rp.ID, Verification: state, VerificationDetails: details}); err != nil {
			return err
		}
		if err := q.SetRepositoryVerified(ctx, rp.RepositoryID); err != nil {
			return err
		}
		d := map[string]any{"recovery_point_id": rp.ID, "details": details}
		if _, err := rec.Record(ctx, s.systemEvent(typ, "application", rp.ApplicationID.String(), outcome, d)); err != nil {
			return err
		}
		if !req.Ok {
			return s.alert(ctx, q, "critical", typ, "application", rp.ApplicationID.String(),
				fmt.Sprintf("Recovery point %s of %s failed verification: data is missing or corrupt in the Repository", rp.ID, rp.ApplicationName), d)
		}
		return nil
	})
	if err != nil {
		return nil, grpcErr(err)
	}
	s.publish(ctx, events.BackupUpdated, rbac.BackupRead, map[string]any{"recovery_point_id": rp.ID, "application_id": rp.ApplicationID.String(), "state": state})
	return &controlv1.SetVerificationResponse{}, nil
}
