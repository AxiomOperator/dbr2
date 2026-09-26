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
	"github.com/robfig/cron/v3"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"

	"github.com/AxiomOperator/dbr2/internal/audit"
	"github.com/AxiomOperator/dbr2/internal/auth"
	"github.com/AxiomOperator/dbr2/internal/manifest"
	"github.com/AxiomOperator/dbr2/internal/store"
	"github.com/AxiomOperator/dbr2/workflows/backup"
	"github.com/AxiomOperator/dbr2/workflows/ops"
)

// Schedule presets accepted in place of a cron expression.
var SchedulePresets = map[string]string{
	"hourly": "0 * * * *", "daily": "0 1 * * *", "weekly": "0 1 * * 0", "monthly": "0 1 1 * *",
}

// ScheduleIDPrefix names per-application backup schedules.
const ScheduleIDPrefix = "backup/"

var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// PolicyInput creates or updates a Protection Policy.
type PolicyInput struct {
	Name            string
	Description     string
	Schedule        string // cron (5 fields) or a preset
	Timezone        string
	Enabled         bool
	ConsistencyMode *string
	RepositoryID    *uuid.UUID
	Retention       Retention
}

// Retention is a grandfather-father-son rule set (newest recovery point per
// period, for the latest N periods) plus keep_last.
type Retention struct {
	KeepLast, KeepHourly, KeepDaily, KeepWeekly, KeepMonthly, KeepYearly int32
}

// NormalizeSchedule validates a cron expression or preset.
func NormalizeSchedule(s string) (string, error) {
	s = strings.TrimSpace(s)
	if p, ok := SchedulePresets[strings.ToLower(s)]; ok {
		return p, nil
	}
	if _, err := cronParser.Parse(s); err != nil {
		return "", fmt.Errorf("%w: schedule must be hourly, daily, weekly, monthly or a 5-field cron expression: %v", ErrInvalid, err)
	}
	return s, nil
}

// NextRun returns the next time a schedule fires.
func NextRun(schedule, tz string, after time.Time) (time.Time, error) {
	sch, err := cronParser.Parse(schedule)
	if err != nil {
		return time.Time{}, err
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.Time{}, err
	}
	return sch.Next(after.In(loc)), nil
}

func (s *Service) validatePolicy(ctx context.Context, in *PolicyInput) error {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return fmt.Errorf("%w: name is required", ErrInvalid)
	}
	var err error
	if in.Schedule, err = NormalizeSchedule(in.Schedule); err != nil {
		return err
	}
	if in.Timezone == "" {
		in.Timezone = "UTC"
	}
	if _, err := time.LoadLocation(in.Timezone); err != nil {
		return fmt.Errorf("%w: unknown timezone %q", ErrInvalid, in.Timezone)
	}
	if m := in.ConsistencyMode; m != nil && *m != manifest.ModeLive && *m != manifest.ModeQuiesced && *m != manifest.ModeOffline {
		return fmt.Errorf("%w: consistency_mode must be live, quiesced or offline", ErrInvalid)
	}
	r := in.Retention
	if r.KeepLast < 1 || r.KeepHourly < 0 || r.KeepDaily < 0 || r.KeepWeekly < 0 || r.KeepMonthly < 0 || r.KeepYearly < 0 {
		return fmt.Errorf("%w: keep_last must be at least 1 and the other retention counts non-negative", ErrInvalid)
	}
	if in.RepositoryID != nil {
		if _, err := s.q.GetRepository(ctx, store.GetRepositoryParams{ID: *in.RepositoryID, OrgID: s.opts.OrgID}); err != nil {
			return fmt.Errorf("%w: unknown repository", ErrInvalid)
		}
	}
	return nil
}

// PolicyView is a policy with its assignment count and next run.
type PolicyView struct {
	store.ProtectionPolicy
	Applications int32
	NextRun      *time.Time
}

func view(p store.ProtectionPolicy, n int32) PolicyView {
	v := PolicyView{ProtectionPolicy: p, Applications: n}
	if p.Enabled {
		if t, err := NextRun(p.Schedule, p.Timezone, time.Now()); err == nil {
			v.NextRun = &t
		}
	}
	return v
}

// ListPolicies lists every policy.
func (s *Service) ListPolicies(ctx context.Context) ([]PolicyView, error) {
	rows, err := s.q.ListPolicies(ctx, s.opts.OrgID)
	if err != nil {
		return nil, err
	}
	out := make([]PolicyView, 0, len(rows))
	for _, r := range rows {
		out = append(out, view(store.ProtectionPolicy{ID: r.ID, OrgID: r.OrgID, Name: r.Name, Description: r.Description,
			Schedule: r.Schedule, Timezone: r.Timezone, Enabled: r.Enabled, ConsistencyMode: r.ConsistencyMode, RepositoryID: r.RepositoryID,
			KeepLast: r.KeepLast, KeepHourly: r.KeepHourly, KeepDaily: r.KeepDaily, KeepWeekly: r.KeepWeekly, KeepMonthly: r.KeepMonthly,
			KeepYearly: r.KeepYearly, CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}, r.ApplicationCount))
	}
	return out, nil
}

