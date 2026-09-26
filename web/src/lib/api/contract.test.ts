// SPDX-License-Identifier: Apache-2.0
import { describe, expect, it } from "vitest";
import { z } from "zod";
import { contract } from "@/lib/api/contract";
import { zApplicationSummary, zProtection } from "@/lib/api/generated/zod.gen";

describe("contract()", () => {
  it("normalises nullable and optional arrays to [] at every depth", () => {
    const s = contract(
      z.object({
        a: z.array(z.string()).nullable(),
        b: z.array(z.string()).optional(),
        c: z.array(z.object({ d: z.array(z.int()).nullish() })).nullable(),
        e: z.object({ f: z.array(z.string()).nullable() }).nullable(),
        g: z.string().nullable(),
      }),
    );
    expect(s.parse({ a: null, c: [{ d: null }, {}], e: { f: null }, g: null })).toEqual({
      a: [],
      b: [],
      c: [{ d: [] }, { d: [] }],
      e: { f: [] },
      g: null,
    });
    expect(s.parse({ a: ["x"], b: ["y"], c: null, e: null, g: "z" })).toEqual({ a: ["x"], b: ["y"], c: [], e: null, g: "z" });
  });

  it("keeps the contract strict everywhere else and ignores unknown keys", () => {
    const protection = contract(zProtection);
    const valid = {
      status: "protected",
      reasons: null,
      last_backup_at: "2026-09-25T10:00:00Z",
      last_attempt_at: null,
      components: null,
      components_total: 0,
      components_protected: 0,
      unresolved_dependencies: 0,
      added_in_a_later_minor: true,
    };
    const out = protection.parse(valid);
    expect(out.reasons).toEqual([]);
    expect(out).not.toHaveProperty("added_in_a_later_minor");
    expect(protection.safeParse({ ...valid, status: "fine" }).success).toBe(false);
    expect(protection.safeParse({ ...valid, last_backup_at: "yesterday" }).success).toBe(false);
    expect(protection.safeParse({ ...valid, components_total: 1.5 }).success).toBe(false);
  });

  it("types the result with required arrays", () => {
    const s = contract(zApplicationSummary);
    type Out = z.infer<typeof s>;
    const components: NonNullable<Out["protection"]>["components"] = [];
    expect(components).toEqual([]);
    expect(s.safeParse({}).success).toBe(false);
  });
});
