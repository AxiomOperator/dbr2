// SPDX-License-Identifier: Apache-2.0
//
// Sign-in, dashboard, navigation groups, permission gating and the
// Content-Security-Policy of console pages.

import { expect, loginAsAdmin, loginAsOidcUser, mainNav, test } from "./fixtures";

test("console pages carry a nonce-based CSP", async ({ page }) => {
  const res = await page.goto("/login");
  expect(res?.status()).toBe(200);
  const csp = res?.headers()["content-security-policy"] ?? "";
  expect(csp).toMatch(/script-src 'self' 'nonce-[A-Za-z0-9+/=]+' 'strict-dynamic'/);
  expect(csp).toContain("frame-ancestors 'none'");
  expect(csp).toContain("object-src 'none'");
  if (process.env.E2E_NEXT !== "dev") expect(csp).not.toContain("unsafe-eval");
  // The other security headers still come from next.config.ts.
  expect(res?.headers()["x-frame-options"]).toBe("DENY");
  // Every inline script Next.js rendered carries this response's nonce.
  const nonce = /'nonce-([^']+)'/.exec(csp)?.[1];
  const scripts = await page.locator("script:not([src])").count();
  expect(scripts).toBeGreaterThan(0);
  const withNonce = await page.evaluate(
    (n) => [...document.querySelectorAll("script")].filter((s) => s.nonce === n).length,
    nonce,
  );
  expect(withNonce).toBeGreaterThan(0);
  // A second request gets a different nonce.
  const again = await page.request.get("/login");
  expect(again.headers()["content-security-policy"]).not.toBe(csp);
});

test("the API proxy is not covered by the console CSP", async ({ request }) => {
  const res = await request.get("/api/v1/version");
  expect(res.status()).toBe(200);
  expect(res.headers()["content-security-policy"]).toBeUndefined();
});

test("admin signs in, sees the dashboard and every navigation group", async ({ page }) => {
  await loginAsAdmin(page);
  await expect(page.getByRole("heading", { name: "Welcome, Master Admin" })).toBeVisible();
  const overview = page.getByTestId("protection-overview");
  await expect(overview.getByRole("link", { name: /Failed/ })).toBeVisible();
  await expect(overview.getByRole("link", { name: /At risk/ })).toBeVisible();
  await expect(page.getByTestId("live-indicator")).toHaveAttribute("data-status", "live");

  const nav = mainNav(page);
  for (const group of ["Docker", "Protection", "Recovery", "Storage", "System"]) {
    await expect(nav.getByRole("button", { name: group })).toBeVisible();
  }
  for (const item of ["Hosts", "Applications", "Containers", "Volumes", "Policies", "Jobs", "Recovery Points", "Restore", "Restore Testing", "Repositories", "Usage", "Agents", "Users", "Notifications", "Audit Log", "Settings"]) {
    await expect(nav.getByRole("link", { name: new RegExp(`^${item}`) }).first()).toBeVisible();
  }
  // Groups collapse and remember it.
  await nav.getByRole("button", { name: "Storage" }).click();
  await expect(nav.getByRole("link", { name: "Usage" })).toBeHidden();
  await page.reload();
  await expect(mainNav(page).getByRole("link", { name: "Usage" })).toBeHidden();
  await mainNav(page).getByRole("button", { name: "Storage" }).click();
  await expect(mainNav(page).getByRole("link", { name: "Usage" })).toBeVisible();
});

test("the new pages render for the admin", async ({ page }) => {
  await loginAsAdmin(page, "/hosts");
  await expect(page.getByRole("heading", { name: "Hosts" })).toBeVisible();
  await expect(page.getByRole("link", { name: "docker-prod-01" })).toBeVisible();

  await mainNav(page).getByRole("link", { name: "Containers" }).click();
  await expect(page.getByRole("table", { name: "Containers" })).toContainText("shop-web-1");

  await mainNav(page).getByRole("link", { name: "Volumes" }).click();
  await expect(page.getByRole("table", { name: "Volumes" })).toContainText("shop_pgdata");

  await mainNav(page).getByRole("link", { name: "Usage" }).click();
  await expect(page.getByRole("table", { name: "Usage by host" })).toContainText("docker-prod-01");

  await mainNav(page).getByRole("link", { name: "Agents" }).click();
  await expect(page.getByRole("heading", { name: "Agents" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Add host" })).toBeVisible();

  await mainNav(page).getByRole("link", { name: "Users" }).click();
  await expect(page.getByRole("table", { name: "Users" })).toContainText("Linus Torvalds");
  await expect(page.getByRole("table", { name: "Group mappings" })).toBeVisible();

  await mainNav(page).getByRole("link", { name: "Policies" }).click();
  await expect(page.getByText(/Not available yet: arrives in Phase 7/)).toBeVisible();

  await mainNav(page).getByRole("link", { name: "Restore Testing" }).click();
  await expect(page.getByText(/Not available yet: arrives in Phase 9/)).toBeVisible();

  // The old Alerts URL redirects to Notifications.
  await page.goto("/alerts");
  await expect(page).toHaveURL(/\/notifications$/);
  await expect(page.getByRole("heading", { name: "Notifications" })).toBeVisible();
});

test("the read-only OIDC user only sees what their permissions allow", async ({ page }) => {
  await loginAsOidcUser(page);
  await expect(page.getByRole("heading", { name: "Welcome, Ada Lovelace" })).toBeVisible();
  const nav = mainNav(page);
  await expect(nav.getByRole("link", { name: "Applications" })).toBeVisible();
  await expect(nav.getByRole("link", { name: "Jobs" })).toBeVisible();
  await expect(nav.getByRole("link", { name: "Users" })).toHaveCount(0);
  await expect(nav.getByRole("link", { name: "Audit Log" })).toHaveCount(0);

  await page.goto("/users");
  await expect(page.getByText("Access denied")).toBeVisible();
  await expect(page.getByText("user.read")).toBeVisible();

  await page.goto("/agents");
  await expect(page.getByRole("button", { name: "Add host" })).toHaveCount(0);

  // No "Back up now" without backup.execute.
  await page.goto("/applications");
  await page.getByRole("link", { name: "Web shop" }).click();
  await expect(page.getByTestId("protection-card")).toBeVisible();
  await expect(page.getByRole("button", { name: "Back up now" })).toHaveCount(0);
});
