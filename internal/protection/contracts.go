// SPDX-License-Identifier: Apache-2.0

package protection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AxiomOperator/dbr2/internal/audit"
	"github.com/AxiomOperator/dbr2/internal/auth"
	"github.com/AxiomOperator/dbr2/internal/events"
	"github.com/AxiomOperator/dbr2/internal/manifest"
	"github.com/AxiomOperator/dbr2/internal/rbac"
	"github.com/AxiomOperator/dbr2/internal/store"
)

// ContractSpec is the v1.0 Recovery Contract: a maximum RPO and components
// every recovery point must contain.
type ContractSpec struct {
	MaxRPOMinutes      *int32   `json:"max_rpo_minutes,omitempty"`
	RequiredComponents []string `json:"required_components"`
}

// Contract states.
const (
	ContractSatisfied = "satisfied"
	ContractViolated  = "violated"
	ContractUnknown   = "unknown"
)

// GetContract returns an application's contract (nil when none).
func (s *Service) GetContract(ctx context.Context, appID uuid.UUID) (*store.RecoveryContract, error) {
	if _, err := s.fleet.GetApplication(ctx, appID); err != nil {
		return nil, err
	}
	c, err := s.q.GetContract(ctx, appID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &c, err
}

// PutContract sets an application's contract (nil spec removes it).
func (s *Service) PutContract(ctx context.Context, p *auth.Principal, appID uuid.UUID, spec *ContractSpec, m auth.RequestMeta) (*store.RecoveryContract, error) {
	if _, err := s.fleet.GetApplication(ctx, appID); err != nil {
		return nil, err
	}
	var out *store.RecoveryContract
	err := s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		ev := s.event(p, m, audit.ContractUpdated)
		ev.TargetType, ev.TargetID = "application", appID.String()
		if spec == nil {
			if _, err := q.DeleteContract(ctx, appID); err != nil {
				return err
			}
			ev.Details = map[string]any{"removed": true}
		} else {
			if spec.MaxRPOMinutes != nil && *spec.MaxRPOMinutes <= 0 {
				return fmt.Errorf("%w: max_rpo_minutes must be positive", ErrInvalid)
			}
			req := uniqueSorted(spec.RequiredComponents)
			c, err := q.UpsertContract(ctx, store.UpsertContractParams{ApplicationID: appID, MaxRpoMinutes: spec.MaxRPOMinutes,
				RequiredComponents: req, UpdatedBy: &p.UserID})
			if err != nil {
				return err
			}
			out = &c
			ev.After = spec
		}
		_, err := rec.Record(ctx, ev)
		return err
	})
	if err != nil {
		return nil, err
	}
	if out != nil {
		_ = s.EvaluateContracts(ctx)
		if c, err := s.q.GetContract(ctx, appID); err == nil {
			out = &c
		}
	}
	return out, nil
}

