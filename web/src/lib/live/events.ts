// SPDX-License-Identifier: Apache-2.0
//
// Live updates over Server-Sent Events (GET /api/v1/events): event names,
// payload parsing with the generated OpenAPI Zod schemas, and which TanStack
// Query caches each event makes stale. Events are only HINTS to re-read
// state: missed events are never replayed, so after (re)connecting the
// console refetches everything live (see LiveEventsProvider).

import type { QueryKey } from "@tanstack/react-query";
import type { z } from "zod";
import { queryKeys } from "@/lib/api/endpoints";
import {
  zAgentStatusEvent,
  zAlertCreatedEvent,
  zBackupUpdatedEvent,
  zInventoryUpdatedEvent,
  zJobProgressEvent,
  zRestoreUpdatedEvent,
} from "@/lib/api/generated/zod.gen";

export const EVENTS_PATH = "/api/v1/events";

export const EVENT_SCHEMAS = {
  "job.progress": zJobProgressEvent,
  "backup.updated": zBackupUpdatedEvent,
  "restore.updated": zRestoreUpdatedEvent,
  "agent.status": zAgentStatusEvent,
  "alert.created": zAlertCreatedEvent,
  "inventory.updated": zInventoryUpdatedEvent,
} as const;

export type LiveEventType = keyof typeof EVENT_SCHEMAS;
export const LIVE_EVENT_TYPES = Object.keys(EVENT_SCHEMAS) as LiveEventType[];

export type LiveEventData<T extends LiveEventType> = z.output<(typeof EVENT_SCHEMAS)[T]>;
export type LiveEvent = { [T in LiveEventType]: { type: T; data: LiveEventData<T> } }[LiveEventType];

export type JobProgressEvent = LiveEventData<"job.progress">;
export type AlertCreatedEvent = LiveEventData<"alert.created">;

/** Parses one SSE message; null for unknown types or payloads outside the contract. */
export function parseLiveEvent(type: string, raw: string): LiveEvent | null {
  if (!(type in EVENT_SCHEMAS)) return null;
  let json: unknown;
  try {
    json = JSON.parse(raw);
  } catch {
    return null;
  }
  const parsed = EVENT_SCHEMAS[type as LiveEventType].safeParse(json);
  return parsed.success ? ({ type, data: parsed.data } as LiveEvent) : null;
}

/** Query-key prefixes refetched after a (re)connect, when events may have been missed. */
export const LIVE_QUERY_PREFIXES: QueryKey[] = [
  queryKeys.agents,
  queryKeys.applications,
  queryKeys.recoveryPointsAll,
  queryKeys.restoresAll,
  queryKeys.jobsAll,
  queryKeys.alertsAll,
  queryKeys.containersAll,
  queryKeys.volumesAll,
  queryKeys.repositories,
];

/**
 * The query-key prefixes an event makes stale. `job.progress` only matters
 * when a command starts or ends (its running updates go to the progress
 * store instead of refetching lists every second).
 */
export function invalidationsFor(event: LiveEvent): QueryKey[] {
  switch (event.type) {
    case "job.progress": {
      const { state, application_id } = event.data;
      if (state === "running") return [];
      const keys: QueryKey[] = [queryKeys.jobsAll];
      if (application_id) keys.push(queryKeys.application(application_id), queryKeys.applications);
      return keys;
    }
    case "backup.updated":
      return [
        queryKeys.recoveryPointsAll,
        queryKeys.jobsAll,
        queryKeys.applications,
        queryKeys.volumesAll,
        queryKeys.repositories,
      ];
    case "restore.updated":
      return [
        queryKeys.restore(event.data.restore_id),
        queryKeys.restoresAll,
        queryKeys.jobsAll,
        queryKeys.applications,
      ];
    case "agent.status":
      return [queryKeys.agents];
    case "alert.created":
      return [queryKeys.alertsAll];
    case "inventory.updated":
      return [
        queryKeys.agentInventory(event.data.host_id),
        queryKeys.applications,
        queryKeys.containersAll,
        queryKeys.volumesAll,
      ];
  }
}

/** Reconnect delay after the stream failed `attempt` times in a row (1 s … 60 s, with jitter). */
export function backoffMs(attempt: number, random: () => number = Math.random): number {
  const base = Math.min(60_000, 1_000 * 2 ** Math.max(0, attempt - 1));
  return Math.round(base * (0.8 + random() * 0.4));
}

// ---------------------------------------------------------------------------
// Job progress (per application)
// ---------------------------------------------------------------------------

/** The latest progress of a running agent command, as the console shows it. */
export interface JobProgress {
  applicationId: string;
  kind: string;
  state: string;
  workflowId?: string;
  hostId: string;
  /** Component being captured / restored now. */
  component?: string;
  hashedBytes?: number;
  uploadedBytes?: number;
  files?: number;
  /** Components finished so far and in total. */
  done?: number;
  total?: number;
  queued: boolean;
  error?: string;
  updatedAt: number;
}

const num = (v: unknown): number | undefined => (typeof v === "number" && Number.isFinite(v) ? v : undefined);

/** Folds a `job.progress` event into the progress shown for its application. */
export function toJobProgress(e: JobProgressEvent, now: number = Date.now()): JobProgress | null {
  if (!e.application_id) return null;
  const p = (e.progress && typeof e.progress === "object" ? e.progress : {}) as Record<string, unknown>;
  return {
    applicationId: e.application_id,
    kind: e.kind,
    state: e.state,
    workflowId: e.workflow_id,
    hostId: e.host_id,
    component: typeof p.component === "string" ? p.component : typeof p.image === "string" ? `image ${p.image}` : undefined,
    hashedBytes: num(p.hashed_bytes),
    uploadedBytes: num(p.uploaded_bytes),
    files: num(p.files),
    done: num(p.done),
    total: num(p.total),
    queued: p.queued === true,
    error: e.error,
    updatedAt: now,
  };
}

/** Human label of a command kind (`snapshot_components` → "Backup"). */
export function jobKindLabel(kind: string): string {
  switch (kind) {
    case "snapshot_components":
      return "Backup";
    case "restore_components":
      return "Restoring data";
    case "recreate_containers":
      return "Re-creating containers";
    case "dump_database":
      return "Database dump";
    default:
      return kind.replace(/_/g, " ");
  }
}

/** True while the command may still report progress. */
export const isActiveProgress = (p: JobProgress | null | undefined): boolean =>
  !!p && (p.state === "accepted" || p.state === "running");
