// SPDX-License-Identifier: Apache-2.0

package obs

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestLoggerRedactsSecrets(t *testing.T) {
	var buf bytes.Buffer
	l := NewLogger(&buf, "debug", "json", "dbr2-test", "0.1.0.0")
	l.InfoContext(context.Background(), "login",
		"username", "dbr2-admin", "password", "hunter2hunter2",
		"session_token", "abc", "Authorization", "Bearer x", "client_secret", "s")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"password", "session_token", "Authorization", "client_secret"} {
		if rec[k] != Redacted {
			t.Errorf("%s = %v, want redacted", k, rec[k])
		}
	}
	if rec["username"] != "dbr2-admin" || rec["service"] != "dbr2-test" {
		t.Errorf("unexpected record %v", rec)
	}
	if bytes.Contains(buf.Bytes(), []byte("hunter2")) {
		t.Fatal("secret leaked into log output")
	}
}

func TestClientIPHonoursOnlyTrustedProxies(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	cases := []struct {
		remote, xff, want string
	}{
		{"203.0.113.9:5000", "1.2.3.4", "203.0.113.9"},             // untrusted peer: ignore XFF
		{"10.1.1.1:5000", "198.51.100.7", "198.51.100.7"},          // trusted proxy
		{"10.1.1.1:5000", "6.6.6.6, 198.51.100.7", "198.51.100.7"}, // rightmost untrusted
		{"10.1.1.1:5000", "198.51.100.7, 10.2.2.2", "198.51.100.7"},
		{"10.1.1.1:5000", "", "10.1.1.1"},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.remote
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := ClientIP(r, trusted).String(); got != c.want {
			t.Errorf("remote=%s xff=%q: got %s want %s", c.remote, c.xff, got, c.want)
		}
	}
}
