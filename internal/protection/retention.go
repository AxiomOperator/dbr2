// SPDX-License-Identifier: Apache-2.0

package protection

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	controlv1 "github.com/AxiomOperator/dbr2/internal/agentpb/control/v1"
	"github.com/AxiomOperator/dbr2/internal/audit"
	"github.com/AxiomOperator/dbr2/internal/auth"
	"github.com/AxiomOperator/dbr2/internal/events"
	"github.com/AxiomOperator/dbr2/internal/rbac"
	"github.com/AxiomOperator/dbr2/internal/store"
)

// DeletionGrace is the default grace period for manual deletions (ADR-0014).
const DeletionGrace = 7 * 24 * time.Hour

// retentionKeep returns the IDs to keep among recovery points (newest
// first): keep_last newest, plus the newest per hour/day/ISO week/month/
// year for the latest N such periods (Kopia-style grandfather-father-son).
func retentionKeep(rps []store.ListCommittedForRetentionRow, r Retention, loc *time.Location) map[string]bool {
	keep := map[string]bool{}
	for i, rp := range rps {
		if int32(i) < r.KeepLast {
			keep[rp.ID] = true
		}
	}
	bucket := func(n int32, key func(time.Time) string) {
		if n <= 0 {
			return
		}
		seen := map[string]bool{}
		for _, rp := range rps { // newest first: the first in a bucket is its newest
			k := key(rp.CreatedAt.In(loc))
			if seen[k] {
				continue
			}
			if int32(len(seen)) >= n {
				return
			}
			seen[k] = true
			keep[rp.ID] = true
		}
	}
	bucket(r.KeepHourly, func(t time.Time) string { return t.Format("2006-01-02T15") })
	bucket(r.KeepDaily, func(t time.Time) string { return t.Format("2006-01-02") })
	bucket(r.KeepWeekly, func(t time.Time) string { y, w := t.ISOWeek(); return fmt.Sprintf("%d-W%02d", y, w) })
	bucket(r.KeepMonthly, func(t time.Time) string { return t.Format("2006-01") })
	bucket(r.KeepYearly, func(t time.Time) string { return t.Format("2006") })
	if len(rps) > 0 {
		keep[rps[0].ID] = true // never delete an application's latest recovery point
	}
	return keep
}

// ListRetentionCandidates implements the RPC (see control.proto).
func (p *Platform) ListRetentionCandidates(ctx context.Context, _ *controlv1.ListRetentionCandidatesRequest) (*controlv1.ListRetentionCandidatesResponse, error) {
	s := p.s
	out := &controlv1.ListRetentionCandidatesResponse{}
	busy := map[string]bool{}
	restores, err := s.q.ActiveRestores(ctx, s.opts.OrgID)
	if err != nil {
		return nil, grpcErr(err)
	}
	for _, r := range restores {
		busy[r.RecoveryPointID] = true
	}
	due, err := s.q.ListDueDeletions(ctx, store.ListDueDeletionsParams{OrgID: s.opts.OrgID, Limit: 1000})
	if err != nil {
		return nil, grpcErr(err)
	}
	seen := map[string]bool{}
	for _, rp := range due {
		if !busy[rp.ID] {
			seen[rp.ID] = true
			out.Candidates = append(out.Candidates, &controlv1.RetentionCandidate{RecoveryPointId: rp.ID, RepositoryId: rp.RepositoryID.String(),
				ApplicationId: rp.ApplicationID.String(), Reason: "manual"})
		}
	}
	assignments, err := s.q.ListPolicyAssignments(ctx, s.opts.OrgID)
	if err != nil {
		return nil, grpcErr(err)
	}
	for _, a := range assignments {
		pol, err := s.q.GetPolicy(ctx, store.GetPolicyParams{ID: *a.PolicyID, OrgID: s.opts.OrgID})
		if err != nil {
			continue
		}
		loc, err := time.LoadLocation(pol.Timezone)
		if err != nil {
			loc = time.UTC
		}
		rps, err := s.q.ListCommittedForRetention(ctx, store.ListCommittedForRetentionParams{OrgID: s.opts.OrgID, ApplicationID: a.ApplicationID})
		if err != nil {
			return nil, grpcErr(err)
		}
		keep := retentionKeep(rps, Retention{KeepLast: pol.KeepLast, KeepHourly: pol.KeepHourly, KeepDaily: pol.KeepDaily,
			KeepWeekly: pol.KeepWeekly, KeepMonthly: pol.KeepMonthly, KeepYearly: pol.KeepYearly}, loc)
		for _, rp := range rps {
			if keep[rp.ID] || busy[rp.ID] || seen[rp.ID] || rp.DeleteAfter != nil {
				continue
			}
			full, err := s.q.GetRecoveryPointByID(ctx, rp.ID)
			if err != nil {
				continue
			}
			out.Candidates = append(out.Candidates, &controlv1.RetentionCandidate{RecoveryPointId: rp.ID, RepositoryId: full.RepositoryID.String(),
				ApplicationId: rp.ApplicationID.String(), Reason: "retention"})
		}
	}
	return out, nil
}

