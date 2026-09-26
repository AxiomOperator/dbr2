// SPDX-License-Identifier: Apache-2.0

//go:build integration

package api_test

import (
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/AxiomOperator/dbr2/db"
	"github.com/AxiomOperator/dbr2/internal/api"
	"github.com/AxiomOperator/dbr2/internal/audit"
	"github.com/AxiomOperator/dbr2/internal/auth"
	"github.com/AxiomOperator/dbr2/internal/notify"
	"github.com/AxiomOperator/dbr2/internal/store"
	"github.com/AxiomOperator/dbr2/internal/testutil"
)

func TestNotificationChannelsAPI(t *testing.T) {
	e := newEnv(t, 100)
	log := testutil.Logger()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, _ := auth.NewSecretBox(key)
	n := notify.New(e.pool, box, audit.NewRecorder(store.New(e.pool), log), nil, log,
		notify.Options{OrgID: uuid.MustParse(db.DefaultOrgID), PublicURL: e.srv.URL})
	e.srv.Config.Handler = api.NewHandler(&api.Deps{Auth: e.svc, Notify: n, Events: e.bus, Log: log, WebLoginPath: "/login",
		PublicURL: e.srv.URL, AllowedOrigins: []string{e.srv.URL}})

	var hits atomic.Int32
	var sig atomic.Value
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		sig.Store(r.Header.Get("X-DBR2-Signature"))
	}))
	defer sink.Close()

	admin := e.browser()
	admin.expect(200, "POST", "/api/v1/auth/login", map[string]any{"username": e.adminU, "password": e.adminP})
	e.browser().expect(401, "GET", "/api/v1/notification-channels", nil)

	const secret = "a-very-secret-signing-key"
	r := admin.expect(201, "POST", "/api/v1/notification-channels", map[string]any{"name": "hook", "kind": "webhook",
		"config": map[string]any{"url": sink.URL}, "secret": secret, "events": []string{"backup.*"}})
	if r.body["secret_set"] != true || r.body["enabled"] != true || r.body["min_severity"] != "warning" || strings.Contains(string(r.raw), secret) {
		t.Fatalf("create: %s", r.raw)
	}
	id := r.body["id"].(string)
	admin.expect(409, "POST", "/api/v1/notification-channels", map[string]any{"name": "hook", "kind": "webhook", "config": map[string]any{"url": sink.URL}})
	if r := admin.expect(400, "POST", "/api/v1/notification-channels", map[string]any{"name": "x", "kind": "webhook",
		"config": map[string]any{"url": "http://hooks.example.com/"}}); code(r) != api.CodeValidation {
		t.Fatalf("plain http: %s", r.raw)
	}
	if r := admin.expect(200, "GET", "/api/v1/notification-channels", nil); len(r.body["items"].([]any)) != 1 {
		t.Fatalf("list: %s", r.raw)
	}

	r = admin.expect(200, "POST", "/api/v1/notification-channels/"+id+"/test", nil)
	if r.body["delivered"] != true || hits.Load() != 1 || !strings.HasPrefix(sig.Load().(string), "sha256=") {
		t.Fatalf("test: %s hits=%d", r.raw, hits.Load())
	}

	// PUT without secret keeps it; empty secret removes it.
	r = admin.expect(200, "PUT", "/api/v1/notification-channels/"+id, map[string]any{"name": "hook2", "enabled": false,
		"config": map[string]any{"url": sink.URL}, "min_severity": "critical"})
	if r.body["name"] != "hook2" || r.body["enabled"] != false || r.body["secret_set"] != true || r.body["min_severity"] != "critical" {
		t.Fatalf("update: %s", r.raw)
	}
	r = admin.expect(200, "PUT", "/api/v1/notification-channels/"+id, map[string]any{"name": "hook2", "enabled": true,
		"config": map[string]any{"url": sink.URL}, "secret": ""})
	if r.body["secret_set"] != false {
		t.Fatalf("secret not removed: %s", r.raw)
	}
	admin.expect(200, "GET", "/api/v1/notification-channels/"+id+"/deliveries", nil)

	// SMTP settings: password write-only.
	if r := admin.expect(200, "GET", "/api/v1/settings/smtp", nil); r.body["configured"] != false || r.body["password_set"] != false {
		t.Fatalf("smtp initial: %s", r.raw)
	}
	admin.expect(400, "PUT", "/api/v1/settings/smtp", map[string]any{"host": "smtp.example.com", "port": 25, "from": "dbr2@example.com", "tls": "none"})
	r = admin.expect(200, "PUT", "/api/v1/settings/smtp", map[string]any{"host": "smtp.example.com", "port": 587, "username": "mailer",
		"from": "DBR2 <dbr2@example.com>", "tls": "starttls", "password": "smtp-pass-123"})
	if r.body["password_set"] != true || r.body["configured"] != true || strings.Contains(string(r.raw), "smtp-pass-123") {
		t.Fatalf("smtp update: %s", r.raw)
	}
	if r := admin.expect(200, "GET", "/api/v1/settings/smtp", nil); strings.Contains(string(r.raw), "smtp-pass-123") || r.body["host"] != "smtp.example.com" {
		t.Fatalf("smtp get: %s", r.raw)
	}

	admin.expect(204, "DELETE", "/api/v1/notification-channels/"+id, nil)
	admin.expect(404, "GET", "/api/v1/notification-channels/"+id, nil)

	types := auditTypes(t, e.pool)
	for _, typ := range []string{audit.NotificationChannelCreated, audit.NotificationChannelUpdated, audit.NotificationChannelTested,
		audit.NotificationChannelDeleted, audit.SMTPSettingsUpdated} {
		if types[typ] == 0 {
			t.Errorf("no %s audit event", typ)
		}
	}
	if types[audit.NotificationChannelUpdated] != 2 {
		t.Errorf("%d update events", types[audit.NotificationChannelUpdated])
	}
}
