// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/sse"

	"github.com/AxiomOperator/dbr2/internal/events"
	"github.com/AxiomOperator/dbr2/internal/gateway"
)

// EventsPath is the Server-Sent Events stream.
const EventsPath = "/api/v1/events"

// Live-update payloads (documented in the OpenAPI spec per event type).

// BackupUpdatedEvent reports a recovery point state change.
type BackupUpdatedEvent struct {
	RecoveryPointID string `json:"recovery_point_id"`
	ApplicationID   string `json:"application_id"`
	State           string `json:"state" enum:"pending,committed,failed"`
	WorkflowID      string `json:"workflow_id,omitempty"`
	Error           string `json:"error,omitempty"`
}

// RestoreUpdatedEvent reports a restore step or outcome.
type RestoreUpdatedEvent struct {
	RestoreID     string `json:"restore_id"`
	ApplicationID string `json:"application_id,omitempty"`
	State         string `json:"state" enum:"requested,running,succeeded,failed,rolled_back"`
	Step          string `json:"step,omitempty"`
	Error         string `json:"error,omitempty"`
}

// AgentStatusEvent reports an agent connecting or disconnecting.
type AgentStatusEvent struct {
	HostID    string `json:"host_id"`
	Connected bool   `json:"connected"`
}

// AlertCreatedEvent reports a new alert.
type AlertCreatedEvent struct {
	Severity   string `json:"severity" enum:"critical,warning,info"`
	Type       string `json:"type"`
	TargetType string `json:"target_type,omitempty"`
	TargetID   string `json:"target_id,omitempty"`
	Message    string `json:"message"`
}

// InventoryUpdatedEvent reports a stored inventory.
type InventoryUpdatedEvent struct {
	HostID       string `json:"host_id"`
	Applications int    `json:"applications"`
}

// HeartbeatInterval keeps proxies and browsers from closing idle streams.
const HeartbeatInterval = 15 * time.Second

func registerEvents(a huma.API, d *Deps) {
	o := op("stream-events", http.MethodGet, EventsPath, "System", "Live updates (Server-Sent Events)",
		"A `text/event-stream` of live updates: agent command progress (`job.progress`: bytes hashed/uploaded, files, "+
			"current component), recovery point and restore state changes, agent connections, new alerts and stored inventories. "+
			"Each event is delivered only to callers holding the permission it needs (for example `backup.read` for backup "+
			"progress). Events are hints to refresh: missed events are never replayed, so clients re-read state after reconnecting. "+
			"`types` limits the stream to a comma-separated list of event types. A comment heartbeat is sent every 15 s.", "")
	sse.Register(a, o, map[string]any{
		events.JobProgress:    gateway.JobProgressEvent{},
		events.BackupUpdated:  BackupUpdatedEvent{},
		events.RestoreUpdated: RestoreUpdatedEvent{},
		events.AgentStatus:    AgentStatusEvent{},
		events.AlertCreated:   AlertCreatedEvent{},
		events.InventoryUpd:   InventoryUpdatedEvent{},
	}, func(ctx context.Context, in *struct {
		Types string `query:"types" doc:"Comma-separated event types (default: all)."`
	}, send sse.Sender) {
		p := principal(ctx)
		if p == nil || d.Events == nil {
			return
		}
		want := map[string]bool{}
		for _, t := range strings.Split(in.Types, ",") {
			if t = strings.TrimSpace(t); t != "" {
				want[t] = true
			}
		}
		sub := d.Events.Subscribe(ctx)
		if send(sse.Message{Comment: "connected", Retry: 5000}) != nil {
			return
		}
		tick := time.NewTicker(HeartbeatInterval)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if send(sse.Message{Comment: "heartbeat"}) != nil {
					return
				}
			case e, ok := <-sub:
				if !ok {
					return
				}
				if (len(want) > 0 && !want[e.Type]) || (e.Permission != "" && !p.Can(e.Permission)) {
					continue
				}
				if err := send.Data(typed(e)); err != nil {
					return
				}
			}
		}
	})
}

// typed decodes an event into its registered payload type so Huma emits the
// matching SSE `event:` name.
func typed(e events.Event) any {
	var v any
	switch e.Type {
	case events.JobProgress:
		v = &gateway.JobProgressEvent{}
	case events.BackupUpdated:
		v = &BackupUpdatedEvent{}
	case events.RestoreUpdated:
		v = &RestoreUpdatedEvent{}
	case events.AgentStatus:
		v = &AgentStatusEvent{}
	case events.AlertCreated:
		v = &AlertCreatedEvent{}
	case events.InventoryUpd:
		v = &InventoryUpdatedEvent{}
	default:
		return nil
	}
	_ = json.Unmarshal(e.Data, v)
	return deref(v)
}

func deref(v any) any {
	switch x := v.(type) {
	case *gateway.JobProgressEvent:
		return *x
	case *BackupUpdatedEvent:
		return *x
	case *RestoreUpdatedEvent:
		return *x
	case *AgentStatusEvent:
		return *x
	case *AlertCreatedEvent:
		return *x
	case *InventoryUpdatedEvent:
		return *x
	}
	return v
}

// streamWriter lets the SSE sender extend the write deadline per message
// (the server's WriteTimeout would otherwise cut long-lived streams).
type streamWriter struct {
	http.ResponseWriter
	rc *http.ResponseController
}

func (w *streamWriter) SetWriteDeadline(t time.Time) error { return w.rc.SetWriteDeadline(t) }
func (w *streamWriter) Flush()                             { _ = w.rc.Flush() }
func (w *streamWriter) Unwrap() http.ResponseWriter        { return w.ResponseWriter }

func streamDeadlines(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == EventsPath {
			w = &streamWriter{ResponseWriter: w, rc: http.NewResponseController(w)}
			w.Header().Set("X-Accel-Buffering", "no")
		}
		next.ServeHTTP(w, r)
	})
}
