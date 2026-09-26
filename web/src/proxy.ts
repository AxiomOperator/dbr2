// SPDX-License-Identifier: Apache-2.0
//
// Next.js Proxy (formerly middleware): a per-request CSP nonce for console
// pages. The nonce travels to rendering in the request's
// Content-Security-Policy and x-nonce headers (Next.js applies it to its
// scripts; the root layout reads x-nonce for next-themes) and the policy is
// set on the response. Pages must therefore render dynamically (the root
// layout reads headers()). `/api/*` (proxied to dbr2-server, which sets its
// own headers), static assets and prefetches are left alone.

import { NextResponse, type NextRequest } from "next/server";
import { buildCsp, generateNonce, NONCE_HEADER } from "@/lib/csp";

export function proxy(request: NextRequest) {
  const nonce = generateNonce();
  const csp = buildCsp(nonce, process.env.NODE_ENV === "development");

  const requestHeaders = new Headers(request.headers);
  requestHeaders.set(NONCE_HEADER, nonce);
  requestHeaders.set("Content-Security-Policy", csp);

  const response = NextResponse.next({ request: { headers: requestHeaders } });
  response.headers.set("Content-Security-Policy", csp);
  return response;
}

export const config = {
  matcher: [
    {
      source: "/((?!api/|_next/static|_next/image|favicon.ico|icon.svg).*)",
      missing: [
        { type: "header", key: "next-router-prefetch" },
        { type: "header", key: "purpose", value: "prefetch" },
      ],
    },
  ],
};
