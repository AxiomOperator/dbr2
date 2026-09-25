// SPDX-License-Identifier: Apache-2.0

//go:build integration

package api_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"github.com/AxiomOperator/dbr2/db"
	"github.com/AxiomOperator/dbr2/internal/api"
	"github.com/AxiomOperator/dbr2/internal/audit"
	"github.com/AxiomOperator/dbr2/internal/auth"
	"github.com/AxiomOperator/dbr2/internal/store"
	"github.com/AxiomOperator/dbr2/internal/testutil"
)

type env struct {
	t      *testing.T
	pool   *pgxpool.Pool
	svc    *auth.Service
	srv    *httptest.Server
	idp    *testutil.FakeOIDC
	adminU string
	adminP string
}

func newEnv(t *testing.T, rateLimit int) *env {
	pool, _ := testutil.Pool(t)
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	log := testutil.Logger()
	svc, err := auth.NewService(pool, audit.NewRecorder(store.New(pool), log), log, auth.Options{
		OrgID: uuid.MustParse(db.DefaultOrgID), MasterAdminUsername: "dbr2-admin",
		SessionTTL: time.Hour, SessionIdle: 30 * time.Minute, SecretKey: key, LoginRateLimit: rateLimit,
	})
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, pool: pool, svc: svc}
	created, err := svc.EnsureMasterAdmin(context.Background(), func(u, p string) error { e.adminU, e.adminP = u, p; return nil })
	if err != nil || !created || e.adminP == "" {
		t.Fatalf("bootstrap: created=%v err=%v", created, err)
	}
	if again, err := svc.EnsureMasterAdmin(context.Background(), func(string, string) error { t.Fatal("re-bootstrapped"); return nil }); again || err != nil {
		t.Fatalf("second bootstrap: %v %v", again, err)
	}
	e.idp = testutil.NewFakeOIDC(t, "dbr2-client")
	e.srv = httptest.NewServer(nil)
	t.Cleanup(e.srv.Close)
	svc.RegisterOIDC(auth.OIDCConfig{ID: "entra", DisplayName: "Microsoft Entra ID", Issuer: e.idp.URL,
		ClientID: "dbr2-client", ClientSecret: "s3cret", RedirectURL: e.srv.URL + "/api/v1/auth/oidc/entra/callback"})
	e.srv.Config.Handler = api.NewHandler(&api.Deps{
		Auth: svc, Log: log, WebLoginPath: "/login", PublicURL: e.srv.URL, AllowedOrigins: []string{e.srv.URL},
		Ready: []api.ReadyCheck{{Name: "postgres", Critical: true, Check: pool.Ping}},
	})
	return e
}

// client is a browser-like client (cookie jar, no redirect following).
type client struct {
	e    *env
	http *http.Client
	// sameOrigin adds Sec-Fetch-Site: same-origin to unsafe requests.
	sameOrigin bool
	bearer     string
}

func (e *env) browser() *client {
	jar, _ := cookiejar.New(nil)
	return &client{e: e, sameOrigin: true, http: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}}
}

type resp struct {
	status int
	header http.Header
	body   map[string]any
	raw    []byte
}

