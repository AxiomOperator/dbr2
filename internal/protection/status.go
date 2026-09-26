// SPDX-License-Identifier: Apache-2.0

package protection

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	agentv1 "github.com/AxiomOperator/dbr2/internal/agentpb/agent/v1"
	"github.com/AxiomOperator/dbr2/internal/fleet"
	"github.com/AxiomOperator/dbr2/internal/manifest"
	"github.com/AxiomOperator/dbr2/internal/store"
)

// Protection statuses.
const (
	StatusProtected   = "protected"
	StatusAtRisk      = "at_risk"
	StatusFailed      = "failed"
	StatusUnprotected = "unprotected"
)

// ComponentCoverage says whether a component the application has now is in
// its latest committed recovery point.
type ComponentCoverage struct {
	Name          string `json:"name"`
	Kind          string `json:"kind" enum:"config,volume,bind_mount,database"`
	Required      bool   `json:"required"`
	Protected     bool   `json:"protected"`
	LastSizeBytes int64  `json:"last_size_bytes"`
}

// Protection is an application's protection status and coverage.
type Protection struct {
	Status                 string              `json:"status" enum:"protected,at_risk,failed,unprotected"`
	Reasons                []string            `json:"reasons"`
	LastBackupAt           *time.Time          `json:"last_backup_at"`
	LastRecoveryPointID    string              `json:"last_recovery_point_id,omitempty"`
	LastStatus             string              `json:"last_status,omitempty"`
	LastMode               string              `json:"last_mode,omitempty"`
	LastAttemptAt          *time.Time          `json:"last_attempt_at"`
	LastAttemptState       string              `json:"last_attempt_state,omitempty"`
	LastError              string              `json:"last_error,omitempty"`
	Running                string              `json:"running,omitempty" enum:"backup,restore"`
	Components             []ComponentCoverage `json:"components"`
	ComponentsTotal        int                 `json:"components_total"`
	ComponentsProtected    int                 `json:"components_protected"`
	UnresolvedDependencies int                 `json:"unresolved_dependencies"`
}

var kindNames = map[agentv1.ComponentKind]string{
	agentv1.ComponentKind_COMPONENT_KIND_CONFIG: manifest.KindConfig, agentv1.ComponentKind_COMPONENT_KIND_VOLUME: manifest.KindVolume,
	agentv1.ComponentKind_COMPONENT_KIND_BIND_MOUNT: manifest.KindBindMount, agentv1.ComponentKind_COMPONENT_KIND_DATABASE: manifest.KindDatabase,
}

// ProtectionFor computes protection for applications in one pass.
func (s *Service) ProtectionFor(ctx context.Context, apps []fleet.Application) (map[uuid.UUID]Protection, error) {
	committed, err := s.q.LatestCommittedPerApplication(ctx, s.opts.OrgID)
	if err != nil {
		return nil, err
	}
	attempts, err := s.q.LatestAttemptPerApplication(ctx, s.opts.OrgID)
	if err != nil {
		return nil, err
	}
	settings, err := s.q.ListApplicationBackupSettings(ctx, s.opts.OrgID)
	if err != nil {
		return nil, err
	}
	restores, err := s.q.ActiveRestores(ctx, s.opts.OrgID)
	if err != nil {
		return nil, err
	}
	lastOK := map[uuid.UUID]store.RecoveryPoint{}
	for _, r := range committed {
		lastOK[r.ApplicationID] = r
	}
	last := map[uuid.UUID]store.RecoveryPoint{}
	for _, r := range attempts {
		last[r.ApplicationID] = r
	}
	set := map[uuid.UUID]store.ApplicationBackupSetting{}
	for _, r := range settings {
		set[r.ApplicationID] = r
	}
	restoring := map[uuid.UUID]bool{}
	for _, r := range restores {
		restoring[r.SourceApplicationID] = true
		if r.TargetApplicationID != nil {
			restoring[*r.TargetApplicationID] = true
		}
	}
	out := make(map[uuid.UUID]Protection, len(apps))
	for _, app := range apps {
		id := app.Record.ID
		var bs BackupSettings
		if row, ok := set[id]; ok {
			bs = BackupSettings{OptionalComponents: row.OptionalComponents, ExcludedComponents: row.ExcludedComponents, DatabaseStrategy: row.DatabaseStrategy}
			_ = json.Unmarshal(row.PreHooks, &bs.PreHooks)
		}
		var ok *store.RecoveryPoint
		if r, found := lastOK[id]; found {
			ok = &r
		}
		var attempt *store.RecoveryPoint
		if r, found := last[id]; found {
			attempt = &r
		}
		out[id] = computeProtection(app, bs, ok, attempt, restoring[id])
	}
	return out, nil
}

