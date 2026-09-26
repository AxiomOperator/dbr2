// SPDX-License-Identifier: Apache-2.0
// @vitest-environment node
import { describe, expect, it, vi } from "vitest";
import { DEFAULT_API_INTERNAL_URL, proxyRequest, resolveApiBaseUrl } from "./proxy";

type FetchCall = [string, RequestInit & { duplex?: string }];
type FetchMock = ReturnType<typeof vi.fn<typeof fetch>>;

function mockFetch(response: Response | (() => Response)): FetchMock {
  return vi.fn<typeof fetch>(async () => (typeof response === "function" ? response() : response));
}

/** The proxy always calls fetch(url: string, init). */
function callOf(fetchImpl: FetchMock, i = 0): FetchCall {
  return fetchImpl.mock.calls[i] as unknown as FetchCall;
}

describe("resolveApiBaseUrl", () => {
  it("defaults to the local backend and trims trailing slashes", () => {
    expect(resolveApiBaseUrl({})).toBe(DEFAULT_API_INTERNAL_URL);
    expect(
      resolveApiBaseUrl({ DBR2_API_INTERNAL_URL: "http://dbr2-server:8080/" }),
    ).toBe("http://dbr2-server:8080");
  });
});

describe("proxyRequest", () => {
  it("forwards method, path, query and security-relevant headers", async () => {
    const fetchImpl = mockFetch(Response.json({ ok: true }));
    const req = new Request("http://console.example:3000/api/v1/audit-events?limit=50&cursor=x", {
      headers: {
        cookie: "dbr2_session=abc; other=1",
        origin: "https://console.example",
        "sec-fetch-site": "same-origin",
        "sec-fetch-mode": "cors",
        authorization: "Bearer t0ken",
        accept: "application/json",
        "user-agent": "vitest",
        "x-forwarded-for": "203.0.113.7",
        connection: "keep-alive, x-hop",
        "x-hop": "drop-me",
        "accept-encoding": "gzip",
        host: "console.example:3000",
      },
    });

    await proxyRequest(req, {
      fetchImpl,
      env: { DBR2_API_INTERNAL_URL: "http://dbr2-server:8080" },
    });

    expect(fetchImpl).toHaveBeenCalledTimes(1);
    const [url, init] = callOf(fetchImpl);
    expect(url).toBe("http://dbr2-server:8080/api/v1/audit-events?limit=50&cursor=x");
    expect(init.method).toBe("GET");
    expect(init.redirect).toBe("manual");
    expect(init.body).toBeUndefined();

    const h = new Headers(init.headers);
    expect(h.get("cookie")).toBe("dbr2_session=abc; other=1");
    expect(h.get("origin")).toBe("https://console.example");
    expect(h.get("sec-fetch-site")).toBe("same-origin");
    expect(h.get("sec-fetch-mode")).toBe("cors");
    expect(h.get("authorization")).toBe("Bearer t0ken");
    expect(h.get("accept")).toBe("application/json");
    expect(h.get("user-agent")).toBe("vitest");
    // Forwarding headers: existing X-Forwarded-For kept; host/proto derived.
    expect(h.get("x-forwarded-for")).toBe("203.0.113.7");
    expect(h.get("x-forwarded-host")).toBe("console.example:3000");
    expect(h.get("x-forwarded-proto")).toBe("http");
    // Hop-by-hop headers are not forwarded.
    expect(h.has("connection")).toBe(false);
    expect(h.has("x-hop")).toBe(false);
    expect(h.has("host")).toBe(false);
    expect(h.has("accept-encoding")).toBe(false);
  });

  it("keeps X-Forwarded-Host/Proto set by a front proxy", async () => {
    const fetchImpl = mockFetch(new Response(null, { status: 204 }));
    const req = new Request("http://10.0.0.5:3000/api/v1/version", {
      headers: { "x-forwarded-host": "dbr2.example.com", "x-forwarded-proto": "https" },
    });
    await proxyRequest(req, { fetchImpl, env: {} });
    const [url, init] = callOf(fetchImpl);
    expect(url).toBe("http://127.0.0.1:8080/api/v1/version");
    const h = new Headers(init.headers);
    expect(h.get("x-forwarded-host")).toBe("dbr2.example.com");
    expect(h.get("x-forwarded-proto")).toBe("https");
  });

  it("streams request bodies with content-type for unsafe methods", async () => {
    const fetchImpl = mockFetch(new Response(null, { status: 204 }));
    const req = new Request("http://console.example/api/v1/auth/login", {
      method: "POST",
      headers: { "content-type": "application/json", "content-length": "17" },
      body: '{"username":"a"}',
    });
    await proxyRequest(req, { fetchImpl, env: {} });

    const [, init] = callOf(fetchImpl);
    expect(init.method).toBe("POST");
    expect(init.duplex).toBe("half");
    expect(init.body).toBeInstanceOf(ReadableStream);
    expect(await new Response(init.body).text()).toBe('{"username":"a"}');
    const h = new Headers(init.headers);
    expect(h.get("content-type")).toBe("application/json");
    expect(h.has("content-length")).toBe(false);
  });

  it("returns status, headers and every Set-Cookie header unmodified", async () => {
    const upstreamHeaders = new Headers({
      "content-type": "application/json",
      "cache-control": "no-store",
      "content-encoding": "gzip",
      "content-length": "999",
    });
    upstreamHeaders.append("set-cookie", "dbr2_session=s3cr3t; Path=/; HttpOnly; SameSite=Lax");
    upstreamHeaders.append("set-cookie", "dbr2_mock=1; Path=/; Expires=Wed, 21 Oct 2026 07:28:00 GMT");
    const fetchImpl = mockFetch(
      new Response('{"user":{}}', { status: 200, headers: upstreamHeaders }),
    );

    const res = await proxyRequest(
      new Request("http://console.example/api/v1/auth/login", { method: "POST", body: "{}" }),
      { fetchImpl, env: {} },
    );

    expect(res.status).toBe(200);
    expect(res.headers.get("content-type")).toBe("application/json");
    expect(res.headers.get("cache-control")).toBe("no-store");
    expect(res.headers.getSetCookie()).toEqual([
      "dbr2_session=s3cr3t; Path=/; HttpOnly; SameSite=Lax",
      "dbr2_mock=1; Path=/; Expires=Wed, 21 Oct 2026 07:28:00 GMT",
    ]);
    // fetch already decoded the body, so these would be wrong downstream.
    expect(res.headers.has("content-encoding")).toBe(false);
    expect(res.headers.has("content-length")).toBe(false);
    expect(await res.text()).toBe('{"user":{}}');
  });

  it("passes redirects through without following them", async () => {
    const fetchImpl = mockFetch(
      new Response(null, {
        status: 302,
        headers: {
          location: "https://login.microsoftonline.com/tenant/oauth2/v2.0/authorize?state=x",
          "set-cookie": "dbr2_oidc_state=x; Path=/api/v1/auth/oidc; HttpOnly",
        },
      }),
    );
    const res = await proxyRequest(
      new Request("http://console.example/api/v1/auth/oidc/entra/login?return_to=%2Faudit"),
      { fetchImpl, env: {} },
    );
    expect(callOf(fetchImpl)[0]).toBe(
      "http://127.0.0.1:8080/api/v1/auth/oidc/entra/login?return_to=%2Faudit",
    );
    expect(res.status).toBe(302);
    expect(res.headers.get("location")).toBe(
      "https://login.microsoftonline.com/tenant/oauth2/v2.0/authorize?state=x",
    );
    expect(res.headers.getSetCookie()).toEqual([
      "dbr2_oidc_state=x; Path=/api/v1/auth/oidc; HttpOnly",
    ]);
  });

  it("preserves problem+json error responses and Retry-After", async () => {
    const fetchImpl = mockFetch(
      new Response('{"title":"Locked","status":423,"code":"account_locked"}', {
        status: 423,
        headers: { "content-type": "application/problem+json", "retry-after": "60" },
      }),
    );
    const res = await proxyRequest(
      new Request("http://console.example/api/v1/auth/login", { method: "POST", body: "{}" }),
      { fetchImpl, env: {} },
    );
    expect(res.status).toBe(423);
    expect(res.headers.get("retry-after")).toBe("60");
    expect(res.headers.get("content-type")).toBe("application/problem+json");
  });

  it("answers 502 problem+json when the backend is unreachable", async () => {
    const fetchImpl = vi.fn<typeof fetch>(async () => {
      throw new TypeError("fetch failed");
    });
    const errSpy = vi.spyOn(console, "error").mockImplementation(() => {});
    const res = await proxyRequest(new Request("http://console.example/api/v1/version"), {
      fetchImpl,
      env: {},
    });
    expect(res.status).toBe(502);
    expect(res.headers.get("content-type")).toBe("application/problem+json");
    expect(await res.json()).toMatchObject({ status: 502, code: "upstream_unavailable" });
    expect(errSpy).toHaveBeenCalled();
  });

  it("reads DBR2_API_INTERNAL_URL from process.env at request time", async () => {
    const fetchImpl = mockFetch(() => new Response(null, { status: 204 }));
    vi.stubEnv("DBR2_API_INTERNAL_URL", "http://first:8080");
    await proxyRequest(new Request("http://c/api/v1/version"), { fetchImpl });
    vi.stubEnv("DBR2_API_INTERNAL_URL", "http://second:9090");
    await proxyRequest(new Request("http://c/api/v1/version"), { fetchImpl });
    expect(fetchImpl.mock.calls.map((c) => c[0])).toEqual([
      "http://first:8080/api/v1/version",
      "http://second:9090/api/v1/version",
    ]);
  });

  it("streams Server-Sent Events event by event, uncompressed and unbuffered", async () => {
    const encoder = new TextEncoder();
    let push: ((s: string) => void) | undefined;
    const body = new ReadableStream<Uint8Array>({
      start(controller) {
        push = (s) => controller.enqueue(encoder.encode(s));
      },
    });
    const fetchImpl = mockFetch(
      () => new Response(body, { status: 200, headers: { "content-type": "text/event-stream", "cache-control": "no-cache" } }),
    );
    const res = await proxyRequest(new Request("http://c/api/v1/events", { headers: { accept: "text/event-stream" } }), {
      fetchImpl,
      env: {},
    });
    expect(res.headers.get("content-type")).toBe("text/event-stream");
    expect(res.headers.get("cache-control")).toBe("no-cache, no-transform");
    expect(res.headers.get("x-accel-buffering")).toBe("no");
    const reader = res.body!.getReader();
    push!(": connected\n\n");
    expect(new TextDecoder().decode((await reader.read()).value)).toBe(": connected\n\n");
    push!("event: agent.status\ndata: {}\n\n");
    expect(new TextDecoder().decode((await reader.read()).value)).toContain("event: agent.status");
    await reader.cancel();
  });

  it("does not touch caching headers of ordinary responses", async () => {
    const fetchImpl = mockFetch(() => Response.json({ ok: true }, { headers: { "cache-control": "private" } }));
    const res = await proxyRequest(new Request("http://c/api/v1/version"), { fetchImpl, env: {} });
    expect(res.headers.get("cache-control")).toBe("private");
    expect(res.headers.get("x-accel-buffering")).toBeNull();
  });
});
