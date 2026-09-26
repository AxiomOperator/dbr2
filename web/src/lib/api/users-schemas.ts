// SPDX-License-Identifier: Apache-2.0
//
// Users, roles and Entra ID group → role mappings (Phase 6 console; the API
// exists since Phase 1). Response schemas are the generated OpenAPI Zod
// schemas wrapped by `contract()`; request schemas are form schemas typed
// against the generated request types.

import { z } from "zod";
import { contract } from "./contract";
import type { GroupMapping as GroupMappingContract, SetRolesInputBody, SetStatusInputBody } from "./generated";
import { zMappingsOutputBody, zRolesOutputBody, zUsersOutputBody } from "./generated/zod.gen";

export const PERMISSION_USER_READ = "user.read";
export const PERMISSION_USER_MANAGE = "user.manage";

export const UserListSchema = contract(zUsersOutputBody);
export type User = z.infer<typeof UserListSchema>["items"][number];
export type RoleAssignment = User["roles"][number];

export const RoleListSchema = contract(zRolesOutputBody);
export type Role = z.infer<typeof RoleListSchema>["items"][number];

export const GroupMappingListSchema = contract(zMappingsOutputBody);
export type GroupMapping = z.infer<typeof GroupMappingListSchema>["items"][number];

const reason = z
  .string()
  .trim()
  .min(1, "Enter a reason; it is stored in the audit log.")
  .max(500, "Use at most 500 characters.");

export const SetRolesRequestSchema = z.object({
  roles: z.array(z.string()),
  reason,
}) satisfies z.ZodType<SetRolesInputBody>;
export type SetRolesRequest = z.infer<typeof SetRolesRequestSchema>;

export const SetStatusRequestSchema = z.object({
  disabled: z.boolean(),
  reason,
}) satisfies z.ZodType<SetStatusInputBody>;
export type SetStatusRequest = z.infer<typeof SetStatusRequestSchema>;

/** The OIDC provider whose groups are mapped (Microsoft Entra ID). */
export const ENTRA_PROVIDER = "entra";

export const GroupMappingRequestSchema = z.object({
  provider: z.string().min(1),
  group_id: z
    .string()
    .trim()
    .min(1, "Enter the Entra ID group object ID.")
    .max(256, "Use at most 256 characters."),
  role: z.string().min(1, "Choose a role."),
}) satisfies z.ZodType<GroupMappingContract>;
export type GroupMappingRequest = z.infer<typeof GroupMappingRequestSchema>;

/** Where a role assignment comes from, for display. */
export const ROLE_SOURCE_LABEL: Record<RoleAssignment["source"], string> = {
  manual: "Assigned manually",
  oidc_group: "From an Entra ID group",
  master_admin: "Master admin",
};
