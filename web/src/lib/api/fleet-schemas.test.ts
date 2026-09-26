// SPDX-License-Identifier: Apache-2.0
import { describe, expect, it } from "vitest";
import {
  AGENT_ACTIVE,
  AGENT_PENDING,
  APP_DETAIL,
  APP_SUMMARY,
  COMPOSE_ORIGINAL,
  COMPOSE_RECONSTRUCTED,
  CREATE_TOKEN_RESPONSE,
  INVENTORY,
  REGISTRATION_TOKEN,
} from "@/test/fleet-fixtures";
import {
  AgentInventorySchema,
  AgentListSchema,
  AgentSchema,
  ApplicationDetailSchema,
  ApplicationListSchema,
  ComposeSchema,
  CreateApplicationRequestSchema,
  CreateRegistrationTokenRequestSchema,
  CreateRegistrationTokenResponseSchema,
  DiscoverResponseSchema,
  ReasonRequestSchema,
  RegistrationTokenListSchema,
  UpdateApplicationRequestSchema,
} from "./fleet-schemas";

describe("host schemas", () => {
  it("parses agents in every state, keeping nulls", () => {
    const list = AgentListSchema.parse({ items: [AGENT_ACTIVE, AGENT_PENDING] });
    expect(list.items).toHaveLength(2);
    expect(list.items[1]?.status).toBe("pending");
    expect(list.items[1]?.latency_ms).toBeNull();
    expect(list.items[1]?.docker_reachable).toBeNull();
  });

  it("normalises a null item list to []", () => {
    expect(AgentListSchema.parse({ items: null }).items).toEqual([]);
    expect(ApplicationListSchema.parse({ items: null }).items).toEqual([]);
    expect(RegistrationTokenListSchema.parse({ items: null }).items).toEqual([]);
  });

  it("rejects an unknown agent status", () => {
    expect(AgentSchema.safeParse({ ...AGENT_ACTIVE, status: "zombie" }).success).toBe(false);
  });

  it("parses registration tokens, the create response and the discover response", () => {
    expect(RegistrationTokenListSchema.parse({ items: [REGISTRATION_TOKEN] }).items[0]?.used_at).toBeNull();
    const created = CreateRegistrationTokenResponseSchema.parse(CREATE_TOKEN_RESPONSE);
    expect(created.join_command).toContain("--ca-sha256");
    expect(DiscoverResponseSchema.parse({ workflow_id: "discover-1" }).workflow_id).toBe("discover-1");
  });

  it("parses the inventory summary and defaults nullable lists", () => {
    const inv = AgentInventorySchema.parse(INVENTORY);
    expect(inv.inventory.host.storage_driver).toBe("overlayfs");
    expect(inv.inventory.containers).toHaveLength(1);
    expect(inv.inventory.compose_projects).toEqual([]);
    expect(inv.inventory.warnings).toHaveLength(1);
  });

  it("validates request bodies", () => {
    expect(ReasonRequestSchema.safeParse({ reason: "   " }).success).toBe(false);
    expect(ReasonRequestSchema.safeParse({ reason: "x".repeat(501) }).success).toBe(false);
    expect(ReasonRequestSchema.parse({ reason: "  planned  " })).toEqual({ reason: "planned" });

    const ok = { description: "docker-prod-01", expires_in_hours: 24 };
    expect(CreateRegistrationTokenRequestSchema.safeParse(ok).success).toBe(true);
    expect(CreateRegistrationTokenRequestSchema.safeParse({ ...ok, expires_in_hours: 0 }).success).toBe(false);
    expect(CreateRegistrationTokenRequestSchema.safeParse({ ...ok, expires_in_hours: 169 }).success).toBe(false);
    expect(CreateRegistrationTokenRequestSchema.safeParse({ ...ok, expires_in_hours: 1.5 }).success).toBe(false);
    expect(CreateRegistrationTokenRequestSchema.safeParse({ ...ok, description: "" }).success).toBe(false);
  });
});

describe("application schemas", () => {
  it("parses the list and detail responses", () => {
    expect(ApplicationListSchema.parse({ items: [APP_SUMMARY] }).items[0]?.unprotected_high).toBe(1);

    const d = ApplicationDetailSchema.parse(APP_DETAIL);
    expect(d.analysis?.unprotected[0]?.severity).toBe("high");
    expect(d.analysis?.tmpfs).toEqual([]); // null -> []
    expect(d.analysis?.volumes[0]?.anonymous).toBeFalsy(); // omitted
    expect(d.analysis?.images[1]?.digests).toEqual([]); // omitted (locally built) -> []
    expect(d.containers_detail[0]?.env[0]).toEqual({ key: "MINIO_ROOT_PASSWORD", value: "********", sensitive: true });
    expect(d.containers_detail[0]?.env[1]?.sensitive).toBeFalsy();
    expect(d.manual_containers).toEqual([]); // omitted -> []
  });

  it("accepts a missing application with a null analysis", () => {
    const d = ApplicationDetailSchema.parse({
      ...APP_SUMMARY,
      missing_since: "2026-09-25T18:00:00Z",
      policy_id: null,
      analysis: null,
      containers_detail: [],
      collected_at: null,
    });
    expect(d.analysis).toBeNull();
    expect(d.missing_since).not.toBeNull();
  });

  it("parses original and reconstructed Compose responses", () => {
    const orig = ComposeSchema.parse(COMPOSE_ORIGINAL);
    expect(orig.config_files[0]?.masked).toBe(true);
    expect(orig.secrets).toBeUndefined();
    const rec = ComposeSchema.parse({ ...COMPOSE_RECONSTRUCTED, revealed: true, secrets: { POSTGRES_PASSWORD: "pw" } });
    expect(rec.source).toBe("reconstructed");
    expect(rec.reconstructed).toContain("${POSTGRES_PASSWORD}");
    expect(rec.secrets).toEqual({ POSTGRES_PASSWORD: "pw" });
  });

  it("validates metadata and manual-application requests", () => {
    expect(UpdateApplicationRequestSchema.parse({ environment: "production", criticality: "" })).toEqual({
      environment: "production",
      criticality: "",
    });
    expect(UpdateApplicationRequestSchema.safeParse({ environment: "prod" }).success).toBe(false);
    expect(UpdateApplicationRequestSchema.safeParse({ criticality: "urgent" }).success).toBe(false);

    const req = { name: "cache", host_id: AGENT_ACTIVE.id, containers: ["redis"] };
    expect(CreateApplicationRequestSchema.safeParse(req).success).toBe(true);
    expect(CreateApplicationRequestSchema.safeParse({ ...req, containers: [] }).success).toBe(false);
    expect(CreateApplicationRequestSchema.safeParse({ ...req, name: " " }).success).toBe(false);
  });
});
