// SPDX-License-Identifier: Apache-2.0
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { SourceBadge } from "@/components/applications/app-badges";
import { ComposeView } from "@/components/applications/compose-view";
import { sortUnprotected, UnprotectedData } from "@/components/applications/unprotected-data";
import { CurrentUserProvider } from "@/components/auth-guard";
import { queryKeys } from "@/lib/api/endpoints";
import {
  COMPOSE_ORIGINAL,
  COMPOSE_ORIGINAL_REVEALED,
  meWith,
} from "@/test/fleet-fixtures";
import { renderWithQuery, routeFetch } from "@/test/render";

const APP_ID = "83b1bb43-cbd9-45cb-add1-c25c29807ba8";

const U = (container: string, path: string, severity: string) => ({
  container,
  path,
  files: 3,
  severity,
  reason: `${severity} reason`,
});

describe("UnprotectedData", () => {
  const items = [
    U("worker", "/var/log/worker", "low"),
    U("web", "/app/data", "high"),
    U("api", "/srv/cache", "low"),
    U("api", "/srv/state", "high"),
  ];

  it("sorts high severity before low without mutating the input", () => {
    const sorted = sortUnprotected(items);
    expect(sorted.map((u) => u.severity)).toEqual(["high", "high", "low", "low"]);
    expect(sorted.map((u) => u.path)).toEqual(["/srv/state", "/app/data", "/srv/cache", "/var/log/worker"]);
    expect(items[0]?.severity).toBe("low");
  });

  it("renders the rows high first with a count of high-severity paths", () => {
    renderWithQuery(<UnprotectedData items={items} />);
    expect(screen.getByRole("heading", { name: "Unprotected data" })).toBeInTheDocument();
    expect(screen.getByText("2 high-severity paths")).toBeInTheDocument();
    const table = screen.getByRole("table", { name: "Unprotected paths" });
    const rows = within(table).getAllByRole("row").slice(1);
    expect(rows.map((r) => r.getAttribute("data-severity"))).toEqual(["high", "high", "low", "low"]);
    // Severity is conveyed as text, not only colour.
    expect(within(rows[0]!).getByText("High")).toBeInTheDocument();
    expect(within(rows[3]!).getByText("Low")).toBeInTheDocument();
  });

  it("renders nothing when there is no unprotected data", () => {
    renderWithQuery(<UnprotectedData items={[]} />);
    expect(screen.queryByRole("heading", { name: "Unprotected data" })).not.toBeInTheDocument();
  });
});

describe("SourceBadge", () => {
  it("renders a distinct Reconstructed badge", () => {
    renderWithQuery(
      <>
        <SourceBadge source="reconstructed" />
        <SourceBadge source="original" />
      </>,
    );
    const rec = screen.getByText("Reconstructed");
    expect(rec).toHaveAttribute("data-source", "reconstructed");
    expect(rec).toHaveAttribute("title", expect.stringContaining("not the original"));
    expect(screen.getByText("Original")).not.toHaveAttribute("data-source");
  });
});

describe("ComposeView", () => {
  function setup(permissions: string[]) {
    const calls: string[] = [];
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      calls.push(url);
      return routeFetch({
        [`GET /api/v1/applications/${APP_ID}/compose`]: () =>
          Response.json(url.includes("reveal=true") ? COMPOSE_ORIGINAL_REVEALED : COMPOSE_ORIGINAL),
      })(input, init);
    });
    vi.stubGlobal("fetch", fetchMock);
    const view = renderWithQuery(
      <CurrentUserProvider me={meWith(permissions)}>
        <ComposeView applicationId={APP_ID} />
      </CurrentUserProvider>,
    );
    return { ...view, calls };
  }

  it("hides the Reveal button without secrets.read", async () => {
    setup(["application.read"]);
    expect(await screen.findByText("/home/garrettpost/Projects/fbcad/docker-compose.yml")).toBeInTheDocument();
    expect(screen.getAllByText("Secrets masked")).toHaveLength(2);
    expect(screen.queryByRole("button", { name: /reveal secrets/i })).not.toBeInTheDocument();
  });

  it("reveals after an audited confirmation, and forgets the values on unmount", async () => {
    const user = userEvent.setup();
    const { calls, client, unmount } = setup(["application.read", "secrets.read"]);

    await user.click(await screen.findByRole("button", { name: "Reveal secrets" }));
    const dialog = await screen.findByRole("dialog", { name: "Reveal secrets?" });
    expect(dialog).toHaveTextContent("This action is audited");
    expect(calls.some((u) => u.includes("reveal=true"))).toBe(false);

    await user.click(within(dialog).getByRole("button", { name: "Reveal secrets" }));
    expect(await screen.findByText(/hunter2-really/)).toBeInTheDocument();
    expect(screen.getByText("Secrets are visible")).toBeInTheDocument();
    expect(calls.filter((u) => u.includes("reveal=true"))).toHaveLength(1);

    const key = queryKeys.applicationComposeRevealed(APP_ID);
    expect(client.getQueryData(key)).toBeDefined();
    unmount();
    await waitFor(() => expect(client.getQueryCache().find({ queryKey: key })).toBeUndefined());
  });

  it("hides revealed values again and drops them from the cache", async () => {
    const user = userEvent.setup();
    const { client } = setup(["application.read", "secrets.read"]);
    await user.click(await screen.findByRole("button", { name: "Reveal secrets" }));
    await user.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Reveal secrets" }));
    await screen.findByText(/hunter2-really/);

    await user.click(screen.getByRole("button", { name: "Hide secrets" }));
    expect(screen.queryByText(/hunter2-really/)).not.toBeInTheDocument();
    expect(client.getQueryData(queryKeys.applicationComposeRevealed(APP_ID))).toBeUndefined();
  });
});
