// SPDX-License-Identifier: Apache-2.0
import type { NextConfig } from "next";

// Security headers for console pages. `/api/*` responses come from dbr2-server
// through the proxy route and keep the backend's own headers (e.g. the strict
// CSP on the Swagger UI), so they are excluded here.
const securityHeaders = [
  { key: "X-Frame-Options", value: "DENY" },
  { key: "X-Content-Type-Options", value: "nosniff" },
  { key: "Referrer-Policy", value: "same-origin" },
  { key: "Cross-Origin-Opener-Policy", value: "same-origin" },
  {
    key: "Permissions-Policy",
    value: "camera=(), microphone=(), geolocation=(), payment=(), usb=()",
  },
];

const nextConfig: NextConfig = {
  // Self-contained server for the container image (web/Dockerfile).
  output: "standalone",
  poweredByHeader: false,
  reactStrictMode: true,
  // The /api proxy is a route handler that reads DBR2_API_INTERNAL_URL at
  // request time; do NOT use `rewrites` for it (they are fixed at build time).
  async headers() {
    return [{ source: "/((?!api/).*)", headers: securityHeaders }];
  },
};

export default nextConfig;