// GetPolicy returns one policy and its applications.
func (s *Service) GetPolicy(ctx context.Context, id uuid.UUID) (PolicyView, []store.ListApplicationsForPolicyRow, error) {
	p, err := s.q.GetPolicy(ctx, store.GetPolicyParams{ID: id, OrgID: s.opts.OrgID})
	if err != nil {
		return PolicyView{}, nil, notFound(err)
	}
	apps, err := s.q.ListApplicationsForPolicy(ctx, &p.ID)
	if err != nil {
		return PolicyView{}, nil, err
	}
	return view(p, int32(len(apps))), apps, nil
}

// CreatePolicy creates a policy.
func (s *Service) CreatePolicy(ctx context.Context, pr *auth.Principal, in PolicyInput, m auth.RequestMeta) (store.ProtectionPolicy, error) {
	if err := s.validatePolicy(ctx, &in); err != nil {
		return store.ProtectionPolicy{}, err
	}
	var out store.ProtectionPolicy
	err := s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		r := in.Retention
		p, err := q.CreatePolicy(ctx, store.CreatePolicyParams{OrgID: s.opts.OrgID, Name: in.Name, Description: in.Description,
			Schedule: in.Schedule, Timezone: in.Timezone, Enabled: in.Enabled, ConsistencyMode: in.ConsistencyMode, RepositoryID: in.RepositoryID,
			KeepLast: r.KeepLast, KeepHourly: r.KeepHourly, KeepDaily: r.KeepDaily, KeepWeekly: r.KeepWeekly, KeepMonthly: r.KeepMonthly,
			KeepYearly: r.KeepYearly, CreatedBy: &pr.UserID})
		if err != nil {
			if strings.Contains(err.Error(), "protection_policies_org_id_name_key") {
				return fmt.Errorf("%w: a policy named %q exists", ErrConflict, in.Name)
			}
			return err
		}
		out = p
		ev := s.event(pr, m, audit.PolicyCreated)
		ev.TargetType, ev.TargetID, ev.After = "policy", p.ID.String(), p
		_, err = rec.Record(ctx, ev)
		return err
	})
	return out, err
}

// UpdatePolicy replaces a policy and re-syncs its schedules.
func (s *Service) UpdatePolicy(ctx context.Context, pr *auth.Principal, id uuid.UUID, in PolicyInput, m auth.RequestMeta) (store.ProtectionPolicy, error) {
	if err := s.validatePolicy(ctx, &in); err != nil {
		return store.ProtectionPolicy{}, err
	}
	var out store.ProtectionPolicy
	err := s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		before, err := q.GetPolicy(ctx, store.GetPolicyParams{ID: id, OrgID: s.opts.OrgID})
		if err != nil {
			return notFound(err)
		}
		r := in.Retention
		out, err = q.UpdatePolicy(ctx, store.UpdatePolicyParams{ID: id, OrgID: s.opts.OrgID, Name: in.Name, Description: in.Description,
			Schedule: in.Schedule, Timezone: in.Timezone, Enabled: in.Enabled, ConsistencyMode: in.ConsistencyMode, RepositoryID: in.RepositoryID,
			KeepLast: r.KeepLast, KeepHourly: r.KeepHourly, KeepDaily: r.KeepDaily, KeepWeekly: r.KeepWeekly, KeepMonthly: r.KeepMonthly,
			KeepYearly: r.KeepYearly})
		if err != nil {
			return err
		}
		ev := s.event(pr, m, audit.PolicyUpdated)
		ev.TargetType, ev.TargetID, ev.Before, ev.After = "policy", id.String(), before, out
		_, err = rec.Record(ctx, ev)
		return err
	})
	if err == nil {
		s.syncSchedulesAsync()
	}
	return out, err
}

// DeletePolicy deletes a policy (its applications become unscheduled).
func (s *Service) DeletePolicy(ctx context.Context, pr *auth.Principal, id uuid.UUID, m auth.RequestMeta) error {
	err := s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		n, err := q.DeletePolicy(ctx, store.DeletePolicyParams{ID: id, OrgID: s.opts.OrgID})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		ev := s.event(pr, m, audit.PolicyDeleted)
		ev.TargetType, ev.TargetID = "policy", id.String()
		_, err = rec.Record(ctx, ev)
		return err
	})
	if err == nil {
		s.syncSchedulesAsync()
	}
	return err
}

