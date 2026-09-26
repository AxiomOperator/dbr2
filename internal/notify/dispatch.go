// SPDX-License-Identifier: Apache-2.0

package notify

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/AxiomOperator/dbr2/internal/audit"
	"github.com/AxiomOperator/dbr2/internal/store"
)

// Dispatcher tuning.
const (
	fanOutBatch = 200
	sendBatch   = 20
	sendWorkers = 4
	// sendLease must exceed the slowest send (SMTP 30 s) times the batch
	// divided by the workers; a crashed sender's deliveries are retried
	// after it.
	sendLease = 5 * time.Minute
	// maxRounds bounds one Dispatch call.
	maxRounds = 50
)

// Dispatch fans out undelivered outbox rows and sends the due deliveries.
func (s *Service) Dispatch(ctx context.Context) error {
	for range maxRounds {
		n, err := s.FanOut(ctx)
		if err != nil {
			return fmt.Errorf("fan-out: %w", err)
		}
		if n < fanOutBatch {
			break
		}
	}
	for range maxRounds {
		n, err := s.SendDue(ctx)
		if err != nil {
			return fmt.Errorf("send: %w", err)
		}
		if n < sendBatch {
			break
		}
	}
	return nil
}

// FanOut claims up to one batch of undelivered outbox rows (FOR UPDATE SKIP
// LOCKED, so concurrent dispatchers take disjoint rows), creates a pending
// delivery for every enabled channel of the row's organization whose event
// and severity filters match, and marks the rows fanned out (delivered_at),
// including rows no channel wants. It returns the number of rows claimed.
func (s *Service) FanOut(ctx context.Context) (int, error) {
	var n int
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		rows, err := q.ClaimUndeliveredNotifications(ctx, fanOutBatch)
		if err != nil || len(rows) == 0 {
			return err
		}
		n = len(rows)
		chans, err := q.ListEnabledNotificationChannels(ctx)
		if err != nil {
			return err
		}
		cutoff := s.now().Add(-s.opts.MaxAge)
		ids := make([]int64, 0, len(rows))
		for _, r := range rows {
			ids = append(ids, r.ID)
			if r.CreatedAt.Before(cutoff) {
				continue // too old to be useful; see Options.MaxAge
			}
			for _, c := range chans {
				if c.OrgID != r.OrgID || !Matches(c.Events, c.MinSeverity, r.EventType, r.Severity) {
					continue
				}
				if err := q.InsertNotificationDelivery(ctx, store.InsertNotificationDeliveryParams{NotificationID: r.ID, ChannelID: c.ID}); err != nil {
					return err
				}
			}
		}
		return q.MarkNotificationsFannedOut(ctx, ids)
	})
	return n, err
}

// SendDue claims up to one batch of due deliveries (with a lease) and sends
// them. It returns the number claimed.
func (s *Service) SendDue(ctx context.Context) (int, error) {
	claimed, err := s.q.ClaimDueDeliveries(ctx, store.ClaimDueDeliveriesParams{LeaseSeconds: sendLease.Seconds(), Batch: sendBatch})
	if err != nil {
		return 0, err
	}
	sem := make(chan struct{}, sendWorkers)
	var wg sync.WaitGroup
	for _, d := range claimed {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			s.deliver(ctx, d)
		})
	}
	wg.Wait()
	return len(claimed), nil
}

func (s *Service) deliver(ctx context.Context, d store.ClaimDueDeliveriesRow) {
	log := s.log.With("delivery_id", d.ID, "channel_id", d.ChannelID, "notification_id", d.NotificationID, "attempt", d.Attempts)
	ch, err := s.q.GetNotificationChannelByID(ctx, d.ChannelID)
	if errors.Is(err, pgx.ErrNoRows) {
		return // deleted: its deliveries are gone too
	}
	if err != nil {
		log.WarnContext(ctx, "loading notification channel failed", "err", err)
		return // retried after the lease
	}
	if !ch.Enabled {
		_ = s.q.MarkDeliveryFailed(ctx, store.MarkDeliveryFailedParams{ID: d.ID, LastError: strPtr("channel disabled")})
		return
	}
	n, err := s.q.GetOutboxNotification(ctx, d.NotificationID)
	if err != nil {
		log.WarnContext(ctx, "loading notification failed", "err", err)
		return
	}
	tt, tid := "", ""
	if n.TargetType != nil {
		tt = *n.TargetType
	}
	if n.TargetID != nil {
		tid = *n.TargetID
	}
	m := Message{ID: n.ID, DeliveryID: fmt.Sprint(d.ID), Type: n.EventType, Severity: n.Severity, Message: n.Message,
		TargetType: tt, TargetID: tid, Details: n.Payload, CreatedAt: n.CreatedAt}
	sendErr := s.send(ctx, ch, m)
	if ctx.Err() != nil {
		return // shutting down: the lease expires and another run retries
	}
	s.record(ctx, d, sendErr)
	if sendErr != nil {
		log.WarnContext(ctx, "notification delivery failed", "kind", ch.Kind, "err", sendErr)
	}
}

