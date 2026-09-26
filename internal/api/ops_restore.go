// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/AxiomOperator/dbr2/internal/protection"
	"github.com/AxiomOperator/dbr2/internal/rbac"
	"github.com/AxiomOperator/dbr2/internal/store"
)

// RestoreBody selects what to restore and where.
type RestoreBody struct {
	TargetHostID string                 `json:"target_host_id,omitempty" format:"uuid" doc:"Host to restore to (default: the host the recovery point was captured on)."`
	Components   []string               `json:"components,omitempty" doc:"Component names from the manifest (default: every captured component). fsmeta records follow their volume or bind mount automatically."`
	PathRemaps   []protection.PathRemap `json:"path_remaps,omitempty" doc:"Bind-mount and config-file path prefix remapping, e.g. {\"from\":\"/srv/app\",\"to\":\"/srv/app-restored\"}."`
}

// StartRestoreBody adds the single-operator safeguards (ADR-0014).
type StartRestoreBody struct {
	RestoreBody
	Reason       string `json:"reason,omitempty" maxLength:"500" doc:"Mandatory for production restores (a change ticket such as INC-48391 is fine). Stored in the audit log."`
	Confirmation string `json:"confirmation,omitempty" maxLength:"200" doc:"Production restores: type the application name exactly."`
}

// RestoreRunDTO is one restore attempt (recovery history).
type RestoreRunDTO struct {
	ID                  string              `json:"id"`
	RecoveryPointID     string              `json:"recovery_point_id"`
	ApplicationID       string              `json:"application_id" format:"uuid"`
	ApplicationName     string              `json:"application_name"`
	SourceHostID        string              `json:"source_host_id" format:"uuid"`
	SourceHostname      string              `json:"source_hostname"`
	TargetHostID        string              `json:"target_host_id" format:"uuid"`
	TargetHostname      string              `json:"target_hostname"`
	TargetApplicationID *string             `json:"target_application_id"`
	Mode                string              `json:"mode" enum:"in_place,alternate_host"`
	Production          bool                `json:"production"`
	Components          []string            `json:"components"`
	PathRemaps          json.RawMessage     `json:"path_remaps"`
	Reason              *string             `json:"reason"`
	RequestedBy         string              `json:"requested_by"`
	State               string              `json:"state" enum:"requested,running,succeeded,failed,rolled_back"`
	Step                *string             `json:"step" doc:"Current step while running: grant-access, agent-access, images, stop-application, restore-data, recreate-containers, start-application, restore-database, health-check, commit."`
	Error               *string             `json:"error"`
	WorkflowID          *string             `json:"workflow_id"`
	CreatedAt           time.Time           `json:"created_at"`
	StartedAt           *time.Time          `json:"started_at"`
	FinishedAt          *time.Time          `json:"finished_at"`
	Preview             *protection.Preview `json:"preview,omitempty" doc:"Impact preview at request time (detail view)."`
	Result              json.RawMessage     `json:"result,omitempty" doc:"Per-component, container and health outcomes (detail view)."`
}

func restoreDTO(r store.RestoreRun, detail bool) RestoreRunDTO {
	d := RestoreRunDTO{ID: r.ID, RecoveryPointID: r.RecoveryPointID, ApplicationID: r.SourceApplicationID.String(), ApplicationName: r.ApplicationName,
		SourceHostID: r.SourceAgentID.String(), SourceHostname: r.SourceHostname, TargetHostID: r.TargetAgentID.String(), TargetHostname: r.TargetHostname, Mode: r.Mode,
		Production: r.Production, Components: r.Components, PathRemaps: r.PathRemaps, Reason: r.Reason, RequestedBy: r.RequestedByDisplay,
		State: r.State, Step: r.Step, Error: r.Error, WorkflowID: r.WorkflowID, CreatedAt: r.CreatedAt, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt}
	if r.TargetApplicationID != nil {
		s := r.TargetApplicationID.String()
		d.TargetApplicationID = &s
	}
	if detail {
		var p protection.Preview
		if json.Unmarshal(r.Preview, &p) == nil {
			d.Preview = &p
		}
		d.Result = r.Result
	}
	return d
}

func restoreRequest(rpID string, b RestoreBody) (protection.RestoreRequest, error) {
	req := protection.RestoreRequest{RecoveryPointID: rpID, Components: b.Components, PathRemaps: b.PathRemaps}
	if b.TargetHostID != "" {
		id, err := uuid.Parse(b.TargetHostID)
		if err != nil {
			return req, huma.Error422UnprocessableEntity("invalid target_host_id")
		}
		req.TargetAgentID = &id
	}
	return req, nil
}

