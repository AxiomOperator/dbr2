// SPDX-License-Identifier: Apache-2.0
//
// One typed function per Phase 1 endpoint. Request bodies are validated with
// the same Zod schemas the forms use before they leave the browser.

import { apiRequest } from "./client";
import {
  AuditEventPageSchema,
  AuthProvidersSchema,
  ChangePasswordRequestSchema,
  LivenessSchema,
  LoginRequestSchema,
  LoginResponseSchema,
  MeSchema,
  ReadinessSchema,
  TotpCodeRequestSchema,
  TotpEnrollResponseSchema,
  VersionSchema,
  type ChangePasswordRequest,
  type LoginRequest,
  type TotpCodeRequest,
} from "./schemas";

export const api = {
  version: (signal?: AbortSignal) => apiRequest("/version", { schema: VersionSchema, signal }),

  live: (signal?: AbortSignal) => apiRequest("/health/live", { schema: LivenessSchema, signal }),

  /** Readiness reports "degraded" with HTTP 503; the body is still meaningful. */
  ready: (signal?: AbortSignal) =>
    apiRequest("/health/ready", { schema: ReadinessSchema, signal, acceptStatuses: [503] }),

  authProviders: (signal?: AbortSignal) =>
    apiRequest("/auth/providers", { schema: AuthProvidersSchema, signal }),

  me: (signal?: AbortSignal) => apiRequest("/auth/me", { schema: MeSchema, signal }),

  login: (req: LoginRequest) =>
    apiRequest("/auth/login", {
      method: "POST",
      body: LoginRequestSchema.parse(req),
      schema: LoginResponseSchema,
    }),

  logout: () => apiRequest("/auth/logout", { method: "POST" }),

  changePassword: (req: ChangePasswordRequest) =>
    apiRequest("/auth/password", { method: "POST", body: ChangePasswordRequestSchema.parse(req) }),

  totpEnroll: () =>
    apiRequest("/auth/totp/enroll", { method: "POST", schema: TotpEnrollResponseSchema }),

  totpConfirm: (req: TotpCodeRequest) =>
    apiRequest("/auth/totp/confirm", { method: "POST", body: TotpCodeRequestSchema.parse(req) }),

  totpDisable: (req: TotpCodeRequest) =>
    apiRequest("/auth/totp/disable", { method: "POST", body: TotpCodeRequestSchema.parse(req) }),

  auditEvents: (params: { limit?: number; cursor?: string | null }, signal?: AbortSignal) =>
    apiRequest("/audit-events", {
      schema: AuditEventPageSchema,
      query: { limit: params.limit ?? 50, cursor: params.cursor },
      signal,
    }),
};

/** TanStack Query keys, centralised so invalidation stays consistent. */
export const queryKeys = {
  me: ["auth", "me"] as const,
  providers: ["auth", "providers"] as const,
  version: ["version"] as const,
  ready: ["health", "ready"] as const,
  auditEvents: (limit: number) => ["audit-events", { limit }] as const,
};
