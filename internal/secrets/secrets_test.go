// SPDX-License-Identifier: Apache-2.0

package secrets

import (
	"strings"
	"testing"
)

func TestSensitiveKeys(t *testing.T) {
	yes := []string{"DB_PASSWORD", "POSTGRES_PASSWORD", "JWT_SECRET", "API_TOKEN", "AWS_SECRET_ACCESS_KEY",
		"STRIPE_API_KEY", "SMTP_PASS", "DATABASE_URL", "PRIVATE_KEY", "APP_KEY", "BASIC_AUTH", "sentry_dsn", "MYSQL_ROOT_PASSWORD"}
	no := []string{"PATH", "TZ", "POSTGRES_USER", "PASSTHROUGH_MODE", "AUTHOR", "PUBLIC_KEY", "PORT", "HOSTNAME", "LOG_LEVEL"}
	for _, k := range yes {
		if !IsSensitiveKey(k) {
			t.Errorf("%s should be sensitive", k)
		}
	}
	for _, k := range no {
		if IsSensitiveKey(k) {
			t.Errorf("%s should not be sensitive", k)
		}
	}
}

func TestSensitiveValues(t *testing.T) {
	if !IsSensitiveValue("postgres://app:s3cr3t@db:5432/app") {
		t.Error("URL with password")
	}
	if IsSensitiveValue("https://example.org/path") || IsSensitiveValue("postgres://app@db/app") {
		t.Error("URL without password flagged")
	}
	if !IsSensitiveValue("-----BEGIN EC PRIVATE KEY-----\nabc") {
		t.Error("PEM key")
	}
}

func TestPointerVariablesAreNotSecrets(t *testing.T) {
	if IsSensitive("DBR2_DATABASE_URL_FILE", "/run/secrets/db") || IsSensitive("TLS_KEY_PATH", "/certs/key.pem") {
		t.Error("a path to a secret is not a secret")
	}
	if IsSensitive("DB_PASSWORD", "") {
		t.Error("an empty value is not a secret")
	}
	if !IsSensitive("DB_PASSWORD_FILE", "hunter2") {
		t.Error("an inline value under a _FILE name is still a secret")
	}
}

func TestMaskEnvFile(t *testing.T) {
	in := "# comment\nTZ=UTC\nexport DB_PASSWORD=\"hunter2\"\nCACHE_URL=redis://:pw@cache:6379\nEMPTY_SECRET=\n"
	out, masked := MaskEnvFile(in)
	if !masked || strings.Contains(out, "hunter2") || strings.Contains(out, ":pw@") {
		t.Fatalf("not masked:\n%s", out)
	}
	if !strings.Contains(out, "TZ=UTC") || !strings.Contains(out, "# comment") || !strings.Contains(out, "EMPTY_SECRET=") {
		t.Fatalf("non-secret content changed:\n%s", out)
	}
}

func TestMaskCompose(t *testing.T) {
	in := `services:
  db:
    image: postgres:18   # comment
    environment:
      POSTGRES_USER: app
      POSTGRES_PASSWORD: "hunter2"
      FROM_ENV: ${DB_PASSWORD}
  api:
    environment:
      - DATABASE_URL=postgres://app:hunter2@db/app
      - LOG_LEVEL=debug
    command: ["serve"]
`
	out, masked := MaskCompose(in)
	if !masked || strings.Contains(out, "hunter2") {
		t.Fatalf("secret leaked:\n%s", out)
	}
	for _, keep := range []string{"POSTGRES_USER: app", "LOG_LEVEL=debug", "FROM_ENV: ${DB_PASSWORD}", "image: postgres:18"} {
		if !strings.Contains(out, keep) {
			t.Errorf("lost %q:\n%s", keep, out)
		}
	}
}
