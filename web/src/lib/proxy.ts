// SPDX-License-Identifier: Apache-2.0
//
// Same-origin reverse proxy from the console to dbr2-server.
//
// The browser only ever talks to the console origin; every `/api/*` request is
// forwarded to `${DBR2_API_INTERNAL_URL}/api/*`. The upstream URL is read from
// the environment on EVERY request (not baked in at build time like
// next.config rewrites), so one image works in every deployment.
//
// Guarantees:
//  - request and response bodies are streamed (SSE-ready);
//  - cookie / origin / sec-fetch-* / authorization / content-type / accept and
//    other end-to-end headers are forwarded; hop-by-hop headers are dropped;
//  - X-Forwarded-For / -Proto / -Host are set (existing values are preserved,
//    because the Next.js server already populates them from the socket);
//  - status, headers and every Set-Cookie header are returned unmodified;
//  - redirects (e.g. 302 to the OIDC provider) are passed to the browser, not
//    followed (`redirect: "manual"`).

export const DEFAULT_API_INTERNAL_URL = "http://127.0.0.1:8080";

/** Hop-by-hop headers (RFC 9110 §7.6.1) plus ones fetch must compute itself. */
const HOP_BY_HOP = new Set([
  "connection",
  "keep-alive",
  "proxy-authenticate",
  "proxy-authorization",
  "proxy-connection",
  "te",
  "trailer",
  "transfer-encoding",
  "upgrade",
  "host",
  "content-length",
  // Undici negotiates and transparently decodes compression itself.
  "accept-encoding",
  // Next.js internal routing headers must never reach the backend.
  "x-middleware-subrequest",
  "x-nextjs-data",
  "rsc",
  "next-router-state-tree",
  "next-router-prefetch",
  "next-url",
]);

/**
 * Response headers that no longer describe the body after undici has decoded
 * it (content-encoding / content-length), or that are hop-by-hop.
 */
const STRIP_RESPONSE = new Set([
  "connection",
  "keep-alive",
  "proxy-authenticate",
  "trailer",
  "transfer-encoding",
  "upgrade",
  "content-encoding",
  "content-length",
  "set-cookie", // re-added individually below
]);

/** Minimal environment shape (process.env or a test double). */
export type Env = Readonly<Record<string, string | undefined>>;

export function resolveApiBaseUrl(env: Env = process.env): string {
  const raw = env.DBR2_API_INTERNAL_URL?.trim() || DEFAULT_API_INTERNAL_URL;
  return raw.replace(/\/+$/, "");
}

/** Builds the upstream URL for an incoming request URL (`/api/...` path kept verbatim). */
export function upstreamUrl(requestUrl: string, base: string): string {
  const incoming = new URL(requestUrl);
  return `${base}${incoming.pathname}${incoming.search}`;
}

export function buildUpstreamHeaders(req: Request): Headers {
  const incoming = new URL(req.url);
  const out = new Headers();

  // Headers named in the `Connection` header are hop-by-hop too.
  const connectionTokens = new Set(
    (req.headers.get("connection") ?? "")
      .split(",")
      .map((t) => t.trim().toLowerCase())
      .filter(Boolean),
  );

  req.headers.forEach((value, key) => {
    const k = key.toLowerCase();
    if (HOP_BY_HOP.has(k) || connectionTokens.has(k)) return;
    out.set(k, value);
  });

  const host = req.headers.get("x-forwarded-host") ?? req.headers.get("host") ?? incoming.host;
  const proto = req.headers.get("x-forwarded-proto") ?? incoming.protocol.replace(/:$/, "");
  if (!out.has("x-forwarded-host") && host) out.set("x-forwarded-host", host);
  if (!out.has("x-forwarded-proto")) out.set("x-forwarded-proto", proto);
  // X-Forwarded-For is populated by the Next.js server from the socket address
  // (or kept from a trusted front proxy); there is no socket to read here.
  return out;
}

export function buildDownstreamHeaders(upstream: Response): Headers {
  const out = new Headers();
  upstream.headers.forEach((value, key) => {
    if (STRIP_RESPONSE.has(key.toLowerCase())) return;
    out.set(key, value);
  });
  for (const cookie of upstream.headers.getSetCookie()) {
    out.append("set-cookie", cookie);
  }
  return out;
}

function problem(status: number, title: string, detail: string, code: string): Response {
  return new Response(JSON.stringify({ title, status, detail, code }), {
    status,
    headers: { "content-type": "application/problem+json", "cache-control": "no-store" },
  });
}

export interface ProxyOptions {
  fetchImpl?: typeof fetch;
  env?: Env;
}

/** Forwards one request upstream and returns the upstream response as-is. */
export async function proxyRequest(req: Request, options: ProxyOptions = {}): Promise<Response> {
  const doFetch = options.fetchImpl ?? fetch;
  const base = resolveApiBaseUrl(options.env);
  const target = upstreamUrl(req.url, base);
  const hasBody = req.method !== "GET" && req.method !== "HEAD" && req.body !== null;

  let upstream: Response;
  try {
    upstream = await doFetch(target, {
      method: req.method,
      headers: buildUpstreamHeaders(req),
      body: hasBody ? req.body : undefined,
      redirect: "manual",
      // The route is force-dynamic, so Next.js never caches this fetch.
      signal: req.signal,
      // Required by undici to stream a request body.
      ...(hasBody ? { duplex: "half" } : {}),
    } as RequestInit);
  } catch (err) {
    if (req.signal.aborted) {
      return new Response(null, { status: 499 });
    }
    console.error(`[dbr2-web] API proxy: upstream ${base} unreachable:`, err);
    return problem(
      502,
      "Bad Gateway",
      "The DBR² API server could not be reached.",
      "upstream_unavailable",
    );
  }

  // 204/304 (and HEAD) must not carry a body.
  const nullBody =
    req.method === "HEAD" || upstream.status === 204 || upstream.status === 304;
  return new Response(nullBody ? null : upstream.body, {
    status: upstream.status,
    statusText: upstream.statusText,
    headers: buildDownstreamHeaders(upstream),
  });
}
