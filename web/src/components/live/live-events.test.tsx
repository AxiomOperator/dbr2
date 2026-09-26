// SPDX-License-Identifier: Apache-2.0
import { act, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApplicationJobProgress, componentFraction, componentPosition } from "@/components/live/job-progress";
import { LiveEventsProvider, LiveIndicator, useLiveEvent } from "@/components/live/live-events";
import { queryKeys } from "@/lib/api/endpoints";
import { AgentSchema } from "@/lib/api/fleet-schemas";
import type { LiveEvent } from "@/lib/live/events";
import { AGENT_ACTIVE } from "@/test/fleet-fixtures";
import { renderWithQuery } from "@/test/render";

/** A controllable stand-in for the browser's EventSource. */
class MockEventSource {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSED = 2;
  static instances: MockEventSource[] = [];

  readonly url: string;
  readonly withCredentials: boolean;
  readyState = MockEventSource.CONNECTING;
  onopen: ((e: Event) => void) | null = null;
  onerror: ((e: Event) => void) | null = null;
  private listeners = new Map<string, ((e: MessageEvent) => void)[]>();

  constructor(url: string, init?: { withCredentials?: boolean }) {
    this.url = url;
    this.withCredentials = init?.withCredentials ?? false;
    MockEventSource.instances.push(this);
  }

  addEventListener(type: string, fn: (e: MessageEvent) => void) {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), fn]);
  }

  close() {
    this.readyState = MockEventSource.CLOSED;
  }

  open() {
    this.readyState = MockEventSource.OPEN;
    this.onopen?.(new Event("open"));
  }

  emit(type: string, data: unknown) {
    const msg = new MessageEvent(type, { data: typeof data === "string" ? data : JSON.stringify(data) });
    for (const fn of this.listeners.get(type) ?? []) fn(msg);
  }

  /** The server refused or ended the stream. */
  fail() {
    this.readyState = MockEventSource.CLOSED;
    this.onerror?.(new Event("error"));
  }

  static latest(): MockEventSource {
    const es = MockEventSource.instances.at(-1);
    if (!es) throw new Error("no EventSource created");
    return es;
  }
}

const APP = "5f939a00-fccb-4376-a6cc-37eeb5542abe";

beforeEach(() => {
  MockEventSource.instances = [];
  vi.stubGlobal("EventSource", MockEventSource);
});

afterEach(() => {
  vi.useRealTimers();
});

