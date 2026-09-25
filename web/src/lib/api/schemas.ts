// SPDX-License-Identifier: Apache-2.0
//
// Zod schemas for the Phase 1 DBR² API contract (`/api/v1`). Response schemas
// are deliberately non-strict (unknown keys are ignored) so that additive,
// backward-compatible API changes (ADR-0015 MINOR bumps) never break the
// console.

import { z } from "zod";

// ---------------------------------------------------------------------------
// Errors (RFC 9457 problem details)
// ---------------------------------------------------------------------------

export const ProblemSchema = z.object({
  type: z.string().optional(),
  title: z.string().optional(),
  status: z.number().int().optional(),
  detail: z.string().optional(),
  code: z.string().optional(),
  instance: z.string().optional(),
  /** Individual validation errors (400 `validation_failed`). */
  errors: z
    .array(
      z.object({
        message: z.string().optional(),
        location: z.string().optional(),
      }),
    )
    .nullish(),
});
export type Problem = z.infer<typeof ProblemSchema>;

// ---------------------------------------------------------------------------
// Version and health (public)
// ---------------------------------------------------------------------------

export const VersionSchema = z.object({
  platform: z.string(),
  components: z.record(z.string(), z.string()),
});
export type VersionInfo = z.infer<typeof VersionSchema>;

export const LivenessSchema = z.object({
  status: z.string(),
});
export type Liveness = z.infer<typeof LivenessSchema>;

export const ReadinessSchema = z.object({
  // "ok" | "degraded"; other values are shown verbatim rather than rejected.
  status: z.string(),
  checks: z.record(z.string(), z.string()).default({}),
});
export type Readiness = z.infer<typeof ReadinessSchema>;

// ---------------------------------------------------------------------------
// Authentication
// ---------------------------------------------------------------------------

export const OidcProviderSchema = z.object({
  id: z.string(),
  display_name: z.string(),
  login_url: z.string(),
});
export type OidcProvider = z.infer<typeof OidcProviderSchema>;

export const AuthProvidersSchema = z.object({
  master_admin: z.boolean(),
  oidc: z.array(OidcProviderSchema).default([]),
});
export type AuthProviders = z.infer<typeof AuthProvidersSchema>;

export const MeSchema = z.object({
  id: z.string(),
  username: z.string(),
  display_name: z.string(),
  email: z.string().nullable().optional(),
  kind: z.enum(["master_admin", "oidc"]),
  roles: z.array(z.string()),
  permissions: z.array(z.string()),
  totp_enabled: z.boolean(),
});
export type Me = z.infer<typeof MeSchema>;

export const LoginRequestSchema = z.object({
  username: z.string().trim().min(1, "Enter your username."),
  password: z.string().min(1, "Enter your password."),
  totp_code: z
    .string()
    .trim()
    .regex(/^\d{6}$/, "Enter the 6-digit code from your authenticator app.")
    .optional(),
});
export type LoginRequest = z.infer<typeof LoginRequestSchema>;

export const LoginResponseSchema = z.object({
  user: MeSchema,
});
export type LoginResponse = z.infer<typeof LoginResponseSchema>;

/** Minimum master-admin password length (mirrors the backend `weak_password` rule). */
export const MIN_PASSWORD_LENGTH = 12;

export const ChangePasswordRequestSchema = z.object({
  current_password: z.string().min(1, "Enter your current password."),
  new_password: z
    .string()
    .min(MIN_PASSWORD_LENGTH, `Use at least ${MIN_PASSWORD_LENGTH} characters.`),
});
export type ChangePasswordRequest = z.infer<typeof ChangePasswordRequestSchema>;

export const TotpCodeSchema = z
  .string()
  .trim()
  .regex(/^\d{6}$/, "Enter the 6-digit code from your authenticator app.");

export const TotpCodeRequestSchema = z.object({
  code: TotpCodeSchema,
});
export type TotpCodeRequest = z.infer<typeof TotpCodeRequestSchema>;

export const TotpEnrollResponseSchema = z.object({
  secret: z.string(),
  otpauth_url: z.string().startsWith("otpauth://"),
});
export type TotpEnrollResponse = z.infer<typeof TotpEnrollResponseSchema>;

// ---------------------------------------------------------------------------
// Audit log
// ---------------------------------------------------------------------------

export const AuditEventSchema = z.object({
  event_id: z.string(),
  occurred_at: z.string(),
  event_type: z.string(),
  actor_display: z.string().nullable().optional(),
  source_ip: z.string().nullable().optional(),
  target_type: z.string().nullable().optional(),
  target_id: z.string().nullable().optional(),
  result: z.string(),
  reason: z.string().nullable().optional(),
  details: z.unknown().optional(),
});
export type AuditEvent = z.infer<typeof AuditEventSchema>;

export const AuditEventPageSchema = z.object({
  items: z.array(AuditEventSchema),
  next_cursor: z.string().nullable(),
});
export type AuditEventPage = z.infer<typeof AuditEventPageSchema>;

/** Permission required to read the audit log. */
export const PERMISSION_AUDIT_READ = "audit.read";
