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
import {
  AddEscrowRecipientRequestSchema,
  AlertListSchema,
  BackupSettingsSchema,
  ConfirmEscrowRequestSchema,
  CreateRepositoryRequestSchema,
  CreateRepositoryResponseSchema,
  EscrowPackageSchema,
  EscrowRecipientListSchema,
  EscrowRecipientSchema,
  HostSettingsSchema,
  RecoveryPointListSchema,
  RecoveryPointSchema,
  RepositoryListSchema,
  RepositorySchema,
  StartBackupRequestSchema,
  UpdateBackupSettingsRequestSchema,
  UpdateHostSettingsRequestSchema,
  WorkflowResponseSchema,
  type AddEscrowRecipientRequest,
  type ConfirmEscrowRequest,
  type CreateRepositoryRequest,
  type RecoveryPointState,
  type StartBackupRequest,
  type UpdateBackupSettingsRequest,
  type UpdateHostSettingsRequest,
} from "./protection-schemas";
import {
  PreviewSchema,
  RestoreBodySchema,
  RestoreRunListSchema,
  RestoreRunSchema,
  StartRestoreBodySchema,
  type RestoreBody,
  type RestoreState,
  type StartRestoreBody,
} from "./restore-schemas";

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

  // --- Repositories & escrow (Phase 4) -------------------------------------

  escrowRecipients: async (signal?: AbortSignal) =>
    (await apiRequest("/escrow/recipients", { schema: EscrowRecipientListSchema, signal })).items,

  addEscrowRecipient: (req: AddEscrowRecipientRequest) =>
    apiRequest("/escrow/recipients", {
      method: "POST",
      body: AddEscrowRecipientRequestSchema.parse(req),
      schema: EscrowRecipientSchema,
    }),

  removeEscrowRecipient: (id: string) => apiRequest(`/escrow/recipients/${seg(id)}`, { method: "DELETE" }),

  repositories: async (signal?: AbortSignal) =>
    (await apiRequest("/repositories", { schema: RepositoryListSchema, signal })).items,

  repository: (id: string, signal?: AbortSignal) =>
    apiRequest(`/repositories/${seg(id)}`, { schema: RepositorySchema, signal }),

  createRepository: (req: CreateRepositoryRequest) =>
    apiRequest("/repositories", {
      method: "POST",
      body: CreateRepositoryRequestSchema.parse(req),
      schema: CreateRepositoryResponseSchema,
    }),

  /** Audited: the package is encrypted to the escrow recipients. */
  repositoryEscrowPackage: (id: string) =>
    apiRequest(`/repositories/${seg(id)}/escrow-package`, { schema: EscrowPackageSchema }),

  confirmRepositoryEscrow: (id: string, req: ConfirmEscrowRequest) =>
    apiRequest(`/repositories/${seg(id)}/escrow/confirm`, {
      method: "POST",
      body: ConfirmEscrowRequestSchema.parse(req),
      schema: RepositorySchema,
    }),

  reindexRepository: (id: string) =>
    apiRequest(`/repositories/${seg(id)}/reindex`, { method: "POST", schema: WorkflowResponseSchema }),

  // --- Backups (Phase 4) ---------------------------------------------------

  /** 409 while another operation runs for the application. */
  startBackup: (applicationId: string, req: StartBackupRequest = {}) =>
    apiRequest(`/applications/${seg(applicationId)}/backups`, {
      method: "POST",
      body: StartBackupRequestSchema.parse(req),
      schema: WorkflowResponseSchema,
    }),

  backupSettings: (applicationId: string, signal?: AbortSignal) =>
    apiRequest(`/applications/${seg(applicationId)}/backup-settings`, { schema: BackupSettingsSchema, signal }),

  updateBackupSettings: (applicationId: string, req: UpdateBackupSettingsRequest) =>
    apiRequest(`/applications/${seg(applicationId)}/backup-settings`, {
      method: "PUT",
      body: UpdateBackupSettingsRequestSchema.parse(req),
      schema: BackupSettingsSchema,
    }),

  recoveryPoints: async (
    params: { applicationId?: string | null; state?: RecoveryPointState | null; limit?: number },
    signal?: AbortSignal,
  ) =>
    (
      await apiRequest("/recovery-points", {
        schema: RecoveryPointListSchema,
        query: { application_id: params.applicationId, state: params.state, limit: params.limit },
        signal,
      })
    ).items,

  recoveryPoint: (id: string, signal?: AbortSignal) =>
    apiRequest(`/recovery-points/${seg(id)}`, { schema: RecoveryPointSchema, signal }),

  alerts: async (params: { all?: boolean; limit?: number }, signal?: AbortSignal) =>
    (
      await apiRequest("/alerts", {
        schema: AlertListSchema,
        query: { all: params.all ? "true" : undefined, limit: params.limit },
        signal,
      })
    ).items,

  acknowledgeAlert: (id: number) => apiRequest(`/alerts/${id}/acknowledge`, { method: "POST" }),

  hostSettings: (agentId: string, signal?: AbortSignal) =>
    apiRequest(`/agents/${seg(agentId)}/settings`, { schema: HostSettingsSchema, signal }),

  /** The agent reconnects to apply a new concurrency limit. */
  updateHostSettings: (agentId: string, req: UpdateHostSettingsRequest) =>
    apiRequest(`/agents/${seg(agentId)}/settings`, {
      method: "PUT",
      body: UpdateHostSettingsRequestSchema.parse(req),
      schema: HostSettingsSchema,
    }),

  // --- Restores (Phase 5) --------------------------------------------------

  /** Impact preview and collision detection; changes nothing. */
  restorePreview: (recoveryPointId: string, req: RestoreBody, signal?: AbortSignal) =>
    apiRequest(`/recovery-points/${seg(recoveryPointId)}/restore-preview`, {
      method: "POST",
      body: RestoreBodySchema.parse(req),
      schema: PreviewSchema,
      signal,
    }),

  /**
   * 202 with the new run. 409 when blocked by collisions or another operation;
   * 403 for a production restore without `restore.production`; 400 without
   * the typed confirmation or reason of a production restore.
   */
  startRestore: (recoveryPointId: string, req: StartRestoreBody) =>
    apiRequest(`/recovery-points/${seg(recoveryPointId)}/restores`, {
      method: "POST",
      body: StartRestoreBodySchema.parse(req),
      schema: RestoreRunSchema,
    }),

  restores: async (
    params: { applicationId?: string | null; state?: RestoreState | null; limit?: number },
    signal?: AbortSignal,
  ) =>
    (
      await apiRequest("/restores", {
        schema: RestoreRunListSchema,
        query: { application_id: params.applicationId, state: params.state, limit: params.limit },
        signal,
      })
    ).items,

  restore: (id: string, signal?: AbortSignal) =>
    apiRequest(`/restores/${seg(id)}`, { schema: RestoreRunSchema, signal }),
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
  escrowRecipients: ["escrow-recipients"] as const,
  repositories: ["repositories"] as const,
  repository: (id: string) => ["repositories", id] as const,
  backupSettings: (applicationId: string) => ["applications", applicationId, "backup-settings"] as const,
  recoveryPointsAll: ["recovery-points"] as const,
  recoveryPoints: (filters: { applicationId?: string | null; state?: string | null }) =>
    ["recovery-points", "list", { applicationId: filters.applicationId ?? null, state: filters.state ?? null }] as const,
  recoveryPoint: (id: string) => ["recovery-points", "detail", id] as const,
  alertsAll: ["alerts"] as const,
  alerts: (all: boolean) => ["alerts", { all }] as const,
  hostSettings: (agentId: string) => ["agents", agentId, "settings"] as const,
  restorePreview: (recoveryPointId: string, body: RestoreBody) => ["restore-preview", recoveryPointId, body] as const,
  restoresAll: ["restores"] as const,
  restores: (filters: { applicationId?: string | null; state?: string | null }) =>
    ["restores", "list", { applicationId: filters.applicationId ?? null, state: filters.state ?? null }] as const,
  restore: (id: string) => ["restores", "detail", id] as const,
};
