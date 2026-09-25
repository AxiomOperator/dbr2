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
