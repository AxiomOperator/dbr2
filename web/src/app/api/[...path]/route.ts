// SPDX-License-Identifier: Apache-2.0
//
// Catch-all same-origin proxy: /api/* -> ${DBR2_API_INTERNAL_URL}/api/*.
// Also serves the backend's Swagger UI (/api/docs) and OpenAPI document
// (/api/openapi.json, /api/openapi.yaml) through the console origin.

import { proxyRequest } from "@/lib/proxy";

// Resolve DBR2_API_INTERNAL_URL and forward on every request; never cache.
export const dynamic = "force-dynamic";
export const runtime = "nodejs";

function handler(req: Request): Promise<Response> {
  return proxyRequest(req);
}

export {
  handler as GET,
  handler as HEAD,
  handler as POST,
  handler as PUT,
  handler as PATCH,
  handler as DELETE,
  handler as OPTIONS,
};