describe("LiveEventsProvider", () => {
  it("opens one same-origin stream and reports its state", async () => {
    renderWithQuery(
      <LiveEventsProvider>
        <LiveIndicator />
      </LiveEventsProvider>,
    );
    expect(MockEventSource.instances).toHaveLength(1);
    const es = MockEventSource.latest();
    expect(es.url).toBe("/api/v1/events");
    expect(es.withCredentials).toBe(true);
    expect(screen.getByTestId("live-indicator")).toHaveAttribute("data-status", "connecting");
    act(() => es.open());
    expect(screen.getByTestId("live-indicator")).toHaveAttribute("data-status", "live");
    expect(screen.getByText("Live updates on")).toBeInTheDocument();
  });

  it("re-reads live state after connecting and invalidates the caches an event affects", async () => {
    const { client } = renderWithQuery(<LiveEventsProvider>{null}</LiveEventsProvider>);
    const spy = vi.spyOn(client, "invalidateQueries");
    const es = MockEventSource.latest();
    act(() => es.open());
    await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: queryKeys.jobsAll }));
    expect(spy).toHaveBeenCalledWith({ queryKey: queryKeys.applications });
    spy.mockClear();
    act(() => es.emit("backup.updated", { recovery_point_id: "rp_1", application_id: APP, state: "committed" }));
    await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: queryKeys.recoveryPointsAll }));
    expect(spy).toHaveBeenCalledWith({ queryKey: queryKeys.volumesAll });
    // Coalesced: each key once.
    expect(spy.mock.calls.filter((c) => JSON.stringify(c[0]) === JSON.stringify({ queryKey: queryKeys.jobsAll }))).toHaveLength(1);
  });

  it("ignores payloads outside the contract", async () => {
    const seen: LiveEvent[] = [];
    function Spy() {
      useLiveEvent((e) => seen.push(e));
      return null;
    }
    renderWithQuery(
      <LiveEventsProvider>
        <Spy />
      </LiveEventsProvider>,
    );
    const es = MockEventSource.latest();
    act(() => {
      es.emit("agent.status", "{broken");
      es.emit("agent.status", { host_id: "h" });
      es.emit("agent.status", { host_id: "h", connected: true });
    });
    expect(seen).toEqual([{ type: "agent.status", data: { host_id: "h", connected: true } }]);
  });

  it("shows per-component backup progress from job.progress", async () => {
    renderWithQuery(
      <LiveEventsProvider>
        <ApplicationJobProgress applicationId={APP} running="backup" />
      </LiveEventsProvider>,
    );
    const es = MockEventSource.latest();
    act(() => es.open());
    expect(screen.getByTestId("job-progress-waiting")).toHaveTextContent("waiting for progress");
    act(() =>
      es.emit("job.progress", {
        command_id: `application/${APP}/r/snapshot-components`,
        host_id: "h",
        kind: "snapshot_components",
        state: "running",
        application_id: APP,
        progress: { component: "volume:shop_pgdata", hashed_bytes: 3 * 1024 ** 2, uploaded_bytes: 1024 ** 2, files: 120, done: 1, total: 3 },
      }),
    );
    const view = screen.getByTestId("job-progress");
    expect(view).toHaveTextContent("Backup");
    expect(view).toHaveTextContent("Component 2 of 3");
    expect(view).toHaveTextContent("volume:shop_pgdata");
    expect(view).toHaveTextContent("3.0 MiB");
    expect(view).toHaveTextContent("1.0 MiB");
    expect(screen.getByRole("progressbar", { name: "Components finished" })).toHaveAttribute("aria-valuenow", "1");
  });

  it("patches host connection state in place on agent.status", async () => {
    const { client } = renderWithQuery(<LiveEventsProvider>{null}</LiveEventsProvider>);
    const agent = AgentSchema.parse(AGENT_ACTIVE);
    client.setQueryData(queryKeys.agents, [agent]);
    client.setQueryData(queryKeys.agent(agent.id), agent);
    act(() => MockEventSource.latest().emit("agent.status", { host_id: agent.id, connected: false }));
    expect(client.getQueryData<typeof agent[]>(queryKeys.agents)?.[0]?.connected).toBe(false);
    expect(client.getQueryData<typeof agent>(queryKeys.agent(agent.id))?.connected).toBe(false);
  });

  it("raises a toast for alert.created", async () => {
    renderWithQuery(<LiveEventsProvider>{null}</LiveEventsProvider>);
    act(() =>
      MockEventSource.latest().emit("alert.created", {
        severity: "critical",
        type: "backup.failed",
        target_type: "application",
        target_id: APP,
        message: "Backup of Web shop failed",
      }),
    );
    expect(await screen.findByText("Critical alert")).toBeInTheDocument();
    expect(screen.getByText("Backup of Web shop failed")).toBeInTheDocument();
  });

  it("reconnects with backoff when the server closes the stream", async () => {
    vi.useFakeTimers();
    renderWithQuery(
      <LiveEventsProvider>
        <LiveIndicator />
      </LiveEventsProvider>,
    );
    const first = MockEventSource.latest();
    act(() => first.open());
    act(() => first.fail());
    expect(screen.getByTestId("live-indicator")).toHaveAttribute("data-status", "offline");
    expect(MockEventSource.instances).toHaveLength(1);
    await act(async () => {
      vi.advanceTimersByTime(1_300);
    });
    expect(MockEventSource.instances).toHaveLength(2);
    act(() => MockEventSource.latest().open());
    expect(screen.getByTestId("live-indicator")).toHaveAttribute("data-status", "live");
  });

  it("closes the stream on unmount", () => {
    const { unmount } = renderWithQuery(<LiveEventsProvider>{null}</LiveEventsProvider>);
    const es = MockEventSource.latest();
    unmount();
    expect(es.readyState).toBe(MockEventSource.CLOSED);
  });
});

describe("progress helpers", () => {
  it("formats the component position and fraction", () => {
    expect(componentPosition({ done: 0, total: 4 })).toBe("Component 1 of 4");
    expect(componentPosition({ done: 4, total: 4 })).toBe("Component 4 of 4");
    expect(componentPosition({ done: undefined, total: undefined })).toBeNull();
    expect(componentFraction({ done: 1, total: 4 })).toBe(0.25);
    expect(componentFraction({ done: 9, total: 4 })).toBe(1);
  });
});
