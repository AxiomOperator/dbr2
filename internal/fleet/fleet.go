// SPDX-License-Identifier: Apache-2.0

// Package fleet is the administrative service for hosts (agents,
// registration tokens, approval, suspension, revocation, discovery requests;
// Phase 2) and applications (inventory, ownership metadata, manual grouping,
// Compose view/reveal; Phase 3).
package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.temporal.io/sdk/client"

	"github.com/AxiomOperator/dbr2/internal/audit"
	"github.com/AxiomOperator/dbr2/internal/auth"
	"github.com/AxiomOperator/dbr2/internal/gateway"
	"github.com/AxiomOperator/dbr2/internal/inventory"
	"github.com/AxiomOperator/dbr2/internal/rbac"
	"github.com/AxiomOperator/dbr2/internal/store"
	"github.com/AxiomOperator/dbr2/internal/temporalx"
	"github.com/AxiomOperator/dbr2/workflows/hosts"
)

// Errors mapped to API responses.
var (
	ErrNotFound          = errors.New("not found")
	ErrInvalidTransition = errors.New("invalid status transition")
	ErrInvalid           = errors.New("invalid request")
	ErrForbidden         = errors.New("forbidden")
	ErrNoInventory       = errors.New("no inventory has been collected for this host yet")
)

// Options configure the service.
type Options struct {
	OrgID uuid.UUID
	// GatewayAddress is what agents connect to (host:port) — used in the
	// join command shown with a new registration token.
	GatewayAddress string
	TaskQueue      string
}

// Service implements fleet administration.
type Service struct {
	opts     Options
	pool     *pgxpool.Pool
	q        *store.Queries
	audit    *audit.Recorder
	gw       *gateway.Gateway
	box      inventory.Sealer
	temporal client.Client
}

// New builds the service. temporal may be nil (discovery requests then fail).
func New(opts Options, pool *pgxpool.Pool, rec *audit.Recorder, gw *gateway.Gateway, box inventory.Sealer, tc client.Client) *Service {
	return &Service{opts: opts, pool: pool, q: store.New(pool), audit: rec, gw: gw, box: box, temporal: tc}
}

func (s *Service) event(p *auth.Principal, m auth.RequestMeta, typ string) audit.Event {
	return audit.Event{OrgID: s.opts.OrgID, Type: typ, ActorUserID: &p.UserID, ActorDisplay: p.Username, ActorKind: audit.ActorUser,
		SourceIP: m.IP, RequestID: m.RequestID, Result: audit.Success}
}

func (s *Service) inTx(ctx context.Context, fn func(q *store.Queries, rec *audit.Recorder) error) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		return fn(q, s.audit.WithQuerier(q))
	})
}

// ---- Registration tokens ------------------------------------------------------

// NewToken is a created registration token (the secret is shown once).
type NewToken struct {
	store.CreateRegistrationTokenRow
	Token          string
	GatewayAddress string
	CASHA256       string
	JoinCommand    string
}

// CreateRegistrationToken issues a single-use, expiring enrollment token.
func (s *Service) CreateRegistrationToken(ctx context.Context, p *auth.Principal, description string, ttl time.Duration, m auth.RequestMeta) (*NewToken, error) {
	if ttl <= 0 || ttl > 7*24*time.Hour {
		return nil, fmt.Errorf("%w: expiry must be between 1 hour and 7 days", ErrInvalid)
	}
	tok, err := auth.NewToken(gateway.RegistrationTokenPrefix)
	if err != nil {
		return nil, err
	}
	var out *NewToken
	err = s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		row, err := q.CreateRegistrationToken(ctx, store.CreateRegistrationTokenParams{OrgID: s.opts.OrgID, TokenHash: auth.HashToken(tok),
			Prefix: tok[:len(gateway.RegistrationTokenPrefix)+6], Description: description, CreatedBy: &p.UserID, ExpiresAt: time.Now().Add(ttl)})
		if err != nil {
			return err
		}
		ev := s.event(p, m, audit.RegTokenCreated)
		ev.TargetType, ev.TargetID = "registration_token", row.ID.String()
		ev.Details = map[string]any{"description": description, "expires_at": row.ExpiresAt}
		if _, err := rec.Record(ctx, ev); err != nil {
			return err
		}
		fp := s.gw.CA().Fingerprint()
		out = &NewToken{CreateRegistrationTokenRow: row, Token: tok, GatewayAddress: s.opts.GatewayAddress, CASHA256: fp,
			JoinCommand: fmt.Sprintf("sudo dbr2-agent enroll --server %s --token %s --ca-sha256 %s && sudo systemctl enable --now dbr2-agent",
				s.opts.GatewayAddress, tok, fp)}
		return nil
	})
	return out, err
}

