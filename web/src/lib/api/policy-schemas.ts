// SPDX-License-Identifier: Apache-2.0
//
// Protection Policies, Recovery Contracts and notifications (Phase 7).
// Response schemas are the generated OpenAPI Zod schemas wrapped by
// `contract()`; request schemas are the console's form schemas (user-facing
// messages), typed against the generated request types and holding only
// writable fields (the server rejects unknown properties).

import { z } from "zod";
import { contract } from "./contract";
import type {
  AssignPolicyRequest,
  CreateNotificationChannelRequestWritable,
  PolicyBody,
  PutContractRequest,
  UpdateNotificationChannelRequestWritable,
  UpdateSmtpSettingsRequestWritable,
} from "./generated";
import {
  zContractDto,
  zListContractsResponse,
  zListNotificationChannelsResponse,
  zListNotificationDeliveriesResponse,
  zListOutBody,
  zNotificationChannelDto,
  zPolicyDto,
  zPolicyOutBody,
  zSmtpSettingsDto,
  zTestNotificationChannelResponse,
} from "./generated/zod.gen";
import { CONSISTENCY_MODES } from "./protection-schemas";
import { MIN_WEBHOOK_SECRET, SEVERITIES } from "@/lib/notifications";

// ---------------------------------------------------------------------------
// Policies
// ---------------------------------------------------------------------------

export const PolicySchema = contract(zPolicyDto);
export type Policy = z.infer<typeof PolicySchema>;
export const PolicyListSchema = contract(zListOutBody);
export const PolicyDetailSchema = contract(zPolicyOutBody);
export type PolicyDetail = z.infer<typeof PolicyDetailSchema>;

const count = (label: string, min: number) =>
  z
    .number({ error: `${label}: enter a whole number.` })
    .int(`${label}: enter a whole number.`)
    .min(min, min === 1 ? `${label} must be at least 1.` : `${label} cannot be negative.`)
    .max(100_000, `${label} is too large.`);

export const PolicyRequestSchema = z.object({
  name: z.string().trim().min(1, "Enter a name.").max(100, "Use at most 100 characters."),
  description: z.string().trim().max(500, "Use at most 500 characters.").optional(),
  schedule: z.string().trim().min(1, "Choose a schedule or enter a cron expression."),
  timezone: z.string().trim().min(1, "Choose a timezone.").optional(),
  enabled: z.boolean(),
  consistency_mode: z.enum(CONSISTENCY_MODES).optional(),
  repository_id: z.string().optional(),
  retention: z.object({
    keep_last: count("Keep last", 1),
    keep_hourly: count("Hourly", 0),
    keep_daily: count("Daily", 0),
    keep_weekly: count("Weekly", 0),
    keep_monthly: count("Monthly", 0),
    keep_yearly: count("Yearly", 0),
  }),
}) satisfies z.ZodType<PolicyBody>;
export type PolicyRequest = z.infer<typeof PolicyRequestSchema>;

export const AssignPolicyRequestSchema = z.object({
  policy_id: z.string().nullable(),
}) satisfies z.ZodType<AssignPolicyRequest>;

// ---------------------------------------------------------------------------
// Recovery Contracts
// ---------------------------------------------------------------------------

export const ContractSchema = contract(zContractDto);
export type Contract = z.infer<typeof ContractSchema>;
export type ContractState = Contract["state"];
export const CONTRACT_STATES = ["satisfied", "violated", "unknown"] as const satisfies readonly ContractState[];
export const ContractListSchema = contract(zListContractsResponse);

export const MAX_RPO_MINUTES = 525_600; // one year

export const ContractRequestSchema = z.object({
  max_rpo_minutes: z
    .number({ error: "Enter the maximum RPO in minutes." })
    .int("The maximum RPO is a whole number of minutes.")
    .min(1, "The maximum RPO is at least 1 minute.")
    .max(MAX_RPO_MINUTES, "The maximum RPO is at most one year.")
    .optional(),
  required_components: z.array(z.string()),
}) satisfies z.ZodType<PutContractRequest>;
export type ContractRequest = z.infer<typeof ContractRequestSchema>;

