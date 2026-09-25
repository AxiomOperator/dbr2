// SPDX-License-Identifier: Apache-2.0
import { describe, expect, it, vi } from "vitest";
import { z } from "zod";
import {
  ApiError,
  apiRequest,
  ErrorCodes,
  errorMessage,
  formatRetryAfter,
  parseRetryAfter,
} from "./client";

function problemResponse(status: number, body: object, headers: Record<string, string> = {}) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/problem+json", ...headers },
  });
}

async function captureError(p: Promise<unknown>): Promise<ApiError> {
  try {
    await p;
  } catch (err) {
    expect(err).toBeInstanceOf(ApiError);
    return err as ApiError;
  }
  throw new Error("expected the request to fail");
}

describe("apiRequest", () => {
  it("sends same-origin JSON requests to /api/v1 and validates the response", async () => {
    const fetchImpl = vi.fn<typeof fetch>(async () =>
      Response.json({ platform: "0.1.0.0", components: { api: "0.1.0.0" } }),
    );
    const schema = z.object({ platform: z.string(), components: z.record(z.string(), z.string()) });

    const data = await apiRequest("/version", { schema, fetchImpl });

    expect(data.platform).toBe("0.1.0.0");
    const [url, init] = fetchImpl.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/v1/version");
    expect(init.credentials).toBe("same-origin");
    expect(init.method).toBe("GET");
  });

  it("serialises bodies and query strings", async () => {
    const fetchImpl = vi.fn<typeof fetch>(async () => new Response(null, { status: 204 }));
    await apiRequest("/auth/login", { method: "POST", body: { a: 1 }, fetchImpl });
    await apiRequest("/audit-events", { query: { limit: 50, cursor: null }, fetchImpl });

    const [, postInit] = fetchImpl.mock.calls[0] as unknown as [string, RequestInit];
    expect(postInit.body).toBe('{"a":1}');
    expect((postInit.headers as Record<string, string>)["content-type"]).toBe("application/json");
    expect(fetchImpl.mock.calls[1]?.[0]).toBe("/api/v1/audit-events?limit=50");
  });

  it("maps problem+json 401 totp_required to a typed ApiError", async () => {
    const fetchImpl = vi.fn<typeof fetch>(async () =>
      problemResponse(401, {
        title: "Unauthorized",
        status: 401,
        detail: "A TOTP code is required.",
        code: "totp_required",
      }),
    );
    const err = await captureError(apiRequest("/auth/login", { method: "POST", body: {}, fetchImpl }));

    expect(err.status).toBe(401);
    expect(err.code).toBe(ErrorCodes.totpRequired);
    expect(err.title).toBe("Unauthorized");
    expect(err.detail).toBe("A TOTP code is required.");
    expect(err.isUnauthorized).toBe(true);
    expect(err.retryAfterSeconds).toBeUndefined();
  });

  it("parses Retry-After on 423 account_locked", async () => {
    const fetchImpl = vi.fn<typeof fetch>(async () =>
      problemResponse(
        423,
        { title: "Locked", status: 423, detail: "Locked.", code: "account_locked" },
        { "retry-after": "120" },
      ),
    );
    const err = await captureError(apiRequest("/auth/login", { method: "POST", body: {}, fetchImpl }));

    expect(err.status).toBe(423);
    expect(err.code).toBe(ErrorCodes.accountLocked);
    expect(err.retryAfterSeconds).toBe(120);
    expect(errorMessage(err)).toBe("Locked. Try again in 2 minutes.");
  });

  it("parses Retry-After on 429 rate_limited", async () => {
    const fetchImpl = vi.fn<typeof fetch>(async () =>
      problemResponse(429, { title: "Too Many Requests", status: 429, code: "rate_limited" }, { "Retry-After": "30" }),
    );
    const err = await captureError(apiRequest("/auth/login", { method: "POST", body: {}, fetchImpl }));
    expect(err.code).toBe(ErrorCodes.rateLimited);
    expect(err.retryAfterSeconds).toBe(30);
  });

  it("handles non-JSON error bodies", async () => {
    const fetchImpl = vi.fn<typeof fetch>(
      async () =>
        new Response("<html>bad gateway</html>", {
          status: 502,
          statusText: "Bad Gateway",
          headers: { "content-type": "text/html" },
        }),
    );
    const err = await captureError(apiRequest("/version", { fetchImpl }));
    expect(err.status).toBe(502);
    expect(err.code).toBeUndefined();
    expect(err.title).toBe("Bad Gateway");
  });

  it("maps network failures to network_error", async () => {
    const fetchImpl = vi.fn<typeof fetch>(async () => {
      throw new TypeError("fetch failed");
    });
    const err = await captureError(apiRequest("/version", { fetchImpl }));
    expect(err.status).toBe(0);
    expect(err.code).toBe(ErrorCodes.networkError);
  });

  it("rejects responses that violate the contract", async () => {
    const fetchImpl = vi.fn<typeof fetch>(async () => Response.json({ platform: 42 }));
    const err = await captureError(
      apiRequest("/version", { schema: z.object({ platform: z.string() }), fetchImpl }),
    );
    expect(err.code).toBe(ErrorCodes.invalidResponse);
  });

  it("accepts explicitly allowed non-2xx statuses (readiness 503)", async () => {
    const fetchImpl = vi.fn<typeof fetch>(async () =>
      Response.json({ status: "degraded", checks: { postgres: "error" } }, { status: 503 }),
    );
    const data = await apiRequest("/health/ready", {
      schema: z.object({ status: z.string() }),
      acceptStatuses: [503],
      fetchImpl,
    });
    expect(data.status).toBe("degraded");
  });
});

describe("parseRetryAfter / formatRetryAfter", () => {
  it("parses delta-seconds and HTTP dates", () => {
    const now = new Date("2026-09-25T12:00:00Z");
    expect(parseRetryAfter("5", now)).toBe(5);
    expect(parseRetryAfter("Fri, 25 Sep 2026 12:01:00 GMT", now)).toBe(60);
    expect(parseRetryAfter(null, now)).toBeUndefined();
    expect(parseRetryAfter("soon", now)).toBeUndefined();
  });

  it("formats human-readable hints", () => {
    expect(formatRetryAfter(undefined)).toBe("Try again later.");
    expect(formatRetryAfter(1)).toBe("Try again in a moment.");
    expect(formatRetryAfter(45)).toBe("Try again in 45 seconds.");
    expect(formatRetryAfter(60)).toBe("Try again in 1 minute.");
    expect(formatRetryAfter(61)).toBe("Try again in 2 minutes.");
  });
});