// ListRegistrationTokens lists tokens (never their secrets).
func (s *Service) ListRegistrationTokens(ctx context.Context) ([]store.ListRegistrationTokensRow, error) {
	return s.q.ListRegistrationTokens(ctx, s.opts.OrgID)
}

// RevokeRegistrationToken revokes an unused token.
func (s *Service) RevokeRegistrationToken(ctx context.Context, p *auth.Principal, id uuid.UUID, m auth.RequestMeta) error {
	return s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		n, err := q.RevokeRegistrationToken(ctx, store.RevokeRegistrationTokenParams{ID: id, OrgID: s.opts.OrgID})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		ev := s.event(p, m, audit.RegTokenRevoked)
		ev.TargetType, ev.TargetID = "registration_token", id.String()
		_, err = rec.Record(ctx, ev)
		return err
	})
}

// ---- Agents -------------------------------------------------------------------

// Agent is an agent with live state.
type Agent struct {
	store.Agent
	Connected bool
	Outdated  bool
}

func (s *Service) view(a store.Agent) Agent {
	return Agent{Agent: a, Connected: s.gw.Connected(a.ID.String()), Outdated: gateway.Outdated(a.AgentVersion)}
}

// ListAgents lists every agent.
func (s *Service) ListAgents(ctx context.Context) ([]Agent, error) {
	rows, err := s.q.ListAgents(ctx, s.opts.OrgID)
	if err != nil {
		return nil, err
	}
	out := make([]Agent, len(rows))
	for i, r := range rows {
		out[i] = s.view(r)
	}
	return out, nil
}

// GetAgent returns one agent.
func (s *Service) GetAgent(ctx context.Context, id uuid.UUID) (Agent, error) {
	a, err := s.q.GetAgent(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && a.OrgID != s.opts.OrgID) {
		return Agent{}, ErrNotFound
	}
	if err != nil {
		return Agent{}, err
	}
	return s.view(a), nil
}

// Status actions.
const (
	ActionApprove = "approve"
	ActionSuspend = "suspend"
	ActionResume  = "resume"
	ActionRevoke  = "revoke"
)

var transitions = map[string]struct {
	from  []string
	to    string
	event string
}{
	ActionApprove: {[]string{"pending"}, "active", audit.AgentApproved},
	ActionSuspend: {[]string{"active", "pending"}, "suspended", audit.AgentSuspended},
	ActionResume:  {[]string{"suspended"}, "active", audit.AgentResumed},
	ActionRevoke:  {[]string{"pending", "active", "suspended"}, "revoked", audit.AgentRevoked},
}

// SetStatus applies an approval-workflow action (Pending → Active, suspend,
// resume, revoke). Suspension and revocation end the live session;
// revocation also revokes every certificate (permanent).
func (s *Service) SetStatus(ctx context.Context, p *auth.Principal, id uuid.UUID, action, reason string, m auth.RequestMeta) (Agent, error) {
	t, ok := transitions[action]
	if !ok {
		return Agent{}, fmt.Errorf("%w: unknown action %q", ErrInvalid, action)
	}
	var updated store.Agent
	err := s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		a, err := q.GetAgent(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && a.OrgID != s.opts.OrgID) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		allowed := false
		for _, f := range t.from {
			allowed = allowed || f == a.Status
		}
		if !allowed {
			return fmt.Errorf("%w: cannot %s an agent that is %s", ErrInvalidTransition, action, a.Status)
		}
		if updated, err = q.SetAgentStatus(ctx, store.SetAgentStatusParams{ID: id, Status: t.to, StatusReason: strPtr(reason),
			StatusChangedBy: &p.UserID, OrgID: s.opts.OrgID}); err != nil {
			return err
		}
		if action == ActionRevoke {
			if err := q.RevokeAgentCertificates(ctx, id); err != nil {
				return err
			}
		}
		ev := s.event(p, m, t.event)
		ev.TargetType, ev.TargetID, ev.Reason = "agent", id.String(), reason
		ev.Before, ev.After = map[string]any{"status": a.Status}, map[string]any{"status": t.to}
		ev.Details = map[string]any{"hostname": a.Hostname}
		_, err = rec.Record(ctx, ev)
		return err
	})
	if err != nil {
		return Agent{}, err
	}
	switch action {
	case ActionSuspend:
		s.gw.Disconnect(id.String(), gateway.RejectSuspended, "suspended by an administrator")
	case ActionRevoke:
		s.gw.Disconnect(id.String(), gateway.RejectRevoked, "revoked by an administrator")
	}
	return s.view(updated), nil
}

