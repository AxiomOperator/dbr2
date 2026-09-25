// SPDX-License-Identifier: Apache-2.0
import { describe, expect, it } from "vitest";
import {
  AddEscrowRecipientRequestSchema,
  AlertListSchema,
  BackupSettingsSchema,
  ConfirmEscrowRequestSchema,
  CreateRepositoryRequestSchema,
  CreateRepositoryResponseSchema,
  HostSettingsSchema,
  looksLikePrivateKey,
  ManifestSchema,
  normalizeConfirmationCode,
  RecoveryPointListSchema,
  RepositoryListSchema,
  UpdateHostSettingsRequestSchema,
} from "@/lib/api/protection-schemas";
import {
  ALERTS,
  BACKUP_SETTINGS,
  CREATE_REPO_RESPONSE,
  HOST_SETTINGS,
  MANIFEST,
  REPO_AWAITING,
  REPO_READY,
  REPO_UNAVAILABLE,
  RP_COMMITTED,
  RP_FAILED,
  RP_PARTIAL,
} from "@/test/protection-fixtures";

describe("normalizeConfirmationCode", () => {
  it("is case-, space- and dash-insensitive", () => {
    for (const input of ["K7QX-M2DA-PL4W-ZT6R", "k7qx-m2da-pl4w-zt6r", "k7qx m2da pl4w zt6r", " K7QXM2DAPL4WZT6R ", "k7-qxm2-dapl-4wzt-6r"]) {
      expect(normalizeConfirmationCode(input)).toBe("K7QX-M2DA-PL4W-ZT6R");
    }
  });

  it("rejects wrong lengths and characters outside base32", () => {
    for (const bad of ["", "K7QX-M2DA-PL4W", "K7QX-M2DA-PL4W-ZT6R-AAAA", "K7QX-M2DA-PL4W-ZT61", "K7QX-M2DA-PL4W-ZT6!"]) {
      expect(normalizeConfirmationCode(bad)).toBeNull();
    }
  });

  it("is applied by the confirm request schema", () => {
    expect(ConfirmEscrowRequestSchema.parse({ confirmation_code: "k7qx m2da pl4w zt6r" })).toEqual({
      confirmation_code: "K7QX-M2DA-PL4W-ZT6R",
    });
    const bad = ConfirmEscrowRequestSchema.safeParse({ confirmation_code: "nope" });
    expect(bad.success).toBe(false);
    expect(bad.error?.issues[0]?.message).toMatch(/XXXX-XXXX-XXXX-XXXX/);
  });
});

describe("escrow recipients", () => {
  it("detects private identities", () => {
    expect(looksLikePrivateKey("AGE-SECRET-KEY-1QQPQ9X0EXAMPLE")).toBe(true);
    expect(looksLikePrivateKey("  age-secret-key-1qqpq  ")).toBe(true);
    expect(looksLikePrivateKey("-----BEGIN OPENSSH PRIVATE KEY-----\nb3Blbn...")).toBe(true);
    expect(looksLikePrivateKey("age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5sfn9aqmcac8p")).toBe(false);
  });

  it("rejects private keys and non-keys before sending", () => {
    const priv = AddEscrowRecipientRequestSchema.safeParse({ name: "Ada", public_key: "AGE-SECRET-KEY-1ABC" });
    expect(priv.success).toBe(false);
    expect(priv.error?.issues[0]?.message).toMatch(/PRIVATE identity/);
    const junk = AddEscrowRecipientRequestSchema.safeParse({ name: "Ada", public_key: "hello" });
    expect(junk.error?.issues[0]?.message).toMatch(/age public key/);
    expect(
      AddEscrowRecipientRequestSchema.parse({ name: " Ada ", public_key: " age1ql3z7hjy54pw3hy \n" }),
    ).toEqual({ name: "Ada", public_key: "age1ql3z7hjy54pw3hy" });
  });
});

describe("Repository schemas", () => {
  it("parses ready, awaiting and unreachable Repositories", () => {
    const [ready, awaiting, unavailable] = RepositoryListSchema.parse({
      items: [REPO_READY, REPO_AWAITING, REPO_UNAVAILABLE],
    }).items;
    expect(ready?.live?.storage_healthy).toBe(true);
    expect(ready?.usage_by_host).toHaveLength(2);
    expect(awaiting?.status).toBe("awaiting_escrow");
    expect(awaiting?.usage_by_host).toEqual([]);
    expect(unavailable?.live).toBeNull();
    expect(unavailable?.live_error).toMatch(/connection refused/);
  });

  it("defaults a missing internal_server_url and treats a missing live as null", () => {
    const rest: Record<string, unknown> = { ...REPO_READY };
    delete rest.internal_server_url;
    delete rest.live;
    const [r] = RepositoryListSchema.parse({ items: [rest] }).items;
    expect(r?.internal_server_url).toBe("");
    expect(r?.live).toBeNull();
  });

  it("parses the create response", () => {
    const res = CreateRepositoryResponseSchema.parse(CREATE_REPO_RESPONSE);
    expect(res.escrow_filename).toMatch(/\.age$/);
  });

  it("validates the create request", () => {
    const ok = CreateRepositoryRequestSchema.safeParse({
      name: "nas01",
      backend: "nfs",
      management_url: "http://dbr2-reposerver:8091",
      server_url: "https://backup.example.lan:51515",
      internal_server_url: "",
    });
    expect(ok.success).toBe(true);
    const bad = CreateRepositoryRequestSchema.safeParse({
      name: "nas01",
      backend: "nfs",
      management_url: "http://dbr2-reposerver:8091",
      server_url: "backup.example.lan",
    });
    expect(bad.error?.issues[0]?.message).toMatch(/server URL must be an http/);
  });
});

describe("backup schemas", () => {
  it("parses recovery points in every state and a manifest", () => {
    const items = RecoveryPointListSchema.parse({ items: [RP_COMMITTED, RP_PARTIAL, RP_FAILED] }).items;
    expect(items.map((r) => [r.state, r.status])).toEqual([
      ["committed", "complete"],
      ["committed", "partial"],
      ["failed", null],
    ]);
    const m = ManifestSchema.parse(MANIFEST);
    expect(m.components.map((c) => c.kind)).toEqual(["config", "volume", "fsmeta"]);
    expect(m.components[2]?.parent).toBe("volume:shop_pgdata");
  });

  it("normalises nullable lists in backup settings", () => {
    const s = BackupSettingsSchema.parse(BACKUP_SETTINGS);
    expect(s.post_hooks).toEqual([]);
    expect(s.excluded_components).toEqual([]);
    expect(s.pre_hooks[0]?.command).toEqual(["sh", "-c", 'psql -U shop -c "CHECKPOINT"']);
  });

  it("parses alerts with int64 ids", () => {
    expect(AlertListSchema.parse({ items: ALERTS }).items.map((a) => a.id)).toEqual([43, 44, 42, 41]);
  });

  it("requires both window bounds or neither for host limits", () => {
    expect(HostSettingsSchema.parse(HOST_SETTINGS).backup_window_start).toBe(1320);
    const half = UpdateHostSettingsRequestSchema.safeParse({ ...HOST_SETTINGS, backup_window_end: null });
    expect(half.error?.issues[0]?.message).toMatch(/both the start and the end/);
    expect(
      UpdateHostSettingsRequestSchema.safeParse({ ...HOST_SETTINGS, backup_window_start: null, backup_window_end: null })
        .success,
    ).toBe(true);
    expect(UpdateHostSettingsRequestSchema.safeParse({ ...HOST_SETTINGS, max_concurrent_jobs: 17 }).success).toBe(false);
  });
});