// BeginRecoveryPointDeletion implements the RPC.
func (p *Platform) BeginRecoveryPointDeletion(ctx context.Context, req *controlv1.BeginRecoveryPointDeletionRequest) (*controlv1.BeginRecoveryPointDeletionResponse, error) {
	s := p.s
	rp, err := s.q.GetRecoveryPointByID(ctx, req.RecoveryPointId)
	if err != nil {
		return nil, grpcErr(err)
	}
	out := &controlv1.BeginRecoveryPointDeletionResponse{RepositoryId: rp.RepositoryID.String(), ApplicationId: rp.ApplicationID.String()}
	if rp.State == "deleted" {
		return out, nil
	}
	if rp.State != "committed" && rp.State != "missing" && rp.State != "deleting" {
		return out, nil
	}
	if err := s.q.MarkRecoveryPointDeleting(ctx, rp.ID); err != nil {
		return nil, grpcErr(err)
	}
	out.Proceed = true
	return out, nil
}

// FinishRecoveryPointDeletion implements the RPC.
func (p *Platform) FinishRecoveryPointDeletion(ctx context.Context, req *controlv1.FinishRecoveryPointDeletionRequest) (*controlv1.FinishRecoveryPointDeletionResponse, error) {
	s := p.s
	rp, err := s.q.GetRecoveryPointByID(ctx, req.RecoveryPointId)
	if err != nil {
		return nil, grpcErr(err)
	}
	details := map[string]any{"recovery_point_id": rp.ID, "reason": req.Reason, "snapshots_deleted": req.SnapshotsDeleted, "error": req.Error}
	err = s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		if req.Error != "" {
			if _, err := rec.Record(ctx, s.systemEvent(audit.RecoveryPointDeleted, "application", rp.ApplicationID.String(), audit.Failure, details)); err != nil {
				return err
			}
			return s.alert(ctx, q, "warning", audit.RecoveryPointDeleted, "application", rp.ApplicationID.String(),
				fmt.Sprintf("Deleting recovery point %s failed (retried next run): %s", rp.ID, req.Error), details)
		}
		if err := q.MarkRecoveryPointDeleted(ctx, rp.ID); err != nil {
			return err
		}
		_, err := rec.Record(ctx, s.systemEvent(audit.RecoveryPointDeleted, "application", rp.ApplicationID.String(), audit.Success, details))
		return err
	})
	if err != nil {
		return nil, grpcErr(err)
	}
	s.publish(ctx, events.BackupUpdated, rbac.BackupRead, map[string]any{"recovery_point_id": rp.ID, "application_id": rp.ApplicationID.String(), "state": "deleted"})
	return &controlv1.FinishRecoveryPointDeletionResponse{}, nil
}

// ---- Manual deletions with a grace period (ADR-0014) -----------------------------------

// ScheduleRecoveryPointDeletion marks a recovery point for deletion after
// the grace period. Typed confirmation (the application name) and a reason
// are mandatory.
func (s *Service) ScheduleRecoveryPointDeletion(ctx context.Context, p *auth.Principal, id, confirmation, reason string, m auth.RequestMeta) (store.RecoveryPoint, error) {
	rp, err := s.q.GetRecoveryPoint(ctx, store.GetRecoveryPointParams{ID: id, OrgID: s.opts.OrgID})
	if err != nil {
		return store.RecoveryPoint{}, notFound(err)
	}
	if strings.TrimSpace(confirmation) != rp.ApplicationName {
		return store.RecoveryPoint{}, fmt.Errorf("%w: type the application name %q to confirm", ErrInvalid, rp.ApplicationName)
	}
	if len(strings.TrimSpace(reason)) < 3 {
		return store.RecoveryPoint{}, fmt.Errorf("%w: a reason is required", ErrInvalid)
	}
	var out store.RecoveryPoint
	err = s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		after := time.Now().Add(DeletionGrace)
		r, err := q.ScheduleRecoveryPointDeletion(ctx, store.ScheduleRecoveryPointDeletionParams{ID: id, OrgID: s.opts.OrgID,
			DeleteAfter: &after, DeleteReason: &reason, DeleteRequestedBy: &p.UserID})
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: recovery point %s is %s or already scheduled for deletion", ErrConflict, id, rp.State)
		}
		if err != nil {
			return err
		}
		out = r
		ev := s.event(p, m, audit.RecoveryPointDeleteScheduled)
		ev.TargetType, ev.TargetID, ev.Reason = "application", rp.ApplicationID.String(), reason
		ev.Details = map[string]any{"recovery_point_id": id, "delete_after": after}
		_, err = rec.Record(ctx, ev)
		return err
	})
	return out, err
}

