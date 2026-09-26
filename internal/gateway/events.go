// SPDX-License-Identifier: Apache-2.0

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	agentv1 "github.com/AxiomOperator/dbr2/internal/agentpb/agent/v1"
	"github.com/AxiomOperator/dbr2/internal/audit"
	"github.com/AxiomOperator/dbr2/internal/events"
	"github.com/AxiomOperator/dbr2/internal/rbac"
	"github.com/AxiomOperator/dbr2/internal/store"
)

// DefaultMaxConcurrentJobs applies when a host has no settings row.
const DefaultMaxConcurrentJobs = 2

var agentEventTypes = map[string]bool{
	audit.QuiesceAutoResumed: true, audit.QuiesceResumeFailed: true, audit.QuiesceLeaseWarning: true,
}

// maxConcurrentJobs returns the host's data-moving job limit (Welcome).
func (g *Gateway) maxConcurrentJobs(ctx context.Context, id uuid.UUID) uint32 {
	hs, err := g.q.GetHostSettings(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) || err != nil {
		return DefaultMaxConcurrentJobs
	}
	return uint32(hs.MaxConcurrentJobs)
}

// recordAgentEvent audits an agent event and raises its alert (ADR-0005:
// an auto-resume or failed resume is critical).
func (g *Gateway) recordAgentEvent(ctx context.Context, agentID string, ev *agentv1.AgentEvent) {
	typ := ev.Type
	if !agentEventTypes[typ] {
		typ = "agent.event.unknown"
	}
	details := map[string]any{"agent_id": agentID, "event": ev.Type, "lease_id": ev.LeaseId, "message": ev.Message,
		"occurred_at": time.UnixMilli(ev.OccurredUnixMs).UTC()}
	result := audit.Success
	if ev.Severity == "critical" {
		result = audit.Failure
	}
	target, targetType := ev.ApplicationId, "application"
	if target == "" {
		target, targetType = agentID, "agent"
	}
	if _, err := g.audit.Record(ctx, audit.Event{OrgID: g.cfg.OrgID, Type: typ, ActorKind: audit.ActorSystem,
		ActorDisplay: "agent:" + agentID, TargetType: targetType, TargetID: target, Result: result, Details: details}); err != nil {
		g.log.ErrorContext(ctx, "recording agent event failed", "agent_id", agentID, "event", ev.Type, "err", err)
	}
	sev := ev.Severity
	if sev != "critical" && sev != "warning" && sev != "info" {
		sev = "warning"
	}
	payload, _ := json.Marshal(details)
	if err := g.q.InsertNotification(ctx, store.InsertNotificationParams{OrgID: g.cfg.OrgID, Severity: sev, EventType: typ,
		TargetType: &targetType, TargetID: &target, Message: ev.Message, Payload: payload}); err != nil {
		g.log.ErrorContext(ctx, "raising agent alert failed", "agent_id", agentID, "err", err)
	} else {
		g.publish(ctx, events.New(events.AlertCreated, rbac.BackupRead, map[string]any{"severity": sev, "type": typ,
			"target_type": targetType, "target_id": target, "message": ev.Message}))
	}
	g.log.WarnContext(ctx, "agent event", "agent_id", agentID, "event", ev.Type, "severity", sev, "application_id", ev.ApplicationId, "message", ev.Message)
}
