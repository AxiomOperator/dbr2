// SPDX-License-Identifier: Apache-2.0

package gateway

import (
	"context"
	"encoding/json"
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"

	agentv1 "github.com/AxiomOperator/dbr2/internal/agentpb/agent/v1"
	"github.com/AxiomOperator/dbr2/internal/events"
	"github.com/AxiomOperator/dbr2/internal/rbac"
)

// SetEvents enables live updates (SSE) from the gateway.
func (g *Gateway) SetEvents(b *events.Bus) { g.events.Store(b) }

func (g *Gateway) publish(ctx context.Context, e events.Event) {
	if b := g.events.Load(); b != nil {
		b.Publish(ctx, e)
	}
}

func oneofName(m protoreflect.Message, oneof string) string {
	od := m.Descriptor().Oneofs().ByName(protoreflect.Name(oneof))
	if od == nil {
		return "unknown"
	}
	if f := m.WhichOneof(od); f != nil {
		return string(f.Name())
	}
	return "unknown"
}

func (g *Gateway) waiterKind(commandID string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if w := g.waiters[commandID]; w != nil {
		return w.kind
	}
	return "unknown"
}

// JobProgressEvent is the payload of job.progress: one agent command update,
// attributed to its workflow (command_id = workflow ID/run ID/activity ID,
// ADR-0001).
type JobProgressEvent struct {
	CommandID     string          `json:"command_id"`
	HostID        string          `json:"host_id"`
	Kind          string          `json:"kind"`
	State         string          `json:"state"`
	WorkflowID    string          `json:"workflow_id,omitempty"`
	RunID         string          `json:"run_id,omitempty"`
	ApplicationID string          `json:"application_id,omitempty"`
	Progress      json.RawMessage `json:"progress,omitempty"`
	Error         string          `json:"error,omitempty"`
}

// ParseCommandID splits a command ID into workflow ID, run ID and the
// application ID for application/<id> workflows.
func ParseCommandID(id string) (workflowID, runID, applicationID string) {
	parts := strings.Split(id, "/")
	switch {
	case len(parts) >= 3 && parts[0] == "application":
		return parts[0] + "/" + parts[1], parts[2], parts[1]
	case len(parts) >= 4 && parts[0] == "host":
		return strings.Join(parts[:3], "/"), parts[3], ""
	case len(parts) >= 4 && parts[0] == "repository":
		return strings.Join(parts[:3], "/"), parts[3], ""
	}
	return "", "", ""
}

func (g *Gateway) publishProgress(ctx context.Context, agentID, kind, state string, u *agentv1.CommandUpdate) {
	if g.events.Load() == nil {
		return
	}
	wf, run, app := ParseCommandID(u.CommandId)
	e := JobProgressEvent{CommandID: u.CommandId, HostID: agentID, Kind: kind, State: state, WorkflowID: wf, RunID: run,
		ApplicationID: app, Error: u.Error}
	if len(u.Progress) > 0 && json.Valid(u.Progress) {
		e.Progress = u.Progress
	}
	perm := rbac.BackupRead
	if restoreKinds[kind] {
		perm = rbac.RestoreRead
	}
	g.publish(ctx, events.New(events.JobProgress, perm, e))
}

// restoreKinds are restore commands: their progress needs restore.read.
var restoreKinds = map[string]bool{"ensure_images": true, "restore_components": true, "recreate_containers": true,
	"start_containers": true, "check_health": true, "finalize_restore": true, "restore_database": true}