func (c *client) do(method, path string, body any) resp {
	c.e.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	u := path
	if !strings.HasPrefix(path, "http") {
		u = c.e.srv.URL + path
	}
	req, _ := http.NewRequest(method, u, r)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.sameOrigin && method != http.MethodGet {
		req.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	res, err := c.http.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := resp{status: res.StatusCode, header: res.Header, raw: raw}
	_ = json.Unmarshal(raw, &out.body)
	return out
}

func (c *client) expect(want int, method, path string, body any) resp {
	c.e.t.Helper()
	r := c.do(method, path, body)
	if r.status != want {
		c.e.t.Fatalf("%s %s: status %d, want %d; body %s", method, path, r.status, want, r.raw)
	}
	return r
}

func code(r resp) string { s, _ := r.body["code"].(string); return s }

func totpCode(t *testing.T, secret string, at time.Time) string {
	c, err := totp.GenerateCodeCustom(secret, at, totp.ValidateOpts{Period: 30, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func auditTypes(t *testing.T, pool *pgxpool.Pool) map[string]int {
	rows, err := pool.Query(context.Background(), "SELECT event_type FROM audit_events")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		out[s]++
	}
	return out
}

func TestMasterAdminLifecycle(t *testing.T) {
	e := newEnv(t, 100)
	anon := e.browser()
	anon.expect(200, "GET", "/api/v1/version", nil)
	anon.expect(200, "GET", "/api/v1/health/ready", nil)
	anon.expect(401, "GET", "/api/v1/auth/me", nil)
	anon.expect(401, "GET", "/api/openapi.json", nil)
	prov := anon.expect(200, "GET", "/api/v1/auth/providers", nil)
	if prov.body["master_admin"] != true || len(prov.body["oidc"].([]any)) != 1 {
		t.Fatalf("providers: %s", prov.raw)
	}

	// Lockout after 5 failures (423 + Retry-After), even for the right password.
	for i := 0; i < 5; i++ {
		if r := anon.expect(401, "POST", "/api/v1/auth/login", map[string]any{"username": "dbr2-admin", "password": "wrong-password-123"}); code(r) != api.CodeInvalidCredentials {
			t.Fatalf("attempt %d: %s", i, r.raw)
		}
	}
	r := anon.expect(423, "POST", "/api/v1/auth/login", map[string]any{"username": "dbr2-admin", "password": e.adminP})
	if code(r) != api.CodeAccountLocked || r.header.Get("Retry-After") == "" {
		t.Fatalf("locked: %s %v", r.raw, r.header)
	}
	if _, err := e.pool.Exec(context.Background(), "UPDATE local_credentials SET locked_until = NULL, failed_attempts = 0"); err != nil {
		t.Fatal(err)
	}
	anon.expect(401, "POST", "/api/v1/auth/login", map[string]any{"username": "nobody", "password": e.adminP})

	// Successful login sets an HttpOnly session cookie.
	admin := e.browser()
	r = admin.expect(200, "POST", "/api/v1/auth/login", map[string]any{"username": "DBR2-ADMIN", "password": e.adminP})
	if sc := r.header.Get("Set-Cookie"); !strings.Contains(sc, "dbr2_session=dbr2s_") || !strings.Contains(sc, "HttpOnly") || !strings.Contains(sc, "SameSite=Lax") {
		t.Fatalf("session cookie: %q", sc)
	}
	me := admin.expect(200, "GET", "/api/v1/auth/me", nil)
	if me.body["kind"] != "master_admin" || !strings.Contains(string(me.raw), "restore.production") {
		t.Fatalf("me: %s", me.raw)
	}
	admin.expect(200, "GET", "/api/openapi.json", nil)
	if r := admin.expect(200, "GET", "/api/docs", nil); !bytes.Contains(r.raw, []byte("swagger")) {
		t.Fatal("docs page does not look like Swagger UI")
	}

	// CSRF: cookie-authenticated unsafe request without same-origin signals.
	admin.sameOrigin = false
	if r := admin.expect(403, "POST", "/api/v1/tokens", map[string]any{"name": "ci"}); code(r) != api.CodeCSRF {
		t.Fatalf("csrf: %s", r.raw)
	}
	admin.sameOrigin = true

	// API token (Bearer) — no CSRF needed, same permissions.
	tok := admin.expect(201, "POST", "/api/v1/tokens", map[string]any{"name": "ci", "expires_in_days": 1})
	pat := tok.body["token"].(string)
	api1 := &client{e: e, http: http.DefaultClient, bearer: pat}
	api1.expect(200, "GET", "/api/v1/audit-events?limit=5", nil)
	api1.expect(200, "GET", "/api/openapi.yaml", nil)
	(&client{e: e, http: http.DefaultClient, bearer: "dbr2pat_bogus"}).expect(401, "GET", "/api/v1/auth/me", nil)

	// Weak password rejected; strong accepted.
	if r := admin.expect(400, "POST", "/api/v1/auth/password", map[string]any{"current_password": e.adminP, "new_password": "short"}); code(r) != api.CodeWeakPassword {
		t.Fatalf("weak: %s", r.raw)
	}
	newPW := "a-much-longer-passphrase-2026"
	admin.expect(204, "POST", "/api/v1/auth/password", map[string]any{"current_password": e.adminP, "new_password": newPW})

	// TOTP enrollment, then login requires a code; replayed codes are rejected.
	enr := admin.expect(200, "POST", "/api/v1/auth/totp/enroll", nil)
	secret := enr.body["secret"].(string)
	if !strings.HasPrefix(enr.body["otpauth_url"].(string), "otpauth://totp/") {
		t.Fatalf("otpauth: %s", enr.raw)
	}
	now := time.Now()
	admin.expect(204, "POST", "/api/v1/auth/totp/confirm", map[string]any{"code": totpCode(t, secret, now)})
	admin.expect(204, "POST", "/api/v1/auth/logout", nil)
	admin.expect(401, "GET", "/api/v1/auth/me", nil)

	if r := admin.expect(401, "POST", "/api/v1/auth/login", map[string]any{"username": "dbr2-admin", "password": newPW}); code(r) != api.CodeTOTPRequired {
		t.Fatalf("totp required: %s", r.raw)
	}
	if r := admin.expect(401, "POST", "/api/v1/auth/login", map[string]any{"username": "dbr2-admin", "password": newPW, "totp_code": totpCode(t, secret, now)}); code(r) != api.CodeInvalidTOTP {
		t.Fatalf("replayed code accepted: %s", r.raw)
	}
	admin.expect(200, "POST", "/api/v1/auth/login", map[string]any{"username": "dbr2-admin", "password": newPW, "totp_code": totpCode(t, secret, now.Add(30*time.Second))})
	admin.expect(200, "GET", "/api/v1/auth/me", nil)

	// Audit log: expected events, append-only enforced by the database.
	types := auditTypes(t, e.pool)
	for _, want := range []string{audit.MasterAdminBootstrapped, audit.LoginFailed, audit.LoginLocked, audit.LoginSucceeded,
		audit.APITokenCreated, audit.PasswordChanged, audit.TOTPEnabled, audit.Logout} {
		if types[want] == 0 {
			t.Errorf("missing audit event %s (have %v)", want, types)
		}
	}
	for _, stmt := range []string{"UPDATE audit_events SET result = 'success'", "DELETE FROM audit_events", "TRUNCATE audit_events"} {
		if _, err := e.pool.Exec(context.Background(), stmt); err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Errorf("%s: expected append-only rejection, got %v", stmt, err)
		}
	}
	var notifications int
	_ = e.pool.QueryRow(context.Background(), "SELECT count(*) FROM notification_outbox WHERE event_type = 'auth.master_admin.login'").Scan(&notifications)
	if notifications < 2 {
		t.Errorf("master admin logins must enqueue notifications, got %d", notifications)
	}

	// Offline reset (server host) revokes every session.
	if err := e.svc.ResetMasterPassword(context.Background(), "reset-by-root-on-host-2026", true, "root@test"); err != nil {
		t.Fatal(err)
	}
	admin.expect(401, "GET", "/api/v1/auth/me", nil)
	admin.expect(200, "POST", "/api/v1/auth/login", map[string]any{"username": "dbr2-admin", "password": "reset-by-root-on-host-2026"})
}

func TestLoginRateLimit(t *testing.T) {
	e := newEnv(t, 2)
	c := e.browser()
	c.expect(401, "POST", "/api/v1/auth/login", map[string]any{"username": "x", "password": "y"})
	c.expect(401, "POST", "/api/v1/auth/login", map[string]any{"username": "x", "password": "y"})
	r := c.expect(429, "POST", "/api/v1/auth/login", map[string]any{"username": "dbr2-admin", "password": e.adminP})
	if code(r) != api.CodeRateLimited || r.header.Get("Retry-After") == "" {
		t.Fatalf("rate limit: %s", r.raw)
	}
}

func (e *env) oidcLogin(c *client, id testutil.Identity, returnTo string) resp {
	e.t.Helper()
	start := c.expect(302, "GET", "/api/v1/auth/oidc/entra/login?return_to="+url.QueryEscape(returnTo), nil)
	code, state := e.idp.Authorize(e.t, start.header.Get("Location"), id)
	return c.expect(302, "GET", "/api/v1/auth/oidc/entra/callback?code="+url.QueryEscape(code)+"&state="+url.QueryEscape(state), nil)
}

func TestEntraOIDCGroupMappingAndRBAC(t *testing.T) {
	e := newEnv(t, 100)
	admin := e.browser()
	admin.expect(200, "POST", "/api/v1/auth/login", map[string]any{"username": "dbr2-admin", "password": e.adminP})
	admin.expect(204, "POST", "/api/v1/oidc/group-mappings", map[string]any{"provider": "entra", "group_id": "g-auditors", "role": "auditor"})
	admin.expect(400, "POST", "/api/v1/oidc/group-mappings", map[string]any{"provider": "entra", "group_id": "g-x", "role": "superuser"})

	// A user in a mapped group signs in and gets the mapped role.
	aud := e.browser()
	r := e.oidcLogin(aud, testutil.Identity{Subject: "sub-alice", Name: "Alice Auditor", PreferredUsername: "alice@example.org",
		Email: "alice@example.org", Groups: []string{"g-auditors", "g-unrelated"}}, "/audit")
	if r.header.Get("Location") != "/audit" {
		t.Fatalf("callback redirect: %v", r.header)
	}
	me := aud.expect(200, "GET", "/api/v1/auth/me", nil)
	if me.body["kind"] != "oidc" || !strings.Contains(string(me.raw), `"roles":["auditor"]`) {
		t.Fatalf("oidc me: %s", me.raw)
	}
	aud.expect(200, "GET", "/api/v1/audit-events", nil)
	aud.expect(200, "GET", "/api/v1/users", nil)
	users := admin.expect(200, "GET", "/api/v1/users", nil)
	var aliceID string
	for _, u := range users.body["items"].([]any) {
		if m := u.(map[string]any); m["username"] == "alice@example.org" {
			aliceID = m["id"].(string)
		}
	}
	// RBAC: auditor cannot change roles (denial audited); admin can.
	if r := aud.expect(403, "PUT", "/api/v1/users/"+aliceID+"/roles", map[string]any{"roles": []string{"administrator"}, "reason": "escalate"}); code(r) != api.CodeForbidden {
		t.Fatalf("expected forbidden: %s", r.raw)
	}
	admin.expect(204, "PUT", "/api/v1/users/"+aliceID+"/roles", map[string]any{"roles": []string{"backup_administrator"}, "reason": "on-call rotation"})
	if !strings.Contains(string(aud.expect(200, "GET", "/api/v1/auth/me", nil).raw), "backup_administrator") {
		t.Fatal("manual role not effective")
	}
	// The master admin is immutable.
	var masterID string
	for _, u := range users.body["items"].([]any) {
		if m := u.(map[string]any); m["kind"] == "master_admin" {
			masterID = m["id"].(string)
		}
	}
	admin.expect(403, "PUT", "/api/v1/users/"+masterID+"/status", map[string]any{"disabled": true, "reason": "x"})

	// Disabling a user revokes their sessions.
	admin.expect(204, "PUT", "/api/v1/users/"+aliceID+"/status", map[string]any{"disabled": true, "reason": "left the team"})
	aud.expect(401, "GET", "/api/v1/auth/me", nil)

	// No mapped group and no manual role: denied, sent back to the login page.
	nobody := e.browser()
	r = e.oidcLogin(nobody, testutil.Identity{Subject: "sub-bob", Name: "Bob", PreferredUsername: "bob@example.org", Groups: []string{"g-other"}}, "/")
	if r.header.Get("Location") != "/login?error=no_roles" {
		t.Fatalf("no-access redirect: %v", r.header.Get("Location"))
	}
	nobody.expect(401, "GET", "/api/v1/auth/me", nil)

	// State must match the browser's cookie (login CSRF), and return_to must be same-origin.
	evil := e.browser()
	start := evil.expect(302, "GET", "/api/v1/auth/oidc/entra/login?return_to=//evil.example", nil)
	code, state := e.idp.Authorize(t, start.header.Get("Location"), testutil.Identity{Subject: "sub-alice2", Groups: []string{"g-auditors"}})
	other := e.browser() // different browser, no state cookie
	if r := other.expect(302, "GET", "/api/v1/auth/oidc/entra/callback?code="+code+"&state="+state, nil); r.header.Get("Location") != "/login?error=invalid_state" {
		t.Fatalf("state mismatch accepted: %v", r.header.Get("Location"))
	}
	r = e.oidcLogin(evil, testutil.Identity{Subject: "sub-carol", PreferredUsername: "carol@example.org", Groups: []string{"g-auditors"}}, "//evil.example")
	if r.header.Get("Location") != "/" {
		t.Fatalf("open redirect: %q", r.header.Get("Location"))
	}

	types := auditTypes(t, e.pool)
	for _, want := range []string{audit.OIDCLoginSucceeded, audit.OIDCLoginFailed, audit.AccessDenied, audit.UserRolesChanged,
		audit.UserDisabledChanged, audit.GroupMappingAdded} {
		if types[want] == 0 {
			t.Errorf("missing audit event %s (have %v)", want, types)
		}
	}
	// Role change audit carries before/after state.
	var before, after []byte
	if err := e.pool.QueryRow(context.Background(), "SELECT before_state, after_state FROM audit_events WHERE event_type = $1", audit.UserRolesChanged).Scan(&before, &after); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(before, []byte("auditor")) || !bytes.Contains(after, []byte("backup_administrator")) {
		t.Fatalf("before/after: %s -> %s", before, after)
	}
}
