// SPDX-License-Identifier: Apache-2.0
//
// Notification channels (Phase 7), as pure functions: the event catalogue for
// the picker, event-pattern validation and matching (mirrors
// internal/notify/match.go), and the write-only webhook secret semantics
// (omit = keep, "" = remove, value = replace).

/** Event types a channel can subscribe to, grouped for the picker. */
export const NOTIFICATION_EVENTS: { group: string; events: { type: string; label: string }[] }[] = [
  {
    group: "backup",
    events: [
      { type: "backup.failed", label: "Backup failed" },
      { type: "backup.completed", label: "Backup completed" },
      { type: "backup.skipped", label: "Scheduled backup skipped (still running)" },
    ],
  },
  {
    group: "restore",
    events: [
      { type: "restore.succeeded", label: "Restore succeeded" },
      { type: "restore.failed", label: "Restore failed" },
      { type: "restore.rolled_back", label: "Restore rolled back" },
    ],
  },
  {
    group: "agent",
    events: [
      { type: "agent.offline", label: "Agent offline" },
      { type: "agent.online", label: "Agent back online" },
    ],
  },
  {
    group: "contract",
    events: [
      { type: "contract.violated", label: "Recovery Contract violated" },
      { type: "contract.satisfied", label: "Recovery Contract satisfied again" },
    ],
  },
  {
    group: "quiesce",
    events: [
      { type: "quiesce.auto_resumed", label: "Application resumed by the agent's dead-man switch" },
      { type: "quiesce.resume_failed", label: "Resume after quiesce failed" },
    ],
  },
  { group: "application", events: [{ type: "application.not_resumed", label: "Application not resumed" }] },
  { group: "escrow", events: [{ type: "escrow.unhealthy", label: "Escrow needs attention" }] },
  { group: "verification", events: [{ type: "verification.failed", label: "Verification failed" }] },
  {
    group: "platform",
    events: [
      { type: "platform.backup.failed", label: "Platform self-backup failed" },
      { type: "platform.backup.partial", label: "Platform self-backup partial" },
    ],
  },
  { group: "auth", events: [{ type: "auth.master_admin.login", label: "Master admin signed in" }] },
];

export const KNOWN_EVENT_TYPES = NOTIFICATION_EVENTS.flatMap((g) => g.events.map((e) => e.type));

export const SEVERITIES = ["info", "warning", "critical"] as const;
export type Severity = (typeof SEVERITIES)[number];

export const MIN_SEVERITY_HELP: Record<Severity, string> = {
  info: "Everything, including informational messages",
  warning: "Warnings and critical alerts",
  critical: "Critical alerts only",
};

const PATTERN = /^(\*|[a-z0-9_]+(\.[a-z0-9_]+)*(\.\*)?)$/;
export const MAX_EVENT_PATTERNS = 100;

/** True for an event type (backup.failed), a prefix wildcard (backup.*) or "*". */
export function isValidEventPattern(p: string): boolean {
  return p.length <= 100 && PATTERN.test(p);
}

/** internal/notify MatchEvent: empty (or "*") matches everything. */
export function matchEvent(patterns: string[], eventType: string): boolean {
  if (patterns.length === 0) return true;
  return patterns.some((p) =>
    p === "*" ? true : p.endsWith(".*") ? eventType.startsWith(p.slice(0, -1)) : p === eventType,
  );
}

/** Known event types a pattern list delivers (for the picker's preview). */
export function coveredEvents(patterns: string[]): string[] {
  return KNOWN_EVENT_TYPES.filter((t) => matchEvent(patterns, t));
}

/**
 * Normalises a subscription list like the server (trim, drop empties and
 * duplicates) and reports the first invalid entry.
 */
export function normalizeEvents(input: string[]): { ok: true; events: string[] } | { ok: false; error: string } {
  const out: string[] = [];
  for (const raw of input) {
    const e = raw.trim();
    if (!e || out.includes(e)) continue;
    if (!isValidEventPattern(e)) {
      return {
        ok: false,
        error: `“${e}” is not an event pattern: use an event type such as backup.failed, or a prefix wildcard such as backup.*`,
      };
    }
    out.push(e);
  }
  if (out.length > MAX_EVENT_PATTERNS) return { ok: false, error: `Use at most ${MAX_EVENT_PATTERNS} event patterns.` };
  return { ok: true, events: out };
}

/** "All events" / "backup.*, agent.offline" for tables. */
export function eventsSummary(events: string[]): string {
  if (events.length === 0 || events.includes("*")) return "All events";
  return events.join(", ");
}

// ---------------------------------------------------------------------------
// Write-only secrets (webhook signing secret, SMTP password)
// ---------------------------------------------------------------------------

/** What to do with a stored write-only value on save. */
export type SecretAction = "keep" | "replace" | "remove";

export const MIN_WEBHOOK_SECRET = 16;

/**
 * The request field for a write-only value: undefined keeps the stored one
 * (omitted from the body), "" removes it, anything else replaces it.
 */
export function secretField(action: SecretAction, value: string): string | undefined {
  switch (action) {
    case "keep":
      return undefined;
    case "remove":
      return "";
    case "replace":
      return value;
  }
}

/** Splits a free-text recipient list (commas, semicolons, whitespace or new lines). */
export function splitRecipients(text: string): string[] {
  return [
    ...new Set(
      text
        .split(/[\s,;]+/)
        .map((s) => s.trim())
        .filter(Boolean),
    ),
  ];
}

/** Loose address check (the server parses RFC 5322 addresses). */
export const looksLikeEmail = (s: string): boolean => /^[^\s@<>]+@[^\s@<>]+\.[^\s@<>]+$/.test(s);

/**
 * internal/notify ValidateWebhookURL in short: https, or http only for
 * localhost / loopback. Returns an error message or null.
 */
export function webhookUrlError(raw: string): string | null {
  const v = raw.trim();
  if (!v) return "Enter the webhook URL.";
  let u: URL;
  try {
    u = new URL(v);
  } catch {
    return "Enter a valid URL, e.g. https://hooks.example.com/dbr2.";
  }
  if (u.protocol === "https:") return null;
  if (u.protocol === "http:") {
    const h = u.hostname.replace(/^\[|\]$/g, "");
    if (h === "localhost" || h === "::1" || /^127\./.test(h)) return null;
    return "Use https: plain http is accepted only for localhost / loopback addresses.";
  }
  return "The webhook URL must start with https://.";
}
