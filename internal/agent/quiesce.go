// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"sync"
	"time"

	agentv1 "github.com/AxiomOperator/dbr2/internal/agentpb/agent/v1"
	"github.com/AxiomOperator/dbr2/internal/runtime"
)

// Quiesce lease defaults (ADR-0005: max quiesce duration 60 min + grace).
const (
	defaultLeaseSeconds = 65 * 60
	leaseWarnFraction   = 0.8
	autoResumeTimeout   = 2 * time.Minute
	autoResumeRetry     = 30 * time.Second
	autoResumedKeep     = 7 * 24 * time.Hour
)

type containerState struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"`
}

// leaseRecord is one journaled quiesce lease (<state>/leases.json).
type leaseRecord struct {
	LeaseID       string           `json:"lease_id"`
	ApplicationID string           `json:"application_id"`
	Mode          string           `json:"mode"` // pause | stop
	PreState      []containerState `json:"pre_state"`
	QuiescedAt    time.Time        `json:"quiesced_at"`
	ExpiresAt     time.Time        `json:"expires_at"`
	Warned        bool             `json:"warned,omitempty"`
	// Set once the dead-man switch resumed the application; the record is
	// kept until Resume arrives (to report auto_resumed) or for 7 days.
	AutoResumedAt *time.Time `json:"auto_resumed_at,omitempty"`
	Resumed       []string   `json:"resumed_container_ids,omitempty"`
}

func (l *leaseRecord) active() bool { return l.AutoResumedAt == nil }

func (l *leaseRecord) result() *agentv1.QuiesceResult {
	r := &agentv1.QuiesceResult{QuiescedAtUnixMs: l.QuiescedAt.UnixMilli(), LeaseExpiresUnixMs: l.ExpiresAt.UnixMilli()}
	for _, c := range l.PreState {
		r.PreState = append(r.PreState, &agentv1.ContainerState{Id: c.ID, Name: c.Name, State: c.State})
	}
	return r
}

// leaseManager implements quiesce/resume and the agent dead-man switch
// (ADR-0005 layer 2): an expired lease resumes the application from its
// journaled pre-state even with no control plane, including after restarts.
type leaseManager struct {
	path  string
	ctl   func() (runtime.ContainerControl, error)
	emit  func(*agentv1.AgentEvent)
	log   *slog.Logger
	retry time.Duration // wait before retrying a failed auto-resume

	// mu is held across runtime calls: quiesce, resume and auto-resume are
	// serialized, so a lease is never resumed and re-quiesced concurrently.
	mu     sync.Mutex
	leases map[string]*leaseRecord
	timers map[string][]*time.Timer
	ctx    context.Context
}

func openLeases(path string, ctl func() (runtime.ContainerControl, error), emit func(*agentv1.AgentEvent), log *slog.Logger) (*leaseManager, error) {
	m := &leaseManager{path: path, ctl: ctl, emit: emit, log: log, retry: autoResumeRetry,
		leases: map[string]*leaseRecord{}, timers: map[string][]*time.Timer{}, ctx: context.Background()}
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if len(b) > 0 {
		var list []*leaseRecord
		if err := json.Unmarshal(b, &list); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		for _, l := range list {
			if !l.active() && time.Since(*l.AutoResumedAt) > autoResumedKeep {
				continue
			}
			m.leases[l.LeaseID] = l
		}
	}
	return m, nil
}

// save journals the leases atomically (caller holds mu).
func (m *leaseManager) save() error {
	list := make([]*leaseRecord, 0, len(m.leases))
	for _, l := range m.leases {
		list = append(list, l)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].LeaseID < list[j].LeaseID })
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(m.path, b, 0o600)
}

// start binds the switch to ctx, resumes expired leases now and re-arms the
// others.
func (m *leaseManager) start(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ctx = ctx
	context.AfterFunc(ctx, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		for id := range m.timers {
			m.disarm(id)
		}
	})
	for _, l := range m.leases {
		if !l.active() {
			continue
		}
		if !time.Now().Before(l.ExpiresAt) {
			m.log.Warn("quiesce lease expired while the agent was down; resuming",
				"lease_id", l.LeaseID, "application_id", l.ApplicationID, "expired_at", l.ExpiresAt)
			m.autoResume(l)
			continue
		}
		m.arm(l)
	}
}

func (m *leaseManager) disarm(id string) {
	for _, t := range m.timers[id] {
		t.Stop()
	}
	delete(m.timers, id)
}

