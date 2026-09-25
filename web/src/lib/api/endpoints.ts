// SPDX-License-Identifier: Apache-2.0
//
// One typed function per endpoint. Request bodies are validated with
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
import {
  AgentInventorySchema,
  AgentListSchema,
  AgentSchema,
  ApplicationDetailSchema,
  ApplicationListSchema,
  ApplicationSummarySchema,
  ComposeSchema,
  CreateApplicationRequestSchema,
  CreateApplicationResponseSchema,
  CreateRegistrationTokenRequestSchema,
  CreateRegistrationTokenResponseSchema,
  DiscoverResponseSchema,
  ReasonRequestSchema,
  RegistrationTokenListSchema,
  UpdateApplicationRequestSchema,
  type CreateApplicationRequest,
  type CreateRegistrationTokenRequest,
  type ReasonRequest,
  type UpdateApplicationRequest,
} from "./fleet-schemas";

/** Host status transitions that take a `{reason}` body. */
export type AgentAction = "approve" | "suspend" | "resume" | "revoke";

const seg = (id: string) => encodeURIComponent(id);

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

  // --- Hosts (Phase 2) -----------------------------------------------------

  agents: async (signal?: AbortSignal) =>
    (await apiRequest("/agents", { schema: AgentListSchema, signal })).items,

  agent: (id: string, signal?: AbortSignal) =>
    apiRequest(`/agents/${seg(id)}`, { schema: AgentSchema, signal }),

  agentInventory: (id: string, signal?: AbortSignal) =>
    apiRequest(`/agents/${seg(id)}/inventory`, { schema: AgentInventorySchema, signal }),

  agentAction: (id: string, action: AgentAction, req: ReasonRequest) =>
    apiRequest(`/agents/${seg(id)}/${action}`, {
      method: "POST",
      body: ReasonRequestSchema.parse(req),
      schema: AgentSchema,
    }),

  discoverAgent: (id: string) =>
    apiRequest(`/agents/${seg(id)}/discover`, { method: "POST", schema: DiscoverResponseSchema }),

  registrationTokens: async (signal?: AbortSignal) =>
    (await apiRequest("/agents/registration-tokens", { schema: RegistrationTokenListSchema, signal }))
      .items,

  createRegistrationToken: (req: CreateRegistrationTokenRequest) =>
    apiRequest("/agents/registration-tokens", {
      method: "POST",
      body: CreateRegistrationTokenRequestSchema.parse(req),
      schema: CreateRegistrationTokenResponseSchema,
    }),

  revokeRegistrationToken: (id: string) =>
    apiRequest(`/agents/registration-tokens/${seg(id)}`, { method: "DELETE" }),

  // --- Applications (Phase 3) ----------------------------------------------

  applications: async (signal?: AbortSignal) =>
    (await apiRequest("/applications", { schema: ApplicationListSchema, signal })).items,

  application: (id: string, signal?: AbortSignal) =>
    apiRequest(`/applications/${seg(id)}`, { schema: ApplicationDetailSchema, signal }),

  updateApplication: (id: string, req: UpdateApplicationRequest) =>
    apiRequest(`/applications/${seg(id)}`, {
      method: "PATCH",
      body: UpdateApplicationRequestSchema.parse(req),
      schema: ApplicationSummarySchema,
    }),

  createApplication: (req: CreateApplicationRequest) =>
    apiRequest("/applications", {
      method: "POST",
      body: CreateApplicationRequestSchema.parse(req),
      schema: CreateApplicationResponseSchema,
    }),

  deleteApplication: (id: string) => apiRequest(`/applications/${seg(id)}`, { method: "DELETE" }),

  /** `reveal: true` returns secret values; requires `secrets.read` and is audited. */
  applicationCompose: (id: string, reveal: boolean, signal?: AbortSignal) =>
    apiRequest(`/applications/${seg(id)}/compose`, {
      schema: ComposeSchema,
      query: { reveal: reveal ? "true" : undefined },
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
  agents: ["agents"] as const,
  agent: (id: string) => ["agents", id] as const,
  agentInventory: (id: string) => ["agents", id, "inventory"] as const,
  registrationTokens: ["registration-tokens"] as const,
  applications: ["applications"] as const,
  application: (id: string) => ["applications", id] as const,
  applicationCompose: (id: string) => ["applications", id, "compose"] as const,
  /**
   * Revealed Compose content: deliberately NOT under `applications` so that
   * broad invalidations never refetch it (every fetch is an audited reveal).
   */
  applicationComposeRevealed: (id: string) => ["revealed-compose", id] as const,
};
