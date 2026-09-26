// SPDX-License-Identifier: Apache-2.0
import { describe, expect, it } from "vitest";
import {
  describeCron,
  parseCron,
  presetOf,
  retentionHorizon,
  retentionSummary,
  scheduleSummary,
  SCHEDULE_PRESETS,
  DEFAULT_RETENTION,
} from "@/lib/schedule";

const text = (expr: string) => {
  const d = describeCron(expr);
  return d.ok ? d.text : `ERR ${d.error}`;
};

describe("cron schedules", () => {
  it("maps stored expressions back to presets", () => {
    expect(presetOf("0 1 * * *")).toBe("daily");
    expect(presetOf(" 0  1 * * 0 ")).toBe("weekly");
    expect(presetOf("0 2 * * *")).toBeNull();
  });

  it("describes the presets", () => {
    expect(text(SCHEDULE_PRESETS.hourly)).toBe("Every hour on the hour");
    expect(text(SCHEDULE_PRESETS.daily)).toBe("Every day at 01:00");
    expect(text(SCHEDULE_PRESETS.weekly)).toBe("Every Sunday at 01:00");
    expect(text(SCHEDULE_PRESETS.monthly)).toBe("On the 1st of the month at 01:00");
  });

  it("describes custom expressions: ranges, lists, steps, names and months", () => {
    expect(text("30 22 * * 1-5")).toBe("Every Monday–Friday at 22:30");
    expect(text("15 * * * *")).toBe("Every hour at minute 15");
    expect(text("0 */6 * * *")).toBe("Every 6 hours on the hour");
    expect(text("*/15 * * * *")).toBe("Every 15 minutes");
    expect(text("0 2,14 * * SAT,SUN")).toBe("Every Sunday and Saturday at 02:00 and 14:00");
    expect(text("0 3 1,15 * *")).toBe("On the 1st and 15th of the month at 03:00");
    expect(text("0 4 * JAN,JUL *")).toBe("Every day at 04:00 in January and July");
    expect(text("0 5 1 * 1")).toBe("On the 1st of the month or on Monday at 05:00");
    expect(scheduleSummary("0 1 * * *", "Europe/Berlin")).toBe("Every day at 01:00 (Europe/Berlin)");
  });

  it("explains why an expression is invalid", () => {
    expect(parseCron("0 1 * *")).toEqual({ ok: false, error: expect.stringMatching(/has 4/) });
    expect(text("61 1 * * *")).toMatch(/Minute: 61 is out of range 0–59/);
    expect(text("0 24 * * *")).toMatch(/Hour: 24 is out of range 0–23/);
    expect(text("0 1 * * 7")).toMatch(/Weekday: 7 is out of range 0–6/);
    expect(text("0 1 * FOO *")).toMatch(/Month: “FOO” is not a number or name/);
    expect(text("0 5-1 * * *")).toMatch(/runs backwards/);
    expect(text("*/0 * * * *")).toMatch(/positive number/);
    expect(scheduleSummary("nope", "UTC")).toBe("nope (UTC)");
  });
});

describe("retention", () => {
  it("summarises grandfather-father-son rules", () => {
    expect(retentionSummary(DEFAULT_RETENTION)).toBe("Last 7 · 14 daily · 8 weekly · 12 monthly");
    expect(retentionSummary({ ...DEFAULT_RETENTION, keep_hourly: 48, keep_yearly: 3 })).toBe(
      "Last 7 · 48 hourly · 14 daily · 8 weekly · 12 monthly · 3 yearly",
    );
  });

  it("estimates how far back the kept recovery points reach", () => {
    expect(retentionHorizon(DEFAULT_RETENTION)).toBe("about 1 year");
    expect(retentionHorizon({ ...DEFAULT_RETENTION, keep_monthly: 6 })).toBe("about 6 months");
    expect(retentionHorizon({ keep_last: 3, keep_hourly: 12, keep_daily: 0, keep_weekly: 0, keep_monthly: 0, keep_yearly: 0 })).toBe(
      "about 12 hours",
    );
    expect(retentionHorizon({ keep_last: 3, keep_hourly: 0, keep_daily: 0, keep_weekly: 0, keep_monthly: 0, keep_yearly: 0 })).toBeNull();
  });
});
