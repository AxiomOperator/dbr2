// SPDX-License-Identifier: Apache-2.0
// @vitest-environment node
//
// The mock API (scripts/mock-api.mjs) must speak the real contract: every
// response it serves is validated with the generated OpenAPI Zod schemas the
// console uses (src/lib/api/generated via ./contract.ts), so the mock cannot
// drift from api/openapi.yaml unnoticed. Also checks the SSE stream.

import { spawn, type ChildProcess } from "node:child_process";
import { createServer } from "node:net";
import { resolve } from "node:path";
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import type { z } from "zod";
import {
  AgentInventorySchema,
  AgentListSchema,
  AgentSchema,
  ApplicationDetailSchema,
  ApplicationListSchema,
  ComposeSchema,
  FleetContainerListSchema,
  FleetVolumeListSchema,
  RegistrationTokenListSchema,
} from "@/lib/api/fleet-schemas";
import { zJobProgressEvent } from "@/lib/api/generated/zod.gen";
import {
  ContractListSchema,
  ContractSchema,
  DeliveryListSchema,
  NotificationChannelListSchema,
  NotificationChannelSchema,
  PolicyDetailSchema,
  PolicyListSchema,
  PolicySchema,
  SmtpSettingsSchema,
  TestResultSchema,
} from "@/lib/api/policy-schemas";
import {
  AlertListSchema,
  BackupSettingsSchema,
  DrillListSchema,
  DrillSchema,
  EscrowHealthSchema,
  EscrowRecipientListSchema,
  PlatformBackupListSchema,
  RegeneratedEscrowSchema,
  VerificationDetailsSchema,
  WorkflowResponseSchema,
  HostSettingsSchema,
  JobListSchema,
  ManifestSchema,
  RecoveryPointListSchema,
  RecoveryPointSchema,
  RepositoryListSchema,
  RepositorySchema,
} from "@/lib/api/protection-schemas";
import { PreviewSchema, RestoreRunListSchema, RestoreRunSchema, runResult } from "@/lib/api/restore-schemas";
import {
  AuditEventPageSchema,
  AuthProvidersSchema,
  LivenessSchema,
  MeSchema,
  ReadinessSchema,
  VersionSchema,
} from "@/lib/api/schemas";
import { GroupMappingListSchema, RoleListSchema, UserListSchema } from "@/lib/api/users-schemas";

const WEB = resolve(__dirname, "../..");
let child: ChildProcess;
let base = "";
let cookie = "";

async function freePort(): Promise<number> {
  return new Promise((ok, fail) => {
    const srv = createServer();
    srv.once("error", fail);
    srv.listen(0, "127.0.0.1", () => {
      const addr = srv.address();
      const port = typeof addr === "object" && addr ? addr.port : 0;
      srv.close(() => ok(port));
    });
  });
}

async function call(method: string, path: string, body?: unknown): Promise<Response> {
  return fetch(`${base}/api/v1${path}`, {
    method,
    headers: { cookie, "content-type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
}

/** A write that must succeed with `status`; the body is validated with `schema` when given. */
async function write<T>(method: string, path: string, body: unknown, status: number, schema?: z.ZodType<T>): Promise<T> {
  const res = await call(method, path, body);
  expect(res.status, `${method} ${path}`).toBe(status);
  if (!schema) return undefined as T;
  const parsed = schema.safeParse(await res.json());
  if (!parsed.success) throw new Error(`${method} ${path} violates the contract: ${parsed.error.message}`);
  return parsed.data;
}

async function get<T>(path: string, schema: z.ZodType<T>): Promise<T> {
  const res = await fetch(`${base}/api/v1${path}`, { headers: { cookie } });
  expect(res.status, `GET ${path}`).toBeLessThan(300);
  const parsed = schema.safeParse(await res.json());
  if (!parsed.success) throw new Error(`GET ${path} violates the contract: ${parsed.error.message}`);
  return parsed.data;
}

beforeAll(async () => {
  const port = await freePort();
  base = `http://127.0.0.1:${port}`;
  child = spawn(process.execPath, ["scripts/mock-api.mjs"], {
    cwd: WEB,
    env: { ...process.env, MOCK_API_PORT: String(port), MOCK_BACKUP_MS: "3000", MOCK_VERIFY_MS: "200", MOCK_PLATFORM_MS: "200" },
    stdio: ["ignore", "pipe", "inherit"],
  });
  await new Promise<void>((ok, fail) => {
    const timer = setTimeout(() => fail(new Error("mock API did not start")), 10_000);
    child.stdout!.on("data", (b: Buffer) => {
      if (b.toString().includes("listening")) {
        clearTimeout(timer);
        ok();
      }
    });
  });
  const login = await fetch(`${base}/api/v1/auth/login`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ username: "admin", password: "correct-horse-battery" }),
  });
  expect(login.status).toBe(200);
  cookie = login.headers
    .getSetCookie()
    .map((c) => c.split(";")[0])
    .join("; ");
}, 20_000);

