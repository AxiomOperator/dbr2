// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"errors"
	"time"

	agentv1 "github.com/AxiomOperator/dbr2/internal/agentpb/agent/v1"
	"github.com/AxiomOperator/dbr2/internal/runtime"
)

// Event types and severities (AgentEvent, ADR-0005 alerts).
const (
	evAutoResumed  = "quiesce.auto_resumed"
	evResumeFailed = "quiesce.resume_failed"
	evLeaseWarning = "quiesce.lease_warning"

	sevCritical = "critical"
	sevWarning  = "warning"
)

// maxPendingEvents bounds the events kept while disconnected.
const maxPendingEvents = 100

// event logs an AgentEvent and sends it on the current session. Events
// raised while disconnected are kept (bounded, in memory) and sent when the
// next session starts; they are lost if the agent restarts first.
func (a *Agent) event(e *agentv1.AgentEvent) {
	if e.OccurredUnixMs == 0 {
		e.OccurredUnixMs = time.Now().UnixMilli()
	}
	log := a.log.Warn
	if e.Severity == sevCritical {
		log = a.log.Error
	}
	log("agent event", "type", e.Type, "severity", e.Severity, "application_id", e.ApplicationId,
		"lease_id", e.LeaseId, "message", e.Message)
	m := &agentv1.ConnectRequest{Body: &agentv1.ConnectRequest_Event{Event: e}}
	a.mu.Lock()
	out := a.cur
	if out == nil {
		if len(a.pending) >= maxPendingEvents {
			a.pending = a.pending[1:]
		}
		a.pending = append(a.pending, e)
	}
	a.mu.Unlock()
	if out != nil {
		out.send(m)
	}
}

// flushEvents sends the events raised while disconnected.
func (a *Agent) flushEvents(out *outbound) {
	a.mu.Lock()
	evs := a.pending
	a.pending = nil
	a.mu.Unlock()
	for _, e := range evs {
		out.send(&agentv1.ConnectRequest{Body: &agentv1.ConnectRequest_Event{Event: e}})
	}
}

// control returns the runtime's container-control capability.
func (a *Agent) control() (runtime.ContainerControl, error) {
	if a.rt == nil {
		return nil, errors.New("container runtime unavailable")
	}
	c, ok := a.rt.(runtime.ContainerControl)
	if !ok {
		return nil, permanent(errors.New("container runtime " + a.rt.Name() + " cannot control containers"))
	}
	return c, nil
}

// permanentError marks a failure that retrying the same command cannot fix.
type permanentError struct{ error }

func (e permanentError) Unwrap() error { return e.error }

func permanent(err error) error { return permanentError{err} }

func isPermanent(err error) bool {
	var p permanentError
	return errors.As(err, &p)
}
