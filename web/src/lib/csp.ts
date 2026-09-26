// SPDX-License-Identifier: Apache-2.0
//
// Content-Security-Policy for console pages, with a fresh nonce per request
// (set by src/proxy.ts). Next.js reads the nonce from the request's CSP
// header and puts it on its own inline and bundle scripts; the root layout
// passes it to next-themes' inline theme script. `'strict-dynamic'` lets
// those nonce'd scripts load the rest of the bundle. Styles allow
// 'unsafe-inline' because Radix, React Flow and Next.js set style attributes.
// `/api/*` responses come from dbr2-server with their own headers.

/** Request header carrying the nonce to server components. */
export const NONCE_HEADER = "x-nonce";

/** 128 random bits, base64. */
export function generateNonce(): string {
  const bytes = new Uint8Array(16);
  crypto.getRandomValues(bytes);
  let s = "";
  for (const b of bytes) s += String.fromCharCode(b);
  return btoa(s);
}

/**
 * The policy for one response. `dev` adds 'unsafe-eval' to script-src
 * (React uses eval for debugging information in development only).
 */
export function buildCsp(nonce: string, dev = false): string {
  return [
    "default-src 'self'",
    `script-src 'self' 'nonce-${nonce}' 'strict-dynamic'${dev ? " 'unsafe-eval'" : ""}`,
    "style-src 'self' 'unsafe-inline'",
    "img-src 'self' data: blob:",
    "font-src 'self'",
    "connect-src 'self'",
    "frame-ancestors 'none'",
    "base-uri 'self'",
    "form-action 'self'",
    "object-src 'none'",
  ].join("; ");
}
