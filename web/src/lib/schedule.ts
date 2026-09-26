// SPDX-License-Identifier: Apache-2.0
//
// Protection Policy schedules and retention (Phase 7), as pure functions:
// cron presets (internal/protection SchedulePresets), a client-side check
// and plain-language description of 5-field cron expressions (the server's
// robfig/cron parser stays authoritative: its validation message is shown
// when it disagrees), and retention (grandfather-father-son) summaries.

/** Presets the API accepts in place of a cron expression; stored as cron. */
export const SCHEDULE_PRESETS = {
  hourly: "0 * * * *",
  daily: "0 1 * * *",
  weekly: "0 1 * * 0",
  monthly: "0 1 1 * *",
} as const;
export type SchedulePreset = keyof typeof SCHEDULE_PRESETS;
export const SCHEDULE_PRESET_NAMES = Object.keys(SCHEDULE_PRESETS) as SchedulePreset[];

export const PRESET_LABEL: Record<SchedulePreset, string> = {
  hourly: "Hourly",
  daily: "Daily",
  weekly: "Weekly",
  monthly: "Monthly",
};

/** The preset a stored cron expression corresponds to, or null for custom schedules. */
export function presetOf(expr: string): SchedulePreset | null {
  const norm = expr.trim().replace(/\s+/g, " ");
  for (const p of SCHEDULE_PRESET_NAMES) if (SCHEDULE_PRESETS[p] === norm) return p;
  return null;
}

// ---------------------------------------------------------------------------
// Cron parsing (minute hour day-of-month month weekday)
// ---------------------------------------------------------------------------

interface FieldSpec {
  name: string;
  min: number;
  max: number;
  names?: string[];
}

const MONTHS = ["JAN", "FEB", "MAR", "APR", "MAY", "JUN", "JUL", "AUG", "SEP", "OCT", "NOV", "DEC"];
const DAYS = ["SUN", "MON", "TUE", "WED", "THU", "FRI", "SAT"];

const FIELDS: FieldSpec[] = [
  { name: "Minute", min: 0, max: 59 },
  { name: "Hour", min: 0, max: 23 },
  { name: "Day of month", min: 1, max: 31 },
  { name: "Month", min: 1, max: 12, names: MONTHS },
  { name: "Weekday", min: 0, max: 6, names: DAYS },
];

/** One parsed field: the sorted values it matches and whether it was `*`. */
export interface CronField {
  raw: string;
  any: boolean;
  /** A plain `*\/n` step (every n units from the minimum). */
  step: number | null;
  values: number[];
}

export type CronParse = { ok: true; fields: CronField[] } | { ok: false; error: string };

function parseValue(token: string, spec: FieldSpec): number {
  if (/^\d+$/.test(token)) {
    const n = Number(token);
    if (n < spec.min || n > spec.max) throw new Error(`${spec.name}: ${n} is out of range ${spec.min}–${spec.max}.`);
    return n;
  }
  const i = spec.names?.indexOf(token.toUpperCase()) ?? -1;
  if (i < 0) throw new Error(`${spec.name}: “${token}” is not a number${spec.names ? " or name" : ""}.`);
  return spec.min + i;
}

function parseField(raw: string, spec: FieldSpec): CronField {
  const values = new Set<number>();
  let step: number | null = null;
  for (const part of raw.split(",")) {
    if (part === "") throw new Error(`${spec.name}: empty list entry.`);
    const [range, stepText, extra] = part.split("/");
    if (extra !== undefined) throw new Error(`${spec.name}: “${part}” has more than one “/”.`);
    let every = 1;
    if (stepText !== undefined) {
      if (!/^\d+$/.test(stepText) || Number(stepText) < 1) throw new Error(`${spec.name}: the step in “${part}” must be a positive number.`);
      every = Number(stepText);
    }
    let lo: number;
    let hi: number;
    if (range === "*" || range === "?") {
      lo = spec.min;
      hi = spec.max;
      if (stepText !== undefined && raw.split(",").length === 1) step = every;
    } else if (range!.includes("-")) {
      const [a, b] = range!.split("-");
      lo = parseValue(a ?? "", spec);
      hi = parseValue(b ?? "", spec);
      if (lo > hi) throw new Error(`${spec.name}: the range “${range}” runs backwards.`);
    } else {
      lo = parseValue(range!, spec);
      hi = stepText !== undefined ? spec.max : lo;
    }
    for (let v = lo; v <= hi; v += every) values.add(v);
  }
  const any = raw === "*" || raw === "?";
  return { raw, any, step, values: [...values].sort((a, b) => a - b) };
}