// computeProtection is pure (tested).
func computeProtection(app fleet.Application, bs BackupSettings, ok, attempt *store.RecoveryPoint, restoring bool) Protection {
	p := Protection{Reasons: []string{}, Components: []ComponentCoverage{}}
	var m *manifest.Manifest
	if ok != nil {
		p.LastBackupAt, p.LastRecoveryPointID, p.LastMode = ok.CommittedAt, ok.ID, ok.ConsistencyMode
		if ok.Status != nil {
			p.LastStatus = *ok.Status
		}
		if len(ok.Manifest) > 0 {
			// Indexed manifests were validated when written; only the
			// component list is needed here.
			var mm manifest.Manifest
			if json.Unmarshal(ok.Manifest, &mm) == nil {
				m = &mm
			}
		}
	}
	if attempt != nil {
		t := attempt.CreatedAt
		p.LastAttemptAt, p.LastAttemptState = &t, attempt.State
		if attempt.Error != nil {
			p.LastError = *attempt.Error
		}
		if attempt.State == "pending" {
			p.Running = "backup"
		}
	}
	if restoring {
		p.Running = "restore"
	}
	captured := map[string]manifest.Component{}
	if m != nil {
		for _, c := range m.Components {
			if c.Status == manifest.ComponentSucceeded {
				captured[c.Name] = c
			}
		}
	}
	if app.Analysis != nil {
		p.UnresolvedDependencies = len(app.Analysis.Dependencies)
		if pl, err := buildPlan(app, fleet.Agent{Agent: store.Agent{ID: app.Record.AgentID}}, bs); err == nil {
			for _, c := range pl.components {
				cc := ComponentCoverage{Name: c.Name, Kind: kindNames[c.Kind], Required: c.Required}
				if got, ok := captured[c.Name]; ok {
					cc.Protected, cc.LastSizeBytes = true, got.SizeBytes
					p.ComponentsProtected++
				}
				p.Components = append(p.Components, cc)
			}
			p.ComponentsTotal = len(p.Components)
		}
	} else {
		p.Reasons = append(p.Reasons, "the application is missing from its host's latest inventory")
	}
	switch {
	case ok == nil && attempt != nil && attempt.State == "failed":
		p.Status = StatusFailed
		p.Reasons = append(p.Reasons, "no backup has succeeded; the last attempt failed")
	case ok == nil:
		p.Status = StatusUnprotected
		p.Reasons = append(p.Reasons, "no recovery point exists")
	case attempt != nil && attempt.State == "failed" && attempt.CreatedAt.After(ok.CreatedAt):
		p.Status = StatusFailed
		p.Reasons = append(p.Reasons, "the last backup attempt failed")
	default:
		p.Status = StatusProtected
		if p.LastStatus == manifest.StatusPartial {
			p.Status = StatusAtRisk
			p.Reasons = append(p.Reasons, "the latest recovery point is Partial")
		}
		if p.ComponentsProtected < p.ComponentsTotal {
			p.Status = StatusAtRisk
			p.Reasons = append(p.Reasons, fmt.Sprintf("%d of %d components are not in the latest recovery point", p.ComponentsTotal-p.ComponentsProtected, p.ComponentsTotal))
		}
	}
	if p.UnresolvedDependencies > 0 {
		p.Reasons = append(p.Reasons, fmt.Sprintf("%d external dependencies are not protected by DBR²", p.UnresolvedDependencies))
	}
	return p
}
