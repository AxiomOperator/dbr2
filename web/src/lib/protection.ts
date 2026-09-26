// SPDX-License-Identifier: Apache-2.0
//
// Pure helpers for the Repositories & backup pages: backup-window times,
// hook command lines and component names. Which components a backup captures
// and whether they are protected comes from the API (`protection.components`,
// computed by the server's backup plan); the console no longer derives it.

import type { Analysis } from "@/lib/api/fleet-schemas";

// ---------------------------------------------------------------------------
// Minutes of the day <-> "HH:MM"
// ---------------------------------------------------------------------------

/** 90 → "01:30"; null / out of range → "". */
export function minutesToHHMM(minutes: number | null | undefined): string {
  if (minutes == null || !Number.isInteger(minutes) || minutes < 0 || minutes > 1439) return "";
  const h = Math.floor(minutes / 60);
  const m = minutes % 60;
  return `${String(h).padStart(2, "0")}:${String(m).padStart(2, "0")}`;
}

/** "01:30" (or "1:30", or "01:30:00" from a time input) → 90; invalid → null. */
export function hhmmToMinutes(value: string): number | null {
  const m = /^(\d{1,2}):(\d{2})(?::\d{2})?$/.exec(value.trim());
  if (!m) return null;
  const h = Number(m[1]);
  const min = Number(m[2]);
  if (h > 23 || min > 59) return null;
  return h * 60 + min;
}

/** Human description of a backup window, e.g. "22:00–04:00 (overnight)". */
export function describeWindow(start: number | null, end: number | null): string {
  if (start === null || end === null) return "No window: scheduled backups may start at any time";
  const s = `${minutesToHHMM(start)}–${minutesToHHMM(end)}`;
  if (start === end) return `${s} (all day)`;
  return start > end ? `${s} (overnight, wraps midnight)` : s;
}

// ---------------------------------------------------------------------------
// Hook command lines
// ---------------------------------------------------------------------------

/**
 * Splits a command line into argv, shell-like: whitespace separates words,
 * single quotes are literal, double quotes allow `\"` and `\\`, and a
 * backslash outside quotes escapes the next character. No expansion happens:
 * the agent runs the argv with `docker exec` (no shell unless the command
 * starts one). Throws on an unterminated quote or trailing backslash.
 */
export function splitCommand(line: string): string[] {
  const out: string[] = [];
  let cur = "";
  let inWord = false;
  let quote: "'" | '"' | null = null;
  for (let i = 0; i < line.length; i++) {
    const c = line[i]!;
    if (quote === "'") {
      if (c === "'") quote = null;
      else cur += c;
      continue;
    }
    if (quote === '"') {
      if (c === '"') quote = null;
      else if (c === "\\" && (line[i + 1] === '"' || line[i + 1] === "\\")) cur += line[++i];
      else cur += c;
      continue;
    }
    if (c === "'" || c === '"') {
      quote = c;
      inWord = true;
    } else if (c === "\\") {
      if (i + 1 >= line.length) throw new Error("The command ends with a lone backslash.");
      cur += line[++i];
      inWord = true;
    } else if (/\s/.test(c)) {
      if (inWord) out.push(cur);
      cur = "";
      inWord = false;
    } else {
      cur += c;
      inWord = true;
    }
  }
  if (quote) throw new Error(`Unterminated ${quote === "'" ? "single" : "double"} quote in the command.`);
  if (inWord) out.push(cur);
  return out;
}

/** Inverse of {@link splitCommand}: quotes words that need it. */
export function joinCommand(argv: string[]): string {
  return argv
    .map((a) => (a !== "" && /^[A-Za-z0-9_@%+=:,./-]+$/.test(a) ? a : `'${a.replace(/'/g, `'\\''`)}'`))
    .join(" ");
}

// ---------------------------------------------------------------------------
// Backup components of an application
// ---------------------------------------------------------------------------

export interface BackupComponent {
  /** Component name as used in backup settings, e.g. `volume:shop_pgdata`. */
  name: string;
  kind: "config" | "volume" | "bind_mount" | "database";
  /** Where the data lives on the host. */
  detail: string;
}

/** Kind of a component from its name (`volume:…`, `bind:…`, `database:…`, `config`). */
export function componentKind(name: string): BackupComponent["kind"] {
  if (name.startsWith("volume:")) return "volume";
  if (name.startsWith("bind:")) return "bind_mount";
  if (name.startsWith("database:")) return "database";
  return "config";
}

/** Where a component's data lives, from the application's analysis (null when unknown). */
export function componentDetail(analysis: Analysis | null, name: string): string | null {
  if (name === "config") return analysis?.working_dir || "Application definition and metadata";
  if (!analysis) return null;
  if (name.startsWith("volume:")) {
    const v = analysis.volumes.find((x) => x.name === name.slice("volume:".length));
    return v ? v.mountpoint || v.name : null;
  }
  if (name.startsWith("bind:")) {
    const src = name.slice("bind:".length);
    return analysis.bind_mounts.some((b) => b.source === src) ? src : null;
  }
  return null;
}

/** Service and container names a hook may target. */
export function hookTargets(analysis: Analysis | null): string[] {
  if (!analysis) return [];
  const names = new Set<string>();
  for (const s of analysis.services) names.add(s.name);
  for (const c of analysis.containers) names.add(c);
  return [...names].sort((a, b) => a.localeCompare(b));
}

// ---------------------------------------------------------------------------
// Timezones
// ---------------------------------------------------------------------------

/** IANA timezones known to this browser (fallback: a short list). */
export function timeZones(): string[] {
  try {
    const zones = Intl.supportedValuesOf("timeZone");
    return zones.includes("UTC") ? zones : ["UTC", ...zones];
  } catch {
    return ["UTC", "Europe/Berlin", "Europe/London", "America/New_York", "America/Chicago", "America/Los_Angeles"];
  }
}

/** True when `tz` is a timezone this browser can resolve. */
export function isValidTimeZone(tz: string): boolean {
  if (!tz.trim()) return false;
  try {
    new Intl.DateTimeFormat("en-US", { timeZone: tz });
    return true;
  } catch {
    return false;
  }
}

// ---------------------------------------------------------------------------
// Downloads
// ---------------------------------------------------------------------------

/** Saves `text` as a file (e.g. an escrow package) via a temporary object URL. */
export function downloadTextFile(filename: string, text: string, type = "application/octet-stream"): void {
  const blob = new Blob([text], { type });
  const href = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = href;
  a.download = filename;
  a.rel = "noopener";
  document.body.appendChild(a);
  a.click();
  a.remove();
  window.setTimeout(() => URL.revokeObjectURL(href), 0);
}
