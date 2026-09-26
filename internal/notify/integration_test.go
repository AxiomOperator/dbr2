// SPDX-License-Identifier: Apache-2.0

//go:build integration

package notify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AxiomOperator/dbr2/db"
	"github.com/AxiomOperator/dbr2/internal/audit"
	"github.com/AxiomOperator/dbr2/internal/auth"
	"github.com/AxiomOperator/dbr2/internal/store"
	"github.com/AxiomOperator/dbr2/internal/testutil"
)

var orgID = uuid.MustParse(db.DefaultOrgID)

type fakePresence struct {
	mu        sync.Mutex
	connected map[string]bool
}

func (p *fakePresence) Connected(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.connected[id]
}

func newTestService(t *testing.T, pool *pgxpool.Pool, presence Presence) *Service {
	t.Helper()
	box, err := auth.NewSecretBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	log := testutil.Logger()
	return New(pool, box, audit.NewRecorder(store.New(pool), log), presence, log, Options{OrgID: orgID, PublicURL: "https://dbr2.test"})
}

func testPrincipal(t *testing.T, pool *pgxpool.Pool) *auth.Principal {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO users (org_id, kind, username, display_name) VALUES ($1, 'master_admin', 'admin', 'Admin') RETURNING id`, orgID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return &auth.Principal{UserID: id, OrgID: orgID, Username: "admin"}
}

func insertOutbox(t *testing.T, pool *pgxpool.Pool, typ, sev string) {
	t.Helper()
	if err := store.New(pool).InsertNotification(context.Background(), store.InsertNotificationParams{OrgID: orgID, Severity: sev, EventType: typ,
		Message: typ + " happened", Payload: []byte(`{"k":"v"}`)}); err != nil {
		t.Fatal(err)
	}
}

func count(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// webhookSink counts requests per X-DBR2-Delivery.
type webhookSink struct {
	srv    *httptest.Server
	mu     sync.Mutex
	hits   map[string]int
	status atomic.Int32
}

func newSink(t *testing.T) *webhookSink {
	s := &webhookSink{hits: map[string]int{}}
	s.status.Store(http.StatusOK)
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits[r.Header.Get("X-DBR2-Delivery")]++
		s.mu.Unlock()
		w.WriteHeader(int(s.status.Load()))
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func TestIntegrationFanOutSkipLocked(t *testing.T) {
	pool, _ := testutil.Pool(t)
	ctx := context.Background()
	svc := newTestService(t, pool, nil)
	p := testPrincipal(t, pool)
	sink := newSink(t)

	all, err := svc.CreateChannel(ctx, p, ChannelInput{Name: "all", Kind: KindWebhook, Enabled: true, URL: sink.srv.URL, MinSeverity: "info"}, auth.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	backups, err := svc.CreateChannel(ctx, p, ChannelInput{Name: "backups", Kind: KindWebhook, Enabled: true, URL: sink.srv.URL,
		Events: []string{"backup.*"}, MinSeverity: "warning"}, auth.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	disabled, err := svc.CreateChannel(ctx, p, ChannelInput{Name: "off", Kind: KindWebhook, Enabled: false, URL: sink.srv.URL, MinSeverity: "info"}, auth.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}

	// 3 kinds × 150 rows: backup.failed/critical, backup.completed/info, agent.offline/warning.
	const perKind = 150
	for i := 0; i < perKind; i++ {
		insertOutbox(t, pool, "backup.failed", "critical")
		insertOutbox(t, pool, "backup.completed", "info")
		insertOutbox(t, pool, "agent.offline", "warning")
	}
	// A stale row is marked but not fanned out.
	if _, err := pool.Exec(ctx, `INSERT INTO notification_outbox (org_id, event_type, severity, payload, created_at)
		VALUES ($1, 'backup.failed', 'critical', '{}', now() - interval '2 days')`, orgID); err != nil {
		t.Fatal(err)
	}
	total := perKind*3 + 1

	// Several dispatchers (as on several dbr2-server instances) fan out at
	// once; each row must be claimed by exactly one of them.
	var claimed atomic.Int64
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for w := 0; w < 6; w++ {
		d := newTestService(t, pool, nil)
		wg.Go(func() {
			for {
				n, err := d.FanOut(ctx)
				if err != nil {
					errs <- err
					return
				}
				if n == 0 {
					return
				}
				claimed.Add(int64(n))
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if claimed.Load() != int64(total) {
		t.Fatalf("claimed %d rows, want %d (a row was claimed twice or missed)", claimed.Load(), total)
	}
	if n := count(t, pool, `SELECT count(*) FROM notification_outbox WHERE delivered_at IS NULL`); n != 0 {
		t.Fatalf("%d rows not fanned out", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM notification_deliveries WHERE channel_id = $1`, all.ID); n != perKind*3 {
		t.Fatalf("all: %d deliveries, want %d", n, perKind*3)
	}
	if n := count(t, pool, `SELECT count(*) FROM notification_deliveries WHERE channel_id = $1`, backups.ID); n != perKind {
		t.Fatalf("backups: %d deliveries, want %d (backup.failed only)", n, perKind)
	}
	if n := count(t, pool, `SELECT count(*) FROM notification_deliveries WHERE channel_id = $1`, disabled.ID); n != 0 {
		t.Fatalf("disabled channel got %d deliveries", n)
	}

	// Concurrent senders: every delivery is sent exactly once.
	for w := 0; w < 4; w++ {
		d := newTestService(t, pool, nil)
		wg.Go(func() {
			for {
				n, err := d.SendDue(ctx)
				if err != nil {
					t.Error(err)
					return
				}
				if n == 0 {
					return
				}
			}
		})
	}
	wg.Wait()
	if n := count(t, pool, `SELECT count(*) FROM notification_deliveries WHERE state <> 'sent'`); n != 0 {
		t.Fatalf("%d deliveries not sent", n)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.hits) != perKind*4 {
		t.Fatalf("%d distinct deliveries received, want %d", len(sink.hits), perKind*4)
	}
	for id, n := range sink.hits {
		if n != 1 {
			t.Fatalf("delivery %s received %d times", id, n)
		}
	}
	ch, _ := svc.GetChannel(ctx, all.ID)
	if ch.LastDeliveryAt == nil || ch.LastError != nil {
		t.Fatalf("channel result not recorded: %+v", ch)
	}
}