// record stores a send outcome: sent; retried after Backoff(attempts); or
// failed after MaxAttempts.
func (s *Service) record(ctx context.Context, d store.ClaimDueDeliveriesRow, sendErr error) {
	var err error
	if sendErr == nil {
		err = s.q.MarkDeliverySent(ctx, d.ID)
		if err == nil {
			err = s.q.RecordNotificationChannelResult(ctx, store.RecordNotificationChannelResultParams{ID: d.ChannelID, Ok: true})
		}
	} else {
		msg := truncate(sendErr.Error(), 1000)
		if int(d.Attempts) >= MaxAttempts {
			err = s.q.MarkDeliveryFailed(ctx, store.MarkDeliveryFailedParams{ID: d.ID, LastError: &msg})
		} else {
			err = s.q.MarkDeliveryRetry(ctx, store.MarkDeliveryRetryParams{ID: d.ID, LastError: &msg, NextAttemptAt: s.now().Add(Backoff(int(d.Attempts)))})
		}
		if err == nil {
			err = s.q.RecordNotificationChannelResult(ctx, store.RecordNotificationChannelResultParams{ID: d.ChannelID, Ok: false, LastError: &msg})
		}
	}
	if err != nil {
		s.log.WarnContext(ctx, "recording notification delivery failed", "delivery_id", d.ID, "err", err)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// send delivers m to one channel.
func (s *Service) send(ctx context.Context, ch store.NotificationChannel, m Message) error {
	cfg := ParseConfig(ch.Config)
	switch ch.Kind {
	case KindWebhook:
		var secret []byte
		if len(ch.SecretEnc) > 0 {
			if s.box == nil {
				return errors.New("secret box not configured")
			}
			var err error
			if secret, err = s.box.Open(ch.SecretEnc, ChannelAD(ch.ID)); err != nil {
				return fmt.Errorf("unsealing the webhook secret: %w", err)
			}
		}
		return sendWebhook(ctx, s.httpClient, cfg.URL, secret, m, s.opts.PublicURL, s.now())
	case KindEmail:
		smtpCfg, err := s.loadSMTP(ctx, ch.OrgID)
		if err != nil {
			return err
		}
		return sendEmail(ctx, smtpCfg, s.rootCAs, cfg.To, m, s.opts.PublicURL, s.now())
	}
	return fmt.Errorf("unknown channel kind %q", ch.Kind)
}

// ---- Agent presence --------------------------------------------------------------

// CheckAgents raises agent.offline (warning) for active agents without a
// live gateway session on this instance whose last sign of life is older
// than OfflineAfter, once per outage (agents.offline_alerted_at, set with a
// conditional update so only one instance raises it), and agent.online
// (info) when such an agent is back (a session here, or last_seen_at after
// the alert — another instance may hold the session).
func (s *Service) CheckAgents(ctx context.Context) error {
	rows, err := s.q.ListActiveAgentPresence(ctx)
	if err != nil {
		return err
	}
	now := s.now()
	for _, r := range rows {
		connected := s.presence != nil && s.presence.Connected(r.ID.String())
		switch {
		case r.OfflineAlertedAt == nil && !connected && now.Sub(r.LastActivity) > s.opts.OfflineAfter:
			silent := now.Sub(r.LastActivity).Round(time.Minute)
			details := map[string]any{"agent_id": r.ID.String(), "hostname": r.Hostname, "last_seen_at": r.LastSeenAt}
			a := Alert{OrgID: r.OrgID, Severity: SeverityWarning, Type: EventAgentOffline, TargetType: "agent", TargetID: r.ID.String(),
				Message: fmt.Sprintf("Agent %s is offline (no contact for %s)", r.Hostname, silent), Details: details, AuditResult: audit.Failure}
			s.raiseOnce(ctx, a, func(q *store.Queries) (int64, error) { return q.MarkAgentOfflineAlerted(ctx, r.ID) })
		case r.OfflineAlertedAt != nil && (connected || (r.LastSeenAt != nil && r.LastSeenAt.After(*r.OfflineAlertedAt))):
			details := map[string]any{"agent_id": r.ID.String(), "hostname": r.Hostname, "offline_since": r.OfflineAlertedAt, "last_seen_at": r.LastSeenAt}
			a := Alert{OrgID: r.OrgID, Severity: SeverityInfo, Type: EventAgentOnline, TargetType: "agent", TargetID: r.ID.String(),
				Message: fmt.Sprintf("Agent %s is back online", r.Hostname), Details: details}
			s.raiseOnce(ctx, a, func(q *store.Queries) (int64, error) { return q.ClearAgentOfflineAlerted(ctx, r.ID) })
		}
	}
	return nil
}

// raiseOnce raises a when claim (a conditional update) wins, atomically.
func (s *Service) raiseOnce(ctx context.Context, a Alert, claim func(q *store.Queries) (int64, error)) {
	raised := false
	err := s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		n, err := claim(q)
		if err != nil || n == 0 {
			return err
		}
		raised = true
		return raiseTx(ctx, q, rec, a)
	})
	if err != nil {
		s.log.WarnContext(ctx, "raising agent presence alert failed", "type", a.Type, "agent_id", a.TargetID, "err", err)
		return
	}
	if raised {
		s.log.InfoContext(ctx, "agent presence changed", "type", a.Type, "agent_id", a.TargetID)
		s.published(ctx, a)
	}
}
