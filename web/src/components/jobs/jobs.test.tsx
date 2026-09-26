// SPDX-License-Identifier: Apache-2.0
import { act, screen, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { CurrentUserProvider } from "@/components/auth-guard";
import { jobDetail, jobHref, JobsView } from "@/components/jobs/jobs-view";
import { LiveEventsProvider } from "@/components/live/live-events";
import { JobListSchema } from "@/lib/api/protection-schemas";
import { meWith } from "@/test/fleet-fixtures";
import { APP_SHOP, JOBS } from "@/test/phase6-fixtures";
import { renderWithQuery, routeFetch } from "@/test/render";

const nav = vi.hoisted(() => ({ replace: vi.fn(), search: "" }));
vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: vi.fn(), replace: nav.replace }),
  usePathname: () => "/jobs",
  useSearchParams: () => new URLSearchParams(nav.search),
}));

beforeEach(() => {
  nav.replace.mockReset();
  nav.search = "";
});

const jobs = JobListSchema.parse({ items: JOBS }).items;

describe("job helpers", () => {
  it("links backups to their recovery point and restores to the restore", () => {
    expect(jobHref(jobs[0]!)).toBe(`/recovery-points/${JOBS[0]!.id}`);
    expect(jobHref(jobs[1]!)).toBe(`/restores/${JOBS[1]!.id}`);
  });

  it("describes the consistency mode of a backup and the step of a running restore", () => {
    expect(jobDetail(jobs[0]!)).toBe("Quiesced");
    expect(jobDetail({ ...jobs[1]!, state: "running", detail: "restore-data" })).not.toBe("restore-data");
    expect(jobDetail(jobs[1]!)).toBe("alternate host");
  });
});

describe("JobsView", () => {
  it("lists backups and restores and asks the API with the URL filters", async () => {
    const urls: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        urls.push(String(input));
        return routeFetch({ "GET /api/v1/jobs": () => Response.json({ items: JOBS }) })(input, init);
      }),
    );
    nav.search = "state=failed";
    renderWithQuery(
      <CurrentUserProvider me={meWith(["backup.read", "restore.read", "application.read"])}>
        <JobsView />
      </CurrentUserProvider>,
    );
    const table = await screen.findByRole("table", { name: "Jobs" });
    const rows = within(table).getAllByRole("row").slice(1);
    expect(rows).toHaveLength(3);
    expect(rows[1]).toHaveAttribute("data-state", "rolled_back");
    expect(within(rows[1]!).getByText("Rolled back")).toBeInTheDocument();
    expect(within(rows[1]!).getByText(/health check failed/)).toBeInTheDocument();
    expect(within(rows[2]!).getByText("Partial")).toBeInTheDocument();
    expect(within(rows[0]!).getByRole("link", { name: /Backup/ })).toHaveAttribute("href", `/recovery-points/${JOBS[0]!.id}`);
    expect(urls.some((u) => u.includes("state=failed"))).toBe(true);
  });

  it("asks for backups only without restore.read", async () => {
    const urls: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        urls.push(String(input));
        return Response.json({ items: JOBS.filter((j) => j.type === "backup") });
      }),
    );
    renderWithQuery(
      <CurrentUserProvider me={meWith(["backup.read"])}>
        <JobsView />
      </CurrentUserProvider>,
    );
    await screen.findByRole("table", { name: "Jobs" });
    expect(urls.every((u) => u.includes("type=backup"))).toBe(true);
    expect(screen.getByText(/Restores are hidden/)).toBeInTheDocument();
  });

  it("shows live progress of a running backup", async () => {
    class ES {
      static CLOSED = 2;
      static last: ES;
      readyState = 0;
      onopen: (() => void) | null = null;
      onerror: (() => void) | null = null;
      handlers = new Map<string, (e: MessageEvent) => void>();
      constructor() {
        ES.last = this;
      }
      addEventListener(t: string, fn: (e: MessageEvent) => void) {
        this.handlers.set(t, fn);
      }
      close() {}
    }
    vi.stubGlobal("EventSource", ES);
    vi.stubGlobal("fetch", vi.fn(routeFetch({ "GET /api/v1/jobs": () => Response.json({ items: JOBS }) })));
    renderWithQuery(
      <CurrentUserProvider me={meWith(["backup.read", "restore.read"])}>
        <LiveEventsProvider>
          <JobsView />
        </LiveEventsProvider>
      </CurrentUserProvider>,
    );
    await screen.findByRole("table", { name: "Jobs" });
    act(() =>
      ES.last.handlers.get("job.progress")!(
        new MessageEvent("job.progress", {
          data: JSON.stringify({
            command_id: "c",
            host_id: "h",
            kind: "snapshot_components",
            state: "running",
            application_id: APP_SHOP,
            progress: { component: "config", hashed_bytes: 10, uploaded_bytes: 5, files: 1, done: 0, total: 2 },
          }),
        }),
      ),
    );
    expect(await screen.findByText("Component 1 of 2")).toBeInTheDocument();
  });
});
