// SPDX-License-Identifier: Apache-2.0

// Package notify delivers notifications (Phase 7): the dispatcher fans
// notification_outbox rows out to the matching email and webhook channels,
// sends them with retries and backoff, and raises agent.offline /
// agent.online from the gateway's live sessions and agents.last_seen_at.
//
// Several dbr2-server instances may run the dispatcher at once: outbox rows
// and deliveries are claimed with SELECT … FOR UPDATE SKIP LOCKED (deliveries
// additionally with a lease), and agent presence alerts use conditional
// updates, so each notification is fanned out and sent once.
package notify

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AxiomOperator/dbr2/internal/audit"
	"github.com/AxiomOperator/dbr2/internal/events"
	"github.com/AxiomOperator/dbr2/internal/rbac"
	"github.com/AxiomOperator/dbr2/internal/store"
)

// Errors returned by the service (mapped by the API).
var (
	ErrNotFound = errors.New("not found")
	ErrInvalid  = errors.New("invalid request")
	ErrConflict = errors.New("conflict")
)

// Severities, lowest first.
const (
	SeverityInfo     = "info"
	SeverityWarning  = "warning"
	SeverityCritical = "critical"
)

// Event types raised by this package.
const (
	EventTest         = "notification.test"
	EventAgentOffline = "agent.offline"
	EventAgentOnline  = "agent.online"
)

// Sealer encrypts and decrypts secrets at rest (auth.SecretBox).
type Sealer interface {
	Seal(plaintext []byte, ad string) ([]byte, error)
	Open(sealed []byte, ad string) ([]byte, error)
}

// Presence reports whether an agent has a live gateway session on this
// instance (gateway.Gateway).
type Presence interface {
	Connected(agentID string) bool
}

// Options configure the service.
type Options struct {
	// OrgID is the organization the API operates on.
	OrgID uuid.UUID
	// PublicURL is the console URL linked from notifications (DBR2_PUBLIC_URL).
	PublicURL string
	// Interval between dispatcher runs (default 10 s).
	Interval time.Duration
	// PresenceInterval between agent presence checks (default 1 min).
	PresenceInterval time.Duration
	// OfflineAfter: an active agent without a session and silent this long
	// raises agent.offline (default 10 min).
	OfflineAfter time.Duration
	// MaxAge: outbox rows older than this when first seen are marked without
	// being sent (e.g. after a long outage; default 24 h).
	MaxAge time.Duration
}

// Service manages channels and settings and runs the dispatcher.
type Service struct {
	pool     *pgxpool.Pool
	q        *store.Queries
	box      Sealer
	audit    *audit.Recorder
	presence Presence
	log      *slog.Logger
	opts     Options
	events   atomic.Pointer[events.Bus]
	nudge    chan struct{}

	httpClient *http.Client
	// rootCAs overrides the system roots for SMTP TLS (tests).
	rootCAs *x509.CertPool
	now     func() time.Time
}

// New builds the service. presence may be nil (no live-session check).
func New(pool *pgxpool.Pool, box Sealer, rec *audit.Recorder, presence Presence, log *slog.Logger, opts Options) *Service {
	if opts.Interval <= 0 {
		opts.Interval = 10 * time.Second
	}
	if opts.PresenceInterval <= 0 {
		opts.PresenceInterval = time.Minute
	}
	if opts.OfflineAfter <= 0 {
		opts.OfflineAfter = 10 * time.Minute
	}
	if opts.MaxAge <= 0 {
		opts.MaxAge = 24 * time.Hour
	}
	if log == nil {
		log = slog.Default()
	}
	return &Service{pool: pool, q: store.New(pool), box: box, audit: rec, presence: presence, log: log.With("component", "notify"),
		opts: opts, nudge: make(chan struct{}, 1), httpClient: newWebhookClient(), now: time.Now}
}

// SetEvents attaches the live-update bus: alert.created events published by
// other services nudge the dispatcher, and alerts raised here are published.
func (s *Service) SetEvents(b *events.Bus) { s.events.Store(b) }

// Nudge asks the dispatcher to run now (non-blocking).
func (s *Service) Nudge() {
	select {
	case s.nudge <- struct{}{}:
	default:
	}
}

// Run runs the dispatcher until ctx ends.
func (s *Service) Run(ctx context.Context) {
	if b := s.events.Load(); b != nil {
		go s.watchAlerts(ctx, b)
	}
	t := time.NewTicker(s.opts.Interval)
	defer t.Stop()
	var lastPresence time.Time
	for {
		if time.Since(lastPresence) >= s.opts.PresenceInterval {
			if err := s.CheckAgents(ctx); err != nil && ctx.Err() == nil {
				s.log.WarnContext(ctx, "agent presence check failed", "err", err)
			}
			lastPresence = time.Now()
		}
		if err := s.Dispatch(ctx); err != nil && ctx.Err() == nil {
			s.log.WarnContext(ctx, "notification dispatch failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.nudge:
		}
	}
}

// watchAlerts nudges the dispatcher when another service raises an alert.
// The publisher may still be inside its transaction, so the nudge is
// delayed briefly; the periodic run catches anything missed.
func (s *Service) watchAlerts(ctx context.Context, b *events.Bus) {
	for e := range b.Subscribe(ctx) {
		if e.Type == events.AlertCreated {
			time.AfterFunc(time.Second, s.Nudge)
		}
	}
}

// Alert is a notification raised through the outbox.
type Alert struct {
	OrgID      uuid.UUID
	Severity   string
	Type       string
	TargetType string
	TargetID   string
	Message    string
	Details    map[string]any
	// AuditResult of the matching system audit event (default success).
	AuditResult string
}

// Raise writes an alert to the outbox and audits it as a system event, in
// one transaction, then publishes alert.created and nudges the dispatcher.
func (s *Service) Raise(ctx context.Context, a Alert) error {
	if err := s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error { return raiseTx(ctx, q, rec, a) }); err != nil {
		return err
	}
	s.published(ctx, a)
	return nil
}

func raiseTx(ctx context.Context, q *store.Queries, rec *audit.Recorder, a Alert) error {
	payload, err := json.Marshal(a.Details)
	if err != nil || a.Details == nil {
		payload = []byte("{}")
	}
	if err := q.InsertNotification(ctx, store.InsertNotificationParams{OrgID: a.OrgID, Severity: a.Severity, EventType: a.Type,
		TargetType: &a.TargetType, TargetID: &a.TargetID, Message: a.Message, Payload: payload}); err != nil {
		return err
	}
	result := a.AuditResult
	if result == "" {
		result = audit.Success
	}
	_, err = rec.Record(ctx, audit.Event{OrgID: a.OrgID, Type: a.Type, ActorKind: audit.ActorSystem, ActorDisplay: "dbr2-server",
		TargetType: a.TargetType, TargetID: a.TargetID, Result: result, Details: a.Details})
	return err
}

func (s *Service) published(ctx context.Context, a Alert) {
	if b := s.events.Load(); b != nil {
		b.Publish(ctx, events.New(events.AlertCreated, rbac.BackupRead, map[string]any{"severity": a.Severity, "type": a.Type,
			"target_type": a.TargetType, "target_id": a.TargetID, "message": a.Message}))
	}
	s.Nudge()
}

func (s *Service) inTx(ctx context.Context, fn func(q *store.Queries, rec *audit.Recorder) error) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		return fn(q, s.audit.WithQuerier(q))
	})
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func strPtr(s string) *string { return &s }
