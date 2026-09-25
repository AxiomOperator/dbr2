// SPDX-License-Identifier: Apache-2.0

// Package secrets classifies configuration values as sensitive (Phase 3:
// secret detection in environment variables and .env files). Sensitive
// values are masked in the API/UI and revealed only with secrets.read.
package secrets

import (
	"net/url"
	"regexp"
	"strings"
)

// Mask replaces sensitive values in API output.
const Mask = "********"

var keyFragments = []string{
	"PASSWORD", "PASSWD", "PASS", "PWD", "SECRET", "TOKEN", "API_KEY", "APIKEY", "ACCESS_KEY",
	"PRIVATE_KEY", "CREDENTIAL", "AUTH", "SALT", "SIGNING_KEY", "ENCRYPTION_KEY", "MASTER_KEY",
	"DATABASE_URL", "DSN", "CONNECTION_STRING", "COOKIE", "SESSION_KEY", "LICENSE_KEY",
}

// Some fragments are too broad on their own ("PASS" in "PASSTHROUGH",
// "AUTH" in "AUTHOR"): they must be a whole underscore-delimited word.
var wordOnly = map[string]bool{"PASS": true, "PWD": true, "AUTH": true, "DSN": true}

// IsSensitiveKey reports whether a variable name suggests a secret.
func IsSensitiveKey(key string) bool {
	k := strings.ToUpper(key)
	words := strings.FieldsFunc(k, func(r rune) bool { return r == '_' || r == '-' || r == '.' })
	for _, f := range keyFragments {
		if wordOnly[f] {
			for _, w := range words {
				if w == f {
					return true
				}
			}
			continue
		}
		if strings.Contains(k, f) {
			return true
		}
	}
	return strings.HasSuffix(k, "_KEY") && !strings.HasSuffix(k, "PUBLIC_KEY")
}

var pemPrivate = regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)

// IsSensitiveValue reports values that are secrets regardless of their key:
// URLs with a password and PEM private keys.
func IsSensitiveValue(v string) bool {
	if pemPrivate.MatchString(v) {
		return true
	}
	if strings.Contains(v, "://") {
		if u, err := url.Parse(v); err == nil && u.User != nil {
			if _, ok := u.User.Password(); ok {
				return true
			}
		}
	}
	return false
}

// IsSensitive combines key and value heuristics. Variables that only point
// to a secret (DB_PASSWORD_FILE=/run/secrets/db) are not secrets themselves.
func IsSensitive(key, value string) bool {
	if strings.TrimSpace(value) == "" {
		return false // nothing to protect
	}
	if IsSensitiveValue(value) {
		return true
	}
	k := strings.ToUpper(key)
	if (strings.HasSuffix(k, "_FILE") || strings.HasSuffix(k, "_PATH") || strings.HasSuffix(k, "_DIR")) && strings.HasPrefix(value, "/") {
		return false
	}
	return IsSensitiveKey(key)
}

// envLine matches KEY=VALUE lines in .env files (optionally `export KEY=`).
var envLine = regexp.MustCompile(`^(\s*(?:export\s+)?)([A-Za-z_][A-Za-z0-9_.-]*)(\s*=\s*)(.*)$`)

// yamlEnv matches Compose environment entries: `KEY: value` and `- KEY=value`.
var yamlMap = regexp.MustCompile(`^(\s*)([A-Za-z_][A-Za-z0-9_.-]*)(\s*:\s*)(\S.*)$`)
var yamlList = regexp.MustCompile(`^(\s*-\s*["']?)([A-Za-z_][A-Za-z0-9_.-]*)(=)(.*?)(["']?\s*)$`)

// MaskEnvFile masks sensitive values in a .env file. It reports whether
// anything was masked.
func MaskEnvFile(content string) (string, bool) {
	masked := false
	lines := strings.Split(content, "\n")
	for i, l := range lines {
		if m := envLine.FindStringSubmatch(l); m != nil && strings.TrimSpace(m[4]) != "" {
			if IsSensitive(m[2], unquote(m[4])) {
				lines[i] = m[1] + m[2] + m[3] + Mask
				masked = true
			}
		}
	}
	return strings.Join(lines, "\n"), masked
}

// MaskCompose masks sensitive values in a Compose file (environment maps and
// lists, and any `key: value` whose key or value looks secret). It is a
// conservative line-based pass: it never parses and re-emits YAML, so the
// file keeps its formatting and comments.
func MaskCompose(content string) (string, bool) {
	masked := false
	lines := strings.Split(content, "\n")
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "#") {
			continue
		}
		if m := yamlList.FindStringSubmatch(l); m != nil && m[4] != "" {
			if IsSensitive(m[2], m[4]) {
				lines[i] = m[1] + m[2] + m[3] + Mask + m[5]
				masked = true
			}
			continue
		}
		if m := yamlMap.FindStringSubmatch(l); m != nil {
			v := unquote(strings.TrimSpace(m[4]))
			if v == "|" || v == ">" || strings.HasPrefix(v, "${") {
				continue // block scalar or interpolation reference: nothing literal to hide
			}
			if IsSensitive(m[2], v) {
				lines[i] = m[1] + m[2] + m[3] + Mask
				masked = true
			}
		}
	}
	return strings.Join(lines, "\n"), masked
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == '"' && s[len(s)-1] == '"' || s[0] == '\'' && s[len(s)-1] == '\'') {
		return s[1 : len(s)-1]
	}
	return s
}