func registerRestore(a huma.API, d *Deps) {
	const tag = "Restores"
	huma.Register(a, op("preview-restore", http.MethodPost, "/api/v1/recovery-points/{id}/restore-preview", tag,
		"Preview a restore", "Impact preview and collision detection against the target host's latest inventory: containers stopped, volumes "+
			"overwritten or created, bind paths replaced, containers re-created, networks created, images pulled by digest, published ports, and "+
			"whether the restore counts as a production restore. Nothing is changed.",
		rbac.RestoreExecute, http.StatusBadRequest, http.StatusNotFound, http.StatusConflict),
		func(ctx context.Context, in *struct {
			ID   string `path:"id" pattern:"^rp_[0-9A-HJKMNP-TV-Z]{26}$"`
			Body RestoreBody
		}) (*struct{ Body *protection.Preview }, error) {
			req, err := restoreRequest(in.ID, in.Body)
			if err != nil {
				return nil, err
			}
			p, err := d.Protection.PreviewRestore(ctx, req)
			if err != nil {
				return nil, d.protErr(ctx, err)
			}
			return &struct{ Body *protection.Preview }{p}, nil
		})

	huma.Register(a, withStatus(op("start-restore", http.MethodPost, "/api/v1/recovery-points/{id}/restores", tag,
		"Restore a recovery point",
		"Starts a restore workflow on the application's workflow ID (never concurrent with a backup or another restore). Data is restored into "+
			"staging, the fsmeta record is applied and verified, and it is swapped in with the previous content kept until the application is "+
			"healthy; any failure rolls back. **Production restores** (target tagged production, or overwriting a running application in place) "+
			"require `restore.production`, a typed confirmation (the application name) and a reason. Blocked by any collision (409).",
		rbac.RestoreExecute, http.StatusBadRequest, http.StatusNotFound, http.StatusConflict), http.StatusAccepted),
		func(ctx context.Context, in *struct {
			ID   string `path:"id" pattern:"^rp_[0-9A-HJKMNP-TV-Z]{26}$"`
			Body StartRestoreBody
		}) (*struct{ Body RestoreRunDTO }, error) {
			req, err := restoreRequest(in.ID, in.Body.RestoreBody)
			if err != nil {
				return nil, err
			}
			req.Reason, req.Confirmation = in.Body.Reason, in.Body.Confirmation
			run, _, err := d.Protection.StartRestore(ctx, principal(ctx), req, metaFrom(ctx))
			if err != nil {
				return nil, d.protErr(ctx, err)
			}
			return &struct{ Body RestoreRunDTO }{restoreDTO(run, false)}, nil
		})

	huma.Register(a, withStatus(op("cancel-restore", http.MethodPost, "/api/v1/restores/{id}/cancel", tag,
		"Cancel a restore", "Requests cancellation of a running restore. Compensation runs: the previous data and containers are put back and "+
			"the restore ends as rolled_back (or failed if nothing had changed yet).", rbac.RestoreExecute, http.StatusNotFound, http.StatusConflict), http.StatusAccepted),
		func(ctx context.Context, in *struct {
			ID string `path:"id" pattern:"^rs_[0-9A-HJKMNP-TV-Z]{26}$"`
		}) (*struct{}, error) {
			return nil, d.protErr(ctx, d.Protection.CancelRestore(ctx, principal(ctx), in.ID, metaFrom(ctx)))
		})

	huma.Register(a, op("list-restores", http.MethodGet, "/api/v1/restores", tag,
		"List restores", "Recovery history: every restore attempt, including failures and rollbacks, newest first.", rbac.RestoreRead),
		func(ctx context.Context, in *struct {
			ApplicationID string `query:"application_id" format:"uuid"`
			State         string `query:"state" enum:"requested,running,succeeded,failed,rolled_back"`
			Limit         int32  `query:"limit" minimum:"1" maximum:"500" default:"100"`
		}) (*struct {
			Body struct {
				Items []RestoreRunDTO `json:"items"`
			}
		}, error) {
			var app *uuid.UUID
			if in.ApplicationID != "" {
				id, err := parseID(in.ApplicationID)
				if err != nil {
					return nil, err
				}
				app = &id
			}
			var state *string
			if in.State != "" {
				state = &in.State
			}
			rows, err := d.Protection.ListRestores(ctx, app, state, in.Limit)
			if err != nil {
				return nil, d.protErr(ctx, err)
			}
			out := &struct {
				Body struct {
					Items []RestoreRunDTO `json:"items"`
				}
			}{}
			out.Body.Items = []RestoreRunDTO{}
			for _, r := range rows {
				out.Body.Items = append(out.Body.Items, restoreDTO(r, false))
			}
			return out, nil
		})

	huma.Register(a, op("get-restore", http.MethodGet, "/api/v1/restores/{id}", tag,
		"Get a restore", "Includes the impact preview recorded at request time and the result document.", rbac.RestoreRead, http.StatusNotFound),
		func(ctx context.Context, in *struct {
			ID string `path:"id" pattern:"^rs_[0-9A-HJKMNP-TV-Z]{26}$"`
		}) (*struct{ Body RestoreRunDTO }, error) {
			r, err := d.Protection.GetRestore(ctx, in.ID)
			if err != nil {
				return nil, d.protErr(ctx, err)
			}
			return &struct{ Body RestoreRunDTO }{restoreDTO(r, true)}, nil
		})
}
