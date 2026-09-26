// SPDX-License-Identifier: Apache-2.0

package protection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/oklog/ulid/v2"

	"github.com/AxiomOperator/dbr2/internal/audit"
	"github.com/AxiomOperator/dbr2/internal/auth"
	"github.com/AxiomOperator/dbr2/internal/fleet"
	"github.com/AxiomOperator/dbr2/internal/inventory"
	"github.com/AxiomOperator/dbr2/internal/manifest"
	"github.com/AxiomOperator/dbr2/internal/rbac"
	"github.com/AxiomOperator/dbr2/internal/store"
	"github.com/AxiomOperator/dbr2/internal/temporalx"
	"github.com/AxiomOperator/dbr2/workflows/restore"
)

// ErrForbidden is returned when a production restore lacks restore.production.
var ErrForbidden = errors.New("forbidden")

// RestoreRequest asks to restore a recovery point.
type RestoreRequest struct {
	RecoveryPointID string
	// TargetAgentID defaults to the source host.
	TargetAgentID *uuid.UUID
	// Components to restore (default: every captured component).
	Components []string
	PathRemaps []PathRemap
	// Reason is mandatory for production restores (ADR-0014).
	Reason string
	// Confirmation must equal the application name for production
	// restores (typed confirmation, ADR-0014).
	Confirmation string
}

type restoreContext struct {
	rp       store.RecoveryPoint
	m        *manifest.Manifest
	target   fleet.Agent
	selected []manifest.Component
	preview  *Preview
}

func (s *Service) resolveRestore(ctx context.Context, q *store.Queries, req RestoreRequest) (*restoreContext, error) {
	rp, err := q.GetRecoveryPoint(ctx, store.GetRecoveryPointParams{ID: req.RecoveryPointID, OrgID: s.opts.OrgID})
	if err != nil {
		return nil, notFound(err)
	}
	if rp.State != "committed" || len(rp.Manifest) == 0 {
		return nil, fmt.Errorf("%w: recovery point %s is %s; only committed recovery points can be restored", ErrConflict, rp.ID, rp.State)
	}
	m, err := manifest.Parse(rp.Manifest)
	if err != nil {
		return nil, err
	}
	targetID := rp.AgentID
	if req.TargetAgentID != nil {
		targetID = *req.TargetAgentID
	}
	target, err := s.fleet.GetAgent(ctx, targetID)
	if err != nil {
		return nil, err
	}
	for _, r := range req.PathRemaps {
		if !strings.HasPrefix(r.From, "/") || !strings.HasPrefix(r.To, "/") || strings.Contains(r.To, "..") {
			return nil, fmt.Errorf("%w: path remaps must be absolute paths without '..'", ErrInvalid)
		}
	}
	selected, err := selectComponents(m, req.Components)
	if err != nil {
		return nil, err
	}
	in := previewInput{m: m, targetAgentID: target.ID.String(), targetHostname: target.Hostname, selected: req.Components, remaps: req.PathRemaps}
	if inv, _, err := s.fleet.Inventory(ctx, target.ID); err == nil {
		in.inv = inv
	} else if !errors.Is(err, fleet.ErrNoInventory) {
		return nil, err
	}
	if app := s.targetApplication(ctx, q, rp, m, target.ID); app != nil {
		in.targetAppID = app.ID.String()
		if app.Environment != nil {
			in.targetAppEnv = *app.Environment
		}
		in.manual = app.ManualContainers
	}
	p := computePreview(in, selected)
	if target.Status != "active" {
		p.Blocked = true
		p.Collisions = append(p.Collisions, Collision{Kind: "dependency", Name: target.Hostname, Detail: "target host is " + target.Status})
	}
	return &restoreContext{rp: rp, m: m, target: target, selected: selected, preview: p}, nil
}

// targetApplication finds the application a restore would overwrite on
// the target host: the source application itself on its own host,
// otherwise the application with the same key (Compose project or
// container name).
func (s *Service) targetApplication(ctx context.Context, q *store.Queries, rp store.RecoveryPoint, m *manifest.Manifest, target uuid.UUID) *store.Application {
	apps, err := q.ListAgentApplications(ctx, target)
	if err != nil {
		return nil
	}
	key := ""
	switch {
	case m.Application.ComposeProject != "":
		key = inventory.KindCompose + ":" + m.Application.ComposeProject
	case m.Topology != nil && len(m.Topology.Containers) == 1:
		key = inventory.KindContainer + ":" + m.Topology.Containers[0].Name
	}
	for i := range apps {
		if apps[i].ID == rp.ApplicationID || (key != "" && apps[i].Key == key) {
			return &apps[i]
		}
	}
	return nil
}

// PreviewRestore computes the impact preview without starting anything.
func (s *Service) PreviewRestore(ctx context.Context, req RestoreRequest) (*Preview, error) {
	rc, err := s.resolveRestore(ctx, s.q, req)
	if err != nil {
		return nil, err
	}
	return rc.preview, nil
}

// NewRestoreID returns a new rs_<ULID>.
func NewRestoreID() string {
	return "rs_" + ulid.Make().String()
}

