// SPDX-License-Identifier: Apache-2.0

package reposerver

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testToken = "0123456789abcdef0123456789abcdef-token"

type fakeBackend struct {
	initialized bool
	users       map[string]string
	grants      map[string]string
	calls       []string
	err         error
}

func (f *fakeBackend) Status(context.Context) Status {
	return Status{RepositoryID: "r1", Initialized: f.initialized, KopiaAddress: "0.0.0.0:51515", KopiaVersion: "0.23.1"}
}

func (f *fakeBackend) Initialize(_ context.Context, pw, sp string) (Status, error) {
	f.calls = append(f.calls, "init:"+sp)
	if f.err != nil {
		return Status{}, f.err
	}
	if f.initialized {
		return Status{}, ErrAlreadyInitialized
	}
	f.initialized = true
	return Status{RepositoryID: "r1", Initialized: true, Splitter: sp}, nil
}

func (f *fakeBackend) PutUser(_ context.Context, u, pw string) error {
	f.calls = append(f.calls, "put:"+u)
	if f.err != nil {
		return f.err
	}
	f.users[u] = pw
	return nil
}

func (f *fakeBackend) DeleteUser(_ context.Context, u string) error {
	f.calls = append(f.calls, "del:"+u)
	if _, ok := f.users[u]; !ok {
		return ErrNotFound
	}
	delete(f.users, u)
	return nil
}

func (f *fakeBackend) AddReadGrant(_ context.Context, u, su, sh string) (string, error) {
	f.calls = append(f.calls, "grant:"+u+">"+su+"@"+sh)
	id := "0123456789abcdef0123456789abcdef"
	f.grants[id] = u
	return id, f.err
}

func (f *fakeBackend) DeleteReadGrant(_ context.Context, id string) error {
	f.calls = append(f.calls, "revoke:"+id)
	if _, ok := f.grants[id]; !ok {
		return ErrNotFound
	}
	delete(f.grants, id)
	return nil
}

func (f *fakeBackend) RepositoryPassword(context.Context) (string, error) {
	f.calls = append(f.calls, "password")
	if !f.initialized {
		return "", ErrNotInitialized
	}
	return "repository-password-0123456789abcdef0123", nil // gitleaks:allow (test fixture)
}

func (f *fakeBackend) ExportState(context.Context) ([]byte, error) {
	f.calls = append(f.calls, "export")
	if !f.initialized {
		return nil, ErrNotInitialized
	}
	return []byte("TAR-BYTES"), nil
}

func (f *fakeBackend) ImportState(_ context.Context, b []byte) (Status, error) {
	f.calls = append(f.calls, "import:"+string(b))
	if f.err != nil {
		return Status{}, f.err
	}
	if f.initialized {
		return Status{}, ErrAlreadyInitialized
	}
	f.initialized = true
	return Status{RepositoryID: "r1", Initialized: true}, nil
}

func newTestAPI() (*fakeBackend, http.Handler) {
	f := &fakeBackend{users: map[string]string{}, grants: map[string]string{}}
	a := &API{Backend: f, Token: testToken, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	return f, a.Handler()
}

func do(t *testing.T, h http.Handler, method, path, body, token string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var m map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatalf("%s %s: non-JSON body %q", method, path, rec.Body.String())
		}
	}
	return rec, m
}

func TestAuth(t *testing.T) {
	f, h := newTestAPI()
	for _, tok := range []string{"", "wrong", testToken + "x", strings.ToUpper(testToken)} {
		rec, m := do(t, h, "GET", "/v1/status", "", tok)
		if rec.Code != 401 || m["code"] != "unauthorized" || rec.Header().Get("Content-Type") != "application/problem+json" {
			t.Fatalf("token %q: got %d %v", tok, rec.Code, m)
		}
	}
	// Unauthenticated requests never reach the backend, even for unknown paths.
	rec, _ := do(t, h, "POST", "/v1/initialize", `{"password":"`+strings.Repeat("x", 40)+`"}`, "")
	if rec.Code != 401 || len(f.calls) != 0 {
		t.Fatalf("unauthenticated initialize: %d calls=%v", rec.Code, f.calls)
	}
	if rec, _ := do(t, h, "GET", "/nope", "", ""); rec.Code != 401 {
		t.Fatalf("unknown path unauthenticated: %d", rec.Code)
	}
	if rec, m := do(t, h, "GET", "/nope", "", testToken); rec.Code != 404 || m["code"] != "not_found" {
		t.Fatalf("unknown path: %d %v", rec.Code, m)
	}
	// A disabled token never authenticates.
	a := &API{Backend: f, Token: "", Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	req := httptest.NewRequest("GET", "/v1/status", nil)
	req.Header.Set("Authorization", "Bearer ")
	rr := httptest.NewRecorder()
	a.Handler().ServeHTTP(rr, req)
	if rr.Code != 401 {
		t.Fatalf("empty token: %d", rr.Code)
	}
}

func TestStatusContract(t *testing.T) {
	_, h := newTestAPI()
	rec, m := do(t, h, "GET", "/v1/status", "", testToken)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status: %d", rec.Code)
	}
	for _, k := range []string{"repository_id", "initialized", "server_running", "kopia_address", "cert_sha256",
		"kopia_version", "splitter", "storage_path", "storage_healthy", "storage_error",
		"storage_total_bytes", "storage_free_bytes", "storage_used_bytes"} {
		if _, ok := m[k]; !ok {
			t.Errorf("status lacks %q", k)
		}
	}
	if len(m) != 13 {
		t.Errorf("status has %d fields, want 13: %v", len(m), m)
	}
}

