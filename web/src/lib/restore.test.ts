// SPDX-License-Identifier: Apache-2.0
import { describe, expect, it } from "vitest";
import { ManifestSchema } from "@/lib/api/protection-schemas";
import {
  PreviewSchema,
  RestoreRunListSchema,
  RestoreRunSchema,
  runPreview,
  runResult,
} from "@/lib/api/restore-schemas";
import {
  checkStart,
  defaultSelection,
  formatDuration,
  remappablePaths,
  restoreSteps,
  selectableComponents,
  stepApplies,
  suggestRemaps,
  validateRemaps,
} from "@/lib/restore";
import {
  PREVIEW_COLLISION,
  PREVIEW_DR,
  PREVIEW_PROD,
  RESTORE_MANIFEST,
  RUN_FAILED,
  RUN_ROLLED_BACK,
  RUN_RUNNING,
  RUN_SUCCEEDED,
} from "@/test/restore-fixtures";

const components = ManifestSchema.parse(RESTORE_MANIFEST).components;
const prod = PreviewSchema.parse(PREVIEW_PROD);
const blocked = PreviewSchema.parse(PREVIEW_COLLISION);
const dr = PreviewSchema.parse(PREVIEW_DR);

describe("restore schemas", () => {
  it("normalises null arrays and defaults of a preview", () => {
    expect(blocked.production_reasons).toEqual([]);
    expect(blocked.target_application_id).toBeFalsy();
    expect(blocked.images[1]?.digest).toBeFalsy();
    const base = {
      recovery_point_id: "rp_x",
      application_name: "a",
      source_host_id: "h",
      target_host_id: "h",
      target_hostname: "host",
    };
    // The contract requires mode / production / blocked and every list (nullable).
    expect(PreviewSchema.safeParse(base).success).toBe(false);
    const nulls = Object.fromEntries(
      ["components", "stop_containers", "create_containers", "networks", "images", "ports", "collisions", "warnings"].map(
        (k) => [k, null],
      ),
    );
    const minimal = PreviewSchema.parse({ ...base, ...nulls, mode: "in_place", production: false, blocked: false });
    expect(minimal).toMatchObject({ mode: "in_place", production: false, blocked: false, collisions: [], components: [] });
  });

  it("parses runs, keeping only well-formed path remaps", () => {
    const run = RestoreRunSchema.parse({ ...RUN_FAILED, components: null, path_remaps: [{ from: "/a", to: "/b" }, { from: 1 }] });
    expect(run.components).toEqual([]);
    expect(run.path_remaps).toEqual([{ from: "/a", to: "/b" }]);
    expect(RestoreRunSchema.parse({ ...RUN_SUCCEEDED, path_remaps: null }).path_remaps).toEqual([]);
    expect(RestoreRunListSchema.parse({ items: null }).items).toEqual([]);
  });

  it("reads the recorded preview and the result document (leniently)", () => {
    const rb = RestoreRunSchema.parse(RUN_ROLLED_BACK);
    expect(runPreview(rb)?.production).toBe(true);
    const result = runResult(rb)!;
    expect(result.rolled_back).toBe(true);
    expect(result.components[0]).toMatchObject({ verify_mismatches: 0, created_volume: false, files: 2143 });
    expect(result.health?.containers[1]).toMatchObject({ ok: false, exit_code: 1 });
    expect(result.images).toEqual([]);
    expect(runResult(RestoreRunSchema.parse(RUN_SUCCEEDED))).toBeNull();
    // The recorded preview is typed by the contract: a malformed one is rejected.
    expect(RestoreRunSchema.safeParse({ ...RUN_SUCCEEDED, preview: { bogus: true } }).success).toBe(false);
  });
});

describe("component selection", () => {
  it("offers volumes, bind mounts and config but never fsmeta, with fsmeta attached to its parent", () => {
    const s = selectableComponents(components);
    expect(s.map((x) => [x.component.name, x.restorable, x.fsmeta?.name ?? null])).toEqual([
      ["config", true, null],
      ["volume:shop_pgdata", true, "fsmeta:volume:shop_pgdata"],
      ["bind:/srv/shop/uploads", false, null],
    ]);
  });

  it("selects every restorable component by default", () => {
    expect(defaultSelection(components)).toEqual(["config", "volume:shop_pgdata"]);
  });
});

describe("path remaps", () => {
  it("suggests one remap per top-level path", () => {
    expect(suggestRemaps(["/srv/shop/uploads", "/srv/shop", "/data/x/", "/srv/shop"])).toEqual([
      { from: "/data/x", to: "/data/x-restored" },
      { from: "/srv/shop", to: "/srv/shop-restored" },
    ]);
    expect(suggestRemaps(["relative", ""])).toEqual([]);
  });

  it("collects the host paths of the selected components", () => {
    const withBind = components.map((c) => (c.kind === "bind_mount" ? { ...c, status: "succeeded" } : c));
    expect(remappablePaths(withBind, ["config", "bind:/srv/shop/uploads"], "/srv/shop")).toEqual([
      "/srv/shop",
      "/srv/shop/uploads",
    ]);
    expect(remappablePaths(withBind, ["volume:shop_pgdata"], "/srv/shop")).toEqual([]);
  });

  it("validates like the server and ignores empty rows", () => {
    expect(validateRemaps([{ from: "/srv/a", to: "/srv/b" }, { from: "", to: " " }])).toEqual({
      ok: true,
      remaps: [{ from: "/srv/a", to: "/srv/b" }],
    });
    expect(validateRemaps([{ from: "srv/a", to: "/b" }])).toEqual({ ok: false, errors: { 0: "Both paths must be absolute (start with /)." } });
    expect(validateRemaps([{ from: "/a", to: "/b/../etc" }])).toMatchObject({ ok: false, errors: { 0: /\.\./ } });
    expect(validateRemaps([{ from: "/a", to: "/b" }, { from: "/a/", to: "/c" }])).toMatchObject({
      ok: false,
      errors: { 1: /already remapped/ },
    });
  });
});

