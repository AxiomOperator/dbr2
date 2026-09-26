// SPDX-License-Identifier: Apache-2.0
//
// Shared Playwright fixtures: every test fails if the page reports a
// Content-Security-Policy violation (securitypolicyviolation events and CSP
// console errors) or an uncaught exception.

import { expect, test as base, type Page } from "@playwright/test";

export const ADMIN = { username: "admin", password: "correct-horse-battery" };

/** Mock application IDs (scripts/mock-fleet.mjs). */
export const APPS = {
  shop: "5f939a00-fccb-4376-a6cc-37eeb5542abe",
  mftPg: "9fc3a8cb-13d7-4c97-a291-4aade4ca63ee",
};

interface Guard {
  violations: string[];
  errors: string[];
}

export const test = base.extend<{ guard: Guard }>({
  guard: [
    async ({ page }, use) => {
      const guard: Guard = { violations: [], errors: [] };
      await page.exposeFunction("__reportCspViolation", (v: string) => guard.violations.push(v));
      await page.addInitScript(() => {
        document.addEventListener("securitypolicyviolation", (e) => {
          (window as unknown as { __reportCspViolation: (v: string) => void }).__reportCspViolation(
            `${e.violatedDirective} blocked ${e.blockedURI || "inline"} (${e.sourceFile}:${e.lineNumber})`,
          );
        });
      });
      page.on("console", (msg) => {
        if (msg.type() === "error" && /Content Security Policy|Content-Security-Policy/i.test(msg.text())) {
          guard.violations.push(msg.text());
        }
      });
      page.on("pageerror", (err) => guard.errors.push(err.message));
      await use(guard);
      expect(guard.violations, "Content-Security-Policy violations").toEqual([]);
      expect(guard.errors, "uncaught page errors").toEqual([]);
    },
    { auto: true },
  ],
});

export { expect };

/** Signs in as the mock master admin through the login form. */
export async function loginAsAdmin(page: Page, returnTo = "/") {
  await page.goto(`/login?return_to=${encodeURIComponent(returnTo)}`);
  await page.getByLabel("Username").fill(ADMIN.username);
  await page.getByLabel("Password").fill(ADMIN.password);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await page.waitForURL((u) => !u.pathname.startsWith("/login"));
}

/** Signs in as the mock read-only OIDC user ("Sign in with Microsoft"). */
export async function loginAsOidcUser(page: Page) {
  await page.goto("/login");
  await page.getByRole("link", { name: "Sign in with Microsoft" }).click();
  await page.waitForURL((u) => !u.pathname.startsWith("/login"));
}

/** The main navigation (desktop sidebar). */
export const mainNav = (page: Page) => page.getByRole("navigation", { name: "Main" });
