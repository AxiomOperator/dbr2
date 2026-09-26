// SPDX-License-Identifier: Apache-2.0
//
// Playwright end-to-end tests (Chromium) for the console against the bundled
// mock API (scripts/mock-api.mjs). Both servers are started by Playwright:
//
//   mock API   node scripts/mock-api.mjs                      127.0.0.1:8199
//   console    next build && next start (production mode:    127.0.0.1:3100
//              the CSP carries no 'unsafe-eval')
//
// `E2E_NEXT=dev npm run test:e2e` runs against `next dev` instead (faster to
// iterate, CSP with 'unsafe-eval'). The mock keeps state in memory for the
// whole run, so the tests run serially in one worker. Run `make web-e2e`
// (installs Chromium first) or `npx playwright install chromium` once.
import { defineConfig, devices } from "@playwright/test";

const MOCK_PORT = Number(process.env.E2E_MOCK_PORT ?? 8199);
const WEB_PORT = Number(process.env.E2E_WEB_PORT ?? 3100);
const dev = process.env.E2E_NEXT === "dev";
const ci = !!process.env.CI;

export default defineConfig({
  testDir: "e2e",
  fullyParallel: false,
  workers: 1,
  forbidOnly: ci,
  retries: ci ? 1 : 0,
  timeout: 60_000,
  expect: { timeout: 15_000 },
  reporter: ci ? [["list"], ["html", { open: "never" }]] : [["list"]],
  use: {
    baseURL: `http://127.0.0.1:${WEB_PORT}`,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"], viewport: { width: 1440, height: 900 } } }],
  webServer: [
    {
      command: "node scripts/mock-api.mjs",
      url: `http://127.0.0.1:${MOCK_PORT}/api/v1/health/live`,
      env: {
        MOCK_API_PORT: String(MOCK_PORT),
        // Long enough to watch progress, short enough for a test.
        MOCK_BACKUP_MS: "9000",
        MOCK_RESTORE_MS: "9000",
      },
      reuseExistingServer: !ci,
      timeout: 30_000,
    },
    {
      command: dev
        ? `npx next dev -H 127.0.0.1 -p ${WEB_PORT}`
        : `npx next build && npx next start -H 127.0.0.1 -p ${WEB_PORT}`,
      url: `http://127.0.0.1:${WEB_PORT}/login`,
      env: {
        DBR2_API_INTERNAL_URL: `http://127.0.0.1:${MOCK_PORT}`,
        NEXT_TELEMETRY_DISABLED: "1",
      },
      reuseExistingServer: !ci,
      timeout: 240_000,
    },
  ],
});