afterAll(() => {
  child?.kill();
});

describe("mock API conforms to api/openapi.yaml", () => {
  it("serves the system, auth and audit endpoints", async () => {
    await get("/version", VersionSchema);
    await get("/health/live", LivenessSchema);
    await get("/health/ready", ReadinessSchema);
    await get("/auth/providers", AuthProvidersSchema);
    const me = await get("/auth/me", MeSchema);
    expect(me.permissions).toContain("user.manage");
    await get("/audit-events?limit=200", AuditEventPageSchema);
  });

  it("serves hosts, applications (with protection), containers and volumes", async () => {
    const { items: agents } = await get("/agents", AgentListSchema);
    await get("/agents/registration-tokens", RegistrationTokenListSchema);
    for (const a of agents) {
      await get(`/agents/${a.id}`, AgentSchema);
      await get(`/agents/${a.id}/settings`, HostSettingsSchema);
      if (a.status !== "pending") await get(`/agents/${a.id}/inventory`, AgentInventorySchema);
    }
    const { items: apps } = await get("/applications", ApplicationListSchema);
    expect(apps.every((a) => a.protection)).toBe(true);
    expect(new Set(apps.map((a) => a.protection?.status))).toEqual(
      new Set(["protected", "at_risk", "failed", "unprotected"]),
    );
    for (const a of apps) {
      const d = await get(`/applications/${a.id}`, ApplicationDetailSchema);
      await get(`/applications/${a.id}/backup-settings`, BackupSettingsSchema);
      if (d.analysis) await get(`/applications/${a.id}/compose`, ComposeSchema);
    }
    const containers = await get("/containers", FleetContainerListSchema);
    expect(containers.items.length).toBeGreaterThan(0);
    const volumes = await get(`/volumes?host_id=${agents[0]!.id}`, FleetVolumeListSchema);
    expect(volumes.items.some((v) => v.class === "unused")).toBe(true);
  });

  it("serves Repositories, recovery points (with manifests), alerts and jobs", async () => {
    await get("/escrow/recipients", EscrowRecipientListSchema);
    const { items: repos } = await get("/repositories", RepositoryListSchema);
    for (const r of repos) await get(`/repositories/${r.id}`, RepositorySchema);
    const { items: rps } = await get("/recovery-points?limit=500", RecoveryPointListSchema);
    expect(new Set(rps.map((r) => r.state))).toContain("deleted");
    expect(rps.some((r) => r.delete_after !== null && r.state === "committed")).toBe(true);
    for (const r of rps) {
      const d = await get(`/recovery-points/${r.id}`, RecoveryPointSchema);
      if (d.manifest !== undefined) expect(ManifestSchema.safeParse(d.manifest).success).toBe(true);
      if (d.verification !== "unverified") {
        expect(VerificationDetailsSchema.safeParse(d.verification_details).success, `verification_details of ${r.id}`).toBe(true);
      }
    }
    const failed = rps.find((r) => r.verification === "verification_failed")!;
    const fd = VerificationDetailsSchema.parse((await get(`/recovery-points/${failed.id}`, RecoveryPointSchema)).verification_details);
    expect(fd.components.some((c) => c.errors.length === 2)).toBe(true);
    const shop = rps.find((r) => r.application_name === "Web shop" && r.status === "complete" && r.state === "committed")!;
    const sm = ManifestSchema.parse((await get(`/recovery-points/${shop.id}`, RecoveryPointSchema)).manifest);
    expect(sm.contract?.satisfied).toBe(true);
    const mft = ManifestSchema.parse((await get(`/recovery-points/${failed.id}`, RecoveryPointSchema)).manifest);
    expect(mft.components.find((c) => c.kind === "database")?.database?.engine).toBe("postgresql");
    expect(repos.some((r) => r.is_system)).toBe(true);
    expect(repos.some((r) => r.status === "pending_deletion")).toBe(true);
    await get("/alerts?all=true", AlertListSchema);
    const jobs = await get("/jobs?limit=500", JobListSchema);
    expect(new Set(jobs.items.map((j) => j.type))).toEqual(new Set(["backup", "restore"]));
  });

  it("serves restores and previews", async () => {
    const { items: runs } = await get("/restores", RestoreRunListSchema);
    for (const r of runs) {
      const d = await get(`/restores/${r.id}`, RestoreRunSchema);
      if (d.result !== undefined) expect(runResult(d)).not.toBeNull();
    }
    const { items: rps } = await get("/recovery-points?state=committed", RecoveryPointListSchema);
    const res = await fetch(`${base}/api/v1/recovery-points/${rps[0]!.id}/restore-preview`, {
      method: "POST",
      headers: { cookie, "content-type": "application/json" },
      body: "{}",
    });
    expect(PreviewSchema.safeParse(await res.json()).success).toBe(true);
  });

  it("serves users, roles and group mappings", async () => {
    const users = await get("/users", UserListSchema);
    expect(users.items.some((u) => u.disabled)).toBe(true);
    await get("/roles", RoleListSchema);
    await get("/oidc/group-mappings", GroupMappingListSchema);
  });

  it("serves policies, contracts, notification channels and SMTP settings", async () => {
    const { items: policies } = await get("/policies", PolicyListSchema);
    expect(policies.map((p) => p.name)).toEqual(expect.arrayContaining(["Nightly production", "Hourly critical", "Weekly archive"]));
    for (const p of policies) {
      const d = await get(`/policies/${p.id}`, PolicyDetailSchema);
      expect(d.assigned_applications.length).toBe(p.applications);
      expect(d.next_run === null).toBe(!p.enabled);
    }
    const { items: contracts } = await get("/contracts", ContractListSchema);
    expect(new Set(contracts.map((c) => c.state))).toEqual(new Set(["satisfied", "violated", "unknown"]));
    for (const c of contracts) await get(`/applications/${c.application_id}/contract`, ContractSchema);
    const { items: channels } = await get("/notification-channels", NotificationChannelListSchema);
    expect(channels.some((c) => c.last_error)).toBe(true);
    for (const c of channels) {
      const { items } = await get(`/notification-channels/${c.id}/deliveries?limit=50`, DeliveryListSchema);
      expect(items.length).toBeGreaterThan(0);
    }
    const smtp = await get("/settings/smtp", SmtpSettingsSchema);
    expect(smtp.password_set).toBe(true);
  });

  it("serves escrow health, drills and platform backups", async () => {
    const health = await get("/escrow/health", EscrowHealthSchema);
    expect(health.healthy).toBe(false);
    expect(new Set(health.problems.map((p) => p.code))).toEqual(
      new Set(["recipients_changed", "not_confirmed", "reconfirm_due", "drill_due"]),
    );
    await get("/escrow/drills", DrillListSchema);
    const { items: runs } = await get("/platform/backups?limit=50", PlatformBackupListSchema);
    expect(new Set(runs.map((r) => r.state))).toEqual(new Set(["succeeded", "partial", "failed"]));
  });

  it("accepts the Phase 7 write flows", async () => {
    const bad = await call("POST", "/policies", { name: "Bad", schedule: "61 * * * *", enabled: true, retention: {} });
    expect(bad.status).toBe(400);
    expect((await bad.json()).detail).toMatch(/^schedule must be hourly, daily, weekly, monthly or a 5-field cron expression: /);
    const tz = await call("POST", "/policies", { name: "Bad tz", schedule: "daily", timezone: "Mars/Olympus", enabled: true, retention: {} });
    expect((await tz.json()).detail).toBe('unknown timezone "Mars/Olympus"');
    const p = await write(
      "POST",
      "/policies",
      { name: "Contract test", schedule: "30 22 * * MON-FRI", timezone: "Europe/Berlin", enabled: true, retention: { keep_last: 3 } },
      201,
      PolicySchema,
    );
    expect(p.retention).toEqual({ keep_last: 3, keep_hourly: 0, keep_daily: 14, keep_weekly: 8, keep_monthly: 12, keep_yearly: 0 });
    expect(p.next_run).not.toBeNull();
    await write("PUT", `/policies/${p.id}`, { name: "Contract test", schedule: "weekly", enabled: false, retention: {} }, 200, PolicySchema);
    const { items: apps } = await get("/applications", ApplicationListSchema);
    const nginx = apps.find((a) => a.name === "nginx-proxy")!;
    await write("PUT", `/applications/${nginx.id}/policy`, { policy_id: p.id }, 204);
    expect((await get(`/policies/${p.id}`, PolicyDetailSchema)).assigned_applications.map((a) => a.id)).toEqual([nginx.id]);
    await write("PUT", `/applications/${nginx.id}/policy`, { policy_id: null }, 204);
    await write("DELETE", `/policies/${p.id}`, undefined, 204);

    const missing = await call("GET", `/applications/${nginx.id}/contract`);
    expect(missing.status).toBe(404);
    const c = await write("PUT", `/applications/${nginx.id}/contract`, { max_rpo_minutes: 120, required_components: ["config"] }, 200, ContractSchema);
    expect(c.max_rpo_minutes).toBe(120);
    await write("DELETE", `/applications/${nginx.id}/contract`, undefined, 204);

    const { items: rps } = await get("/recovery-points?state=committed", RecoveryPointListSchema);
    const rp = rps.find((r) => r.delete_after === null)!;
    const wrong = await call("POST", `/recovery-points/${rp.id}/delete`, { confirmation: "nope", reason: "cleanup" });
    expect(wrong.status).toBe(400);
    const del = await write("POST", `/recovery-points/${rp.id}/delete`, { confirmation: rp.application_name, reason: "cleanup" }, 200, RecoveryPointSchema);
    expect(del.delete_after).not.toBeNull();
    expect(del.state).toBe("committed");
    expect((await write("POST", `/recovery-points/${rp.id}/undelete`, undefined, 200, RecoveryPointSchema)).delete_after).toBeNull();

    const { items: repos } = await get("/repositories", RepositoryListSchema);
    const old = repos.find((r) => r.name === "old-nas02")!;
    expect((await write("POST", `/repositories/${old.id}/undelete`, undefined, 200, RepositorySchema)).status).toBe("ready");
    const pending = await write("POST", `/repositories/${old.id}/delete`, { confirmation: old.name, reason: "hardware replaced" }, 200, RepositorySchema);
    expect(pending.status).toBe("pending_deletion");
    expect(pending.delete_after).not.toBeNull();

    const hook = await write(
      "POST",
      "/notification-channels",
      { kind: "webhook", name: "CT hook", config: { url: "https://hooks.example.com/dbr2" }, secret: "0123456789abcdef", events: ["backup.*"] },
      201,
      NotificationChannelSchema,
    );
    expect(hook.secret_set).toBe(true);
    expect(hook.min_severity).toBe("warning");
    expect((await write("POST", `/notification-channels/${hook.id}/test`, undefined, 200, TestResultSchema)).delivered).toBe(true);
    const kept = await write(
      "PUT",
      `/notification-channels/${hook.id}`,
      { name: "CT hook", enabled: true, config: { url: "https://fail.example.com/x" }, events: ["*"], min_severity: "info" },
      200,
      NotificationChannelSchema,
    );
    expect(kept.secret_set).toBe(true);
    const test = await write("POST", `/notification-channels/${hook.id}/test`, undefined, 200, TestResultSchema);
    expect(test.delivered).toBe(false);
    expect(test.error).toBeTruthy();
    const removed = await write(
      "PUT",
      `/notification-channels/${hook.id}`,
      { name: "CT hook", enabled: true, config: { url: "https://hooks.example.com/dbr2" }, events: [], min_severity: "info", secret: "" },
      200,
      NotificationChannelSchema,
    );
    expect(removed.secret_set).toBe(false);
    const badPattern = await call("POST", "/notification-channels", { kind: "email", name: "x", config: { to: ["a@example.com"] }, events: ["Backup!"] });
    expect(badPattern.status).toBe(400);
    await write("DELETE", `/notification-channels/${hook.id}`, undefined, 204);

    const smtp = await write("PUT", "/settings/smtp", { host: "smtp.example.com", port: 465, from: "DBR2 <dbr2@example.com>", tls: "tls" }, 200, SmtpSettingsSchema);
    expect(smtp.password_set).toBe(true);
    const plain = await call("PUT", "/settings/smtp", { host: "smtp.example.com", port: 25, from: "a@example.com", tls: "none" });
    expect(plain.status).toBe(400);
  });

  it("accepts the Phase 9 write flows", async () => {
    const { items: repos } = await get("/repositories", RepositoryListSchema);
    const lab = repos.find((r) => r.name === "lab-scratch")!;
    const nas = repos.find((r) => r.name === "nas01-backups")!;

    const regen = await write("POST", `/repositories/${nas.id}/escrow/regenerate`, undefined, 200, RegeneratedEscrowSchema);
    expect(regen.repository.escrow_confirmed_at).toBeNull();
    const code = /confirmation_code ([A-Z2-7-]+)/.exec(regen.escrow_package)![1]!;
    const confirmed = await write("POST", `/repositories/${nas.id}/escrow/confirm`, { confirmation_code: code }, 200, RepositorySchema);
    expect(confirmed.escrow_confirmed_at).not.toBeNull();

    const drill = await write("POST", "/escrow/drills", undefined, 201, DrillSchema);
    const drillCode = /confirmation_code ([A-Z2-7-]+)/.exec(drill.package ?? "")![1]!;
    const wrong = await call("POST", `/escrow/drills/${drill.id}/complete`, { confirmation_code: "AAAA-BBBB-CCCC-DDDD" });
    expect(wrong.status).toBe(400);
    const done = await write("POST", `/escrow/drills/${drill.id}/complete`, { confirmation_code: drillCode.toLowerCase() }, 200, DrillSchema);
    expect(done.completed_at).not.toBeNull();
    const health = await get("/escrow/health", EscrowHealthSchema);
    expect(health.problems.map((p) => p.code)).not.toContain("drill_due");
    expect(health.problems.some((p) => p.code === "recipients_changed")).toBe(false);

    const v = await write("POST", `/repositories/${nas.id}/verify`, { read_percent: 5 }, 202, WorkflowResponseSchema);
    expect(v.workflow_id).toBe(`repository/${nas.id}/verify`);
    const offline = await call("POST", `/repositories/${repos.find((r) => r.status === "unavailable")!.id}/verify`, {});
    expect(offline.status).toBe(409);

    await write("POST", "/platform/backups", undefined, 202, WorkflowResponseSchema);
    expect((await call("POST", "/platform/backups")).status).toBe(409);
    const { items: runs } = await get("/platform/backups", PlatformBackupListSchema);
    expect(runs[0]!.state).toBe("running");

    await expect
      .poll(async () => (await get("/platform/backups", PlatformBackupListSchema)).items[0]!.state, { timeout: 5_000 })
      .toBe("succeeded");
    await expect.poll(async () => (await get(`/repositories/${nas.id}`, RepositorySchema)).last_verified_at, { timeout: 5_000 }).not.toBeNull();

    const sys = await write("PUT", `/repositories/${lab.id}/system`, undefined, 200, RepositorySchema);
    expect(sys.is_system).toBe(true);
    expect((await get(`/repositories/${nas.id}`, RepositorySchema)).is_system).toBe(false);
    await write("PUT", `/repositories/${nas.id}/system`, undefined, 200, RepositorySchema);
  });

  it("streams contract-shaped events while a backup runs", async () => {
    const { items: apps } = await get("/applications", ApplicationListSchema);
    const target = apps.find((a) => a.name === "nginx-proxy")!;
    const ctrl = new AbortController();
    const stream = await fetch(`${base}/api/v1/events?types=job.progress,backup.updated`, {
      headers: { cookie, accept: "text/event-stream" },
      signal: ctrl.signal,
    });
    expect(stream.headers.get("content-type")).toContain("text/event-stream");
    const start = await fetch(`${base}/api/v1/applications/${target.id}/backups`, {
      method: "POST",
      headers: { cookie, "content-type": "application/json" },
      body: "{}",
    });
    expect(start.status).toBe(202);

    const reader = stream.body!.getReader();
    const decoder = new TextDecoder();
    let buf = "";
    const seen: { event: string; data: unknown }[] = [];
    while (!seen.some((e) => e.event === "backup.updated" && (e.data as { state: string }).state === "committed")) {
      const { value, done } = await reader.read();
      if (done) break;
      buf += decoder.decode(value, { stream: true });
      let i;
      while ((i = buf.indexOf("\n\n")) >= 0) {
        const frame = buf.slice(0, i);
        buf = buf.slice(i + 2);
        const event = /^event: (.*)$/m.exec(frame)?.[1];
        const data = /^data: (.*)$/m.exec(frame)?.[1];
        if (event && data) seen.push({ event, data: JSON.parse(data) });
      }
    }
    ctrl.abort();
    const progress = seen.filter((e) => e.event === "job.progress").map((e) => zJobProgressEvent.parse(e.data));
    expect(progress.some((p) => p.application_id === target.id && p.state === "running")).toBe(true);
    expect(seen.every((e) => e.event === "job.progress" || e.event === "backup.updated")).toBe(true);
  }, 20_000);
});
