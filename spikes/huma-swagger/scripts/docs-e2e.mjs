// Headless check of /api/docs (Swagger UI): loads under a session cookie,
// "Try it out" runs with the caller's permissions, "Authorize" sends the bearer.
// Run: node scripts/docs-e2e.mjs  (needs playwright-core in .work/node_modules
// and a Chromium; CHROMIUM env overrides the executable path).
import { createRequire } from "node:module";
const require = createRequire(new URL("../.work/", import.meta.url));
const { chromium } = require("playwright-core");

const BASE = process.env.BASE ?? "http://127.0.0.1:18888";
const OUT = new URL("../.work/", import.meta.url).pathname;
const browser = await chromium.launch({ executablePath: process.env.CHROMIUM ?? "/usr/bin/chromium-browser" });
const ctx = await browser.newContext({ viewport: { width: 1280, height: 1400 } });
const page = await ctx.newPage();
const problems = [];
page.on("console", (m) => m.type() === "error" && problems.push(m.text()));
page.on("pageerror", (e) => problems.push(String(e)));
page.on("response", (res) => res.status() >= 400 && console.log("  http", res.status(), res.url()));

let r = await page.goto(`${BASE}/api/docs`);
console.log("anonymous GET /api/docs ->", r.status());

// Simulate the browser session a login would create (viewer: read-only).
await ctx.addCookies([{ name: "dbr2_session", value: "viewer-token", url: BASE, httpOnly: true, sameSite: "Lax" }]);
r = await page.goto(`${BASE}/api/docs`);
console.log("session GET /api/docs ->", r.status());
await page.waitForSelector(".opblock", { timeout: 15000 });
console.log("title:", await page.title(), "| operations rendered:", await page.locator(".opblock").count(),
  "| info.version:", (await page.locator(".info .version").first().textContent()).trim());
await page.screenshot({ path: OUT + "docs.png", fullPage: true });

async function tryCreateBackup() {
  const op = page.locator("#operations-Backups-create-backup");
  if (!(await op.locator(".try-out__btn").isVisible().catch(() => false))) await op.locator(".opblock-summary").click();
  const tryBtn = op.locator("button.try-out__btn");
  if ((await tryBtn.textContent()).includes("Try it out")) await tryBtn.click();
  const [req] = await Promise.all([
    page.waitForRequest((q) => q.url().endsWith("/api/v1/backups") && q.method() === "POST"),
    op.locator("button.execute").click(),
  ]);
  const resp = await req.response();
  const h = await req.allHeaders();
  return { status: resp.status(), authorization: h["authorization"] ?? "(none)", cookie: h["cookie"] ? "sent" : "(none)" };
}

console.log("Try it out POST /api/v1/backups as viewer session:", await tryCreateBackup());

// Authorize with an admin bearer token via the dialog.
await page.locator("button.authorize").first().click();
await page.locator(".modal-ux input[type=text], .modal-ux input[type=password]").first().fill("admin-token");
await page.locator(".modal-ux button.modal-btn.authorize").click();
await page.locator(".modal-ux button.btn-done").click();
console.log("Try it out POST /api/v1/backups after Authorize(admin-token):", await tryCreateBackup());
await page.screenshot({ path: OUT + "docs-tryitout.png", fullPage: false });

console.log("console/page errors:", problems.length ? problems : "none");
await browser.close();