// arm schedules the 80 % warning and the expiry (caller holds mu).
func (m *leaseManager) arm(l *leaseRecord) {
	m.disarm(l.LeaseID)
	id := l.LeaseID
	var ts []*time.Timer
	if !l.Warned {
		warnAt := l.QuiescedAt.Add(time.Duration(float64(l.ExpiresAt.Sub(l.QuiescedAt)) * leaseWarnFraction))
		ts = append(ts, time.AfterFunc(max(0, time.Until(warnAt)), func() { m.warn(id) }))
	}
	ts = append(ts, time.AfterFunc(max(0, time.Until(l.ExpiresAt)), func() { m.expire(id) }))
	m.timers[id] = ts
}

func (m *leaseManager) warn(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.leases[id]
	if l == nil || !l.active() || l.Warned || m.ctx.Err() != nil {
		return
	}
	l.Warned = true
	if err := m.save(); err != nil {
		m.log.Error("journal quiesce lease failed", "lease_id", id, "err", err)
	}
	m.emit(&agentv1.AgentEvent{Type: evLeaseWarning, Severity: sevWarning, ApplicationId: l.ApplicationID, LeaseId: id,
		Message: fmt.Sprintf("application quiesced for %s; the lease expires at %s and the agent will then resume it",
			time.Since(l.QuiescedAt).Round(time.Second), l.ExpiresAt.UTC().Format(time.RFC3339))})
}

func (m *leaseManager) expire(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.leases[id]
	if l == nil || !l.active() || m.ctx.Err() != nil {
		return
	}
	m.autoResume(l)
}

// autoResume is the dead-man switch firing (caller holds mu). A failure is
// retried until it succeeds or Resume arrives.
func (m *leaseManager) autoResume(l *leaseRecord) {
	ctx, cancel := context.WithTimeout(m.ctx, autoResumeTimeout)
	defer cancel()
	resumed, err := m.restore(ctx, l)
	if m.ctx.Err() != nil {
		return // shutting down: re-evaluated on the next start
	}
	id := l.LeaseID
	if err != nil {
		m.emit(&agentv1.AgentEvent{Type: evResumeFailed, Severity: sevCritical, ApplicationId: l.ApplicationID, LeaseId: id,
			Message: "quiesce lease expired but the automatic resume failed (retrying): " + err.Error()})
		m.disarm(id)
		m.timers[id] = []*time.Timer{time.AfterFunc(m.retry, func() { m.expire(id) })}
		return
	}
	m.disarm(id)
	now := time.Now().UTC()
	l.AutoResumedAt, l.Resumed = &now, resumed
	if err := m.save(); err != nil {
		m.log.Error("journal quiesce lease failed", "lease_id", id, "err", err)
	}
	m.emit(&agentv1.AgentEvent{Type: evAutoResumed, Severity: sevCritical, ApplicationId: l.ApplicationID, LeaseId: id,
		Message: fmt.Sprintf("quiesce lease expired at %s without Resume; the agent resumed the application (%d containers)",
			l.ExpiresAt.UTC().Format(time.RFC3339), len(resumed))})
}

// restore brings every container that was running before quiesce back to
// running; containers that were not running are left alone.
func (m *leaseManager) restore(ctx context.Context, l *leaseRecord) ([]string, error) {
	ctl, err := m.ctl()
	if err != nil {
		return nil, err
	}
	resumed := []string{}
	var errs []error
	for _, c := range l.PreState {
		if c.State != "running" {
			continue
		}
		cur, err := ctl.InspectState(ctx, c.ID)
		if err != nil {
			errs = append(errs, fmt.Errorf("inspect %s: %w", c.Name, err))
			continue
		}
		switch cur.State {
		case "running", "restarting":
			continue
		case "paused":
			err = ctl.Unpause(ctx, c.ID)
		case "exited", "created":
			err = ctl.Start(ctx, c.ID)
		default:
			err = fmt.Errorf("container is %s", cur.State)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("resume %s: %w", c.Name, err))
			continue
		}
		resumed = append(resumed, c.ID)
	}
	return resumed, errors.Join(errs...)
}