func TestInitialize(t *testing.T) {
	f, h := newTestAPI()
	pw := strings.Repeat("p", 32)
	for _, body := range []string{
		`{"password":"short"}`,
		`{"password":"` + strings.Repeat("p", 31) + `"}`,
		`{"password":"` + pw + `","splitter":"NOPE"}`,
		`{"password":"` + pw + `\n"}`,
		`not json`,
	} {
		if rec, m := do(t, h, "POST", "/v1/initialize", body, testToken); rec.Code != 400 || m["code"] != "invalid_request" {
			t.Fatalf("%s: %d %v", body, rec.Code, m)
		}
	}
	if len(f.calls) != 0 {
		t.Fatalf("backend called for invalid requests: %v", f.calls)
	}
	rec, m := do(t, h, "POST", "/v1/initialize", `{"password":"`+pw+`","splitter":"DYNAMIC-1M-BUZHASH"}`, testToken)
	if rec.Code != 200 || m["initialized"] != true || m["splitter"] != "DYNAMIC-1M-BUZHASH" {
		t.Fatalf("initialize: %d %v", rec.Code, m)
	}
	rec, m = do(t, h, "POST", "/v1/initialize", `{"password":"`+pw+`"}`, testToken)
	if rec.Code != 409 || m["code"] != "already_initialized" || m["status"] != float64(409) || m["title"] == "" {
		t.Fatalf("second initialize: %d %v", rec.Code, m)
	}
	f.initialized, f.err = false, ErrStorageNotReady
	if rec, m := do(t, h, "POST", "/v1/initialize", `{"password":"`+pw+`"}`, testToken); rec.Code != 422 || m["code"] != "storage_not_ready" {
		t.Fatalf("storage not ready: %d %v", rec.Code, m)
	}
	f.err = &KopiaError{Command: "repository create", Err: io.EOF}
	if rec, m := do(t, h, "POST", "/v1/initialize", `{"password":"`+pw+`"}`, testToken); rec.Code != 500 || m["code"] != "kopia_error" {
		t.Fatalf("kopia error: %d %v", rec.Code, m)
	}
}

func TestUsers(t *testing.T) {
	f, h := newTestAPI()
	for _, name := range []string{"agent", "Agent@a1", "agent@", "@a1", "-a@b", "a@b@c", "a%20b@c", "*@*"} {
		if rec, _ := do(t, h, "PUT", "/v1/users/"+name, `{"password":"0123456789abcdef"}`, testToken); rec.Code != 400 && rec.Code != 404 { // gitleaks:allow (test fixture)
			t.Fatalf("PUT %q: %d", name, rec.Code)
		}
	}
	if rec, _ := do(t, h, "PUT", "/v1/users/agent@a1", `{"password":"short"}`, testToken); rec.Code != 400 {
		t.Fatalf("short password: %d", rec.Code)
	}
	if len(f.calls) != 0 {
		t.Fatalf("backend called: %v", f.calls)
	}
	if rec, _ := do(t, h, "PUT", "/v1/users/agent@0f1e2d3c-aaaa-bbbb-cccc-0123456789ab", `{"password":"0123456789abcdef"}`, testToken); rec.Code != 204 || rec.Body.Len() != 0 { // gitleaks:allow (test fixture)
		t.Fatalf("PUT: %d", rec.Code)
	}
	if rec, _ := do(t, h, "PUT", "/v1/users/maint@dbr2", `{"password":"0123456789abcdef"}`, testToken); rec.Code != 204 { // gitleaks:allow (test fixture)
		t.Fatalf("PUT maint: %d", rec.Code)
	}
	if rec, _ := do(t, h, "DELETE", "/v1/users/maint@dbr2", "", testToken); rec.Code != 204 {
		t.Fatalf("DELETE: %d", rec.Code)
	}
	if rec, m := do(t, h, "DELETE", "/v1/users/maint@dbr2", "", testToken); rec.Code != 404 || m["code"] != "not_found" {
		t.Fatalf("DELETE absent: %d %v", rec.Code, m)
	}
	f.err = ErrNotInitialized
	if rec, m := do(t, h, "PUT", "/v1/users/agent@a1", `{"password":"0123456789abcdef"}`, testToken); rec.Code != 409 || m["code"] != "not_initialized" { // gitleaks:allow (test fixture)
		t.Fatalf("not initialized: %d %v", rec.Code, m)
	}
}