/** "45 minutes", "4 hours", "1 day 6 hours". */
export function formatMinutes(total: number): string {
  if (!Number.isFinite(total) || total <= 0) return "—";
  const d = Math.floor(total / 1440);
  const h = Math.floor((total % 1440) / 60);
  const m = total % 60;
  const parts: string[] = [];
  if (d) parts.push(`${d} day${d === 1 ? "" : "s"}`);
  if (h) parts.push(`${h} hour${h === 1 ? "" : "s"}`);
  if (m) parts.push(`${m} minute${m === 1 ? "" : "s"}`);
  return parts.join(" ");
}

// ---------------------------------------------------------------------------
// Notification channels and SMTP
// ---------------------------------------------------------------------------

export const NotificationChannelSchema = contract(zNotificationChannelDto);
export type NotificationChannel = z.infer<typeof NotificationChannelSchema>;
export type ChannelKind = NotificationChannel["kind"];
export const NotificationChannelListSchema = contract(zListNotificationChannelsResponse);
export const DeliveryListSchema = contract(zListNotificationDeliveriesResponse);
export type Delivery = z.infer<typeof DeliveryListSchema>["items"][number];
export const TestResultSchema = contract(zTestNotificationChannelResponse);
export type TestResult = z.infer<typeof TestResultSchema>;

const channelFields = {
  name: z.string().trim().min(1, "Enter a name.").max(100, "Use at most 100 characters."),
  config: z.object({
    to: z.array(z.string()).max(50, "Use at most 50 recipients.").optional(),
    url: z.string().max(2000, "The URL is too long.").optional(),
  }),
  enabled: z.boolean(),
  events: z.array(z.string()).max(100, "Use at most 100 event patterns."),
  min_severity: z.enum(SEVERITIES),
  secret: z
    .string()
    .max(500, "The secret is too long.")
    .refine((v) => v === "" || v.length >= MIN_WEBHOOK_SECRET, {
      message: `The signing secret must be at least ${MIN_WEBHOOK_SECRET} characters.`,
    })
    .optional(),
};

export const CreateChannelRequestSchema = z.object({
  kind: z.enum(["email", "webhook"]),
  ...channelFields,
}) satisfies z.ZodType<CreateNotificationChannelRequestWritable>;
export type CreateChannelRequest = z.infer<typeof CreateChannelRequestSchema>;

export const UpdateChannelRequestSchema = z.object(channelFields) satisfies z.ZodType<UpdateNotificationChannelRequestWritable>;
export type UpdateChannelRequest = z.infer<typeof UpdateChannelRequestSchema>;

export const SmtpSettingsSchema = contract(zSmtpSettingsDto);
export type SmtpSettings = z.infer<typeof SmtpSettingsSchema>;
export const SMTP_TLS_MODES = ["starttls", "tls", "none"] as const;
export type SmtpTls = (typeof SMTP_TLS_MODES)[number];
export const SMTP_TLS_LABEL: Record<SmtpTls, string> = {
  starttls: "STARTTLS (usually port 587)",
  tls: "Implicit TLS (usually port 465)",
  none: "None: plain text, only for a relay on localhost",
};

export const SmtpRequestSchema = z.object({
  host: z.string().trim().min(1, "Enter the SMTP host.").max(253, "The host name is too long."),
  port: z
    .number({ error: "Enter the port." })
    .int("Enter a whole port number.")
    .min(1, "The port is 1–65535.")
    .max(65535, "The port is 1–65535."),
  username: z.string().trim().max(200, "Use at most 200 characters.").optional(),
  password: z.string().max(500, "The password is too long.").optional(),
  from: z.string().trim().min(3, "Enter the sender address, e.g. DBR2 <dbr2@example.com>.").max(320, "The sender is too long."),
  tls: z.enum(SMTP_TLS_MODES),
}) satisfies z.ZodType<UpdateSmtpSettingsRequestWritable>;
export type SmtpRequest = z.infer<typeof SmtpRequestSchema>;
