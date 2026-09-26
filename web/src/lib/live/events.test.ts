// SPDX-License-Identifier: Apache-2.0
import { describe, expect, it } from "vitest";
import { queryKeys } from "@/lib/api/endpoints";
import {
  backoffMs,
  invalidationsFor,
  isActiveProgress,
  jobKindLabel,
  parseLiveEvent,
  toJobProgress,
  type LiveEvent,
} from "@/lib/live/events";

const APP = "5f939a00-fccb-4376-a6cc-37eeb5542abe";
const progress = {
  command_id: `application/${APP}/run-1/snapshot-components`,
  host_id: "3f0d7a52-8c1e-4f7b-9a26-1d5e8b7c4a01",
  kind: "snapshot_components",
  state: "running",
  workflow_id: `application/${APP}`,
  run_id: "run-1",
  application_id: APP,
  progress: { component: "volume:shop_pgdata", hashed_bytes: 1024, uploaded_bytes: 512, files: 7, done: 1, total: 3 },
};

describe("parseLiveEvent", () => {
  it("validates payloads with the generated contract schemas", () => {
    const e = parseLiveEvent("job.progress", JSON.stringify(progress));
    expect(e?.type).toBe("job.progress");
    expect(parseLiveEvent("agent.status", JSON.stringify({ host_id: "h", connected: true }))).toEqual({
      type: "agent.status",
      data: { host_id: "h", connected: true },
    });
  });

  it("ignores unknown types, invalid JSON and payloads outside the contract", () => {
    expect(parseLiveEvent("something.else", "{}")).toBeNull();
    expect(parseLiveEvent("agent.status", "not json")).toBeNull();
    expect(parseLiveEvent("agent.status", JSON.stringify({ host_id: "h" }))).toBeNull();
    expect(parseLiveEvent("backup.updated", JSON.stringify({ recovery_point_id: "rp", application_id: APP, state: "exploded" }))).toBeNull();
  });
});

describe("invalidationsFor", () => {
  const ev = (e: LiveEvent) => invalidationsFor(e);

  it("does not refetch lists for running progress updates", () => {
    expect(ev({ type: "job.progress", data: { ...progress } })).toEqual([]);
  });

  it("refetches jobs and the application when a command starts or ends", () => {
    expect(ev({ type: "job.progress", data: { ...progress, state: "succeeded" } })).toEqual([
      queryKeys.jobsAll,
      queryKeys.application(APP),
      queryKeys.applications,
    ]);
  });

  it("maps every event type to the caches it makes stale", () => {
    expect(ev({ type: "backup.updated", data: { recovery_point_id: "rp", application_id: APP, state: "committed" } })).toEqual(
      expect.arrayContaining([queryKeys.recoveryPointsAll, queryKeys.jobsAll, queryKeys.applications, queryKeys.volumesAll]),
    );
    expect(ev({ type: "restore.updated", data: { restore_id: "rs_1", state: "running", step: "images" } })).toEqual(
      expect.arrayContaining([queryKeys.restore("rs_1"), queryKeys.restoresAll, queryKeys.jobsAll]),
    );
    expect(ev({ type: "agent.status", data: { host_id: "h", connected: false } })).toEqual([queryKeys.agents]);
    expect(ev({ type: "alert.created", data: { severity: "critical", type: "backup.failed", message: "x" } })).toEqual([
      queryKeys.alertsAll,
    ]);
    expect(ev({ type: "inventory.updated", data: { host_id: "h", applications: 3 } })).toEqual(
      expect.arrayContaining([queryKeys.agentInventory("h"), queryKeys.containersAll, queryKeys.volumesAll]),
    );
  });
});

describe("backoffMs", () => {
  it("doubles from 1 s up to 60 s, with ±20 % jitter", () => {
    const mid = () => 0.5;
    expect([1, 2, 3, 4, 7, 20].map((n) => backoffMs(n, mid))).toEqual([1000, 2000, 4000, 8000, 60000, 60000]);
    expect(backoffMs(1, () => 0)).toBe(800);
    expect(backoffMs(1, () => 1)).toBe(1200);
  });
});

describe("toJobProgress", () => {
  it("reads the agent's progress document", () => {
    expect(toJobProgress(progress, 42)).toEqual({
      applicationId: APP,
      kind: "snapshot_components",
      state: "running",
      workflowId: `application/${APP}`,
      hostId: progress.host_id,
      component: "volume:shop_pgdata",
      hashedBytes: 1024,
      uploadedBytes: 512,
      files: 7,
      done: 1,
      total: 3,
      queued: false,
      error: undefined,
      updatedAt: 42,
    });
  });

  it("handles queued commands, image progress and events without an application", () => {
    expect(toJobProgress({ ...progress, state: "accepted", progress: { queued: true } })?.queued).toBe(true);
    expect(toJobProgress({ ...progress, kind: "recreate_containers", progress: { image: "nginx:1.29", done: 0, total: 2 } })?.component).toBe(
      "image nginx:1.29",
    );
    expect(toJobProgress({ ...progress, application_id: undefined })).toBeNull();
  });

  it("labels command kinds and knows when progress is still active", () => {
    expect(jobKindLabel("snapshot_components")).toBe("Backup");
    expect(jobKindLabel("restore_components")).toBe("Restoring data");
    expect(jobKindLabel("some_new_kind")).toBe("some new kind");
    expect(isActiveProgress(toJobProgress(progress))).toBe(true);
    expect(isActiveProgress(toJobProgress({ ...progress, state: "failed" }))).toBe(false);
    expect(isActiveProgress(null)).toBe(false);
  });
});
