// SPDX-License-Identifier: Apache-2.0
// @vitest-environment node
import { NextRequest } from "next/server";
import { describe, expect, it, vi } from "vitest";
import { buildCsp, generateNonce, NONCE_HEADER } from "@/lib/csp";
import { config, proxy } from "@/proxy";

describe("buildCsp", () => {
  it("is the console's strict policy with the nonce", () => {
    expect(buildCsp("abc")).toBe(
      "default-src 'self'; script-src 'self' 'nonce-abc' 'strict-dynamic'; style-src 'self' 'unsafe-inline'; " +
        "img-src 'self' data: blob:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; " +
        "form-action 'self'; object-src 'none'",
    );
  });

  it("allows eval only in development", () => {
    expect(buildCsp("n", true)).toContain("script-src 'self' 'nonce-n' 'strict-dynamic' 'unsafe-eval';");
    expect(buildCsp("n", false)).not.toContain("unsafe-eval");
  });
});

describe("generateNonce", () => {
  it("returns 128 random bits in base64, different every time", () => {
    const a = generateNonce();
    expect(a).toMatch(/^[A-Za-z0-9+/]{22}==$/);
    expect(generateNonce()).not.toBe(a);
  });
});

describe("proxy", () => {
  it("sets the policy on the response and hands the nonce to rendering", () => {
    vi.stubEnv("NODE_ENV", "production");
    const res = proxy(new NextRequest("http://console.example/applications"));
    const csp = res.headers.get("content-security-policy")!;
    const nonce = /'nonce-([^']+)'/.exec(csp)?.[1];
    expect(nonce).toBeTruthy();
    expect(csp).not.toContain("unsafe-eval");
    // NextResponse.next({ request: { headers } }) forwards overridden request headers.
    expect(res.headers.get(`x-middleware-request-${NONCE_HEADER}`)).toBe(nonce);
    expect(res.headers.get("x-middleware-request-content-security-policy")).toBe(csp);
  });

  it("leaves the API proxy, static assets and prefetches alone", () => {
    const [m] = config.matcher;
    const re = new RegExp(`^${m!.source}$`);
    expect(re.test("/")).toBe(true);
    expect(re.test("/applications/123")).toBe(true);
    expect(re.test("/api/v1/events")).toBe(false);
    expect(re.test("/_next/static/chunks/main.js")).toBe(false);
    expect(m!.missing.map((x) => x.key)).toEqual(["next-router-prefetch", "purpose"]);
  });
});