/** Parses a 5-field cron expression (the syntax the server's parser accepts). */
export function parseCron(expr: string): CronParse {
  const parts = expr.trim().split(/\s+/).filter(Boolean);
  if (parts.length !== 5) {
    return {
      ok: false,
      error: `A cron expression has 5 fields (minute hour day-of-month month weekday); this one has ${parts.length}.`,
    };
  }
  try {
    return { ok: true, fields: parts.map((p, i) => parseField(p, FIELDS[i]!)) };
  } catch (err) {
    return { ok: false, error: (err as Error).message };
  }
}

// ---------------------------------------------------------------------------
// Description
// ---------------------------------------------------------------------------

const DAY_NAMES = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];
const MONTH_NAMES = ["January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"];

const pad = (n: number) => String(n).padStart(2, "0");

/** "a, b and c". */
export function joinList(items: string[]): string {
  if (items.length <= 1) return items.join("");
  return `${items.slice(0, -1).join(", ")} and ${items[items.length - 1]}`;
}

/** Values with runs of three or more collapsed: [1,2,3,5] → ["1–3", "5"] (via `label`). */
function runs(values: number[], label: (n: number) => string): string[] {
  const out: string[] = [];
  let i = 0;
  while (i < values.length) {
    let j = i;
    while (j + 1 < values.length && values[j + 1] === values[j]! + 1) j++;
    if (j - i >= 2) out.push(`${label(values[i]!)}–${label(values[j]!)}`);
    else for (let k = i; k <= j; k++) out.push(label(values[k]!));
    i = j + 1;
  }
  return out;
}

const ordinal = (n: number) => {
  const s = ["th", "st", "nd", "rd"];
  const v = n % 100;
  return `${n}${s[(v - 20) % 10] ?? s[v] ?? s[0]}`;
};

function dayPhrase(dom: CronField, dow: CronField): { every: string; on: string } {
  const days = joinList(runs(dow.values, (d) => DAY_NAMES[d]!));
  const doms = joinList(runs(dom.values, ordinal));
  if (dom.any && dow.any) return { every: "Every day", on: "" };
  if (dom.any) return { every: `Every ${days}`, on: `on ${days}` };
  if (dow.any) return { every: `On the ${doms} of the month`, on: `on the ${doms} of the month` };
  // Both restricted: cron fires when EITHER matches.
  const both = `on the ${doms} of the month or on ${days}`;
  return { every: both.charAt(0).toUpperCase() + both.slice(1), on: both };
}

/**
 * Plain-language description of a valid cron expression, e.g.
 * "Every day at 01:00", "Every hour at minute 15", "Every Monday–Friday at
 * 22:30", "Every 15 minutes". Returns the error for an invalid one.
 */
export function describeCron(expr: string): { ok: true; text: string } | { ok: false; error: string } {
  const parsed = parseCron(expr);
  if (!parsed.ok) return parsed;
  const [min, hour, dom, mon, dow] = parsed.fields as [CronField, CronField, CronField, CronField, CronField];
  const days = dayPhrase(dom, dow);
  const monthSuffix = mon.any ? "" : ` in ${joinList(runs(mon.values, (m) => MONTH_NAMES[m - 1]!))}`;
  const daySuffix = days.on ? `, ${days.on}` : "";

  let text: string;
  if (min.any && hour.any) {
    text = `Every minute${daySuffix}`;
  } else if (min.step && hour.any) {
    text = `Every ${min.step} minutes${daySuffix}`;
  } else if (hour.any || hour.step) {
    const at = min.values.length === 1 ? (min.values[0] === 0 ? "on the hour" : `at minute ${min.values[0]}`) : `at minutes ${joinList(min.values.map(String))}`;
    const every = hour.step && hour.step > 1 ? `Every ${hour.step} hours` : "Every hour";
    text = `${every} ${at}${daySuffix}`;
  } else if (min.values.length * hour.values.length <= 6) {
    const times = hour.values.flatMap((h) => min.values.map((m) => `${pad(h)}:${pad(m)}`));
    text = `${days.every} at ${joinList(times)}`;
  } else {
    text = `${days.every}, at minute ${joinList(min.values.map(String))} of hours ${joinList(runs(hour.values, String))}`;
  }
  return { ok: true, text: text + monthSuffix };
}

