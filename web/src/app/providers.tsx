// SPDX-License-Identifier: Apache-2.0
"use client";

import { MutationCache, QueryCache, QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ThemeProvider } from "next-themes";
import { useState, type ReactNode } from "react";
import { ToastProvider } from "@/components/toast";
import { isApiError } from "@/lib/api/client";
import { queryKeys } from "@/lib/api/endpoints";

export function makeQueryClient(): QueryClient {
  // Any 401 outside of /auth/me means the session may have expired: re-check
  // `me`, and the AuthGuard redirects to /login if it is really gone.
  const onUnauthorized = (err: unknown, isMeQuery: boolean) => {
    if (isApiError(err) && err.status === 401 && !isMeQuery) {
      void client.invalidateQueries({ queryKey: queryKeys.me });
    }
  };

  const client: QueryClient = new QueryClient({
    queryCache: new QueryCache({
      onError: (err, query) =>
        onUnauthorized(err, query.queryKey[0] === "auth" && query.queryKey[1] === "me"),
    }),
    mutationCache: new MutationCache({
      onError: (err, _vars, _ctx, mutation) => {
        if (mutation.meta?.skipSessionCheck) return;
        onUnauthorized(err, false);
      },
    }),
    defaultOptions: {
      queries: {
        staleTime: 30_000,
        refetchOnWindowFocus: true,
        retry: (failureCount, err) => {
          // Client errors are final; retry transient failures twice.
          if (isApiError(err) && err.status >= 400 && err.status < 500) return false;
          return failureCount < 2;
        },
      },
      mutations: { retry: false },
    },
  });
  return client;
}

export function Providers({ children, nonce }: { children: ReactNode; nonce?: string }) {
  const [queryClient] = useState(makeQueryClient);
  return (
    // `nonce` lets next-themes' inline anti-flash script run under the CSP.
    <ThemeProvider attribute="class" defaultTheme="system" enableSystem disableTransitionOnChange nonce={nonce}>
      <QueryClientProvider client={queryClient}>
        <ToastProvider>{children}</ToastProvider>
      </QueryClientProvider>
    </ThemeProvider>
  );
}
