// SPDX-License-Identifier: Apache-2.0
//
// Hosts (Phase 2), Applications (Phase 3) and the fleet-wide container /
// volume lists (Phase 6). Response schemas are the generated OpenAPI Zod
// schemas wrapped by `contract()` (nullable arrays normalised to `[]`); the
// request schemas are the console's form schemas, typed against the generated
// request types.

import { z } from "zod";
import { contract } from "./contract";
import type {
  CreateApplicationRequest as CreateApplicationRequestContract,
  CreateRegistrationTokenRequest as CreateRegistrationTokenRequestContract,
  ReasonInputBody,
  UpdateApplicationRequest as UpdateApplicationRequestContract,
} from "./generated";
import {
  zAgentDto,
  zApplication,
  zApplicationDetail,
  zApplicationSummary,
  zComposeDto,
  zContainerDto,
  zCreateApplicationResponse,
  zCreateRegistrationTokenResponse,
  zDiscoverAgentResponse,
  zGetAgentInventoryResponse,
  zListAgentsResponse,
  zListApplicationsResponse,
  zListContainersResponse,
  zListRegistrationTokensResponse,
  zListVolumesResponse,
  zProtection,
  zRegistrationTokenDto,
} from "./generated/zod.gen";

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
export type AgentStatus = (typeof AGENT_STATUSES)[number];

export const AgentSchema = contract(zAgentDto);
export type Agent = z.infer<typeof AgentSchema>;

export const AgentListSchema = contract(zListAgentsResponse);
export type AgentList = z.infer<typeof AgentListSchema>;

/** Body of approve / suspend / resume / revoke (`ReasonInputBody`). */
export const ReasonRequestSchema = z.object({
  reason: z
    .string()
    .trim()
    .min(1, "Enter a reason; it is stored in the audit log.")
    .max(500, "Use at most 500 characters."),
}) satisfies z.ZodType<ReasonInputBody>;
export type ReasonRequest = z.infer<typeof ReasonRequestSchema>;

export const DiscoverResponseSchema = contract(zDiscoverAgentResponse);
export type DiscoverResponse = z.infer<typeof DiscoverResponseSchema>;

export const RegistrationTokenSchema = contract(zRegistrationTokenDto);
export type RegistrationToken = z.infer<typeof RegistrationTokenSchema>;

export const RegistrationTokenListSchema = contract(zListRegistrationTokensResponse);

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
}) satisfies z.ZodType<CreateRegistrationTokenRequestContract>;
export type CreateRegistrationTokenRequest = z.infer<typeof CreateRegistrationTokenRequestSchema>;

export const CreateRegistrationTokenResponseSchema = contract(zCreateRegistrationTokenResponse);
export type CreateRegistrationTokenResponse = z.infer<typeof CreateRegistrationTokenResponseSchema>;

// ---------------------------------------------------------------------------
// Host inventory
// ---------------------------------------------------------------------------

export const AgentInventorySchema = contract(zGetAgentInventoryResponse);
export type AgentInventory = z.infer<typeof AgentInventorySchema>;
export type Inventory = AgentInventory["inventory"];
export type HostFacts = Inventory["host"];

// ---------------------------------------------------------------------------
// Applications
// ---------------------------------------------------------------------------

export const APPLICATION_KINDS = ["compose", "container", "manual"] as const;
export type ApplicationKind = (typeof APPLICATION_KINDS)[number];
export type ApplicationSource = "original" | "reconstructed" | "unknown";

export const ENVIRONMENTS = ["production", "staging", "development", "test", "other"] as const;
export const CRITICALITIES = ["critical", "high", "medium", "low"] as const;

export const ApplicationSummarySchema = contract(zApplicationSummary);
export type ApplicationSummary = z.infer<typeof ApplicationSummarySchema>;

export const ApplicationListSchema = contract(zListApplicationsResponse);

/** Protection status and coverage of an application (Phase 6). */
export const ProtectionSchema = contract(zProtection);
export type Protection = z.infer<typeof ProtectionSchema>;
export type ProtectionStatus = Protection["status"];
export type ComponentCoverage = Protection["components"][number];
export const PROTECTION_STATUSES = ["protected", "at_risk", "failed", "unprotected"] as const satisfies readonly ProtectionStatus[];

/** The `analysis` object of an application (OpenAPI `Application`). */
export const AnalysisSchema = contract(zApplication);
export type Analysis = z.infer<typeof AnalysisSchema>;
export type Service = Analysis["services"][number];
export type VolumeUse = Analysis["volumes"][number];
export type BindUse = Analysis["bind_mounts"][number];
export type ImageRef = Analysis["images"][number];
export type Unprotected = Analysis["unprotected"][number];

/** Volume classes (ADR-0006). */
export const VOLUME_CLASSES = ["local", "external", "ephemeral"] as const;

export const ContainerDetailSchema = contract(zContainerDto);
export type ContainerDetail = z.infer<typeof ContainerDetailSchema>;
export type EnvVar = ContainerDetail["env"][number];

export const ApplicationDetailSchema = contract(zApplicationDetail);
export type ApplicationDetail = z.infer<typeof ApplicationDetailSchema>;

/** PATCH body: empty strings clear a field, omitted fields are unchanged. */
export const UpdateApplicationRequestSchema = z.object({
  display_name: z.string().trim().max(200, "Use at most 200 characters.").optional(),
  owner: z.string().trim().max(200, "Use at most 200 characters.").optional(),
  environment: z.union([z.enum(ENVIRONMENTS), z.literal("")]).optional(),
  criticality: z.union([z.enum(CRITICALITIES), z.literal("")]).optional(),
}) satisfies z.ZodType<UpdateApplicationRequestContract>;
export type UpdateApplicationRequest = z.infer<typeof UpdateApplicationRequestSchema>;

export const CreateApplicationRequestSchema = z.object({
  name: z.string().trim().min(1, "Enter a name.").max(200, "Use at most 200 characters."),
  host_id: z.string().min(1, "Choose a host."),
  containers: z.array(z.string()).min(1, "Pick at least one container."),
}) satisfies z.ZodType<CreateApplicationRequestContract>;
export type CreateApplicationRequest = z.infer<typeof CreateApplicationRequestSchema>;

export const CreateApplicationResponseSchema = contract(zCreateApplicationResponse);

export const ComposeSchema = contract(zComposeDto);
export type Compose = z.infer<typeof ComposeSchema>;
export type ComposeFile = Compose["config_files"][number];

// ---------------------------------------------------------------------------
// Fleet-wide containers and volumes (Phase 6)
// ---------------------------------------------------------------------------

export const FleetContainerListSchema = contract(zListContainersResponse);
export type FleetContainer = z.infer<typeof FleetContainerListSchema>["items"][number];

export const FleetVolumeListSchema = contract(zListVolumesResponse);
export type FleetVolume = z.infer<typeof FleetVolumeListSchema>["items"][number];

/** Classes of a fleet volume: the ADR-0006 classes plus "unused" (no container mounts it). */
export const FLEET_VOLUME_CLASSES = ["local", "external", "ephemeral", "unused"] as const;
