// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/AxiomOperator/dbr2/internal/rbac"
)

// AuditEvent is one audit record.
type AuditEvent struct {
	EventID      string          `json:"event_id" format:"uuid"`
	OccurredAt   time.Time       `json:"occurred_at"`
	EventType    string          `json:"event_type" example:"auth.login.succeeded" doc:"Stable event identifier."`
	ActorDisplay string          `json:"actor_display"`
	ActorKind    string          `json:"actor_kind" enum:"user,system,anonymous"`
	SourceIP     *string         `json:"source_ip"`
	TargetType   *string         `json:"target_type"`
	TargetID     *string         `json:"target_id"`
	Result       string          `json:"result" enum:"success,failure,denied"`
	Reason       *string         `json:"reason"`
	Details      json.RawMessage `json:"details"`
	Before       json.RawMessage `json:"before,omitempty"`
	After        json.RawMessage `json:"after,omitempty"`
	RequestID    *string         `json:"request_id"`
	TraceID      *string         `json:"trace_id"`
}

type auditListInput struct {
	Limit     int    `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Cursor    string `query:"cursor" doc:"Opaque cursor from next_cursor."`
	EventType string `query:"event_type" doc:"Filter by stable event type, e.g. auth.login.failed."`
}

type auditListOutput struct {
	Body struct {
		Items      []AuditEvent `json:"items"`
		NextCursor *string      `json:"next_cursor"`
	}
}

func registerAudit(a huma.API, d *Deps) {
	huma.Register(a, op("list-audit-events", http.MethodGet, "/api/v1/audit-events", "Audit",
		"List audit events", "Pages through the append-only audit log, newest first.", rbac.AuditRead),
		func(ctx context.Context, in *auditListInput) (*auditListOutput, error) {
			var before *int64
			if in.Cursor != "" {
				n, err := strconv.ParseInt(in.Cursor, 10, 64)
				if err != nil || n <= 0 {
					return nil, huma.Error422UnprocessableEntity("invalid cursor")
				}
				before = &n
			}
			var et *string
			if in.EventType != "" {
				et = &in.EventType
			}
			rows, err := d.Auth.ListAuditEvents(ctx, before, et, int32(in.Limit))
			if err != nil {
				return nil, d.fail(ctx, err)
			}
			out := &auditListOutput{}
			out.Body.Items = make([]AuditEvent, 0, len(rows))
			for _, r := range rows {
				var ip *string
				if r.SourceIp != nil {
					s := r.SourceIp.String()
					ip = &s
				}
				out.Body.Items = append(out.Body.Items, AuditEvent{
					EventID: r.EventID.String(), OccurredAt: r.OccurredAt, EventType: r.EventType,
					ActorDisplay: r.ActorDisplay, ActorKind: r.ActorKind, SourceIP: ip, TargetType: r.TargetType,
					TargetID: r.TargetID, Result: r.Result, Reason: r.Reason, Details: r.Details,
					Before: r.BeforeState, After: r.AfterState, RequestID: r.RequestID, TraceID: r.TraceID,
				})
			}
			if len(rows) == in.Limit {
				c := strconv.FormatInt(rows[len(rows)-1].Seq, 10)
				out.Body.NextCursor = &c
			}
			return out, nil
		})
}
