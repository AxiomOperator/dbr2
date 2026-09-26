// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/AxiomOperator/dbr2/internal/protection"
	"github.com/AxiomOperator/dbr2/internal/rbac"
	"github.com/AxiomOperator/dbr2/internal/store"
)

// RetentionDTO is a grandfather-father-son retention rule set.
type RetentionDTO struct {
	KeepLast    int32 `json:"keep_last" minimum:"1" doc:"Newest recovery points always kept."`
	KeepHourly  int32 `json:"keep_hourly" minimum:"0" doc:"Newest per hour, for this many hours."`
	KeepDaily   int32 `json:"keep_daily" minimum:"0"`
	KeepWeekly  int32 `json:"keep_weekly" minimum:"0"`
	KeepMonthly int32 `json:"keep_monthly" minimum:"0"`
	KeepYearly  int32 `json:"keep_yearly" minimum:"0"`
}

// RetentionInput is RetentionDTO with defaults for omitted counts.
type RetentionInput struct {
	KeepLast    int32 `json:"keep_last,omitempty" minimum:"1" default:"7"`
	KeepHourly  int32 `json:"keep_hourly,omitempty" minimum:"0" default:"0"`
	KeepDaily   int32 `json:"keep_daily,omitempty" minimum:"0" default:"14"`
	KeepWeekly  int32 `json:"keep_weekly,omitempty" minimum:"0" default:"8"`
	KeepMonthly int32 `json:"keep_monthly,omitempty" minimum:"0" default:"12"`
	KeepYearly  int32 `json:"keep_yearly,omitempty" minimum:"0" default:"0"`
}

// PolicyBody creates or replaces a policy.
type PolicyBody struct {
	Name            string         `json:"name" minLength:"1" maxLength:"100"`
	Description     string         `json:"description,omitempty" maxLength:"500"`
	Schedule        string         `json:"schedule" doc:"hourly, daily, weekly, monthly or a 5-field cron expression (minute hour day month weekday)."`
	Timezone        string         `json:"timezone,omitempty" doc:"IANA timezone for the schedule (default UTC)."`
	Enabled         bool           `json:"enabled"`
	ConsistencyMode *string        `json:"consistency_mode,omitempty" enum:"live,quiesced,offline" doc:"Used when the application sets none."`
	RepositoryID    *string        `json:"repository_id,omitempty" format:"uuid" doc:"Used when the application sets none (default: the default Repository)."`
	Retention       RetentionInput `json:"retention"`
}

// PolicyDTO is a Protection Policy.
type PolicyDTO struct {
	ID              string       `json:"id" format:"uuid"`
	Name            string       `json:"name"`
	Description     string       `json:"description"`
	Schedule        string       `json:"schedule" doc:"Cron expression (presets are stored as cron)."`
	Timezone        string       `json:"timezone"`
	Enabled         bool         `json:"enabled"`
	ConsistencyMode *string      `json:"consistency_mode"`
	RepositoryID    *string      `json:"repository_id"`
	Retention       RetentionDTO `json:"retention"`
	Applications    int32        `json:"applications"`
	NextRun         *time.Time   `json:"next_run"`
	CreatedAt       time.Time    `json:"created_at"`
	UpdatedAt       time.Time    `json:"updated_at"`
}

// PolicyApplicationDTO is an application assigned to a policy.
type PolicyApplicationDTO struct {
	ID     string `json:"id" format:"uuid"`
	Name   string `json:"name"`
	HostID string `json:"host_id" format:"uuid"`
}

func policyDTO(v protection.PolicyView) PolicyDTO {
	p := v.ProtectionPolicy
	d := PolicyDTO{ID: p.ID.String(), Name: p.Name, Description: p.Description, Schedule: p.Schedule, Timezone: p.Timezone, Enabled: p.Enabled,
		ConsistencyMode: p.ConsistencyMode, Applications: v.Applications, NextRun: v.NextRun, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
		Retention: RetentionDTO{KeepLast: p.KeepLast, KeepHourly: p.KeepHourly, KeepDaily: p.KeepDaily, KeepWeekly: p.KeepWeekly,
			KeepMonthly: p.KeepMonthly, KeepYearly: p.KeepYearly}}
	if p.RepositoryID != nil {
		s := p.RepositoryID.String()
		d.RepositoryID = &s
	}
	return d
}