func uniqueSorted(xs []string) []string {
	out := []string{}
	for _, x := range xs {
		if x = strings.TrimSpace(x); x != "" && !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	slices.Sort(out)
	return out
}

// evaluateContract is pure: the contract against the latest committed
// recovery point.
func evaluateContract(c store.RecoveryContract, last *store.RecoveryPoint, now time.Time) (string, []string) {
	var reasons []string
	if last == nil {
		return ContractViolated, []string{"no recovery point exists"}
	}
	if c.MaxRpoMinutes != nil {
		age := now.Sub(last.CreatedAt)
		if limit := time.Duration(*c.MaxRpoMinutes) * time.Minute; age > limit {
			reasons = append(reasons, fmt.Sprintf("RPO violated: the latest recovery point is %s old (maximum %s)", age.Round(time.Minute), limit))
		}
	}
	if len(c.RequiredComponents) > 0 {
		got := map[string]bool{}
		var m manifest.Manifest
		if json.Unmarshal(last.Manifest, &m) == nil {
			for _, x := range m.Components {
				if x.Status == manifest.ComponentSucceeded {
					got[x.Name] = true
				}
			}
		}
		for _, r := range c.RequiredComponents {
			if !got[r] {
				reasons = append(reasons, "required component "+r+" is not in the latest recovery point")
			}
		}
	}
	if len(reasons) > 0 {
		return ContractViolated, reasons
	}
	return ContractSatisfied, []string{}
}

// EvaluateContracts evaluates every contract and alerts on state changes
// (RPO-violation reporting).
func (s *Service) EvaluateContracts(ctx context.Context) error {
	contracts, err := s.q.ListContracts(ctx, s.opts.OrgID)
	if err != nil {
		return err
	}
	now := time.Now()
	var errs []error
	for _, row := range contracts {
		c := store.RecoveryContract{ApplicationID: row.ApplicationID, MaxRpoMinutes: row.MaxRpoMinutes, RequiredComponents: row.RequiredComponents,
			State: row.State}
		var last *store.RecoveryPoint
		if rp, err := s.q.LastCommittedRecoveryPoint(ctx, row.ApplicationID); err == nil {
			last = &rp
		} else if !errors.Is(err, pgx.ErrNoRows) {
			errs = append(errs, err)
			continue
		}
		state, reasons := evaluateContract(c, last, now)
		if err := s.q.SetContractState(ctx, store.SetContractStateParams{ApplicationID: row.ApplicationID, State: state, StateReasons: reasons}); err != nil {
			errs = append(errs, err)
			continue
		}
		if state == row.State {
			continue
		}
		typ, sev, msg := audit.ContractViolated, "critical", fmt.Sprintf("Recovery contract of %s violated: %s", row.ApplicationName, strings.Join(reasons, "; "))
		if state == ContractSatisfied {
			typ, sev, msg = audit.ContractSatisfied, "info", fmt.Sprintf("Recovery contract of %s is satisfied again", row.ApplicationName)
		}
		details := map[string]any{"state": state, "reasons": reasons, "previous": row.State}
		errs = append(errs, s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
			if _, err := rec.Record(ctx, s.systemEvent(typ, "application", row.ApplicationID.String(), outcomeFor(state), details)); err != nil {
				return err
			}
			return s.alert(ctx, q, sev, typ, "application", row.ApplicationID.String(), msg, details)
		}))
		s.publish(ctx, events.BackupUpdated, rbac.BackupRead, map[string]any{"application_id": row.ApplicationID.String(), "state": "contract_" + state})
	}
	return errors.Join(errs...)
}

func outcomeFor(state string) string {
	if state == ContractViolated {
		return audit.Failure
	}
	return audit.Success
}

// contractJSON is passed to the worker for the capture-time evaluation.
func (s *Service) contractJSON(ctx context.Context, appID uuid.UUID) []byte {
	c, err := s.q.GetContract(ctx, appID)
	if err != nil {
		return nil
	}
	b, _ := json.Marshal(ContractSpec{MaxRPOMinutes: c.MaxRpoMinutes, RequiredComponents: c.RequiredComponents})
	return b
}

// ContractView is a contract with its application name.
type ContractView struct {
	store.RecoveryContract
	ApplicationName string
}

// ListContractViews lists every contract.
func (s *Service) ListContractViews(ctx context.Context) ([]ContractView, error) {
	rows, err := s.q.ListContracts(ctx, s.opts.OrgID)
	if err != nil {
		return nil, err
	}
	out := make([]ContractView, 0, len(rows))
	for _, r := range rows {
		out = append(out, ContractView{ApplicationName: r.ApplicationName, RecoveryContract: store.RecoveryContract{ApplicationID: r.ApplicationID,
			MaxRpoMinutes: r.MaxRpoMinutes, RequiredComponents: r.RequiredComponents, State: r.State, StateReasons: r.StateReasons,
			EvaluatedAt: r.EvaluatedAt, ViolatedSince: r.ViolatedSince, UpdatedBy: r.UpdatedBy, UpdatedAt: r.UpdatedAt}})
	}
	return out, nil
}