func TestReadGrants(t *testing.T) {
	f, h := newTestAPI()
	for _, body := range []string{
		`{"user":"agent","source_user":"agent","source_host":"a1"}`,
		`{"user":"agent@b2","source_user":"OWN_USER","source_host":"a1"}`,
		`{"user":"agent@b2","source_user":"agent","source_host":""}`,
		`{"user":"agent@b2","source_user":"agent","source_host":"*"}`,
		`{"user":"agent@a1","source_user":"agent","source_host":"a1"}`,
	} {
		if rec, _ := do(t, h, "POST", "/v1/acl/read-grants", body, testToken); rec.Code != 400 {
			t.Fatalf("%s: %d", body, rec.Code)
		}
	}
	rec, m := do(t, h, "POST", "/v1/acl/read-grants", `{"user":"agent@b2","source_user":"agent","source_host":"a1"}`, testToken)
	if rec.Code != 201 || m["id"] != "0123456789abcdef0123456789abcdef" || len(m) != 1 {
		t.Fatalf("grant: %d %v", rec.Code, m)
	}
	if f.calls[0] != "grant:agent@b2>agent@a1" {
		t.Fatalf("calls: %v", f.calls)
	}
	if rec, _ := do(t, h, "DELETE", "/v1/acl/read-grants/0123456789abcdef0123456789abcdef", "", testToken); rec.Code != 204 {
		t.Fatalf("revoke: %d", rec.Code)
	}
	if rec, _ := do(t, h, "DELETE", "/v1/acl/read-grants/0123456789abcdef0123456789abcdef", "", testToken); rec.Code != 404 {
		t.Fatalf("revoke again: %d", rec.Code)
	}
	if rec, _ := do(t, h, "DELETE", "/v1/acl/read-grants/..%2f", "", testToken); rec.Code != 404 {
		t.Fatalf("bad id: %d", rec.Code)
	}
}

func TestRepositoryPasswordAndState(t *testing.T) {
	f, h := newTestAPI()
	if rec, m := do(t, h, "GET", "/v1/repository-password", "", testToken); rec.Code != 409 || m["code"] != "not_initialized" {
		t.Fatalf("password before init: %d %v", rec.Code, m)
	}
	if rec, m := do(t, h, "GET", "/v1/state-export", "", testToken); rec.Code != 409 || m["code"] != "not_initialized" {
		t.Fatalf("export before init: %d %v", rec.Code, m)
	}
	for _, p := range []string{"/v1/repository-password", "/v1/state-export"} {
		if rec, _ := do(t, h, "GET", p, "", "wrong"); rec.Code != 401 {
			t.Fatalf("%s unauthenticated: %d", p, rec.Code)
		}
	}
	if rec, _ := do(t, h, "POST", "/v1/state-import", "x", ""); rec.Code != 401 {
		t.Fatalf("import unauthenticated: %d", rec.Code)
	}
	if rec, m := do(t, h, "POST", "/v1/state-import", "", testToken); rec.Code != 400 || m["code"] != "invalid_request" {
		t.Fatalf("empty import: %d %v", rec.Code, m)
	}
	if rec, m := do(t, h, "POST", "/v1/state-import", strings.Repeat("x", MaxStateArchive+1), testToken); rec.Code != 413 || m["code"] != "too_large" {
		t.Fatalf("large import: %d %v", rec.Code, m)
	}
	for err, want := range map[error]struct {
		code int
		name string
	}{ErrStatePresent: {409, "state_present"}, ErrInvalidState: {400, "invalid_state"}, ErrStorageNotReady: {422, "storage_not_ready"},
		ErrInvalidPassword: {422, "invalid_password"}} {
		f.err = err
		if rec, m := do(t, h, "POST", "/v1/state-import", "archive", testToken); rec.Code != want.code || m["code"] != want.name {
			t.Fatalf("%v: %d %v", err, rec.Code, m)
		}
	}
	f.err = nil
	rec, m := do(t, h, "POST", "/v1/state-import", "archive", testToken)
	if rec.Code != 200 || m["initialized"] != true || f.calls[len(f.calls)-1] != "import:archive" {
		t.Fatalf("import: %d %v", rec.Code, m)
	}
	if rec, m := do(t, h, "POST", "/v1/state-import", "archive", testToken); rec.Code != 409 || m["code"] != "already_initialized" {
		t.Fatalf("import again: %d %v", rec.Code, m)
	}
	rec, m = do(t, h, "GET", "/v1/repository-password", "", testToken)
	if rec.Code != 200 || m["password"] != "repository-password-0123456789abcdef0123" || len(m) != 1 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("password: %d %v", rec.Code, m)
	}
	req := httptest.NewRequest("GET", "/v1/state-export", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 || rr.Header().Get("Content-Type") != "application/x-tar" || rr.Body.String() != "TAR-BYTES" ||
		rr.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("export: %d %q %v", rr.Code, rr.Body.String(), rr.Header())
	}
}