// RequestDiscovery starts the DiscoverHost workflow for an agent.
func (s *Service) RequestDiscovery(ctx context.Context, p *auth.Principal, id uuid.UUID, m auth.RequestMeta) (string, error) {
	a, err := s.GetAgent(ctx, id)
	if err != nil {
		return "", err
	}
	if a.Status != "active" {
		return "", fmt.Errorf("%w: agent is %s", ErrInvalidTransition, a.Status)
	}
	if s.temporal == nil {
		return "", errors.New("workflow engine unavailable")
	}
	run, err := temporalx.StartHostOperation(ctx, s.temporal, s.opts.TaskQueue, id.String(), "discover", hosts.DiscoverHost, hosts.DiscoverInput{AgentID: id.String()})
	if err != nil {
		return "", err
	}
	ev := s.event(p, m, audit.DiscoveryRequested)
	ev.TargetType, ev.TargetID = "agent", id.String()
	ev.Details = map[string]any{"workflow_id": run.GetID(), "run_id": run.GetRunID()}
	if _, err := s.audit.Record(ctx, ev); err != nil {
		return "", err
	}
	return run.GetID(), nil
}

// ---- Applications ----------------------------------------------------------------

// Application combines the stored record with its analyzed view.
type Application struct {
	Record   store.ListApplicationsRow
	Analysis *inventory.Application // nil when missing from the latest inventory
	// Inventory is the (sealed) inventory of its host; used for detail views.
	Inventory *inventory.Inventory
}

