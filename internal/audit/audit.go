// SPDX-License-Identifier: Apache-2.0

// Package audit records security-relevant events in the append-only
// audit_events table and mirrors each one to the structured log (event
// "audit") so it can be shipped to a SIEM. Event types are stable identifiers:
// never rename one; add a new type instead.
package audit

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/netip"

	"github.com/google/uuid"

	"github.com/AxiomOperator/dbr2/internal/obs"
	"github.com/AxiomOperator/dbr2/internal/store"
)

// Stable event types (SIEM contract; see docs/dev/audit-events.md).
const (
	MasterAdminBootstrapped  = "auth.master_admin.bootstrapped"
	MasterAdminPasswordReset = "auth.master_admin.password_reset_offline"
	LoginSucceeded           = "auth.login.succeeded"
	LoginFailed              = "auth.login.failed"
	LoginLocked              = "auth.login.locked"
	LoginRateLimited         = "auth.login.rate_limited"
	OIDCLoginSucceeded       = "auth.oidc.login.succeeded"
	OIDCLoginFailed          = "auth.oidc.login.failed"
	Logout                   = "auth.logout"
	PasswordChanged          = "auth.password.changed"
	TOTPEnabled              = "auth.totp.enabled"
	TOTPDisabled             = "auth.totp.disabled"
	APITokenCreated          = "auth.api_token.created"
	APITokenRevoked          = "auth.api_token.revoked"
	UserRolesChanged         = "user.roles.changed"
	UserDisabledChanged      = "user.disabled.changed"
	GroupMappingAdded        = "rbac.group_mapping.added"
	GroupMappingRemoved      = "rbac.group_mapping.removed"
	AccessDenied             = "authz.denied"
)

// Result values.
const (
	Success = "success"
	Failure = "failure"
	Denied  = "denied"
)

// Actor kinds.
const (
	ActorUser      = "user"
	ActorSystem    = "system"
	ActorAnonymous = "anonymous"
)

// Event is one audit record. Who / what / when / source IP / target /
// reason / result / before / after (roadmap → audit requirements).
type Event struct {
	OrgID        uuid.UUID
	Type         string
	ActorUserID  *uuid.UUID
	ActorDisplay string
	ActorKind    string
	SourceIP     netip.Addr
	TargetType   string
	TargetID     string
	Reason       string
	Result       string
	Details      map[string]any
	Before       any
	After        any
	RequestID    string
}

// Recorder persists audit events.
type Recorder struct {
	q   store.Querier
	log *slog.Logger
}

// NewRecorder returns a Recorder writing through q.
func NewRecorder(q store.Querier, log *slog.Logger) *Recorder {
	return &Recorder{q: q, log: log}
}

// WithQuerier returns a Recorder bound to another querier (e.g. a transaction).
func (r *Recorder) WithQuerier(q store.Querier) *Recorder { return &Recorder{q: q, log: r.log} }

// Record writes e. Failing to audit is an error the caller must handle:
// security-relevant actions must not proceed silently unaudited.
func (r *Recorder) Record(ctx context.Context, e Event) (uuid.UUID, error) {
	details, err := marshal(sanitize(e.Details))
	if err != nil {
		return uuid.Nil, err
	}
	if details == nil {
		details = json.RawMessage(`{}`)
	}
	before, err := marshal(e.Before)
	if err != nil {
		return uuid.Nil, err
	}
	after, err := marshal(e.After)
	if err != nil {
		return uuid.Nil, err
	}
	var ip *netip.Addr
	if e.SourceIP.IsValid() {
		a := e.SourceIP
		ip = &a
	}
	traceID := obs.TraceID(ctx)
	row, err := r.q.InsertAuditEvent(ctx, store.InsertAuditEventParams{
		OrgID:        e.OrgID,
		EventType:    e.Type,
		ActorUserID:  e.ActorUserID,
		ActorDisplay: e.ActorDisplay,
		ActorKind:    e.ActorKind,
		SourceIp:     ip,
		TargetType:   strPtr(e.TargetType),
		TargetID:     strPtr(e.TargetID),
		Reason:       strPtr(e.Reason),
		Result:       e.Result,
		Details:      details,
		BeforeState:  before,
		AfterState:   after,
		RequestID:    strPtr(e.RequestID),
		TraceID:      strPtr(traceID),
	})
	if err != nil {
		return uuid.Nil, err
	}
	r.log.LogAttrs(ctx, slog.LevelInfo, "audit",
		slog.String("event", "audit"),
		slog.String("event_id", row.EventID.String()),
		slog.String("event_type", e.Type),
		slog.String("actor", e.ActorDisplay),
		slog.String("actor_kind", e.ActorKind),
		slog.String("source_ip", addrString(e.SourceIP)),
		slog.String("target_type", e.TargetType),
		slog.String("target_id", e.TargetID),
		slog.String("result", e.Result),
		slog.String("request_id", e.RequestID),
	)
	return row.EventID, nil
}

// sanitize drops secret-looking keys from details (threat model T9).
func sanitize(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		if obs.IsSensitiveKey(k) {
			out[k] = obs.Redacted
			continue
		}
		out[k] = v
	}
	return out
}

func marshal(v any) (json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	if m, ok := v.(map[string]any); ok && m == nil {
		return nil, nil
	}
	return json.Marshal(v)
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func addrString(a netip.Addr) string {
	if !a.IsValid() {
		return ""
	}
	return a.String()
}
