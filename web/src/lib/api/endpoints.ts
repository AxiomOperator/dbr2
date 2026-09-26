// SPDX-License-Identifier: Apache-2.0
//
// One typed function per endpoint. Request bodies are validated twice before
// they leave the browser: with the console's form schema (user-facing
// messages) and with the generated OpenAPI schema (the contract). Responses
// are validated with the generated schemas (see ./contract.ts).

import { apiRequest } from "./client";
import {
  zAddEscrowRecipientBody,
  zAddGroupMappingBody,
  zAssignPolicyBody,
  zCompleteEscrowDrillBody,
  zCreateNotificationChannelBody,
  zCreatePolicyBody,
  zDeleteRecoveryPointBody,
  zDeleteRepositoryBody,
  zPutContractBody,
  zUpdateNotificationChannelBody,
  zUpdatePolicyBody,
  zUpdateSmtpSettingsBody,
  zVerifyRepositoryBody,
  zChangePasswordBody,
  zConfirmRepositoryEscrowBody,
  zConfirmTotpBody,
  zCreateApplicationBody,
  zCreateRegistrationTokenBody,
  zCreateRepositoryBody,
  zDisableTotpBody,
  zLoginBody,
  zPreviewRestoreBody,
  zPutBackupSettingsBody,
  zPutHostSettingsBody,
  zReasonInputBody,
  zRemoveGroupMappingBody,
  zSetUserRolesBody,
  zSetUserStatusBody,
  zStartBackupBody,
  zStartRestoreBody2,
  zUpdateApplicationBody,
} from "./generated/zod.gen";
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
  FleetContainerListSchema,
  FleetVolumeListSchema,
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
  CompleteDrillRequestSchema,
  DrillListSchema,
  DrillSchema,
  EscrowHealthSchema,
  PlatformBackupListSchema,
  RegeneratedEscrowSchema,
  VerifyRepositoryRequestSchema,
  deleteRequestSchema,
  type DeleteRequest,
  type VerifyRepositoryRequestInput,
  BackupSettingsSchema,
  ConfirmEscrowRequestSchema,
  CreateRepositoryRequestSchema,
  CreateRepositoryResponseSchema,
  EscrowPackageSchema,
  EscrowRecipientListSchema,
  EscrowRecipientSchema,
  HostSettingsSchema,
  JobListSchema,
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
  type JobState,
  type JobType,
  type RecoveryPointFilterState,
  type StartBackupRequest,
  type UpdateBackupSettingsRequest,
  type UpdateHostSettingsRequest,
} from "./protection-schemas";
import {
  GroupMappingListSchema,
  GroupMappingRequestSchema,
  RoleListSchema,
  SetRolesRequestSchema,
  SetStatusRequestSchema,
  UserListSchema,
  type GroupMappingRequest,
  type SetRolesRequest,
  type SetStatusRequest,
} from "./users-schemas";
import {
  AssignPolicyRequestSchema,
  ContractListSchema,
  ContractRequestSchema,
  ContractSchema,
  CreateChannelRequestSchema,
  DeliveryListSchema,
  NotificationChannelListSchema,
  NotificationChannelSchema,
  PolicyDetailSchema,
  PolicyListSchema,
  PolicyRequestSchema,
  PolicySchema,
  SmtpRequestSchema,
  SmtpSettingsSchema,
  TestResultSchema,
  UpdateChannelRequestSchema,
  type ContractRequest,
  type CreateChannelRequest,
  type PolicyRequest,
  type SmtpRequest,
  type UpdateChannelRequest,
} from "./policy-schemas";
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
      body: zLoginBody.parse(LoginRequestSchema.parse(req)),
      schema: LoginResponseSchema,
    }),

  logout: () => apiRequest("/auth/logout", { method: "POST" }),

  changePassword: (req: ChangePasswordRequest) =>
    apiRequest("/auth/password", { method: "POST", body: zChangePasswordBody.parse(ChangePasswordRequestSchema.parse(req)) }),

  totpEnroll: () =>
    apiRequest("/auth/totp/enroll", { method: "POST", schema: TotpEnrollResponseSchema }),

  totpConfirm: (req: TotpCodeRequest) =>
    apiRequest("/auth/totp/confirm", { method: "POST", body: zConfirmTotpBody.parse(TotpCodeRequestSchema.parse(req)) }),

  totpDisable: (req: TotpCodeRequest) =>
    apiRequest("/auth/totp/disable", { method: "POST", body: zDisableTotpBody.parse(TotpCodeRequestSchema.parse(req)) }),

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
      body: zReasonInputBody.parse(ReasonRequestSchema.parse(req)),
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
      body: zCreateRegistrationTokenBody.parse(CreateRegistrationTokenRequestSchema.parse(req)),
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
      body: zUpdateApplicationBody.parse(UpdateApplicationRequestSchema.parse(req)),
      schema: ApplicationSummarySchema,
    }),

  createApplication: (req: CreateApplicationRequest) =>
    apiRequest("/applications", {
      method: "POST",
      body: zCreateApplicationBody.parse(CreateApplicationRequestSchema.parse(req)),
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
      body: zAddEscrowRecipientBody.parse(AddEscrowRecipientRequestSchema.parse(req)),
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
      body: zCreateRepositoryBody.parse(CreateRepositoryRequestSchema.parse(req)),
      schema: CreateRepositoryResponseSchema,
    }),

  /** Audited: the package is encrypted to the escrow recipients. */
  repositoryEscrowPackage: (id: string) =>
    apiRequest(`/repositories/${seg(id)}/escrow-package`, { schema: EscrowPackageSchema }),

  confirmRepositoryEscrow: (id: string, req: ConfirmEscrowRequest) =>
    apiRequest(`/repositories/${seg(id)}/escrow/confirm`, {
      method: "POST",
      body: zConfirmRepositoryEscrowBody.parse(ConfirmEscrowRequestSchema.parse(req)),
      schema: RepositorySchema,
    }),

  reindexRepository: (id: string) =>
    apiRequest(`/repositories/${seg(id)}/reindex`, { method: "POST", schema: WorkflowResponseSchema }),

  // --- Backups (Phase 4) ---------------------------------------------------

  /** 409 while another operation runs for the application. */
  startBackup: (applicationId: string, req: StartBackupRequest = {}) =>
    apiRequest(`/applications/${seg(applicationId)}/backups`, {
      method: "POST",
      body: zStartBackupBody.parse(StartBackupRequestSchema.parse(req)),
      schema: WorkflowResponseSchema,
    }),

  backupSettings: (applicationId: string, signal?: AbortSignal) =>
    apiRequest(`/applications/${seg(applicationId)}/backup-settings`, { schema: BackupSettingsSchema, signal }),

  updateBackupSettings: (applicationId: string, req: UpdateBackupSettingsRequest) =>
    apiRequest(`/applications/${seg(applicationId)}/backup-settings`, {
      method: "PUT",
      body: zPutBackupSettingsBody.parse(UpdateBackupSettingsRequestSchema.parse(req)),
      schema: BackupSettingsSchema,
    }),

  recoveryPoints: async (
    params: { applicationId?: string | null; state?: RecoveryPointFilterState | null; limit?: number },
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
      body: zPutHostSettingsBody.parse(UpdateHostSettingsRequestSchema.parse(req)),
      schema: HostSettingsSchema,
    }),

  // --- Restores (Phase 5) --------------------------------------------------

  /** Impact preview and collision detection; changes nothing. */
  restorePreview: (recoveryPointId: string, req: RestoreBody, signal?: AbortSignal) =>
    apiRequest(`/recovery-points/${seg(recoveryPointId)}/restore-preview`, {
      method: "POST",
      body: zPreviewRestoreBody.parse(RestoreBodySchema.parse(req)),
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
      body: zStartRestoreBody2.parse(StartRestoreBodySchema.parse(req)),
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

  /** 202: compensation runs and the restore ends rolled_back (or failed). 409 when it is not running. */
  cancelRestore: (id: string) => apiRequest(`/restores/${seg(id)}/cancel`, { method: "POST" }),

  // --- Phase 6: jobs, fleet-wide containers / volumes, users ----------------

  jobs: async (
    params: { applicationId?: string | null; type?: JobType | null; state?: JobState | null; limit?: number },
    signal?: AbortSignal,
  ) =>
    (
      await apiRequest("/jobs", {
        schema: JobListSchema,
        query: { application_id: params.applicationId, type: params.type, state: params.state, limit: params.limit },
        signal,
      })
    ).items,

  containers: async (params: { hostId?: string | null } = {}, signal?: AbortSignal) =>
    (await apiRequest("/containers", { schema: FleetContainerListSchema, query: { host_id: params.hostId }, signal }))
      .items,

  volumes: async (params: { hostId?: string | null } = {}, signal?: AbortSignal) =>
    (await apiRequest("/volumes", { schema: FleetVolumeListSchema, query: { host_id: params.hostId }, signal })).items,

  users: async (signal?: AbortSignal) => (await apiRequest("/users", { schema: UserListSchema, signal })).items,

  roles: async (signal?: AbortSignal) => (await apiRequest("/roles", { schema: RoleListSchema, signal })).items,

  /** Replaces the manually granted roles (group-mapped roles are managed by Entra ID). */
  setUserRoles: (id: string, req: SetRolesRequest) =>
    apiRequest(`/users/${seg(id)}/roles`, {
      method: "PUT",
      body: zSetUserRolesBody.parse(SetRolesRequestSchema.parse(req)),
    }),

  /** Disabling revokes every session of the user. */
  setUserStatus: (id: string, req: SetStatusRequest) =>
    apiRequest(`/users/${seg(id)}/status`, {
      method: "PUT",
      body: zSetUserStatusBody.parse(SetStatusRequestSchema.parse(req)),
    }),

  groupMappings: async (signal?: AbortSignal) =>
    (await apiRequest("/oidc/group-mappings", { schema: GroupMappingListSchema, signal })).items,

  addGroupMapping: (req: GroupMappingRequest) =>
    apiRequest("/oidc/group-mappings", {
      method: "POST",
      body: zAddGroupMappingBody.parse(GroupMappingRequestSchema.parse(req)),
    }),

  removeGroupMapping: (req: GroupMappingRequest) =>
    apiRequest("/oidc/group-mappings/remove", {
      method: "POST",
      body: zRemoveGroupMappingBody.parse(GroupMappingRequestSchema.parse(req)),
    }),

  // --- Phase 7: policies, contracts, deletion, notifications ---------------

  policies: async (signal?: AbortSignal) => (await apiRequest("/policies", { schema: PolicyListSchema, signal })).items,

  policy: (id: string, signal?: AbortSignal) =>
    apiRequest(`/policies/${seg(id)}`, { schema: PolicyDetailSchema, signal }),

  /** 400 with the server's message for an invalid schedule, timezone or retention. */
  createPolicy: (req: PolicyRequest) =>
    apiRequest("/policies", {
      method: "POST",
      body: zCreatePolicyBody.parse(PolicyRequestSchema.parse(req)),
      schema: PolicySchema,
    }),

  updatePolicy: (id: string, req: PolicyRequest) =>
    apiRequest(`/policies/${seg(id)}`, {
      method: "PUT",
      body: zUpdatePolicyBody.parse(PolicyRequestSchema.parse(req)),
      schema: PolicySchema,
    }),

  /** Assigned applications are no longer scheduled; their recovery points are kept. */
  deletePolicy: (id: string) => apiRequest(`/policies/${seg(id)}`, { method: "DELETE" }),

  /** `policyId: null` removes the assignment. */
  assignPolicy: (applicationId: string, policyId: string | null) =>
    apiRequest(`/applications/${seg(applicationId)}/policy`, {
      method: "PUT",
      body: zAssignPolicyBody.parse(AssignPolicyRequestSchema.parse({ policy_id: policyId })),
    }),

  contracts: async (signal?: AbortSignal) =>
    (await apiRequest("/contracts", { schema: ContractListSchema, signal })).items,

  /** 404 when the application has no contract. */
  contract: (applicationId: string, signal?: AbortSignal) =>
    apiRequest(`/applications/${seg(applicationId)}/contract`, { schema: ContractSchema, signal }),

  putContract: (applicationId: string, req: ContractRequest) =>
    apiRequest(`/applications/${seg(applicationId)}/contract`, {
      method: "PUT",
      body: zPutContractBody.parse(ContractRequestSchema.parse(req)),
      schema: ContractSchema,
    }),

  deleteContract: (applicationId: string) =>
    apiRequest(`/applications/${seg(applicationId)}/contract`, { method: "DELETE" }),

  /** Schedules deletion after the grace period; `name` is the application name to type. */
  deleteRecoveryPoint: (id: string, name: string, req: DeleteRequest) =>
    apiRequest(`/recovery-points/${seg(id)}/delete`, {
      method: "POST",
      body: zDeleteRecoveryPointBody.parse(deleteRequestSchema(name).parse(req)),
      schema: RecoveryPointSchema,
    }),

  undeleteRecoveryPoint: (id: string) =>
    apiRequest(`/recovery-points/${seg(id)}/undelete`, { method: "POST", schema: RecoveryPointSchema }),

  /** Retires the Repository after the grace period; `name` is the Repository name to type. */
  deleteRepository: (id: string, name: string, req: DeleteRequest) =>
    apiRequest(`/repositories/${seg(id)}/delete`, {
      method: "POST",
      body: zDeleteRepositoryBody.parse(deleteRequestSchema(name).parse(req)),
      schema: RepositorySchema,
    }),

  undeleteRepository: (id: string) =>
    apiRequest(`/repositories/${seg(id)}/undelete`, { method: "POST", schema: RepositorySchema }),

  notificationChannels: async (signal?: AbortSignal) =>
    (await apiRequest("/notification-channels", { schema: NotificationChannelListSchema, signal })).items,

  createNotificationChannel: (req: CreateChannelRequest) =>
    apiRequest("/notification-channels", {
      method: "POST",
      body: zCreateNotificationChannelBody.parse(CreateChannelRequestSchema.parse(req)),
      schema: NotificationChannelSchema,
    }),

  /** Omit `secret` to keep the stored one, "" to remove it. */
  updateNotificationChannel: (id: string, req: UpdateChannelRequest) =>
    apiRequest(`/notification-channels/${seg(id)}`, {
      method: "PUT",
      body: zUpdateNotificationChannelBody.parse(UpdateChannelRequestSchema.parse(req)),
      schema: NotificationChannelSchema,
    }),

  deleteNotificationChannel: (id: string) => apiRequest(`/notification-channels/${seg(id)}`, { method: "DELETE" }),

  /** A failed delivery is reported in the body (`delivered: false`). */
  testNotificationChannel: (id: string) =>
    apiRequest(`/notification-channels/${seg(id)}/test`, { method: "POST", schema: TestResultSchema }),

  notificationDeliveries: async (id: string, signal?: AbortSignal) =>
    (await apiRequest(`/notification-channels/${seg(id)}/deliveries`, { schema: DeliveryListSchema, query: { limit: 50 }, signal }))
      .items,

  smtpSettings: (signal?: AbortSignal) => apiRequest("/settings/smtp", { schema: SmtpSettingsSchema, signal }),

  /** Omit `password` to keep the stored one, "" to remove it. */
  updateSmtpSettings: (req: SmtpRequest) =>
    apiRequest("/settings/smtp", {
      method: "PUT",
      body: zUpdateSmtpSettingsBody.parse(SmtpRequestSchema.parse(req)),
      schema: SmtpSettingsSchema,
    }),

  // --- Phase 9: verification, escrow health, platform protection ------------

  /** 202: verifies the least recently verified recovery points (or one). */
  verifyRepository: (id: string, req: VerifyRepositoryRequestInput = {}) =>
    apiRequest(`/repositories/${seg(id)}/verify`, {
      method: "POST",
      body: zVerifyRepositoryBody.parse(VerifyRepositoryRequestSchema.parse(req)),
      schema: WorkflowResponseSchema,
    }),

  escrowHealth: (signal?: AbortSignal) => apiRequest("/escrow/health", { schema: EscrowHealthSchema, signal }),

  /** Re-seals the password to the current recipients; escrow must be confirmed again. */
  regenerateEscrow: (id: string) =>
    apiRequest(`/repositories/${seg(id)}/escrow/regenerate`, { method: "POST", schema: RegeneratedEscrowSchema }),

  escrowDrills: async (signal?: AbortSignal) =>
    (await apiRequest("/escrow/drills", { schema: DrillListSchema, signal })).items,

  /** The drill package is only returned here. */
  startEscrowDrill: () => apiRequest("/escrow/drills", { method: "POST", schema: DrillSchema }),

  completeEscrowDrill: (id: string, code: string) =>
    apiRequest(`/escrow/drills/${seg(id)}/complete`, {
      method: "POST",
      body: zCompleteEscrowDrillBody.parse(CompleteDrillRequestSchema.parse({ confirmation_code: code })),
      schema: DrillSchema,
    }),

  platformBackups: async (signal?: AbortSignal) =>
    (await apiRequest("/platform/backups", { schema: PlatformBackupListSchema, query: { limit: 50 }, signal })).items,

  /** 202; 409 while a run is in progress. */
  startPlatformBackup: () => apiRequest("/platform/backups", { method: "POST", schema: WorkflowResponseSchema }),

  designateSystemRepository: (id: string) =>
    apiRequest(`/repositories/${seg(id)}/system`, { method: "PUT", schema: RepositorySchema }),
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
  jobsAll: ["jobs"] as const,
  jobs: (filters: { applicationId?: string | null; type?: string | null; state?: string | null }) =>
    [
      "jobs",
      { applicationId: filters.applicationId ?? null, type: filters.type ?? null, state: filters.state ?? null },
    ] as const,
  containers: (hostId?: string | null) => ["containers", { hostId: hostId ?? null }] as const,
  containersAll: ["containers"] as const,
  volumes: (hostId?: string | null) => ["volumes", { hostId: hostId ?? null }] as const,
  volumesAll: ["volumes"] as const,
  users: ["users"] as const,
  roles: ["roles"] as const,
  groupMappings: ["group-mappings"] as const,
  policies: ["policies"] as const,
  policy: (id: string) => ["policies", id] as const,
  contractsAll: ["contracts"] as const,
  contract: (applicationId: string) => ["contracts", applicationId] as const,
  notificationChannels: ["notification-channels"] as const,
  notificationDeliveries: (id: string) => ["notification-channels", id, "deliveries"] as const,
  smtpSettings: ["settings", "smtp"] as const,
  escrowHealth: ["escrow", "health"] as const,
  escrowDrills: ["escrow", "drills"] as const,
  platformBackups: ["platform-backups"] as const,
};
