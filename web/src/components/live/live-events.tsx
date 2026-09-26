// SPDX-License-Identifier: Apache-2.0
"use client";

// One shared EventSource for the whole console (mounted in the console
// layout, below the AuthGuard). Every event invalidates the TanStack Query
// caches it affects (coalesced), `job.progress` feeds a small per-application
// progress store, `agent.status` patches host connection badges in place and
// `alert.created` raises a toast. The stream reconnects with backoff when the
// server closes it, pauses while the tab is hidden for a while, and refetches
// everything live after every (re)connect because missed events are never
// replayed.

import { useQueryClient, type QueryKey } from "@tanstack/react-query";
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
  type ReactNode,
} from "react";
import { useToast } from "@/components/toast";
import { queryKeys } from "@/lib/api/endpoints";
import type { Agent } from "@/lib/api/fleet-schemas";
import {
  backoffMs,
  EVENTS_PATH,
  invalidationsFor,
  isActiveProgress,
  LIVE_EVENT_TYPES,
  LIVE_QUERY_PREFIXES,
  parseLiveEvent,
  toJobProgress,
  type JobProgress,
  type LiveEvent,
} from "@/lib/live/events";

export type LiveStatus = "connecting" | "live" | "offline" | "paused";

/** Pause the stream after the tab has been hidden this long. */
const HIDDEN_PAUSE_MS = 30_000;
/** Coalescing window for cache invalidations. */
const FLUSH_MS = 150;
/** Finished progress stays visible this long. */
const FINISHED_TTL_MS = 10_000;

// ---------------------------------------------------------------------------
// Progress store
// ---------------------------------------------------------------------------

class ProgressStore {
  private byApp = new Map<string, JobProgress>();
  private listeners = new Set<() => void>();

  subscribe = (fn: () => void) => {
    this.listeners.add(fn);
    return () => this.listeners.delete(fn);
  };

  get = (applicationId: string): JobProgress | null => this.byApp.get(applicationId) ?? null;

  set(p: JobProgress) {
    this.byApp.set(p.applicationId, p);
    this.emit();
  }

  /** Drops finished entries older than the TTL. */
  prune(now: number) {
    let changed = false;
    for (const [k, v] of this.byApp) {
      if (!isActiveProgress(v) && now - v.updatedAt > FINISHED_TTL_MS) {
        this.byApp.delete(k);
        changed = true;
      }
    }
    if (changed) this.emit();
  }

  private emit() {
    for (const fn of this.listeners) fn();
  }
}

interface LiveContextValue {
  status: LiveStatus;
  progress: ProgressStore;
  /** Subscribe to every parsed event (tests, special views). */
  subscribe: (fn: (e: LiveEvent) => void) => () => void;
}

const LiveContext = createContext<LiveContextValue | null>(null);
const fallbackStore = new ProgressStore();

/** Connection state of the live stream ("offline" outside the provider). */
export function useLiveStatus(): LiveStatus {
  return useContext(LiveContext)?.status ?? "offline";
}

/** Latest `job.progress` of an application's running command (null when none). */
export function useJobProgress(applicationId: string | null | undefined): JobProgress | null {
  const store = useContext(LiveContext)?.progress ?? fallbackStore;
  const get = useCallback(() => (applicationId ? store.get(applicationId) : null), [store, applicationId]);
  return useSyncExternalStore(store.subscribe, get, () => null);
}

/** Calls `fn` for every live event while mounted. */
export function useLiveEvent(fn: (e: LiveEvent) => void) {
  const ctx = useContext(LiveContext);
  const ref = useRef(fn);
  useEffect(() => {
    ref.current = fn;
  });
  useEffect(() => ctx?.subscribe((e) => ref.current(e)), [ctx]);
}

// ---------------------------------------------------------------------------
// Provider
// ---------------------------------------------------------------------------

const ALERT_TITLE: Record<string, string> = {
  critical: "Critical alert",
  warning: "Warning",
  info: "Notification",
};

