// SPDX-License-Identifier: Apache-2.0
import { describe, expect, it } from "vitest";
import {
  coveredEvents,
  eventsSummary,
  isValidEventPattern,
  matchEvent,
  normalizeEvents,
  secretField,
  splitRecipients,
  webhookUrlError,
} from "@/lib/notifications";

describe("event patterns", () => {
  it("validates exact names and prefix wildcards like the server", () => {
    expect(isValidEventPattern("backup.failed")).toBe(true);
    expect(isValidEventPattern("platform.backup.*")).toBe(true);
    expect(isValidEventPattern("*")).toBe(true);
    expect(isValidEventPattern("backup*")).toBe(false);
    expect(isValidEventPattern("Backup.failed")).toBe(false);
    expect(isValidEventPattern("backup.*.failed")).toBe(false);
  });

  it("matches like internal/notify MatchEvent", () => {
    expect(matchEvent([], "agent.offline")).toBe(true);
    expect(matchEvent(["*"], "agent.offline")).toBe(true);
    expect(matchEvent(["backup.*"], "backup.settings.updated")).toBe(true);
    expect(matchEvent(["backup.*"], "backup")).toBe(false);
    expect(matchEvent(["backup.failed"], "backup.completed")).toBe(false);
    expect(coveredEvents(["restore.*", "agent.offline"])).toEqual([
      "restore.succeeded",
      "restore.failed",
      "restore.rolled_back",
      "agent.offline",
    ]);
  });

  it("normalises subscription lists and reports the first invalid entry", () => {
    expect(normalizeEvents([" backup.failed", "backup.failed", "", "contract.*"])).toEqual({
      ok: true,
      events: ["backup.failed", "contract.*"],
    });
    expect(normalizeEvents(["ok.event", "bad pattern"])).toEqual({ ok: false, error: expect.stringContaining("“bad pattern”") });
    expect(eventsSummary([])).toBe("All events");
    expect(eventsSummary(["backup.*", "agent.offline"])).toBe("backup.*, agent.offline");
  });
});

describe("write-only secrets and targets", () => {
  it("omits the field to keep, sends an empty string to remove, the value to replace", () => {
    expect(secretField("keep", "ignored")).toBeUndefined();
    expect(secretField("remove", "ignored")).toBe("");
    expect(secretField("replace", "new-secret")).toBe("new-secret");
  });

  it("splits recipients and checks webhook URLs", () => {
    expect(splitRecipients("a@example.com, b@example.com;\nA@example.com a@example.com")).toEqual([
      "a@example.com",
      "b@example.com",
      "A@example.com",
    ]);
    expect(webhookUrlError("https://hooks.example.com/x")).toBeNull();
    expect(webhookUrlError("http://localhost:8080/x")).toBeNull();
    expect(webhookUrlError("http://127.0.0.1/x")).toBeNull();
    expect(webhookUrlError("http://hooks.example.com/x")).toMatch(/Use https/);
    expect(webhookUrlError("ftp://x")).toMatch(/https:\/\//);
    expect(webhookUrlError("")).toMatch(/Enter/);
  });
});
