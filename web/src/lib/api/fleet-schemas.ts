// SPDX-License-Identifier: Apache-2.0
//
// Zod schemas for the Phase 2 (Hosts) and Phase 3 (Applications) API contract,
// derived from `api/openapi.yaml` (operations tagged `Hosts` and
// `Applications`). As in `schemas.ts`, response objects are non-strict:
// unknown keys are ignored. Array fields the contract declares as nullable
// (`type: [array, "null"]`) are normalised to `[]`.

import { z } from "zod";

/** A nullable (or omitted) array, normalised to `[]`. */
function list<T extends z.ZodType>(item: T) {
  return z
    .array(item)
    .nullish()
    .transform((v): z.output<T>[] => v ?? []);
}

// ---------------------------------------------------------------------------
// Permissions
// ---------------------------------------------------------------------------

export const PERMISSION_HOST_READ = "host.read";
export const PERMISSION_HOST_MANAGE = "host.manage";
export const PERMISSION_APPLICATION_READ = "application.read";
export const PERMISSION_APPLICATION_MANAGE = "application.manage";
export const PERMISSION_SECRETS_READ = "secrets.read";

// ---------------------------------------------------------------------------
// Hosts (agents)
// ---------------------------------------------------------------------------

export const AGENT_STATUSES = ["pending", "active", "suspended", "revoked"] as const;
export const AgentStatusSchema = z.enum(AGENT_STATUSES);
export type AgentStatus = z.infer<typeof AgentStatusSchema>;

export const AgentSchema = z.object({
  id: z.string(),
  hostname: z.string(),
  status: AgentStatusSchema,
  status_reason: z.string().nullable(),
  connected: z.boolean(),
  outdated: z.boolean(),
  agent_version: z.string(),
  protocol_version: z.string(),
  os_release: z.string().nullable(),
  architecture: z.string().nullable(),
  enrolled_at: z.string(),
  approved_at: z.string().nullable(),
  last_seen_at: z.string().nullable(),
  latency_ms: z.number().int().nullable(),
  docker_reachable: z.boolean().nullable(),
  docker_version: z.string().nullable(),
  health_error: z.string().nullable(),
  certificate_not_after: z.string().nullable(),
});
export type Agent = z.infer<typeof AgentSchema>;

export const AgentListSchema = z.object({ items: list(AgentSchema) });
export type AgentList = z.infer<typeof AgentListSchema>;

/** Body of approve / suspend / resume / revoke (`ReasonInputBody`). */
export const ReasonRequestSchema = z.object({
  reason: z
    .string()
    .trim()
    .min(1, "Enter a reason; it is stored in the audit log.")
    .max(500, "Use at most 500 characters."),
});
export type ReasonRequest = z.infer<typeof ReasonRequestSchema>;

export const DiscoverResponseSchema = z.object({ workflow_id: z.string() });
export type DiscoverResponse = z.infer<typeof DiscoverResponseSchema>;

export const RegistrationTokenSchema = z.object({
  id: z.string(),
  prefix: z.string(),
  description: z.string(),
  created_at: z.string(),
  expires_at: z.string(),
  used_at: z.string().nullable(),
  used_by_agent: z.string().nullable(),
  revoked_at: z.string().nullable(),
});
export type RegistrationToken = z.infer<typeof RegistrationTokenSchema>;

export const RegistrationTokenListSchema = z.object({ items: list(RegistrationTokenSchema) });

export const TOKEN_MAX_HOURS = 168;
export const TOKEN_DEFAULT_HOURS = 24;

export const CreateRegistrationTokenRequestSchema = z.object({
  description: z
    .string()
    .trim()
    .min(1, "Describe the host this token is for.")
    .max(200, "Use at most 200 characters."),
  expires_in_hours: z
    .number({ error: "Enter a number of hours." })
    .int("Enter a whole number of hours.")
    .min(1, "The token must be valid for at least 1 hour.")
    .max(TOKEN_MAX_HOURS, `The token can be valid for at most ${TOKEN_MAX_HOURS} hours (7 days).`),
});
export type CreateRegistrationTokenRequest = z.infer<typeof CreateRegistrationTokenRequestSchema>;

export const CreateRegistrationTokenResponseSchema = z.object({
  token: z.string(),
  gateway_address: z.string(),
  ca_sha256: z.string(),
  join_command: z.string(),
  registration_token: RegistrationTokenSchema,
});
export type CreateRegistrationTokenResponse = z.infer<typeof CreateRegistrationTokenResponseSchema>;

// ---------------------------------------------------------------------------
// Host inventory (only what the console shows; the full document is large)
// ---------------------------------------------------------------------------

