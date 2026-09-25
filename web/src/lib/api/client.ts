// SPDX-License-Identifier: Apache-2.0
//
// Typed, same-origin API client. Every request goes to `/api/v1/*` on the
// console's own origin (proxied to dbr2-server by `src/app/api/[...path]`), so
// the HttpOnly session cookie is sent automatically and unsafe requests carry
// `Sec-Fetch-Site: same-origin` for the backend's CSRF check.

import type { z } from "zod";
import { ProblemSchema, type Problem } from "./schemas";

export const API_BASE = "/api/v1";

/** Well-known problem `code` values the console reacts to. */
export const ErrorCodes = {
  invalidCredentials: "invalid_credentials",
  totpRequired: "totp_required",
  invalidTotp: "invalid_totp",
  accountLocked: "account_locked",
  rateLimited: "rate_limited",
  weakPassword: "weak_password",
  networkError: "network_error",
  invalidResponse: "invalid_response",
} as const;

export interface ApiErrorInit {
  status: number;
  code?: string;
  title?: string;
  detail?: string;
  retryAfterSeconds?: number;
  problem?: Problem;
  cause?: unknown;
}

/** Error thrown for every non-2xx response, network failure or contract violation. */
export class ApiError extends Error {
  readonly status: number;
  readonly code: string | undefined;
  readonly title: string | undefined;
  readonly detail: string | undefined;
  /** Parsed `Retry-After` header, in seconds (423 / 429 / 503). */
  readonly retryAfterSeconds: number | undefined;
  readonly problem: Problem | undefined;

  constructor(init: ApiErrorInit) {
    super(init.detail || init.title || `Request failed with status ${init.status}`, {
      cause: init.cause,
    });
    this.name = "ApiError";
    this.status = init.status;
    this.code = init.code;
    this.title = init.title;
    this.detail = init.detail;
    this.retryAfterSeconds = init.retryAfterSeconds;
    this.problem = init.problem;
  }

  get isUnauthorized(): boolean {
    return this.status === 401;
  }
}

export function isApiError(err: unknown): err is ApiError {
  return err instanceof ApiError;
}

/**
 * Parses a `Retry-After` header (delta-seconds or HTTP-date) into whole
 * seconds from `now`. Returns undefined when absent or unparseable.
 */
export function parseRetryAfter(value: string | null, now: Date = new Date()): number | undefined {
  if (value == null) return undefined;
  const trimmed = value.trim();
  if (trimmed === "") return undefined;
  if (/^\d+$/.test(trimmed)) return Number.parseInt(trimmed, 10);
  const date = Date.parse(trimmed);
  if (Number.isNaN(date)) return undefined;
  return Math.max(0, Math.ceil((date - now.getTime()) / 1000));
}

/** Human-readable "try again" hint for a Retry-After duration. */
export function formatRetryAfter(seconds: number | undefined): string {
  if (seconds === undefined) return "Try again later.";
  if (seconds <= 1) return "Try again in a moment.";
  if (seconds < 60) return `Try again in ${seconds} seconds.`;
  const minutes = Math.ceil(seconds / 60);
  return `Try again in ${minutes} minute${minutes === 1 ? "" : "s"}.`;
}

/** Builds an ApiError from a failed Response, decoding problem+json when present. */
export async function errorFromResponse(res: Response): Promise<ApiError> {
  const retryAfterSeconds = parseRetryAfter(res.headers.get("retry-after"));
  const contentType = res.headers.get("content-type") ?? "";
  let problem: Problem | undefined;
  if (contentType.includes("json")) {
    try {
      const parsed = ProblemSchema.safeParse(await res.json());
      if (parsed.success) problem = parsed.data;
    } catch {
      // Body was not valid JSON; fall through with status-only error.
    }
  }
  return new ApiError({
    status: res.status,
    code: problem?.code,
    title: problem?.title ?? (res.statusText || undefined),
    detail: problem?.detail,
    retryAfterSeconds,
    problem,
  });
}

export interface RequestOptions<T> {
  method?: "GET" | "POST" | "PUT" | "PATCH" | "DELETE";
  /** JSON-serialisable request body. */
  body?: unknown;
  /** Schema validating the success body; omit for 204 / no-content endpoints. */
  schema?: z.ZodType<T>;
  query?: Record<string, string | number | undefined | null>;
  signal?: AbortSignal;
  /** Treat these non-2xx statuses as success (the body is still validated). */
  acceptStatuses?: number[];
  /** Injectable for tests. */
  fetchImpl?: typeof fetch;
}

function buildUrl(path: string, query?: RequestOptions<unknown>["query"]): string {
  const url = `${API_BASE}${path.startsWith("/") ? path : `/${path}`}`;
  if (!query) return url;
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(query)) {
    if (value !== undefined && value !== null && value !== "") params.set(key, String(value));
  }
  const qs = params.toString();
  return qs ? `${url}?${qs}` : url;
}

/**
 * Performs an API request against `/api/v1${path}`.
 * Resolves with the schema-validated body (or `undefined` when no schema is
 * given); rejects with an {@link ApiError} otherwise.
 */
export async function apiRequest<T = undefined>(
  path: string,
  options: RequestOptions<T> = {},
): Promise<T> {
  const { method = "GET", body, schema, query, signal, acceptStatuses = [] } = options;
  const doFetch = options.fetchImpl ?? fetch;
  const headers: Record<string, string> = { accept: "application/json, application/problem+json" };
  if (body !== undefined) headers["content-type"] = "application/json";

  let res: Response;
  try {
    res = await doFetch(buildUrl(path, query), {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      credentials: "same-origin",
      cache: "no-store",
      signal,
    });
  } catch (cause) {
    if (cause instanceof DOMException && cause.name === "AbortError") throw cause;
    throw new ApiError({
      status: 0,
      code: ErrorCodes.networkError,
      title: "Network error",
      detail: "The DBR² API could not be reached.",
      cause,
    });
  }

  if (!res.ok && !acceptStatuses.includes(res.status)) {
    throw await errorFromResponse(res);
  }

  if (!schema) return undefined as T;

  let json: unknown;
  try {
    json = await res.json();
  } catch (cause) {
    throw new ApiError({
      status: res.status,
      code: ErrorCodes.invalidResponse,
      title: "Invalid response",
      detail: "The API returned a response that is not valid JSON.",
      cause,
    });
  }
  const parsed = schema.safeParse(json);
  if (!parsed.success) {
    throw new ApiError({
      status: res.status,
      code: ErrorCodes.invalidResponse,
      title: "Invalid response",
      detail: "The API response did not match the expected contract.",
      cause: parsed.error,
    });
  }
  return parsed.data;
}

/** Short, user-facing message for an error of any kind. */
export function errorMessage(err: unknown): string {
  if (isApiError(err)) {
    const base = err.detail || err.title || `Request failed (HTTP ${err.status}).`;
    if (err.status === 423 || err.status === 429) {
      return `${base} ${formatRetryAfter(err.retryAfterSeconds)}`;
    }
    return base;
  }
  if (err instanceof Error) return err.message;
  return "Something went wrong.";
}