func (s *Service) snapshots(ctx context.Context) (map[uuid.UUID]*inventory.Inventory, error) {
	agents, err := s.q.ListAgents(ctx, s.opts.OrgID)
	if err != nil {
		return nil, err
	}
	out := map[uuid.UUID]*inventory.Inventory{}
	for _, a := range agents {
		snap, err := s.q.GetInventorySnapshot(ctx, a.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var inv inventory.Inventory
		if err := json.Unmarshal(snap.Data, &inv); err != nil {
			return nil, err
		}
		out[a.ID] = &inv
	}
	return out, nil
}

func (s *Service) manualMap(rows []store.ListApplicationsRow, agent uuid.UUID) map[string][]string {
	m := map[string][]string{}
	for _, r := range rows {
		if r.AgentID == agent && r.Kind == inventory.KindManual {
			m[r.Key] = r.ManualContainers
		}
	}
	return m
}

// ListApplications returns every application with its analysis.
func (s *Service) ListApplications(ctx context.Context) ([]Application, error) {
	rows, err := s.q.ListApplications(ctx, s.opts.OrgID)
	if err != nil {
		return nil, err
	}
	invs, err := s.snapshots(ctx)
	if err != nil {
		return nil, err
	}
	analyzed := map[uuid.UUID]map[string]*inventory.Application{}
	for agentID, inv := range invs {
		m := map[string]*inventory.Application{}
		apps := inventory.Analyze(inv, s.manualMap(rows, agentID))
		for i := range apps {
			m[apps[i].Key] = &apps[i]
		}
		analyzed[agentID] = m
	}
	out := make([]Application, 0, len(rows))
	for _, r := range rows {
		a := Application{Record: r, Inventory: invs[r.AgentID]}
		if m := analyzed[r.AgentID]; m != nil && r.MissingSince == nil {
			a.Analysis = m[r.Key]
		}
		out = append(out, a)
	}
	return out, nil
}

// GetApplication returns one application.
func (s *Service) GetApplication(ctx context.Context, id uuid.UUID) (Application, error) {
	apps, err := s.ListApplications(ctx)
	if err != nil {
		return Application{}, err
	}
	for _, a := range apps {
		if a.Record.ID == id {
			return a, nil
		}
	}
	return Application{}, ErrNotFound
}

// Metadata is the editable ownership metadata (roadmap Phase 3).
type Metadata struct {
	DisplayName *string
	Owner       *string
	Environment *string
	Criticality *string
}

var environments = map[string]bool{"production": true, "staging": true, "development": true, "test": true, "other": true}
var criticalities = map[string]bool{"critical": true, "high": true, "medium": true, "low": true}

// UpdateMetadata sets owner, environment, criticality and display name.
func (s *Service) UpdateMetadata(ctx context.Context, p *auth.Principal, id uuid.UUID, md Metadata, m auth.RequestMeta) error {
	if md.Environment != nil && !environments[*md.Environment] {
		return fmt.Errorf("%w: environment must be production, staging, development, test or other", ErrInvalid)
	}
	if md.Criticality != nil && !criticalities[*md.Criticality] {
		return fmt.Errorf("%w: criticality must be critical, high, medium or low", ErrInvalid)
	}
	return s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		before, err := q.GetApplication(ctx, store.GetApplicationParams{ID: id, OrgID: s.opts.OrgID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		after, err := q.UpdateApplicationMetadata(ctx, store.UpdateApplicationMetadataParams{ID: id, OrgID: s.opts.OrgID,
			DisplayName: blankNil(md.DisplayName), Owner: blankNil(md.Owner), Environment: blankNil(md.Environment), Criticality: blankNil(md.Criticality)})
		if err != nil {
			return err
		}
		ev := s.event(p, m, audit.ApplicationUpdated)
		ev.TargetType, ev.TargetID = "application", id.String()
		ev.Before = map[string]any{"display_name": before.DisplayName, "owner": before.Owner, "environment": before.Environment, "criticality": before.Criticality}
		ev.After = map[string]any{"display_name": after.DisplayName, "owner": after.Owner, "environment": after.Environment, "criticality": after.Criticality}
		_, err = rec.Record(ctx, ev)
		return err
	})
}

// CreateManual groups standalone containers of one host into an application.
func (s *Service) CreateManual(ctx context.Context, p *auth.Principal, agentID uuid.UUID, name string, containers []string, m auth.RequestMeta) (uuid.UUID, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(containers) == 0 {
		return uuid.Nil, fmt.Errorf("%w: name and at least one container are required", ErrInvalid)
	}
	invs, err := s.snapshots(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	inv := invs[agentID]
	if inv == nil {
		return uuid.Nil, ErrNoInventory
	}
	known := map[string]inventory.Container{}
	for _, c := range inv.Containers {
		known[c.Name] = c
	}
	for _, c := range containers {
		kc, ok := known[c]
		if !ok {
			return uuid.Nil, fmt.Errorf("%w: container %q not found on this host", ErrInvalid, c)
		}
		if kc.Labels[inventory.LabelProject] != "" {
			return uuid.Nil, fmt.Errorf("%w: container %q belongs to Compose project %q", ErrInvalid, c, kc.Labels[inventory.LabelProject])
		}
	}
	var id uuid.UUID
	err = s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		ag, err := q.GetAgent(ctx, agentID)
		if err != nil {
			return ErrNotFound
		}
		app, err := q.CreateManualApplication(ctx, store.CreateManualApplicationParams{OrgID: s.opts.OrgID, AgentID: agentID,
			Key: inventory.KindManual + ":" + uuid.NewString(), Name: name, ManualContainers: containers})
		if err != nil {
			return err
		}
		id = app.ID
		if _, err := gateway.Reconcile(ctx, q, ag, inv); err != nil {
			return err
		}
		ev := s.event(p, m, audit.ApplicationCreated)
		ev.TargetType, ev.TargetID = "application", id.String()
		ev.Details = map[string]any{"name": name, "containers": containers, "host": ag.Hostname}
		_, err = rec.Record(ctx, ev)
		return err
	})
	return id, err
}

// DeleteManual removes a manual application (its containers become
// standalone applications again at the next reconciliation).
func (s *Service) DeleteManual(ctx context.Context, p *auth.Principal, id uuid.UUID, m auth.RequestMeta) error {
	invs, err := s.snapshots(ctx)
	if err != nil {
		return err
	}
	return s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		app, err := q.GetApplication(ctx, store.GetApplicationParams{ID: id, OrgID: s.opts.OrgID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if app.Kind != inventory.KindManual {
			return fmt.Errorf("%w: only manual applications can be deleted", ErrInvalid)
		}
		if _, err := q.DeleteManualApplication(ctx, store.DeleteManualApplicationParams{ID: id, OrgID: s.opts.OrgID}); err != nil {
			return err
		}
		if inv := invs[app.AgentID]; inv != nil {
			ag, err := q.GetAgent(ctx, app.AgentID)
			if err != nil {
				return err
			}
			if _, err := gateway.Reconcile(ctx, q, ag, inv); err != nil {
				return err
			}
		}
		ev := s.event(p, m, audit.ApplicationDeleted)
		ev.TargetType, ev.TargetID = "application", id.String()
		ev.Details = map[string]any{"name": app.Name}
		_, err = rec.Record(ctx, ev)
		return err
	})
}