func policyInput(b PolicyBody) (protection.PolicyInput, error) {
	in := protection.PolicyInput{Name: b.Name, Description: b.Description, Schedule: b.Schedule, Timezone: b.Timezone, Enabled: b.Enabled,
		ConsistencyMode: b.ConsistencyMode, Retention: protection.Retention{KeepLast: b.Retention.KeepLast, KeepHourly: b.Retention.KeepHourly,
			KeepDaily: b.Retention.KeepDaily, KeepWeekly: b.Retention.KeepWeekly, KeepMonthly: b.Retention.KeepMonthly, KeepYearly: b.Retention.KeepYearly}}
	if b.RepositoryID != nil && *b.RepositoryID != "" {
		id, err := uuid.Parse(*b.RepositoryID)
		if err != nil {
			return in, huma.Error422UnprocessableEntity("invalid repository_id")
		}
		in.RepositoryID = &id
	}
	return in, nil
}

// ContractDTO is an application's Recovery Contract.
type ContractDTO struct {
	ApplicationID      string     `json:"application_id" format:"uuid"`
	ApplicationName    string     `json:"application_name,omitempty"`
	MaxRPOMinutes      *int32     `json:"max_rpo_minutes" doc:"Maximum age of the latest recovery point (null = no RPO)."`
	RequiredComponents []string   `json:"required_components"`
	State              string     `json:"state" enum:"satisfied,violated,unknown"`
	StateReasons       []string   `json:"state_reasons"`
	EvaluatedAt        *time.Time `json:"evaluated_at"`
	ViolatedSince      *time.Time `json:"violated_since"`
}

func contractDTO(c store.RecoveryContract, name string) ContractDTO {
	return ContractDTO{ApplicationID: c.ApplicationID.String(), ApplicationName: name, MaxRPOMinutes: c.MaxRpoMinutes,
		RequiredComponents: nonNilStrings(c.RequiredComponents), State: c.State, StateReasons: nonNilStrings(c.StateReasons),
		EvaluatedAt: c.EvaluatedAt, ViolatedSince: c.ViolatedSince}
}

type confirmBody struct {
	Body struct {
		Confirmation string `json:"confirmation" doc:"Type the application (or Repository) name."`
		Reason       string `json:"reason" minLength:"3" maxLength:"500"`
	}
}