func (m *leaseManager) quiesce(ctx context.Context, q *agentv1.QuiesceCommand) (*agentv1.QuiesceResult, error) {
	if q.LeaseId == "" || q.ApplicationId == "" {
		return nil, permanent(errors.New("lease_id and application_id are required"))
	}
	var mode string
	switch q.Mode {
	case agentv1.QuiesceMode_QUIESCE_MODE_PAUSE:
		mode = "pause"
	case agentv1.QuiesceMode_QUIESCE_MODE_STOP:
		mode = "stop"
	default:
		return nil, permanent(fmt.Errorf("unsupported quiesce mode %s", q.Mode))
	}
	secs := q.LeaseSeconds
	if secs == 0 {
		secs = defaultLeaseSeconds
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if l := m.leases[q.LeaseId]; l != nil {
		switch {
		case l.ApplicationID != q.ApplicationId:
			return nil, permanent(fmt.Errorf("lease %s belongs to application %s", l.LeaseID, l.ApplicationID))
		case !l.active():
			return nil, permanent(fmt.Errorf("lease %s already expired and the application was auto-resumed", l.LeaseID))
		}
		return l.result(), nil // idempotent repeat
	}
	for _, l := range m.leases {
		if l.ApplicationID == q.ApplicationId && l.active() {
			return nil, permanent(fmt.Errorf("application already quiesced by lease %s", l.LeaseID))
		}
	}
	ctl, err := m.ctl()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	l := &leaseRecord{LeaseID: q.LeaseId, ApplicationID: q.ApplicationId, Mode: mode, PreState: []containerState{},
		QuiescedAt: now, ExpiresAt: now.Add(time.Duration(secs) * time.Second)}
	for _, id := range q.ContainerIds {
		s, err := ctl.InspectState(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("inspect container %s: %w", id, err)
		}
		l.PreState = append(l.PreState, containerState{ID: id, Name: s.Name, State: s.State})
	}
	// Journal before touching anything: a crash mid-quiesce still resumes.
	m.leases[l.LeaseID] = l
	if err := m.save(); err != nil {
		delete(m.leases, l.LeaseID)
		return nil, fmt.Errorf("journal quiesce lease: %w", err)
	}
	for _, c := range l.PreState {
		if c.State != "running" {
			continue
		}
		if mode == "pause" {
			err = ctl.Pause(ctx, c.ID)
		} else {
			err = ctl.Stop(ctx, c.ID)
		}
		if err != nil {
			err = fmt.Errorf("%s container %s: %w", mode, c.Name, err)
			// Roll back with a context that outlives the command's.
			rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), autoResumeTimeout)
			_, rerr := m.restore(rctx, l)
			cancel()
			if rerr != nil {
				// Keep the lease: the dead-man switch retries the resume.
				m.arm(l)
				m.emit(&agentv1.AgentEvent{Type: evResumeFailed, Severity: sevCritical, ApplicationId: l.ApplicationID,
					LeaseId: l.LeaseID, Message: "quiesce failed and the rollback resume failed: " + rerr.Error()})
				return nil, errors.Join(err, rerr)
			}
			delete(m.leases, l.LeaseID)
			if serr := m.save(); serr != nil {
				m.log.Error("journal quiesce lease failed", "lease_id", l.LeaseID, "err", serr)
			}
			return nil, err
		}
	}
	m.arm(l)
	m.log.Info("application quiesced", "lease_id", l.LeaseID, "application_id", l.ApplicationID, "mode", mode,
		"containers", len(l.PreState), "lease_expires", l.ExpiresAt)
	return l.result(), nil
}

func (m *leaseManager) resume(ctx context.Context, leaseID string) (*agentv1.ResumeResult, error) {
	if leaseID == "" {
		return nil, permanent(errors.New("lease_id is required"))
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.leases[leaseID]
	if l == nil {
		return &agentv1.ResumeResult{AlreadyResumed: true}, nil
	}
	if !l.active() {
		delete(m.leases, leaseID)
		if err := m.save(); err != nil {
			m.log.Error("journal quiesce lease failed", "lease_id", leaseID, "err", err)
		}
		return &agentv1.ResumeResult{AlreadyResumed: true, AutoResumed: true, ResumedContainerIds: l.Resumed}, nil
	}
	resumed, err := m.restore(ctx, l)
	if err != nil {
		// The lease stays armed: the dead-man switch still fires at expiry.
		m.emit(&agentv1.AgentEvent{Type: evResumeFailed, Severity: sevCritical, ApplicationId: l.ApplicationID, LeaseId: leaseID,
			Message: "resume failed: " + err.Error()})
		return nil, err
	}
	m.disarm(leaseID)
	delete(m.leases, leaseID)
	if err := m.save(); err != nil {
		m.log.Error("journal quiesce lease failed", "lease_id", leaseID, "err", err)
	}
	m.log.Info("application resumed", "lease_id", leaseID, "application_id", l.ApplicationID, "containers", len(resumed))
	return &agentv1.ResumeResult{ResumedContainerIds: resumed}, nil
}

// autoResumed reports whether lease id exists and the dead-man switch has
// already resumed its application.
func (m *leaseManager) autoResumed(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.leases[id]
	return l != nil && !l.active()
}
