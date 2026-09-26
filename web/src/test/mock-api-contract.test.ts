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
  AlertListSchema,
  BackupSettingsSchema,
  EscrowRecipientListSchema,
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
    env: { ...process.env, MOCK_API_PORT: String(port), MOCK_BACKUP_MS: "3000" },
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
    for (const r of rps) {
      const d = await get(`/recovery-points/${r.id}`, RecoveryPointSchema);
      if (d.manifest !== undefined) expect(ManifestSchema.safeParse(d.manifest).success).toBe(true);
    }
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
