// SPDX-License-Identifier: Apache-2.0
//
// Phase 1 contract (auth, version, health, audit). Response schemas are the
// generated OpenAPI Zod schemas (./generated, from api/openapi.yaml) wrapped
// by `contract()`; the request schemas below are the console's form schemas
// (with user-facing messages), typed against the generated request types.
// Every request body is additionally validated against the generated schema
// in ./endpoints.ts before it leaves the browser.

import { z } from "zod";
import { contract } from "./contract";
import type { CodeInputBody, LoginInputBody, PasswordInputBody } from "./generated";
import {
  zAuditListOutputBody,
  zLoginOutputBody,
  zMe,
  zProblem,
  zProvidersOutputBody,
  zStatusBody,
  zTotpEnrollOutputBody,
  zVersionBody,
} from "./generated/zod.gen";

// ---------------------------------------------------------------------------
// Errors (RFC 9457 problem details)
// ---------------------------------------------------------------------------

export const ProblemSchema = contract(zProblem);
export type Problem = z.infer<typeof ProblemSchema>;

// ---------------------------------------------------------------------------
// Version and health (public)
// ---------------------------------------------------------------------------

export const VersionSchema = contract(zVersionBody);
export type VersionInfo = z.infer<typeof VersionSchema>;

export const LivenessSchema = contract(zStatusBody);
export type Liveness = z.infer<typeof LivenessSchema>;

export const ReadinessSchema = contract(zStatusBody);
export type Readiness = z.infer<typeof ReadinessSchema>;

// ---------------------------------------------------------------------------
// Authentication
// ---------------------------------------------------------------------------

export const AuthProvidersSchema = contract(zProvidersOutputBody);
export type AuthProviders = z.infer<typeof AuthProvidersSchema>;
export type OidcProvider = AuthProviders["oidc"][number];

export const MeSchema = contract(zMe);
export type Me = z.infer<typeof MeSchema>;

export const LoginRequestSchema = z.object({
  username: z.string().trim().min(1, "Enter your username."),
  password: z.string().min(1, "Enter your password."),
  totp_code: z
    .string()
    .trim()
    .regex(/^\d{6}$/, "Enter the 6-digit code from your authenticator app.")
    .optional(),
}) satisfies z.ZodType<LoginInputBody>;
export type LoginRequest = z.infer<typeof LoginRequestSchema>;

export const LoginResponseSchema = contract(zLoginOutputBody);
export type LoginResponse = z.infer<typeof LoginResponseSchema>;

/** Minimum master-admin password length (mirrors the backend `weak_password` rule). */
export const MIN_PASSWORD_LENGTH = 12;

export const ChangePasswordRequestSchema = z.object({
  current_password: z.string().min(1, "Enter your current password."),
  new_password: z
    .string()
    .min(MIN_PASSWORD_LENGTH, `Use at least ${MIN_PASSWORD_LENGTH} characters.`),
}) satisfies z.ZodType<PasswordInputBody>;
export type ChangePasswordRequest = z.infer<typeof ChangePasswordRequestSchema>;

export const TotpCodeSchema = z
  .string()
  .trim()
  .regex(/^\d{6}$/, "Enter the 6-digit code from your authenticator app.");

export const TotpCodeRequestSchema = z.object({
  code: TotpCodeSchema,
}) satisfies z.ZodType<CodeInputBody>;
export type TotpCodeRequest = z.infer<typeof TotpCodeRequestSchema>;

export const TotpEnrollResponseSchema = contract(zTotpEnrollOutputBody);
export type TotpEnrollResponse = z.infer<typeof TotpEnrollResponseSchema>;

// ---------------------------------------------------------------------------
// Audit log
// ---------------------------------------------------------------------------

export const AuditEventPageSchema = contract(zAuditListOutputBody);
export type AuditEventPage = z.infer<typeof AuditEventPageSchema>;
export type AuditEvent = AuditEventPage["items"][number];

/** Permission required to read the audit log. */
export const PERMISSION_AUDIT_READ = "audit.read";