export const HostFactsSchema = z.object({
  hostname: z.string(),
  operating_system: z.string(),
  os_type: z.string(),
  kernel_version: z.string(),
  architecture: z.string(),
  runtime: z.string(),
  engine_version: z.string(),
  api_version: z.string(),
  root_dir: z.string(),
  storage_driver: z.string(),
  cgroup_version: z.string(),
  rootless: z.boolean(),
  selinux: z.boolean(),
  cpus: z.number().int(),
  memory_bytes: z.number().int(),
  security_options: list(z.string()),
});
export type HostFacts = z.infer<typeof HostFactsSchema>;

const InventoryContainerSchema = z.object({
  id: z.string(),
  name: z.string(),
  image: z.string(),
  state: z.string(),
});
const InventoryVolumeSchema = z.object({ name: z.string(), driver: z.string() });
const InventoryNetworkSchema = z.object({ id: z.string(), name: z.string(), driver: z.string() });
const InventoryImageSchema = z.object({ id: z.string() });
const InventoryComposeProjectSchema = z.object({ name: z.string(), working_dir: z.string() });

export const InventorySchema = z.object({
  schema_version: z.number().int(),
  collected_at: z.string(),
  host: HostFactsSchema,
  containers: list(InventoryContainerSchema),
  volumes: list(InventoryVolumeSchema),
  networks: list(InventoryNetworkSchema),
  images: list(InventoryImageSchema),
  compose_projects: list(InventoryComposeProjectSchema),
  warnings: list(z.string()),
});
export type Inventory = z.infer<typeof InventorySchema>;

export const AgentInventorySchema = z.object({
  received_at: z.string(),
  inventory: InventorySchema,
});
export type AgentInventory = z.infer<typeof AgentInventorySchema>;

// ---------------------------------------------------------------------------
// Applications
// ---------------------------------------------------------------------------

export const APPLICATION_KINDS = ["compose", "container", "manual"] as const;
export const ApplicationKindSchema = z.enum(APPLICATION_KINDS);
export type ApplicationKind = z.infer<typeof ApplicationKindSchema>;

export const ApplicationSourceSchema = z.enum(["original", "reconstructed", "unknown"]);
export type ApplicationSource = z.infer<typeof ApplicationSourceSchema>;

export const ENVIRONMENTS = ["production", "staging", "development", "test", "other"] as const;
export const CRITICALITIES = ["critical", "high", "medium", "low"] as const;

export const ApplicationSummarySchema = z.object({
  id: z.string(),
  name: z.string(),
  display_name: z.string().nullable(),
  kind: ApplicationKindSchema,
  host_id: z.string(),
  hostname: z.string(),
  source: ApplicationSourceSchema,
  services: z.number().int(),
  containers: z.number().int(),
  volumes: z.number().int(),
  bind_mounts: z.number().int(),
  unprotected_high: z.number().int(),
  dependencies: z.number().int(),
  secrets_count: z.number().int(),
  owner: z.string().nullable(),
  environment: z.string().nullable(),
  criticality: z.string().nullable(),
  last_seen_at: z.string(),
  missing_since: z.string().nullable(),
});
export type ApplicationSummary = z.infer<typeof ApplicationSummarySchema>;

export const ApplicationListSchema = z.object({ items: list(ApplicationSummarySchema) });

export const ContainerRefSchema = z.object({ id: z.string(), name: z.string(), state: z.string() });

export const ServiceSchema = z.object({
  name: z.string(),
  image: z.string(),
  containers: list(ContainerRefSchema),
});
export type Service = z.infer<typeof ServiceSchema>;

/** Volume classes (ADR-0006). */
export const VOLUME_CLASSES = ["local", "external", "ephemeral"] as const;

export const VolumeUseSchema = z.object({
  name: z.string(),
  driver: z.string(),
  mountpoint: z.string(),
  // "local" | "external" | "ephemeral"; unknown values are shown verbatim.
  class: z.string(),
  reasons: list(z.string()),
  anonymous: z.boolean().optional().default(false),
  external: z.boolean().optional().default(false),
  used_by: list(z.string()),
  protected_by_default: z.boolean(),
});
export type VolumeUse = z.infer<typeof VolumeUseSchema>;

export const BindUseSchema = z.object({
  container: z.string(),
  source: z.string(),
  destination: z.string(),
  rw: z.boolean(),
});
export type BindUse = z.infer<typeof BindUseSchema>;

export const TmpfsUseSchema = z.object({ container: z.string(), destination: z.string() });