describe("start safeguards", () => {
  const ok = { reason: "INC-48391", confirmation: "Web shop", canProduction: true };

  it("requires the application name typed exactly for a production restore", () => {
    expect(checkStart({ preview: prod, ...ok }).canStart).toBe(true);
    for (const c of ["", "web shop", "Web shop!", "Web"]) {
      const r = checkStart({ preview: prod, ...ok, confirmation: c });
      expect(r.canStart).toBe(false);
      expect(r.confirmationMatches).toBe(false);
      expect(r.blockers).toContain("Type Web shop exactly to confirm.");
    }
    // Surrounding whitespace is trimmed like the server does.
    expect(checkStart({ preview: prod, ...ok, confirmation: " Web shop " }).canStart).toBe(true);
  });

  it("requires a reason of at least three characters for a production restore", () => {
    expect(checkStart({ preview: prod, ...ok, reason: "  ab " })).toMatchObject({ canStart: false, reasonRequired: true });
    expect(checkStart({ preview: prod, ...ok, reason: "abc" }).canStart).toBe(true);
  });

  it("requires restore.production for a production restore", () => {
    const r = checkStart({ preview: prod, ...ok, canProduction: false });
    expect(r.canStart).toBe(false);
    expect(r.blockers[0]).toMatch(/restore\.production/);
  });

  it("needs neither reason nor confirmation outside production, and never starts when blocked", () => {
    expect(checkStart({ preview: dr, reason: "", confirmation: "", canProduction: false })).toMatchObject({
      canStart: true,
      reasonRequired: false,
      confirmationRequired: false,
    });
    const b = checkStart({ preview: blocked, reason: "x", confirmation: "", canProduction: true });
    expect(b.canStart).toBe(false);
    expect(b.blockers).toEqual(["The restore is blocked by 2 collisions."]);
  });
});

describe("step indicator", () => {
  const running = RestoreRunSchema.parse(RUN_RUNNING);

  it("leaves out steps that do not apply to an in-place restore", () => {
    const steps = restoreSteps(running, prod);
    expect(steps.map((s) => s.step)).toEqual([
      "agent-access",
      "images",
      "stop-application",
      "restore-data",
      "start-application",
      "health-check",
      "commit",
    ]);
    expect(steps.map((s) => s.status)).toEqual(["done", "done", "done", "current", "pending", "pending", "pending"]);
  });

  it("includes access grant and container re-creation for an alternate host", () => {
    const run = { ...running, target_host_id: dr.target_host_id, state: "requested" as const, step: null };
    const steps = restoreSteps(run, dr);
    expect(steps.map((s) => s.step)).toEqual([
      "grant-access",
      "agent-access",
      "images",
      "restore-data",
      "recreate-containers",
      "start-application",
      "health-check",
      "commit",
    ]);
    expect(steps.every((s) => s.status === "pending")).toBe(true);
    expect(stepApplies("restore-database", run, dr)).toBe(false);
    expect(stepApplies("restore-database", run, { ...dr, components: [{ ...dr.components[0]!, action: "load_dump" }] })).toBe(true);
  });

  it("marks where a failed or rolled-back restore stopped, and all steps of a success as done", () => {
    const rb = RestoreRunSchema.parse(RUN_ROLLED_BACK);
    const steps = restoreSteps(rb, runPreview(rb));
    expect(steps.find((s) => s.status === "failed")?.step).toBe("health-check");
    expect(steps.at(-1)?.status).toBe("pending");
    const ok = RestoreRunSchema.parse({ ...RUN_SUCCEEDED, preview: PREVIEW_DR });
    expect(restoreSteps(ok, runPreview(ok)).every((s) => s.status === "done")).toBe(true);
  });

  it("assumes every step without a preview and keeps a reported step even if it looked inapplicable", () => {
    const noPreview = restoreSteps({ ...running, step: "images" }, null);
    expect(noPreview).toHaveLength(9); // all but grant-access (same host)
    const odd = restoreSteps({ ...running, step: "restore-database" }, prod);
    expect(odd.find((s) => s.step === "restore-database")?.status).toBe("current");
  });
});

describe("formatDuration", () => {
  it("formats seconds, minutes and hours, and running durations against now", () => {
    expect(formatDuration("2026-09-25T10:00:00Z", "2026-09-25T10:00:08Z")).toBe("8 s");
    expect(formatDuration("2026-09-25T10:00:00Z", "2026-09-25T10:03:12Z")).toBe("3 min 12 s");
    expect(formatDuration("2026-09-25T10:00:00Z", "2026-09-25T11:04:00Z")).toBe("1 h 4 min");
    expect(formatDuration("2026-09-25T10:00:00Z", null, new Date("2026-09-25T10:00:30Z"))).toBe("30 s");
    expect(formatDuration(null, null)).toBeNull();
  });
});
