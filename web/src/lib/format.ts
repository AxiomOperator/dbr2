// SPDX-License-Identifier: Apache-2.0
//
// Small, locale-aware display helpers shared by the console pages.

const dateTimeFormat = new Intl.DateTimeFormat(undefined, {
  dateStyle: "medium",
  timeStyle: "short",
});

const dateFormat = new Intl.DateTimeFormat(undefined, { dateStyle: "medium" });

const relativeFormat = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" });

function parse(iso: string | null | undefined): Date | null {
  if (!iso) return null;
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? null : d;
}

/** "25 Sep 2026, 17:53" (or "—" when absent / unparseable). */
export function formatDateTime(iso: string | null | undefined): string {
  const d = parse(iso);
  if (!d) return iso ? iso : "—";
  return dateTimeFormat.format(d);
}

export function formatDate(iso: string | null | undefined): string {
  const d = parse(iso);
  if (!d) return iso ? iso : "—";
  return dateFormat.format(d);
}

const UNITS: [Intl.RelativeTimeFormatUnit, number][] = [
  ["year", 365 * 24 * 3600],
  ["month", 30 * 24 * 3600],
  ["week", 7 * 24 * 3600],
  ["day", 24 * 3600],
  ["hour", 3600],
  ["minute", 60],
];

/** "3 minutes ago", "in 2 days", "now". */
export function formatRelative(iso: string | null | undefined, now: Date = new Date()): string {
  const d = parse(iso);
  if (!d) return "—";
  const seconds = Math.round((d.getTime() - now.getTime()) / 1000);
  const abs = Math.abs(seconds);
  if (abs < 45) return relativeFormat.format(0, "second");
  for (const [unit, size] of UNITS) {
    if (abs >= size) return relativeFormat.format(Math.round(seconds / size), unit);
  }
  return relativeFormat.format(Math.round(seconds / 60), "minute");
}

/** Days from now until `iso` (negative when in the past), or null. */
export function daysUntil(iso: string | null | undefined, now: Date = new Date()): number | null {
  const d = parse(iso);
  if (!d) return null;
  return Math.floor((d.getTime() - now.getTime()) / (24 * 3600 * 1000));
}

export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return "—";
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let v = bytes;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v >= 10 || i === 0 ? Math.round(v) : v.toFixed(1)} ${units[i]}`;
}

export const dash = (v: string | null | undefined): string => (v ? v : "—");