// StartRestore validates the safeguards, records the attempt and starts the
// restore workflow on application/<id> (so it cannot overlap a backup or
// another restore; ADR-0011).
func (s *Service) StartRestore(ctx context.Context, p *auth.Principal, req RestoreRequest, m auth.RequestMeta) (store.RestoreRun, *Preview, error) {
	rc, err := s.resolveRestore(ctx, s.q, req)
	if err != nil {
		return store.RestoreRun{}, nil, err
	}
	pv := rc.preview
	if pv.Blocked {
		return store.RestoreRun{}, pv, fmt.Errorf("%w: the restore is blocked by %d collision(s); see the preview", ErrConflict, len(pv.Collisions))
	}
	reason := strings.TrimSpace(req.Reason)
	if pv.Production {
		if !p.Can(rbac.RestoreProduction) {
			return store.RestoreRun{}, pv, fmt.Errorf("%w: this is a production restore (%s) and requires restore.production", ErrForbidden, strings.Join(pv.ProductionReasons, "; "))
		}
		if strings.TrimSpace(req.Confirmation) != rc.m.Application.Name {
			return store.RestoreRun{}, pv, fmt.Errorf("%w: type the application name %q to confirm a production restore", ErrInvalid, rc.m.Application.Name)
		}
		if len(reason) < 3 {
			return store.RestoreRun{}, pv, fmt.Errorf("%w: a reason (or change ticket) is required for a production restore", ErrInvalid)
		}
	}
	if s.temporal == nil {
		return store.RestoreRun{}, pv, errors.New("workflow engine unavailable")
	}
	names := make([]string, 0, len(rc.selected))
	for _, c := range rc.selected {
		names = append(names, c.Name)
	}
	remaps, _ := json.Marshal(nonNilRemaps(req.PathRemaps))
	pvJSON, _ := json.Marshal(pv)
	var targetApp *uuid.UUID
	if pv.TargetApplicationID != "" {
		id := uuid.MustParse(pv.TargetApplicationID)
		targetApp = &id
	}
	id := NewRestoreID()
	var run store.RestoreRun
	err = s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		run, err = q.CreateRestoreRun(ctx, store.CreateRestoreRunParams{ID: id, OrgID: s.opts.OrgID, RecoveryPointID: rc.rp.ID,
			RepositoryID: rc.rp.RepositoryID, SourceApplicationID: rc.rp.ApplicationID, ApplicationName: rc.rp.ApplicationName,
			SourceAgentID: rc.rp.AgentID, SourceHostname: rc.rp.Hostname, TargetAgentID: rc.target.ID, TargetHostname: rc.target.Hostname, TargetApplicationID: targetApp,
			Mode: pv.Mode, Production: pv.Production, Components: names, PathRemaps: remaps, Preview: pvJSON, Reason: strPtr(reason),
			RequestedBy: &p.UserID, RequestedByDisplay: p.Username})
		if err != nil {
			return err
		}
		ev := s.event(p, m, audit.RestoreRequested)
		ev.TargetType, ev.TargetID, ev.Reason = "application", rc.rp.ApplicationID.String(), reason
		ev.Details = map[string]any{"restore_id": id, "recovery_point_id": rc.rp.ID, "target_host": rc.target.Hostname,
			"mode": pv.Mode, "production": pv.Production, "components": names, "path_remaps": req.PathRemaps}
		_, err = rec.Record(ctx, ev)
		return err
	})
	if err != nil {
		return store.RestoreRun{}, pv, err
	}
	claim := rc.rp.ApplicationID.String()
	if targetApp != nil {
		claim = targetApp.String()
	}
	wf, err := temporalx.StartApplicationOperation(ctx, s.temporal, s.opts.TaskQueue, claim, restore.RestoreWorkflow, restore.Input{RestoreID: id})
	if err != nil {
		msg := err.Error()
		_, _ = s.q.FinishRestoreRun(ctx, store.FinishRestoreRunParams{ID: id, State: "failed", Error: &msg})
		return store.RestoreRun{}, pv, err
	}
	_ = s.q.SetRestoreWorkflow(ctx, store.SetRestoreWorkflowParams{ID: id, WorkflowID: strPtr(wf.GetID()), RunID: strPtr(wf.GetRunID())})
	run.WorkflowID = strPtr(wf.GetID())
	return run, pv, nil
}

func nonNilRemaps(r []PathRemap) []PathRemap {
	if r == nil {
		return []PathRemap{}
	}
	return r
}

// CancelRestore cancels a running restore. Cancellation runs the saga, so
// the previous data and containers are put back (never Terminate; ADR-0005).
func (s *Service) CancelRestore(ctx context.Context, p *auth.Principal, id string, m auth.RequestMeta) error {
	r, err := s.GetRestore(ctx, id)
	if err != nil {
		return err
	}
	if (r.State != "requested" && r.State != "running") || r.WorkflowID == nil {
		return fmt.Errorf("%w: restore %s is %s", ErrConflict, id, r.State)
	}
	if err := s.temporal.CancelWorkflow(ctx, *r.WorkflowID, deref(r.RunID)); err != nil {
		return err
	}
	ev := s.event(p, m, audit.RestoreCancelRequested)
	ev.TargetType, ev.TargetID = "application", r.SourceApplicationID.String()
	ev.Details = map[string]any{"restore_id": id}
	_, err = s.audit.Record(ctx, ev)
	return err
}

// ListRestores lists restore attempts, newest first.
func (s *Service) ListRestores(ctx context.Context, appID *uuid.UUID, state *string, limit int32) ([]store.RestoreRun, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	return s.q.ListRestoreRuns(ctx, store.ListRestoreRunsParams{OrgID: s.opts.OrgID, Limit: limit, ApplicationID: appID, State: state})
}

// GetRestore returns one restore attempt.
func (s *Service) GetRestore(ctx context.Context, id string) (store.RestoreRun, error) {
	r, err := s.q.GetRestoreRun(ctx, store.GetRestoreRunParams{ID: id, OrgID: s.opts.OrgID})
	return r, notFound(err)
}

var errNoRows = pgx.ErrNoRows