export function LiveEventsProvider({ children, url = EVENTS_PATH }: { children: ReactNode; url?: string }) {
  const queryClient = useQueryClient();
  const toast = useToast();
  // Rendered only below the AuthGuard, i.e. never on the server.
  const [status, setStatus] = useState<LiveStatus>(() =>
    typeof globalThis.EventSource === "undefined" ? "offline" : "connecting",
  );
  const [progress] = useState(() => new ProgressStore());
  const listeners = useRef(new Set<(e: LiveEvent) => void>());
  const toastRef = useRef(toast);
  useEffect(() => {
    toastRef.current = toast;
  });

  useEffect(() => {
    if (typeof window === "undefined" || typeof window.EventSource === "undefined") return;
    let source: EventSource | null = null;
    let retryTimer: number | undefined;
    let hiddenTimer: number | undefined;
    let flushTimer: number | undefined;
    let failures = 0;
    let disposed = false;
    const pending = new Map<string, QueryKey>();

    const flush = () => {
      flushTimer = undefined;
      const keys = [...pending.values()];
      pending.clear();
      for (const queryKey of keys) void queryClient.invalidateQueries({ queryKey });
    };
    const invalidate = (keys: QueryKey[]) => {
      for (const k of keys) pending.set(JSON.stringify(k), k);
      if (keys.length > 0 && flushTimer === undefined) flushTimer = window.setTimeout(flush, FLUSH_MS);
    };

    const handle = (e: LiveEvent) => {
      invalidate(invalidationsFor(e));
      switch (e.type) {
        case "job.progress": {
          const p = toJobProgress(e.data);
          if (p) progress.set(p);
          break;
        }
        case "agent.status":
          queryClient.setQueryData<Agent[]>(queryKeys.agents, (cur) =>
            cur?.map((a) => (a.id === e.data.host_id ? { ...a, connected: e.data.connected } : a)),
          );
          queryClient.setQueryData<Agent>(queryKeys.agent(e.data.host_id), (cur) =>
            cur ? { ...cur, connected: e.data.connected } : cur,
          );
          break;
        case "alert.created":
          toastRef.current({
            title: ALERT_TITLE[e.data.severity] ?? "Alert",
            description: e.data.message,
            variant: e.data.severity === "critical" ? "destructive" : e.data.severity === "warning" ? "warning" : "default",
            duration: e.data.severity === "critical" ? 15_000 : 8_000,
          });
          break;
      }
      for (const fn of listeners.current) fn(e);
    };

    const connect = () => {
      if (disposed || source) return;
      setStatus("connecting");
      const es = new EventSource(url, { withCredentials: true });
      source = es;
      es.onopen = () => {
        failures = 0;
        setStatus("live");
        // Anything may have changed while disconnected: re-read live state.
        invalidate(LIVE_QUERY_PREFIXES);
      };
      es.onerror = () => {
        if (es.readyState === EventSource.CLOSED) {
          // The server refused or ended the stream (e.g. 401): reconnect
          // ourselves, with backoff. While CONNECTING the browser retries
          // on its own (retry: 5000).
          source = null;
          failures++;
          setStatus("offline");
          window.clearTimeout(retryTimer);
          retryTimer = window.setTimeout(connect, backoffMs(failures));
          // A 401 may mean the session expired: let the AuthGuard re-check.
          void queryClient.invalidateQueries({ queryKey: queryKeys.me });
        } else {
          setStatus("connecting");
        }
      };
      for (const type of LIVE_EVENT_TYPES) {
        es.addEventListener(type, (msg) => {
          const e = parseLiveEvent(type, (msg as MessageEvent<string>).data);
          if (e) handle(e);
        });
      }
    };

    const disconnect = () => {
      source?.close();
      source = null;
    };

    const onVisibility = () => {
      if (document.visibilityState === "hidden") {
        window.clearTimeout(hiddenTimer);
        hiddenTimer = window.setTimeout(() => {
          disconnect();
          window.clearTimeout(retryTimer);
          setStatus("paused");
        }, HIDDEN_PAUSE_MS);
      } else {
        window.clearTimeout(hiddenTimer);
        if (!source) {
          window.clearTimeout(retryTimer);
          connect();
        }
      }
    };

    const prune = window.setInterval(() => progress.prune(Date.now()), 5_000);
    document.addEventListener("visibilitychange", onVisibility);
    connect();
    return () => {
      disposed = true;
      document.removeEventListener("visibilitychange", onVisibility);
      window.clearTimeout(retryTimer);
      window.clearTimeout(hiddenTimer);
      window.clearTimeout(flushTimer);
      window.clearInterval(prune);
      disconnect();
    };
  }, [queryClient, url, progress]);

  const subscribe = useCallback((fn: (e: LiveEvent) => void) => {
    listeners.current.add(fn);
    return () => {
      listeners.current.delete(fn);
    };
  }, []);

  const value = useMemo(() => ({ status, progress, subscribe }), [status, progress, subscribe]);
  return <LiveContext.Provider value={value}>{children}</LiveContext.Provider>;
}

const STATUS_LABEL: Record<LiveStatus, string> = {
  live: "Live updates on",
  connecting: "Connecting to live updates…",
  offline: "Live updates unavailable; refreshing periodically",
  paused: "Live updates paused while the tab is hidden",
};

/** Small header indicator of the live stream (dot + accessible label). */
export function LiveIndicator() {
  const status = useLiveStatus();
  return (
    <span
      className="inline-flex items-center gap-1.5 rounded-md px-2 py-1 text-xs text-muted-foreground"
      title={STATUS_LABEL[status]}
      data-testid="live-indicator"
      data-status={status}
    >
      <span
        aria-hidden="true"
        className={
          status === "live"
            ? "inline-block size-2 rounded-full bg-emerald-500"
            : status === "connecting"
              ? "inline-block size-2 rounded-full bg-amber-500 motion-safe:animate-pulse"
              : "inline-block size-2 rounded-full bg-muted-foreground/40"
        }
      />
      <span className="hidden lg:inline" aria-hidden="true">{status === "live" ? "Live" : status === "connecting" ? "Connecting" : "Not live"}</span>
      <span className="sr-only">{STATUS_LABEL[status]}</span>
    </span>
  );
}
