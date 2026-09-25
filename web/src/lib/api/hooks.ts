// SPDX-License-Identifier: Apache-2.0
"use client";

import { useQuery } from "@tanstack/react-query";
import { api, queryKeys } from "./endpoints";
import type { RecoveryPointState } from "./protection-schemas";

export function useMe() {
  return useQuery({
    queryKey: queryKeys.me,
    queryFn: ({ signal }) => api.me(signal),
    retry: false,
    staleTime: 60_000,
  });
}

export function useVersion() {
  return useQuery({
    queryKey: queryKeys.version,
    queryFn: ({ signal }) => api.version(signal),
    staleTime: 5 * 60_000,
    refetchOnWindowFocus: false,
  });
}

export function useReadiness() {
  return useQuery({
    queryKey: queryKeys.ready,
    queryFn: ({ signal }) => api.ready(signal),
    refetchInterval: 30_000,
  });
}

export function useAuthProviders() {
  return useQuery({
    queryKey: queryKeys.providers,
    queryFn: ({ signal }) => api.authProviders(signal),
    staleTime: 5 * 60_000,
  });
}

/** Host list refresh interval (live connection state and health). */
export const AGENTS_REFRESH_MS = 15_000;

export function useAgents(options: { enabled?: boolean } = {}) {
  return useQuery({
    queryKey: queryKeys.agents,
    queryFn: ({ signal }) => api.agents(signal),
    refetchInterval: AGENTS_REFRESH_MS,
    enabled: options.enabled ?? true,
  });
}

export function useAgent(id: string) {
  return useQuery({
    queryKey: queryKeys.agent(id),
    queryFn: ({ signal }) => api.agent(id, signal),
    refetchInterval: AGENTS_REFRESH_MS,
  });
}

export function useAgentInventory(id: string, options: { enabled?: boolean } = {}) {
  return useQuery({
    queryKey: queryKeys.agentInventory(id),
    queryFn: ({ signal }) => api.agentInventory(id, signal),
    enabled: options.enabled ?? true,
    staleTime: 60_000,
  });
}

export function useRegistrationTokens(options: { enabled?: boolean } = {}) {
  return useQuery({
    queryKey: queryKeys.registrationTokens,
    queryFn: ({ signal }) => api.registrationTokens(signal),
    enabled: options.enabled ?? true,
  });
}

export function useApplications(options: { enabled?: boolean } = {}) {
  return useQuery({
    queryKey: queryKeys.applications,
    queryFn: ({ signal }) => api.applications(signal),
    enabled: options.enabled ?? true,
  });
}

export function useApplication(id: string) {
  return useQuery({
    queryKey: queryKeys.application(id),
    queryFn: ({ signal }) => api.application(id, signal),
  });
}

/** The masked Compose definition (safe to cache). */
export function useApplicationCompose(id: string, options: { enabled?: boolean } = {}) {
  return useQuery({
    queryKey: queryKeys.applicationCompose(id),
    queryFn: ({ signal }) => api.applicationCompose(id, false, signal),
    enabled: options.enabled ?? true,
  });
}

// --- Repositories & backup (Phase 4) ----------------------------------------

/** Repository list refresh interval (live storage health and capacity). */
export const REPOSITORIES_REFRESH_MS = 30_000;
/** Recovery point and alert refresh interval (backups finish asynchronously). */
export const BACKUPS_REFRESH_MS = 15_000;

export function useEscrowRecipients(options: { enabled?: boolean } = {}) {
  return useQuery({
    queryKey: queryKeys.escrowRecipients,
    queryFn: ({ signal }) => api.escrowRecipients(signal),
    enabled: options.enabled ?? true,
  });
}

export function useRepositories(options: { enabled?: boolean } = {}) {
  return useQuery({
    queryKey: queryKeys.repositories,
    queryFn: ({ signal }) => api.repositories(signal),
    refetchInterval: REPOSITORIES_REFRESH_MS,
    enabled: options.enabled ?? true,
  });
}

export function useRepository(id: string) {
  return useQuery({
    queryKey: queryKeys.repository(id),
    queryFn: ({ signal }) => api.repository(id, signal),
    refetchInterval: REPOSITORIES_REFRESH_MS,
  });
}

export function useBackupSettings(applicationId: string, options: { enabled?: boolean } = {}) {
  return useQuery({
    queryKey: queryKeys.backupSettings(applicationId),
    queryFn: ({ signal }) => api.backupSettings(applicationId, signal),
    enabled: options.enabled ?? true,
  });
}

export function useRecoveryPoints(
  filters: { applicationId?: string | null; state?: RecoveryPointState | null },
  options: { enabled?: boolean } = {},
) {
  return useQuery({
    queryKey: queryKeys.recoveryPoints(filters),
    queryFn: ({ signal }) => api.recoveryPoints({ ...filters, limit: RECOVERY_POINTS_LIMIT }, signal),
    refetchInterval: BACKUPS_REFRESH_MS,
    enabled: options.enabled ?? true,
  });
}

/** Page size of recovery point lists (the API has no cursor; newest first). */
export const RECOVERY_POINTS_LIMIT = 200;

export function useRecoveryPoint(id: string) {
  return useQuery({
    queryKey: queryKeys.recoveryPoint(id),
    queryFn: ({ signal }) => api.recoveryPoint(id, signal),
  });
}

export function useAlerts(all: boolean, options: { enabled?: boolean } = {}) {
  return useQuery({
    queryKey: queryKeys.alerts(all),
    queryFn: ({ signal }) => api.alerts({ all, limit: 200 }, signal),
    refetchInterval: BACKUPS_REFRESH_MS,
    enabled: options.enabled ?? true,
  });
}

export function useHostSettings(agentId: string, options: { enabled?: boolean } = {}) {
  return useQuery({
    queryKey: queryKeys.hostSettings(agentId),
    queryFn: ({ signal }) => api.hostSettings(agentId, signal),
    enabled: options.enabled ?? true,
  });
}
