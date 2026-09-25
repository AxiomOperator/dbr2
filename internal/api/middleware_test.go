// SPDX-License-Identifier: Apache-2.0

package api

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"

	"github.com/AxiomOperator/dbr2/internal/auth"
)

func TestSafeReturnTo(t *testing.T) {
	cases := map[string]string{
		"":                       "/",
		"/audit?x=1":             "/audit?x=1",
		"//evil.example":         "/",
		"/\\evil.example":        "/",
		"https://evil.example/":  "/",
		"javascript:alert(1)":    "/",
		"/ok\r\nSet-Cookie: x=y": "/",
		"relative":               "/",
	}
	for in, want := range cases {
		if got := SafeReturnTo(in); got != want {
			t.Errorf("SafeReturnTo(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestProtectDocs(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	d := &Deps{WebLoginPath: "/login"}
	h := protectDocs(d, DocsPath, OpenAPIPath)(ok)

	check := func(path, accept string, want int, loc string) {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, path, nil)
		if accept != "" {
			r.Header.Set("Accept", accept)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want || (loc != "" && w.Header().Get("Location") != loc) {
			t.Errorf("%s (%s): got %d %q, want %d %q", path, accept, w.Code, w.Header().Get("Location"), want, loc)
		}
	}
	check("/api/openapi.json", "", 401, "")
	check("/api/docs", "text/html", 302, "/login?return_to=%2Fapi%2Fdocs")
	check("/api/v1/version", "", 200, "") // not a docs path

	d.DocsPublic = true
	check("/api/openapi.yaml", "", 200, "")
}

func TestSameOrigin(t *testing.T) {
	allowed := []string{"https://dbr2.example.lan"}
	for _, c := range []struct {
		site, origin string
		want         bool
	}{
		{"same-origin", "", true},
		{"cross-site", "https://dbr2.example.lan", false},
		{"", "https://dbr2.example.lan", true},
		{"", "https://evil.example", false},
		{"", "", false},
	} {
		r := httptest.NewRequest(http.MethodPost, "/x", nil)
		if c.site != "" {
			r.Header.Set("Sec-Fetch-Site", c.site)
		}
		if c.origin != "" {
			r.Header.Set("Origin", c.origin)
		}
		hc := humatest.NewContext(nil, r, httptest.NewRecorder())
		if res := sameOrigin(hc, allowed); res != c.want {
			t.Errorf("site=%q origin=%q: got %v want %v", c.site, c.origin, res, c.want)
		}
	}
}

func TestMapErrorCodes(t *testing.T) {
	d := &Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{auth.ErrInvalidCredentials, 401, CodeInvalidCredentials},
		{auth.ErrTOTPRequired, 401, CodeTOTPRequired},
		{&auth.LockedError{RetryAfter: 90 * time.Second}, 423, CodeAccountLocked},
		{&auth.RateLimitedError{RetryAfter: time.Second}, 429, CodeRateLimited},
		{auth.ErrWeakPassword, 400, CodeWeakPassword},
		{errors.New("boom: secret db detail"), 500, CodeInternal},
	}
	for _, c := range cases {
		err := d.fail(t.Context(), c.err)
		var p *Problem
		if !errors.As(err, &p) {
			t.Fatalf("%v: not a Problem: %T", c.err, err)
		}
		if p.Status != c.status || p.Code != c.code {
			t.Errorf("%v: got %d %s", c.err, p.Status, p.Code)
		}
		if strings.Contains(p.Detail, "secret db detail") {
			t.Error("internal error detail leaked to client")
		}
		var he huma.HeadersError
		if c.status == 423 && (!errors.As(err, &he) || he.GetHeaders().Get("Retry-After") != "90") {
			t.Errorf("locked error missing Retry-After")
		}
	}
}
