// SPDX-License-Identifier: Apache-2.0

// Package obs provides structured logging (slog, JSON), secret redaction and
// OpenTelemetry setup shared by every DBR² binary.
package obs

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
)

// redactedKeys are attribute-name fragments whose values are never logged
// (threat model T9). Matching is case-insensitive on the attribute key.
var redactedKeys = []string{
	"password", "passwd", "secret", "token", "authorization", "cookie",
	"api_key", "apikey", "private_key", "totp", "credential", "session",
}

// Redacted is the placeholder written instead of a secret value.
const Redacted = "[REDACTED]"

// IsSensitiveKey reports whether an attribute key names a secret.
func IsSensitiveKey(key string) bool {
	k := strings.ToLower(key)
	for _, frag := range redactedKeys {
		if strings.Contains(k, frag) {
			return true
		}
	}
	return false
}

// NewLogger builds the process logger. service is emitted on every record.
func NewLogger(w io.Writer, level, format, service, version string) *slog.Logger {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		lvl = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: lvl, ReplaceAttr: redact}
	var h slog.Handler
	if format == "text" {
		h = slog.NewTextHandler(w, opts)
	} else {
		h = slog.NewJSONHandler(w, opts)
	}
	return slog.New(traceHandler{h}).With("service", service, "version", version)
}

// Setup installs the process-wide default logger on stderr.
func Setup(level, format, service, version string) *slog.Logger {
	l := NewLogger(os.Stderr, level, format, service, version)
	slog.SetDefault(l)
	return l
}

func redact(groups []string, a slog.Attr) slog.Attr {
	if IsSensitiveKey(a.Key) && a.Value.Kind() != slog.KindGroup {
		return slog.String(a.Key, Redacted)
	}
	return a
}

// traceHandler adds trace_id/span_id from the context to every record so logs
// correlate with traces (final_stack → Logging).
type traceHandler struct{ slog.Handler }

func (h traceHandler) Handle(ctx context.Context, r slog.Record) error {
	if tid, sid, ok := spanIDs(ctx); ok {
		r.AddAttrs(slog.String("trace_id", tid), slog.String("span_id", sid))
	}
	return h.Handler.Handle(ctx, r)
}

func (h traceHandler) WithAttrs(as []slog.Attr) slog.Handler {
	return traceHandler{h.Handler.WithAttrs(as)}
}

func (h traceHandler) WithGroup(name string) slog.Handler {
	return traceHandler{h.Handler.WithGroup(name)}
}