// CancelRecoveryPointDeletion restores a recovery point scheduled for deletion.
func (s *Service) CancelRecoveryPointDeletion(ctx context.Context, p *auth.Principal, id string, m auth.RequestMeta) (store.RecoveryPoint, error) {
	var out store.RecoveryPoint
	err := s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		r, err := q.CancelRecoveryPointDeletion(ctx, store.CancelRecoveryPointDeletionParams{ID: id, OrgID: s.opts.OrgID})
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: recovery point %s is not scheduled for deletion (or was already deleted)", ErrConflict, id)
		}
		if err != nil {
			return err
		}
		out = r
		ev := s.event(p, m, audit.RecoveryPointDeleteCanceled)
		ev.TargetType, ev.TargetID = "application", r.ApplicationID.String()
		ev.Details = map[string]any{"recovery_point_id": id}
		_, err = rec.Record(ctx, ev)
		return err
	})
	return out, err
}

// ScheduleRepositoryDeletion stops using a Repository now and retires it
// after the grace period. Stored data is never erased by DBR² (the NAS
// share is removed by its administrator).
func (s *Service) ScheduleRepositoryDeletion(ctx context.Context, p *auth.Principal, id uuid.UUID, confirmation, reason string, m auth.RequestMeta) (store.Repository, error) {
	repo, err := s.q.GetRepository(ctx, store.GetRepositoryParams{ID: id, OrgID: s.opts.OrgID})
	if err != nil {
		return store.Repository{}, notFound(err)
	}
	if strings.TrimSpace(confirmation) != repo.Name {
		return store.Repository{}, fmt.Errorf("%w: type the Repository name %q to confirm", ErrInvalid, repo.Name)
	}
	if len(strings.TrimSpace(reason)) < 3 {
		return store.Repository{}, fmt.Errorf("%w: a reason is required", ErrInvalid)
	}
	var out store.Repository
	err = s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		after := time.Now().Add(DeletionGrace)
		r, err := q.ScheduleRepositoryDeletion(ctx, store.ScheduleRepositoryDeletionParams{ID: id, OrgID: s.opts.OrgID, DeleteAfter: &after, DeleteReason: &reason})
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: Repository %s is %s", ErrConflict, repo.Name, repo.Status)
		}
		if err != nil {
			return err
		}
		out = r
		ev := s.event(p, m, audit.RepositoryDeleteScheduled)
		ev.TargetType, ev.TargetID, ev.Reason = "repository", id.String(), reason
		ev.Details = map[string]any{"delete_after": after}
		_, err = rec.Record(ctx, ev)
		return err
	})
	return out, err
}

// CancelRepositoryDeletion returns a pending Repository to service.
func (s *Service) CancelRepositoryDeletion(ctx context.Context, p *auth.Principal, id uuid.UUID, m auth.RequestMeta) (store.Repository, error) {
	var out store.Repository
	err := s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		r, err := q.CancelRepositoryDeletion(ctx, store.CancelRepositoryDeletionParams{ID: id, OrgID: s.opts.OrgID})
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: the Repository is not scheduled for deletion", ErrConflict)
		}
		if err != nil {
			return err
		}
		out = r
		ev := s.event(p, m, audit.RepositoryDeleteCanceled)
		ev.TargetType, ev.TargetID = "repository", id.String()
		_, err = rec.Record(ctx, ev)
		return err
	})
	return out, err
}

// RetireDueRepositories retires Repositories whose grace period ended.
func (s *Service) RetireDueRepositories(ctx context.Context) error {
	rows, err := s.q.RetireDueRepositories(ctx, s.opts.OrgID)
	if err != nil {
		return err
	}
	for _, r := range rows {
		_, _ = s.audit.Record(ctx, s.systemEvent(audit.RepositoryRetired, "repository", r.ID.String(), audit.Success,
			map[string]any{"name": r.Name, "reason": deref(r.DeleteReason)}))
	}
	return nil
}
