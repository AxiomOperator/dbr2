// SPDX-License-Identifier: Apache-2.0
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, type RenderOptions } from "@testing-library/react";
import type { ReactElement, ReactNode } from "react";

/** Renders `ui` inside a fresh, non-retrying QueryClient. */
export function renderWithQuery(ui: ReactElement, options?: RenderOptions) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: Infinity }, mutations: { retry: false } },
  });
  const Wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  return { client, ...render(ui, { wrapper: Wrapper, ...options }) };
}

/** A fetch stub answering by `METHOD path` from a routing table. */
export function routeFetch(
  routes: Record<string, (init: RequestInit | undefined) => Response | Promise<Response>>,
) {
  return async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
    const method = (init?.method ?? "GET").toUpperCase();
    const path = url.split("?")[0];
    const handler = routes[`${method} ${path}`];
    if (!handler) throw new Error(`unexpected request ${method} ${url}`);
    return handler(init);
  };
}