/** Description with the timezone, or the expression itself when it cannot be described. */
export function scheduleSummary(expr: string, timezone?: string): string {
  const d = describeCron(expr);
  const base = d.ok ? d.text : expr;
  return timezone ? `${base} (${timezone})` : base;
}

// ---------------------------------------------------------------------------
// Retention (grandfather-father-son)
// ---------------------------------------------------------------------------

export interface Retention {
  keep_last: number;
  keep_hourly: number;
  keep_daily: number;
  keep_weekly: number;
  keep_monthly: number;
  keep_yearly: number;
}

/** The server's defaults (RetentionInput). */
export const DEFAULT_RETENTION: Retention = {
  keep_last: 7,
  keep_hourly: 0,
  keep_daily: 14,
  keep_weekly: 8,
  keep_monthly: 12,
  keep_yearly: 0,
};

export const RETENTION_FIELDS: { key: keyof Retention; label: string; unit: string; help: string }[] = [
  { key: "keep_last", label: "Keep last", unit: "recovery points", help: "The newest recovery points, whatever their age (at least 1)." },
  { key: "keep_hourly", label: "Hourly", unit: "hours", help: "The newest recovery point of each of the last N hours." },
  { key: "keep_daily", label: "Daily", unit: "days", help: "The newest recovery point of each of the last N days." },
  { key: "keep_weekly", label: "Weekly", unit: "weeks", help: "The newest recovery point of each of the last N weeks." },
  { key: "keep_monthly", label: "Monthly", unit: "months", help: "The newest recovery point of each of the last N months." },
  { key: "keep_yearly", label: "Yearly", unit: "years", help: "The newest recovery point of each of the last N years." },
];

/** "Last 7 · 14 daily · 8 weekly · 12 monthly". */
export function retentionSummary(r: Retention): string {
  const parts = [`Last ${r.keep_last}`];
  if (r.keep_hourly) parts.push(`${r.keep_hourly} hourly`);
  if (r.keep_daily) parts.push(`${r.keep_daily} daily`);
  if (r.keep_weekly) parts.push(`${r.keep_weekly} weekly`);
  if (r.keep_monthly) parts.push(`${r.keep_monthly} monthly`);
  if (r.keep_yearly) parts.push(`${r.keep_yearly} yearly`);
  return parts.join(" · ");
}

/** How far back the oldest kept recovery point can reach, in words ("about 1 year"), or null. */
export function retentionHorizon(r: Retention): string | null {
  const days = Math.max(r.keep_hourly / 24, r.keep_daily, r.keep_weekly * 7, r.keep_monthly * 30.4, r.keep_yearly * 365);
  if (days <= 0) return null;
  if (days < 1) return `about ${Math.round(days * 24)} hours`;
  if (days < 14) return `about ${Math.round(days)} day${Math.round(days) === 1 ? "" : "s"}`;
  if (days < 60) return `about ${Math.round(days / 7)} weeks`;
  if (days < 360) return `about ${Math.round(days / 30.4)} months`;
  const years = Math.round(days / 365);
  return `about ${years} year${years === 1 ? "" : "s"}`;
}

export const GFS_EXPLANATION =
  "Retention works like grandfather-father-son rotation: for each rule, the newest recovery point of each of the last N periods is kept; a recovery point kept by any rule stays. Everything else is pruned after each backup. The latest recovery point is never deleted, whatever the rules say.";