func TestIntegrationRetryBackoffAndFail(t *testing.T) {
	pool, _ := testutil.Pool(t)
	ctx := context.Background()
	svc := newTestService(t, pool, nil)
	p := testPrincipal(t, pool)
	sink := newSink(t)
	sink.status.Store(http.StatusInternalServerError)
	ch, err := svc.CreateChannel(ctx, p, ChannelInput{Name: "flaky", Kind: KindWebhook, Enabled: true, URL: sink.srv.URL,
		Secret: ptr("0123456789abcdef0123"), MinSeverity: "info"}, auth.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	insertOutbox(t, pool, "restore.failed", "critical")
	if err := svc.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	var state string
	var attempts int
	var next time.Time
	row := func() {
		if err := pool.QueryRow(ctx, `SELECT state, attempts, next_attempt_at FROM notification_deliveries WHERE channel_id = $1`, ch.ID).
			Scan(&state, &attempts, &next); err != nil {
			t.Fatal(err)
		}
	}
	row()
	if state != "pending" || attempts != 1 {
		t.Fatalf("after 1st failure: %s/%d", state, attempts)
	}
	if d := time.Until(next); d < 50*time.Second || d > 70*time.Second {
		t.Fatalf("next attempt in %v, want ~1m", d)
	}
	got, _ := svc.GetChannel(ctx, ch.ID)
	if got.LastError == nil {
		t.Fatal("channel last_error not recorded")
	}
	for i := 2; i <= MaxAttempts; i++ {
		if _, err := pool.Exec(ctx, `UPDATE notification_deliveries SET next_attempt_at = now() - interval '1 second'`); err != nil {
			t.Fatal(err)
		}
		if err := svc.Dispatch(ctx); err != nil {
			t.Fatal(err)
		}
		row()
		if i < MaxAttempts {
			want := Backoff(i)
			if state != "pending" || attempts != i || time.Until(next) < want-10*time.Second || time.Until(next) > want+10*time.Second {
				t.Fatalf("attempt %d: %s/%d next in %v (want %v)", i, state, attempts, time.Until(next), want)
			}
		}
	}
	if state != "failed" || attempts != MaxAttempts {
		t.Fatalf("after %d attempts: %s/%d", MaxAttempts, state, attempts)
	}
	// Recovery: now it succeeds, but a failed delivery is not retried.
	sink.status.Store(http.StatusOK)
	if _, err := svc.SendDue(ctx); err != nil {
		t.Fatal(err)
	}
	row()
	if state != "failed" {
		t.Fatalf("failed delivery retried: %s", state)
	}
	// Deliveries API view.
	ds, err := svc.ListDeliveries(ctx, ch.ID, 10)
	if err != nil || len(ds) != 1 || ds[0].EventType != "restore.failed" || ds[0].LastError == nil {
		t.Fatalf("deliveries: %+v %v", ds, err)
	}
	// Test sends directly (signed) and reports the result.
	res, err := svc.TestChannel(ctx, p, ch.ID, auth.RequestMeta{})
	if err != nil || !res.Delivered {
		t.Fatalf("test: %+v %v", res, err)
	}
	if n := count(t, pool, `SELECT count(*) FROM audit_events WHERE event_type = $1`, audit.NotificationChannelTested); n != 1 {
		t.Fatalf("%d tested audit events", n)
	}
}

func ptr(s string) *string { return &s }

func TestIntegrationEmailChannelAndSMTPSettings(t *testing.T) {
	pool, _ := testutil.Pool(t)
	ctx := context.Background()
	svc := newTestService(t, pool, nil)
	p := testPrincipal(t, pool)
	f := newFakeSMTP(t, nil, false, "mailer", "pw-123")

	v, err := svc.SMTPSettings(ctx)
	if err != nil || v.Configured {
		t.Fatalf("initial: %+v %v", v, err)
	}
	ch, err := svc.CreateChannel(ctx, p, ChannelInput{Name: "ops", Kind: KindEmail, Enabled: true, To: []string{"ops@example.com"}}, auth.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if res, _ := svc.TestChannel(ctx, p, ch.ID, auth.RequestMeta{}); res.Delivered || res.Error == "" {
		t.Fatalf("test without SMTP: %+v", res)
	}
	v, err = svc.UpdateSMTPSettings(ctx, p, SMTPInput{Config: SMTPConfig{Host: "127.0.0.1", Port: f.port(), Username: "mailer",
		From: "dbr2@example.com", TLS: TLSNone}, Password: ptr("pw-123")}, auth.RequestMeta{})
	if err != nil || !v.PasswordSet || !v.Configured {
		t.Fatalf("update: %+v %v", v, err)
	}
	// Omitting the password keeps it.
	if v, err = svc.UpdateSMTPSettings(ctx, p, SMTPInput{Config: v.Config}, auth.RequestMeta{}); err != nil || !v.PasswordSet {
		t.Fatalf("keep: %+v %v", v, err)
	}
	var raw []byte
	if err := pool.QueryRow(ctx, `SELECT value::text FROM platform_settings WHERE key = 'smtp'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "pw-123") {
		t.Fatal("password stored in clear")
	}
	insertOutbox(t, pool, "backup.failed", "critical")
	insertOutbox(t, pool, "restore.succeeded", "info") // below the default warning threshold
	if err := svc.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	msgs := f.messages()
	if len(msgs) != 1 || msgs[0].to[0] != "ops@example.com" {
		t.Fatalf("messages: %+v", msgs)
	}
	subj, _, _ := parseMail(t, msgs[0].data)
	if subj != "[DBR²] critical: backup.failed happened" {
		t.Fatalf("subject %q", subj)
	}
	if n := count(t, pool, `SELECT count(*) FROM audit_events WHERE event_type = $1`, audit.SMTPSettingsUpdated); n != 2 {
		t.Fatalf("%d smtp audit events", n)
	}
	// Channel name conflict and delete.
	if _, err := svc.CreateChannel(ctx, p, ChannelInput{Name: "ops", Kind: KindEmail, To: []string{"x@example.com"}}, auth.RequestMeta{}); err == nil {
		t.Fatal("duplicate name accepted")
	}
	if err := svc.DeleteChannel(ctx, p, ch.ID, auth.RequestMeta{}); err != nil {
		t.Fatal(err)
	}
	if n := count(t, pool, `SELECT count(*) FROM notification_deliveries`); n != 0 {
		t.Fatalf("%d deliveries left after delete", n)
	}
}

func TestIntegrationAgentPresence(t *testing.T) {
	pool, _ := testutil.Pool(t)
	ctx := context.Background()
	presence := &fakePresence{connected: map[string]bool{}}
	svc := newTestService(t, pool, presence)
	other := newTestService(t, pool, presence) // a second instance

	var id uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO agents (org_id, hostname, status, agent_version, protocol_version, last_seen_at, status_changed_at)
		VALUES ($1, 'host-a', 'active', '1.0.0', '1', now() - interval '20 minutes', now() - interval '1 day') RETURNING id`, orgID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	// Recently seen agent: no alert.
	if _, err := pool.Exec(ctx, `INSERT INTO agents (org_id, hostname, status, agent_version, protocol_version, last_seen_at)
		VALUES ($1, 'host-b', 'active', '1.0.0', '1', now() - interval '1 minute')`, orgID); err != nil {
		t.Fatal(err)
	}
	// Connected locally although last_seen is old: no alert.
	var connectedID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO agents (org_id, hostname, status, agent_version, protocol_version, last_seen_at, status_changed_at)
		VALUES ($1, 'host-c', 'active', '1.0.0', '1', now() - interval '1 hour', now() - interval '1 day') RETURNING id`, orgID).Scan(&connectedID); err != nil {
		t.Fatal(err)
	}
	presence.connected[connectedID.String()] = true

	var wg sync.WaitGroup
	for _, s := range []*Service{svc, other, svc, other} {
		wg.Go(func() {
			if err := s.CheckAgents(ctx); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if n := count(t, pool, `SELECT count(*) FROM notification_outbox WHERE event_type = 'agent.offline'`); n != 1 {
		t.Fatalf("%d agent.offline alerts, want 1", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM notification_outbox WHERE event_type = 'agent.offline' AND target_id = $1 AND severity = 'warning'`, id.String()); n != 1 {
		t.Fatal("offline alert has the wrong target or severity")
	}
	if n := count(t, pool, `SELECT count(*) FROM audit_events WHERE event_type = 'agent.offline' AND actor_kind = 'system'`); n != 1 {
		t.Fatalf("%d agent.offline audit events", n)
	}
	// Still offline: no repeat.
	if err := svc.CheckAgents(ctx); err != nil {
		t.Fatal(err)
	}
	if n := count(t, pool, `SELECT count(*) FROM notification_outbox WHERE event_type = 'agent.offline'`); n != 1 {
		t.Fatalf("repeated alert: %d", n)
	}
	// Back (heartbeat seen via another instance).
	if _, err := pool.Exec(ctx, `UPDATE agents SET last_seen_at = now() WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if err := other.CheckAgents(ctx); err != nil {
		t.Fatal(err)
	}
	if n := count(t, pool, `SELECT count(*) FROM notification_outbox WHERE event_type = 'agent.online' AND severity = 'info' AND target_id = $1`, id.String()); n != 1 {
		t.Fatalf("%d agent.online alerts", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM agents WHERE offline_alerted_at IS NOT NULL`); n != 0 {
		t.Fatal("offline_alerted_at not cleared")
	}
	// A second outage alerts again.
	if _, err := pool.Exec(ctx, `UPDATE agents SET last_seen_at = now() - interval '11 minutes' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if err := svc.CheckAgents(ctx); err != nil {
		t.Fatal(err)
	}
	if n := count(t, pool, `SELECT count(*) FROM notification_outbox WHERE event_type = 'agent.offline'`); n != 2 {
		t.Fatalf("%d agent.offline alerts after a second outage, want 2", n)
	}
}
