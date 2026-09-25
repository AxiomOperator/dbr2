// SPDX-License-Identifier: Apache-2.0
"use client";

import { useQuery } from "@tanstack/react-query";
import { api, queryKeys } from "./endpoints";

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
