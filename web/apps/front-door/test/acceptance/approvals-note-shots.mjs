// Shots for the approvals note feature: three states, light and dark variants.
//
//   FD_APP_PORT=<a free port> FD_STUB_PORT=<a free port> node test/acceptance/approvals-note-shots.mjs <out-dir>
//
// Prerequisites (same harness as the front door's own shot walk, each on a
// port you have checked free):
//   FD_STUB_PORT=<a free port> node test/stub/demo-stub.mjs
//   npx vite --port <the app port> --strictPort --host 127.0.0.1
import { chromium } from "@playwright/test";
import { mkdirSync } from "node:fs";
import { join } from "node:path";

const appPort = Number(process.env.FD_APP_PORT);
const stubPort = Number(process.env.FD_STUB_PORT);
if (!Number.isInteger(appPort) || appPort <= 0 || !Number.isInteger(stubPort) || stubPort <= 0) {
  process.stderr.write("approvals-note shots: set FD_APP_PORT and FD_STUB_PORT to free ports\n");
  process.exit(1);
}
const base = `http://127.0.0.1:${appPort}`;
const stubBase = `http://127.0.0.1:${stubPort}`;
const outDir = process.argv[2] ?? "/tmp/fd-approvals-shots";
mkdirSync(outDir, { recursive: true });

const browser = await chromium.launch({ channel: "chrome", headless: true });

async function settle(page) {
  await page.mouse.move(0, 0);
  await page.waitForTimeout(300);
}

async function stubSetApprovals(page, count) {
  const res = await page.request.post(`${stubBase}/_stub/set-approvals`, { data: { count } });
  if (!res.ok()) throw new Error(`stub set-approvals failed: ${res.status()}`);
}

async function stubReset(page) {
  const res = await page.request.post(`${stubBase}/_stub/reset`);
  if (!res.ok()) throw new Error(`stub reset failed: ${res.status()}`);
}

async function signInSalesManager(page) {
  await page.getByRole("button", { name: "Sales manager", exact: true }).click();
  await expect(page.getByRole("button", { name: "Counter Till", exact: true })).toBeVisible();
}

async function signInSales(page) {
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByRole("button", { name: "Counter Till", exact: true })).toBeVisible();
}

// Import expect for the sign-in helpers.
const { expect } = await import("@playwright/test");

// State 1: Sales manager with pending approvals (count = 3), light and dark.
{
  const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
  await stubReset(page);
  await stubSetApprovals(page, 3);
  await page.goto(`${base}/`);
  await page.getByRole("button", { name: "Sales manager", exact: true }).waitFor();
  await settle(page);
  await page.screenshot({ path: join(outDir, "front-door-sales-manager-home-with-approvals-light.png"), fullPage: true });

  // Dark theme: remove the data-theme attribute.
  await page.evaluate(() => document.documentElement.removeAttribute("data-theme"));
  await settle(page);
  await page.screenshot({ path: join(outDir, "front-door-sales-manager-home-with-approvals-dark.png"), fullPage: true });
  await page.close();
}

// State 2: Sales manager with zero approvals, light and dark.
{
  const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
  await stubReset(page);
  await stubSetApprovals(page, 0);
  await page.goto(`${base}/`);
  await page.getByRole("button", { name: "Sales manager", exact: true }).waitFor();
  await signInSalesManager(page);
  await settle(page);
  await page.screenshot({ path: join(outDir, "front-door-sales-manager-home-zero-approvals-light.png"), fullPage: true });

  await page.evaluate(() => document.documentElement.removeAttribute("data-theme"));
  await settle(page);
  await page.screenshot({ path: join(outDir, "front-door-sales-manager-home-zero-approvals-dark.png"), fullPage: true });
  await page.close();
}

// State 3: Sales role (non-Sales-manager), light and dark. The note is absent.
{
  const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
  await stubReset(page);
  await stubSetApprovals(page, 5);
  await page.goto(`${base}/`);
  await page.getByRole("button", { name: "Sales", exact: true }).waitFor();
  await signInSales(page);
  await settle(page);
  await page.screenshot({ path: join(outDir, "front-door-sales-home-no-note-light.png"), fullPage: true });

  await page.evaluate(() => document.documentElement.removeAttribute("data-theme"));
  await settle(page);
  await page.screenshot({ path: join(outDir, "front-door-sales-home-no-note-dark.png"), fullPage: true });
  await page.close();
}

await browser.close();
process.stdout.write(`approvals-note shots in ${outDir}\n`);