func registerPolicies(a huma.API, d *Deps) {
	const tag = "Policies"
	type listOut struct {
		Body struct {
			Items []PolicyDTO `json:"items"`
		}
	}
	huma.Register(a, op("list-policies", http.MethodGet, "/api/v1/policies", tag, "List Protection Policies",
		"Schedules and retention; applications are assigned to at most one policy.", rbac.PolicyRead),
		func(ctx context.Context, _ *struct{}) (*listOut, error) {
			rows, err := d.Protection.ListPolicies(ctx)
			if err != nil {
				return nil, d.protErr(ctx, err)
			}
			out := &listOut{}
			out.Body.Items = []PolicyDTO{}
			for _, r := range rows {
				out.Body.Items = append(out.Body.Items, policyDTO(r))
			}
			return out, nil
		})

	type policyOut struct {
		Body struct {
			PolicyDTO
			AssignedApplications []PolicyApplicationDTO `json:"assigned_applications"`
		}
	}
	huma.Register(a, op("get-policy", http.MethodGet, "/api/v1/policies/{id}", tag, "Get a policy", "", rbac.PolicyRead, http.StatusNotFound),
		func(ctx context.Context, in *idPath) (*policyOut, error) {
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			v, apps, err := d.Protection.GetPolicy(ctx, id)
			if err != nil {
				return nil, d.protErr(ctx, err)
			}
			out := &policyOut{}
			out.Body.PolicyDTO = policyDTO(v)
			out.Body.AssignedApplications = []PolicyApplicationDTO{}
			for _, x := range apps {
				out.Body.AssignedApplications = append(out.Body.AssignedApplications, PolicyApplicationDTO{ID: x.ID.String(), Name: x.Name, HostID: x.AgentID.String()})
			}
			return out, nil
		})

	huma.Register(a, withStatus(op("create-policy", http.MethodPost, "/api/v1/policies", tag, "Create a policy",
		"Assign applications with PUT /applications/{id}/policy; each gets a Temporal schedule (overlapping runs are skipped and recorded).",
		rbac.PolicyManage, http.StatusBadRequest, http.StatusConflict), http.StatusCreated),
		func(ctx context.Context, in *struct{ Body PolicyBody }) (*struct{ Body PolicyDTO }, error) {
			pin, err := policyInput(in.Body)
			if err != nil {
				return nil, err
			}
			p, err := d.Protection.CreatePolicy(ctx, principal(ctx), pin, metaFrom(ctx))
			if err != nil {
				return nil, d.protErr(ctx, err)
			}
			return &struct{ Body PolicyDTO }{policyDTO(protection.NewPolicyView(p))}, nil
		})

	huma.Register(a, op("update-policy", http.MethodPut, "/api/v1/policies/{id}", tag, "Replace a policy",
		"Schedules of assigned applications are updated.", rbac.PolicyManage, http.StatusBadRequest, http.StatusNotFound),
		func(ctx context.Context, in *struct {
			ID   string `path:"id" format:"uuid"`
			Body PolicyBody
		}) (*struct{ Body PolicyDTO }, error) {
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			pin, err := policyInput(in.Body)
			if err != nil {
				return nil, err
			}
			p, err := d.Protection.UpdatePolicy(ctx, principal(ctx), id, pin, metaFrom(ctx))
			if err != nil {
				return nil, d.protErr(ctx, err)
			}
			return &struct{ Body PolicyDTO }{policyDTO(protection.NewPolicyView(p))}, nil
		})

	huma.Register(a, withStatus(op("delete-policy", http.MethodDelete, "/api/v1/policies/{id}", tag, "Delete a policy",
		"Assigned applications are no longer scheduled; their recovery points are kept.", rbac.PolicyManage, http.StatusNotFound), http.StatusNoContent),
		func(ctx context.Context, in *idPath) (*struct{}, error) {
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			return nil, d.protErr(ctx, d.Protection.DeletePolicy(ctx, principal(ctx), id, metaFrom(ctx)))
		})

	huma.Register(a, withStatus(op("assign-policy", http.MethodPut, "/api/v1/applications/{id}/policy", tag, "Assign a policy",
		"`policy_id: null` removes the assignment.", rbac.PolicyManage, http.StatusBadRequest, http.StatusNotFound), http.StatusNoContent),
		func(ctx context.Context, in *struct {
			ID   string `path:"id" format:"uuid"`
			Body struct {
				PolicyID *string `json:"policy_id" format:"uuid"`
			}
		}) (*struct{}, error) {
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			var pid *uuid.UUID
			if in.Body.PolicyID != nil && *in.Body.PolicyID != "" {
				x, err := parseID(*in.Body.PolicyID)
				if err != nil {
					return nil, err
				}
				pid = &x
			}
			return nil, d.protErr(ctx, d.Protection.AssignPolicy(ctx, principal(ctx), id, pid, metaFrom(ctx)))
		})

	// ---- contracts ----
	huma.Register(a, op("list-contracts", http.MethodGet, "/api/v1/contracts", tag, "List Recovery Contracts",
		"Every contract with its state (satisfied, violated) and reasons (RPO violation reporting).", rbac.PolicyRead),
		func(ctx context.Context, _ *struct{}) (*struct {
			Body struct {
				Items []ContractDTO `json:"items"`
			}
		}, error) {
			rows, err := d.Protection.ListContractViews(ctx)
			if err != nil {
				return nil, d.protErr(ctx, err)
			}
			out := &struct {
				Body struct {
					Items []ContractDTO `json:"items"`
				}
			}{}
			out.Body.Items = []ContractDTO{}
			for _, r := range rows {
				out.Body.Items = append(out.Body.Items, contractDTO(r.RecoveryContract, r.ApplicationName))
			}
			return out, nil
		})

	huma.Register(a, op("get-contract", http.MethodGet, "/api/v1/applications/{id}/contract", tag, "Get a Recovery Contract",
		"404 when the application has no contract.", rbac.PolicyRead, http.StatusNotFound),
		func(ctx context.Context, in *idPath) (*struct{ Body ContractDTO }, error) {
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			c, err := d.Protection.GetContract(ctx, id)
			if err != nil {
				return nil, d.protErr(ctx, err)
			}
			if c == nil {
				return nil, problem(http.StatusNotFound, CodeNotFound, "no recovery contract")
			}
			return &struct{ Body ContractDTO }{contractDTO(*c, "")}, nil
		})

	huma.Register(a, op("put-contract", http.MethodPut, "/api/v1/applications/{id}/contract", tag, "Set a Recovery Contract",
		"v1.0 subset: maximum RPO and required components. Evaluated every 5 minutes and after each change; violations raise critical alerts. "+
			"Each recovery point also records the contract evaluation at capture time in its manifest.", rbac.PolicyManage, http.StatusBadRequest, http.StatusNotFound),
		func(ctx context.Context, in *struct {
			ID   string `path:"id" format:"uuid"`
			Body struct {
				MaxRPOMinutes      *int32   `json:"max_rpo_minutes,omitempty" minimum:"1"`
				RequiredComponents []string `json:"required_components"`
			}
		}) (*struct{ Body ContractDTO }, error) {
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			c, err := d.Protection.PutContract(ctx, principal(ctx), id, &protection.ContractSpec{MaxRPOMinutes: in.Body.MaxRPOMinutes,
				RequiredComponents: in.Body.RequiredComponents}, metaFrom(ctx))
			if err != nil {
				return nil, d.protErr(ctx, err)
			}
			return &struct{ Body ContractDTO }{contractDTO(*c, "")}, nil
		})

	huma.Register(a, withStatus(op("delete-contract", http.MethodDelete, "/api/v1/applications/{id}/contract", tag, "Remove a Recovery Contract",
		"", rbac.PolicyManage, http.StatusNotFound), http.StatusNoContent),
		func(ctx context.Context, in *idPath) (*struct{}, error) {
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			_, err = d.Protection.PutContract(ctx, principal(ctx), id, nil, metaFrom(ctx))
			return nil, d.protErr(ctx, err)
		})

	// ---- deletions with a grace period (ADR-0014) ----
	huma.Register(a, op("delete-recovery-point", http.MethodPost, "/api/v1/recovery-points/{id}/delete", "Backups",
		"Delete a recovery point (grace period)", "Schedules deletion after a 7-day grace period; until then it can be undeleted and still restored. "+
			"Requires typing the application name and a reason.", rbac.BackupDelete, http.StatusBadRequest, http.StatusNotFound, http.StatusConflict),
		func(ctx context.Context, in *struct {
			ID string `path:"id" pattern:"^rp_[0-9A-HJKMNP-TV-Z]{26}$"`
			confirmBody
		}) (*struct{ Body RecoveryPointDTO }, error) {
			r, err := d.Protection.ScheduleRecoveryPointDeletion(ctx, principal(ctx), in.ID, in.Body.Confirmation, in.Body.Reason, metaFrom(ctx))
			if err != nil {
				return nil, d.protErr(ctx, err)
			}
			return &struct{ Body RecoveryPointDTO }{rpDTO(r, false)}, nil
		})

	huma.Register(a, op("undelete-recovery-point", http.MethodPost, "/api/v1/recovery-points/{id}/undelete", "Backups",
		"Cancel a scheduled deletion", "", rbac.BackupDelete, http.StatusNotFound, http.StatusConflict),
		func(ctx context.Context, in *struct {
			ID string `path:"id" pattern:"^rp_[0-9A-HJKMNP-TV-Z]{26}$"`
		}) (*struct{ Body RecoveryPointDTO }, error) {
			r, err := d.Protection.CancelRecoveryPointDeletion(ctx, principal(ctx), in.ID, metaFrom(ctx))
			if err != nil {
				return nil, d.protErr(ctx, err)
			}
			return &struct{ Body RecoveryPointDTO }{rpDTO(r, false)}, nil
		})

	huma.Register(a, op("delete-repository", http.MethodPost, "/api/v1/repositories/{id}/delete", "Repositories",
		"Delete a Repository (grace period)", "Stops using the Repository now and retires it after 7 days (undo until then). DBR² never erases the "+
			"stored data; remove the NAS share yourself afterwards. Requires typing the Repository name and a reason.",
		rbac.RepositoryManage, http.StatusBadRequest, http.StatusNotFound, http.StatusConflict),
		func(ctx context.Context, in *struct {
			ID string `path:"id" format:"uuid"`
			confirmBody
		}) (*struct{ Body RepositoryDTO }, error) {
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			r, err := d.Protection.ScheduleRepositoryDeletion(ctx, principal(ctx), id, in.Body.Confirmation, in.Body.Reason, metaFrom(ctx))
			if err != nil {
				return nil, d.protErr(ctx, err)
			}
			return &struct{ Body RepositoryDTO }{repositoryDTO(protection.RepositoryView{Repository: r})}, nil
		})

	huma.Register(a, op("undelete-repository", http.MethodPost, "/api/v1/repositories/{id}/undelete", "Repositories",
		"Cancel a Repository deletion", "", rbac.RepositoryManage, http.StatusNotFound, http.StatusConflict),
		func(ctx context.Context, in *idPath) (*struct{ Body RepositoryDTO }, error) {
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			r, err := d.Protection.CancelRepositoryDeletion(ctx, principal(ctx), id, metaFrom(ctx))
			if err != nil {
				return nil, d.protErr(ctx, err)
			}
			return &struct{ Body RepositoryDTO }{repositoryDTO(protection.RepositoryView{Repository: r})}, nil
		})

	// ---- verification (Phase 9) ----
	huma.Register(a, withStatus(op("verify-repository", http.MethodPost, "/api/v1/repositories/{id}/verify", "Repositories",
		"Verify a Repository", "Starts repository/{id}/verify: the least recently verified recovery points are checked (every object present, "+
			"`read_percent` of files fully read and hash-verified). Results set each recovery point to verified or verification_failed "+
			"(critical alert). A weekly schedule verifies every Repository.", rbac.RepositoryManage, http.StatusNotFound, http.StatusConflict), http.StatusAccepted),
		func(ctx context.Context, in *struct {
			ID   string `path:"id" format:"uuid"`
			Body *struct {
				RecoveryPointID string  `json:"recovery_point_id,omitempty" doc:"Verify only this recovery point."`
				ReadPercent     float64 `json:"read_percent,omitempty" minimum:"0" maximum:"100" doc:"Default 10."`
			} `required:"false"`
		}) (*workflowOut, error) {
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			rp, pct := "", 0.0
			if in.Body != nil {
				rp, pct = in.Body.RecoveryPointID, in.Body.ReadPercent
			}
			wf, err := d.Protection.StartVerification(ctx, principal(ctx), id, rp, pct, metaFrom(ctx))
			if err != nil {
				return nil, d.protErr(ctx, err)
			}
			return wfOut(wf), nil
		})

	// ---- escrow health, regeneration and drills (Phase 9, ADR-0008) ----
	huma.Register(a, op("get-escrow-health", http.MethodGet, "/api/v1/escrow/health", "Repositories", "Escrow health",
		"Recipients configured, packages confirmed and current (regenerate after recipients change), re-confirmation within 90 days, and "+
			"an escrow drill within 12 months. Checked hourly; problems raise `escrow.unhealthy` alerts.", rbac.RepositoryRead),
		func(ctx context.Context, _ *struct{}) (*struct{ Body protection.EscrowHealth }, error) {
			h, err := d.Protection.CheckEscrowHealth(ctx)
			if err != nil {
				return nil, d.protErr(ctx, err)
			}
			return &struct{ Body protection.EscrowHealth }{h}, nil
		})

	type escrowPkgOut struct {
		Body struct {
			Repository     RepositoryDTO `json:"repository"`
			EscrowPackage  string        `json:"escrow_package"`
			EscrowFilename string        `json:"escrow_filename"`
		}
	}
	huma.Register(a, op("regenerate-repository-escrow", http.MethodPost, "/api/v1/repositories/{id}/escrow/regenerate", "Repositories",
		"Regenerate the escrow package", "Re-seals the repository password (read from the reposerver, not stored) to the current escrow recipients. "+
			"The Repository stays usable; enter the new package's confirmation code to confirm escrow again.",
		rbac.RepositoryManage, http.StatusNotFound, http.StatusConflict),
		func(ctx context.Context, in *idPath) (*escrowPkgOut, error) {
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			c, err := d.Protection.RegenerateEscrow(ctx, principal(ctx), id, metaFrom(ctx))
			if err != nil {
				return nil, d.protErr(ctx, err)
			}
			out := &escrowPkgOut{}
			out.Body.Repository = repositoryDTO(protection.RepositoryView{Repository: c.Repository})
			out.Body.EscrowPackage, out.Body.EscrowFilename = string(c.EscrowPackage), "dbr2-escrow-repository-"+c.Repository.Name+".age"
			return out, nil
		})

	type drillDTO struct {
		ID          string     `json:"id" format:"uuid"`
		CreatedAt   time.Time  `json:"created_at"`
		CompletedAt *time.Time `json:"completed_at"`
		Recipients  int        `json:"recipients"`
		Package     string     `json:"package,omitempty" doc:"The drill package (only when it is created)."`
	}
	huma.Register(a, op("list-escrow-drills", http.MethodGet, "/api/v1/escrow/drills", "Repositories", "List escrow drills", "", rbac.RepositoryRead),
		func(ctx context.Context, _ *struct{}) (*struct {
			Body struct {
				Items []drillDTO `json:"items"`
			}
		}, error) {
			rows, err := d.Protection.ListEscrowDrills(ctx)
			if err != nil {
				return nil, d.protErr(ctx, err)
			}
			out := &struct {
				Body struct {
					Items []drillDTO `json:"items"`
				}
			}{}
			out.Body.Items = []drillDTO{}
			for _, r := range rows {
				out.Body.Items = append(out.Body.Items, drillDTO{ID: r.ID.String(), CreatedAt: r.CreatedAt, CompletedAt: r.CompletedAt, Recipients: len(r.RecipientIds)})
			}
			return out, nil
		})
	huma.Register(a, withStatus(op("start-escrow-drill", http.MethodPost, "/api/v1/escrow/drills", "Repositories", "Start an escrow drill",
		"Creates a drill package (a random secret, no real key) sealed to the escrow recipients. An escrow holder decrypts it with the identity "+
			"from the safe (`age -d -i identity.txt drill.age`) and enters its confirmation code, proving the identities still work.",
		rbac.RepositoryManage, http.StatusConflict), http.StatusCreated),
		func(ctx context.Context, _ *struct{}) (*struct{ Body drillDTO }, error) {
			r, err := d.Protection.StartEscrowDrill(ctx, principal(ctx), metaFrom(ctx))
			if err != nil {
				return nil, d.protErr(ctx, err)
			}
			return &struct{ Body drillDTO }{drillDTO{ID: r.ID.String(), CreatedAt: r.CreatedAt, Recipients: len(r.RecipientIds), Package: string(r.Package)}}, nil
		})
	huma.Register(a, op("complete-escrow-drill", http.MethodPost, "/api/v1/escrow/drills/{id}/complete", "Repositories", "Complete an escrow drill",
		"", rbac.RepositoryManage, http.StatusBadRequest, http.StatusNotFound, http.StatusConflict),
		func(ctx context.Context, in *struct {
			ID   string `path:"id" format:"uuid"`
			Body struct {
				ConfirmationCode string `json:"confirmation_code" minLength:"16" maxLength:"40"`
			}
		}) (*struct{ Body drillDTO }, error) {
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			r, err := d.Protection.CompleteEscrowDrill(ctx, principal(ctx), id, in.Body.ConfirmationCode, metaFrom(ctx))
			if err != nil {
				return nil, d.protErr(ctx, err)
			}
			return &struct{ Body drillDTO }{drillDTO{ID: r.ID.String(), CreatedAt: r.CreatedAt, CompletedAt: r.CompletedAt, Recipients: len(r.RecipientIds)}}, nil
		})
}
