// SPDX-License-Identifier: Apache-2.0
import { screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { renderWithQuery, routeFetch } from "@/test/render";
import { SiteFooter } from "./site-footer";

describe("SiteFooter", () => {
  it("shows the console version from NEXT_PUBLIC_DBR2_VERSION and the platform version", async () => {
    vi.stubEnv("NEXT_PUBLIC_DBR2_VERSION", "0.1.0.57");
    vi.stubGlobal(
      "fetch",
      vi.fn(
        routeFetch({
          "GET /api/v1/version": () =>
            Response.json({ platform: "0.1.0.12", components: { api: "0.1.0.0" } }),
        }),
      ),
    );

    renderWithQuery(<SiteFooter />);

    const footer = screen.getByTestId("version-footer");
    expect(footer).toHaveTextContent("DBR² console 0.1.0.57 · platform …");
    await waitFor(() =>
      expect(footer).toHaveTextContent("DBR² console 0.1.0.57 · platform 0.1.0.12"),
    );
    const docs = screen.getByRole("link", { name: "API docs" });
    expect(docs).toHaveAttribute("href", "/api/docs");
    expect(docs).toHaveAttribute("target", "_blank");
    expect(screen.getByRole("link", { name: "About" })).toHaveAttribute("href", "/about");
  });

  it("falls back to 0.1.0.0 locally and reports an unavailable platform", async () => {
    vi.stubEnv("NEXT_PUBLIC_DBR2_VERSION", "");
    vi.stubGlobal(
      "fetch",
      vi.fn(
        routeFetch({
          "GET /api/v1/version": () =>
            new Response(JSON.stringify({ title: "Bad Gateway", status: 502, code: "upstream_unavailable" }), {
              status: 502,
              headers: { "content-type": "application/problem+json" },
            }),
        }),
      ),
    );

    renderWithQuery(<SiteFooter />);

    await waitFor(() =>
      expect(screen.getByTestId("version-footer")).toHaveTextContent(
        "DBR² console 0.1.0.0 · platform unavailable",
      ),
    );
  });
});