export const NetworkUseSchema = z.object({
  name: z.string(),
  driver: z.string(),
  external: z.boolean().optional().default(false),
});

export const ImageRefSchema = z.object({
  reference: z.string(),
  image_id: z.string(),
  digests: list(z.string()),
  platform: z.string().optional(),
});
export type ImageRef = z.infer<typeof ImageRefSchema>;

export const DependencySchema = z.object({ kind: z.string(), name: z.string(), detail: z.string() });

export const UnprotectedSchema = z.object({
  container: z.string(),
  path: z.string(),
  files: z.number().int(),
  // "high" | "low"
  severity: z.string(),
  reason: z.string(),
});
export type Unprotected = z.infer<typeof UnprotectedSchema>;

/** The `analysis` object of an application (OpenAPI `Application`). */
export const AnalysisSchema = z.object({
  key: z.string(),
  kind: z.string(),
  name: z.string(),
  compose_project: z.string().optional(),
  working_dir: z.string().optional(),
  source: z.string(),
  source_reason: z.string().optional(),
  services: list(ServiceSchema),
  volumes: list(VolumeUseSchema),
  bind_mounts: list(BindUseSchema),
  tmpfs: list(TmpfsUseSchema),
  networks: list(NetworkUseSchema),
  images: list(ImageRefSchema),
  dependencies: list(DependencySchema),
  unprotected: list(UnprotectedSchema),
  secrets_count: z.number().int(),
  containers: list(z.string()),
});
export type Analysis = z.infer<typeof AnalysisSchema>;

export const EnvVarSchema = z.object({
  key: z.string(),
  // Sensitive values arrive already masked as "********".
  value: z.string().optional(),
  sensitive: z.boolean().optional().default(false),
});
export type EnvVar = z.infer<typeof EnvVarSchema>;

export const PortSchema = z.object({
  container_port: z.string(),
  protocol: z.string(),
  host_ip: z.string().optional(),
  host_port: z.string().optional(),
});

export const MountSchema = z.object({
  type: z.string(),
  destination: z.string(),
  rw: z.boolean(),
  name: z.string().optional(),
  source: z.string().optional(),
  driver: z.string().optional(),
  mode: z.string().optional(),
});

export const ContainerDetailSchema = z.object({
  id: z.string(),
  name: z.string(),
  image: z.string(),
  state: z.string(),
  env: list(EnvVarSchema),
  ports: list(PortSchema),
  mounts: list(MountSchema),
  networks: list(z.string()),
});
export type ContainerDetail = z.infer<typeof ContainerDetailSchema>;

export const ApplicationDetailSchema = ApplicationSummarySchema.extend({
  analysis: AnalysisSchema.nullable(),
  containers_detail: list(ContainerDetailSchema),
  collected_at: z.string().nullable(),
  manual_containers: list(z.string()),
});
export type ApplicationDetail = z.infer<typeof ApplicationDetailSchema>;

/** PATCH body: empty strings clear a field, omitted fields are unchanged. */
export const UpdateApplicationRequestSchema = z.object({
  display_name: z.string().trim().max(200, "Use at most 200 characters.").optional(),
  owner: z.string().trim().max(200, "Use at most 200 characters.").optional(),
  environment: z.union([z.enum(ENVIRONMENTS), z.literal("")]).optional(),
  criticality: z.union([z.enum(CRITICALITIES), z.literal("")]).optional(),
});
export type UpdateApplicationRequest = z.infer<typeof UpdateApplicationRequestSchema>;

export const CreateApplicationRequestSchema = z.object({
  name: z.string().trim().min(1, "Enter a name.").max(200, "Use at most 200 characters."),
  host_id: z.string().min(1, "Choose a host."),
  containers: z.array(z.string()).min(1, "Pick at least one container."),
});
export type CreateApplicationRequest = z.infer<typeof CreateApplicationRequestSchema>;

export const CreateApplicationResponseSchema = z.object({ id: z.string() });

export const ComposeFileSchema = z.object({
  path: z.string(),
  content: z.string(),
  masked: z.boolean(),
  error: z.string().optional(),
});
export type ComposeFile = z.infer<typeof ComposeFileSchema>;

export const ComposeSchema = z.object({
  source: z.enum(["original", "reconstructed"]),
  reason: z.string().optional(),
  config_files: list(ComposeFileSchema),
  env_files: list(ComposeFileSchema),
  reconstructed: z.string().optional(),
  revealed: z.boolean(),
  secrets: z.record(z.string(), z.string()).optional(),
});
export type Compose = z.infer<typeof ComposeSchema>;