// AssignPolicy sets (or clears, with nil) an application's policy.
func (s *Service) AssignPolicy(ctx context.Context, pr *auth.Principal, appID uuid.UUID, policyID *uuid.UUID, m auth.RequestMeta) error {
	if policyID != nil {
		if _, err := s.q.GetPolicy(ctx, store.GetPolicyParams{ID: *policyID, OrgID: s.opts.OrgID}); err != nil {
			return fmt.Errorf("%w: unknown policy", ErrInvalid)
		}
	}
	err := s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		n, err := q.SetApplicationPolicy(ctx, store.SetApplicationPolicyParams{ID: appID, OrgID: s.opts.OrgID, PolicyID: policyID})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		ev := s.event(pr, m, audit.ApplicationPolicyAssigned)
		ev.TargetType, ev.TargetID = "application", appID.String()
		ev.Details = map[string]any{"policy_id": policyID}
		_, err = rec.Record(ctx, ev)
		return err
	})
	if err == nil {
		s.syncSchedulesAsync()
	}
	return err
}

// ---- Temporal schedules (ADR-0011: schedule → trigger → child) ---------------------

func (s *Service) syncSchedulesAsync() {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := s.SyncSchedules(ctx); err != nil && s.opts.Log != nil {
			s.opts.Log.Warn("syncing backup schedules failed (retried by the janitor)", "err", err)
		}
	}()
}

// ScheduleID is the Temporal schedule of an application's backups.
func ScheduleID(appID uuid.UUID) string { return ScheduleIDPrefix + appID.String() }

// SyncSchedules makes the Temporal schedules match the policy assignments:
// one schedule per application with an enabled policy (missing applications
// excluded); overlap SKIP, recorded by the trigger.
func (s *Service) SyncSchedules(ctx context.Context) error {
	if s.temporal == nil {
		return errors.New("workflow engine unavailable")
	}
	rows, err := s.q.ListPolicyAssignments(ctx, s.opts.OrgID)
	if err != nil {
		return err
	}
	type want struct{ cron, tz string }
	desired := map[string]want{}
	appOf := map[string]string{}
	for _, r := range rows {
		if !r.Enabled || r.MissingSince != nil {
			continue
		}
		id := ScheduleID(r.ApplicationID)
		desired[id] = want{r.Schedule, r.Timezone}
		appOf[id] = r.ApplicationID.String()
	}
	sc := s.temporal.ScheduleClient()
	existing := map[string]bool{}
	it, err := sc.List(ctx, client.ScheduleListOptions{PageSize: 1000})
	if err != nil {
		return err
	}
	for it.HasNext() {
		e, err := it.Next()
		if err != nil {
			return err
		}
		if strings.HasPrefix(e.ID, ScheduleIDPrefix) {
			existing[e.ID] = true
		}
	}
	var errs []error
	for id, w := range desired {
		spec := client.ScheduleSpec{CronExpressions: []string{w.cron}, TimeZoneName: w.tz}
		action := &client.ScheduleWorkflowAction{ID: "trigger/" + id, Workflow: ops.ScheduledOperationTrigger,
			Args: []any{ops.TriggerInput{ApplicationID: appOf[id], WorkflowType: "BackupWorkflow",
				Args: []any{backup.Input{ApplicationID: appOf[id], Trigger: "scheduled"}}}},
			TaskQueue: s.opts.TaskQueue, WorkflowExecutionTimeout: ops.ScheduleTimeout}
		if existing[id] {
			err = sc.GetHandle(ctx, id).Update(ctx, client.ScheduleUpdateOptions{DoUpdate: func(in client.ScheduleUpdateInput) (*client.ScheduleUpdate, error) {
				sch := in.Description.Schedule
				sch.Spec, sch.Action = &spec, action
				if sch.Policy == nil {
					sch.Policy = &client.SchedulePolicies{}
				}
				sch.Policy.Overlap = enumspb.SCHEDULE_OVERLAP_POLICY_SKIP
				return &client.ScheduleUpdate{Schedule: &sch}, nil
			}})
		} else {
			_, err = sc.Create(ctx, client.ScheduleOptions{ID: id, Spec: spec, Action: action, Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP})
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("schedule %s: %w", id, err))
		}
	}
	for id := range existing {
		if _, ok := desired[id]; !ok {
			var nf *serviceerror.NotFound
			if err := sc.GetHandle(ctx, id).Delete(ctx); err != nil && !errors.As(err, &nf) {
				errs = append(errs, fmt.Errorf("delete schedule %s: %w", id, err))
			}
		}
	}
	return errors.Join(errs...)
}

// policyFor returns an application's policy, if any.
func (s *Service) policyFor(ctx context.Context, q *store.Queries, policyID *uuid.UUID) *store.ProtectionPolicy {
	if policyID == nil {
		return nil
	}
	p, err := q.GetPolicy(ctx, store.GetPolicyParams{ID: *policyID, OrgID: s.opts.OrgID})
	if errors.Is(err, pgx.ErrNoRows) || err != nil {
		return nil
	}
	return &p
}

// NewPolicyView wraps a policy with its next run (assignment count 0).
func NewPolicyView(p store.ProtectionPolicy) PolicyView { return view(p, 0) }
