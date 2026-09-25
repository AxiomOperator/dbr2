// SPDX-License-Identifier: Apache-2.0

package config

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

// testKey is a throwaway 32-byte key, derived at runtime (not a real secret).
var testKey = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))

func TestLoadServerDefaultsAndSecretFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "db")
	if err := os.WriteFile(f, []byte("postgres://x@y/z\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DBR2_DATABASE_URL_FILE", f)
	t.Setenv("DBR2_SECRET_KEY", testKey)
	t.Setenv("DBR2_PUBLIC_URL", "https://dbr2.example.lan/")

	c, err := LoadServer()
	if err != nil {
		t.Fatal(err)
	}
	if c.DatabaseURL != "postgres://x@y/z" {
		t.Errorf("DatabaseURL = %q", c.DatabaseURL)
	}
	if c.PublicURL != "https://dbr2.example.lan" {
		t.Errorf("PublicURL not normalised: %q", c.PublicURL)
	}
	if c.DocsPublic || !c.CookieSecure || c.Entra.Enabled() {
		t.Error("unexpected defaults")
	}
	if len(c.TrustedProxies()) != 2 {
		t.Errorf("trusted proxies = %v", c.TrustedProxies())
	}
}

func TestLoadServerRejectsBadKeyAndHalfEntra(t *testing.T) {
	t.Setenv("DBR2_DATABASE_URL", "postgres://x")
	t.Setenv("DBR2_SECRET_KEY", "c2hvcnQ=")
	if _, err := LoadServer(); err == nil {
		t.Fatal("expected short key error")
	}
	t.Setenv("DBR2_SECRET_KEY", testKey)
	t.Setenv("DBR2_ENTRA_TENANT_ID", "tenant")
	if _, err := LoadServer(); err == nil {
		t.Fatal("expected incomplete Entra error")
	}
}

func TestReadyHTTPChecks(t *testing.T) {
	t.Setenv("DBR2_DATABASE_URL", "postgres://x")
	t.Setenv("DBR2_SECRET_KEY", testKey)
	t.Setenv("DBR2_READY_HTTP_CHECKS", "proxy=http://proxy:8090/healthz, worker=http://dbr2-worker:8082/healthz")
	c, err := LoadServer()
	if err != nil {
		t.Fatal(err)
	}
	got := c.HTTPChecks()
	if len(got) != 2 || got[0].Name != "proxy" || got[1].URL != "http://dbr2-worker:8082/healthz" {
		t.Fatalf("checks = %+v", got)
	}
	for _, bad := range []string{"proxy", "=http://x/", "proxy=ftp://x/", "proxy=not a url"} {
		t.Setenv("DBR2_READY_HTTP_CHECKS", bad)
		if _, err := LoadServer(); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestSecretRejectsBothForms(t *testing.T) {
	t.Setenv("DBR2_X", "a")
	t.Setenv("DBR2_X_FILE", "/nope")
	if _, err := secret("DBR2_X", true); err == nil {
		t.Fatal("expected error")
	}
}
