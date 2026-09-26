// SPDX-License-Identifier: Apache-2.0
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { filterApplications } from "@/components/applications/applications-view";
import { CurrentUserProvider } from "@/components/auth-guard";
import { RecoveryPointDetail } from "@/components/backups/recovery-point-detail";
import { RecoveryPointsTable } from "@/components/backups/recovery-points-table";
import { ApplicationSummarySchema } from "@/lib/api/fleet-schemas";
import { ManifestSchema, RecoveryPointSchema } from "@/lib/api/protection-schemas";
import { APP_SUMMARY, meWith } from "@/test/fleet-fixtures";
import { MANIFEST, REPO_ID, RP_COMMITTED, RP_ID, RP_PARTIAL } from "@/test/protection-fixtures";
import { renderWithQuery, routeFetch } from "@/test/render";

vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: vi.fn(), replace: vi.fn() }),
  usePathname: () => "/",
  useSearchParams: () => new URLSearchParams(),
}));

const FAILED_DETAILS = {
  read_percent: 10,
  components: [
    { name: "config", snapshot_id: "k1", files: 4, dirs: 1, files_read: 4, bytes_read: 48213 },
    {
      name: "volume:shop_pgdata",
      snapshot_id: "k2",
      files: 2143,
      dirs: 40,
      files_read: 214,
      bytes_read: 1_288_490_188,
      errors: ["object 4f1a… is missing", "file /pg_wal/0003: hash mismatch"],
    },
  ],
};

const DB_MANIFEST = {
  ...MANIFEST,
  components: [
    ...MANIFEST.components,
    {
      name: "database:db",
      kind: "database",
      required: true,
      status: "succeeded",
      size_bytes: 18_874_368,
      file_name: "db.sql.zst",
      database: { engine: "postgresql", format: "pg_dumpall-sql-zstd", service: "db", container: "shop-db-1" },
      validation: "pg_dumpall exit 0; zstd frame verified",
    },
  ],
  contract: {
    satisfied: false,
    details: { required_components: ["config", "bind:/srv/shop/uploads"], missing_components: ["bind:/srv/shop/uploads"], max_rpo_minutes: 1440 },
  },
};

describe("recovery point verification and contract at capture", () => {
  it("parses the contract at capture (absent in older manifests)", () => {
    expect(ManifestSchema.parse(MANIFEST).contract).toBeNull();
    expect(ManifestSchema.parse({ ...MANIFEST, contract: { satisfied: true } }).contract).toEqual({
      satisfied: true,
      details: { required_components: [], missing_components: [], max_rpo_minutes: null },
    });
  });

  it("shows per-component verification errors, database dumps and the contract; verifies now", async () => {
    const verifies: unknown[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(
        routeFetch({
          [`GET /api/v1/recovery-points/${RP_ID}`]: () =>
            Response.json({ ...RP_COMMITTED, verification: "verification_failed", verification_details: FAILED_DETAILS, manifest: DB_MANIFEST }),
          [`POST /api/v1/repositories/${REPO_ID}/verify`]: (init) => {
            verifies.push(JSON.parse(String(init?.body)));
            return Response.json({ workflow_id: `repository/${REPO_ID}/verify` }, { status: 202 });
          },
        }),
      ),
    );
    const user = userEvent.setup();
    renderWithQuery(
      <CurrentUserProvider me={meWith(["backup.read", "repository.manage", "policy.read"])}>
        <RecoveryPointDetail id={RP_ID} />
      </CurrentUserProvider>,
    );
    const card = await screen.findByTestId("verification-card");
    expect(card).toHaveTextContent("Verification failed");
    expect(card).toHaveTextContent("reading 10 % of the files back");
    const rows = within(within(card).getByRole("table", { name: "Verification results" })).getAllByRole("row").slice(1);
    expect(rows[0]).toHaveAttribute("data-verify-ok", "true");
    expect(rows[1]).toHaveAttribute("data-verify-ok", "false");
    expect(rows[1]).toHaveTextContent("2 errors");
    expect(rows[1]).toHaveTextContent("hash mismatch");

    const dbs = screen.getByRole("table", { name: "Database dumps" });
    expect(dbs).toHaveTextContent("PostgreSQL");
    expect(dbs).toHaveTextContent("shop-db-1");
    expect(dbs).toHaveTextContent("zstd frame verified");

    const contract = screen.getByRole("heading", { name: /Recovery Contract at capture/ }).closest("[data-slot=card]") as HTMLElement;
    expect(contract).toHaveTextContent("Not satisfied");
    expect(contract).toHaveTextContent("1 day");
    expect(within(contract).getAllByText("bind:/srv/shop/uploads")).toHaveLength(2);

    await user.click(within(card).getByRole("button", { name: "Verify this recovery point" }));
    const dialog = await screen.findByRole("dialog", { name: "Verify this recovery point?" });
    const pct = within(dialog).getByLabelText("Files to read back (%)");
    await user.clear(pct);
    await user.type(pct, "150");
    await user.click(within(dialog).getByRole("button", { name: "Start verification" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("0–100 %");
    await user.clear(pct);
    await user.type(pct, "100");
    await user.click(within(dialog).getByRole("button", { name: "Start verification" }));
    await waitFor(() => expect(verifies).toEqual([{ read_percent: 100, recovery_point_id: RP_ID }]));
  });

  it("shows verification and scheduled deletion in the recovery point table", () => {
    renderWithQuery(
      <RecoveryPointsTable
        items={[
          RecoveryPointSchema.parse(RP_COMMITTED),
          RecoveryPointSchema.parse({ ...RP_PARTIAL, delete_after: "2026-10-02T10:00:00Z" }),
        ]}
        emptyText="none"
      />,
    );
    const rows = screen.getAllByRole("row").slice(1);
    expect(rows[0]).toHaveTextContent("Verified");
    expect(rows[0]).not.toHaveAttribute("data-scheduled-deletion");
    expect(rows[1]).toHaveTextContent("Not verified");
    expect(rows[1]).toHaveAttribute("data-scheduled-deletion", "true");
    expect(rows[1]).toHaveTextContent(/Deletion /);
  });

  it("filters applications by contract state", () => {
    const app = ApplicationSummarySchema.parse(APP_SUMMARY);
    const base = { host: "all", kind: "all", status: "all", unprotectedOnly: false } as const;
    const states = new Map([[app.id, "violated" as const]]);
    expect(filterApplications([app], { ...base, contract: "violated" }, states)).toHaveLength(1);
    expect(filterApplications([app], { ...base, contract: "satisfied" }, states)).toHaveLength(0);
    expect(filterApplications([app], { ...base, contract: "none" }, new Map())).toHaveLength(1);
    expect(filterApplications([app], base)).toHaveLength(1);
  });
});