// Compose is the Compose definition view of an application.
type Compose struct {
	Source        string
	Reason        string
	ConfigFiles   []inventory.File
	EnvFiles      []inventory.File
	Reconstructed string
	Revealed      bool
	Secrets       map[string]string // placeholder values (reveal only)
}

// Compose returns the original (masked) Compose files, or the reconstructed
// definition. reveal=true requires secrets.read and is audited.
func (s *Service) Compose(ctx context.Context, p *auth.Principal, id uuid.UUID, reveal bool, m auth.RequestMeta) (*Compose, error) {
	if reveal && !p.Can(rbac.SecretsRead) {
		return nil, fmt.Errorf("%w: revealing secrets requires secrets.read", ErrForbidden)
	}
	app, err := s.GetApplication(ctx, id)
	if err != nil {
		return nil, err
	}
	if app.Analysis == nil || app.Inventory == nil {
		return nil, ErrNoInventory
	}
	inv := app.Inventory
	if reveal {
		if err := inventory.Reveal(inv, s.box, inventory.SealAD(app.Record.AgentID.String())); err != nil {
			return nil, err
		}
		ev := s.event(p, m, audit.SecretsRevealed)
		ev.TargetType, ev.TargetID = "application", id.String()
		ev.Details = map[string]any{"name": app.Record.Name, "view": "compose"}
		if _, err := s.audit.Record(ctx, ev); err != nil {
			return nil, err
		}
	}
	a := inventory.Analyze(inv, s.manualMapFor(ctx, app.Record.AgentID))
	var cur *inventory.Application
	for i := range a {
		if a[i].Key == app.Record.Key {
			cur = &a[i]
		}
	}
	if cur == nil {
		return nil, ErrNotFound
	}
	out := &Compose{Source: cur.Source, Reason: cur.SourceReason, Revealed: reveal}
	if cur.Source == inventory.SourceOriginal {
		out.ConfigFiles, out.EnvFiles = cur.ConfigFiles, cur.EnvFiles
	} else {
		if out.Reconstructed, err = inventory.Reconstruct(cur, inv); err != nil {
			return nil, err
		}
		if reveal {
			out.Secrets = map[string]string{}
			for _, c := range inv.Containers {
				for _, cn := range cur.Containers {
					if c.Name != cn {
						continue
					}
					for _, e := range c.Env {
						if e.Sensitive {
							out.Secrets[e.Key] = e.Value
						}
					}
				}
			}
		}
	}
	if !reveal {
		for i := range out.ConfigFiles {
			out.ConfigFiles[i].Sealed = nil
		}
		for i := range out.EnvFiles {
			out.EnvFiles[i].Sealed = nil
		}
	}
	return out, nil
}

func (s *Service) manualMapFor(ctx context.Context, agentID uuid.UUID) map[string][]string {
	rows, err := s.q.ListAgentApplications(ctx, agentID)
	if err != nil {
		return nil
	}
	m := map[string][]string{}
	for _, r := range rows {
		if r.Kind == inventory.KindManual {
			m[r.Key] = r.ManualContainers
		}
	}
	return m
}

// Inventory returns an agent's inventory with secrets masked.
func (s *Service) Inventory(ctx context.Context, agentID uuid.UUID) (*inventory.Inventory, time.Time, error) {
	snap, err := s.q.GetInventorySnapshot(ctx, agentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, time.Time{}, ErrNoInventory
	}
	if err != nil {
		return nil, time.Time{}, err
	}
	var inv inventory.Inventory
	if err := json.Unmarshal(snap.Data, &inv); err != nil {
		return nil, time.Time{}, err
	}
	inventory.Redact(&inv)
	return &inv, snap.ReceivedAt, nil
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func blankNil(p *string) *string {
	if p == nil {
		return nil
	}
	v := strings.TrimSpace(*p)
	if v == "" {
		return nil
	}
	return &v
}
