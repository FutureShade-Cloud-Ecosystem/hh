// Shared helpers for the Playwright legs: the front door's handoff, written
// the way the front door writes it, and the stub's reset.
import { expect, test as base, type APIRequestContext, type Page } from "@playwright/test";

export { expect };

/** Runs in the page before any script: the front door's record, as the minted session. */
export function procurementHandoff(): void {
  const part = (value: unknown): string =>
    btoa(JSON.stringify(value)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  const exp = Math.floor(Date.now() / 1000) + 7 * 86_400;
  const token = `${part({ alg: "ES256", typ: "JWT" })}.${part({ sub: "de110000-0000-4000-9000-000000000008", exp })}.c2ln`;
  const record = {
    token,
    tokenExpiresAtMs: Date.now() + 7 * 86_400_000,
    flow: "procurement-manager",
    roleLabel: "Procurement manager",
    user: { userId: "de110000-0000-4000-9000-000000000008", email: "procurement.manager@hh-demo.invalid", name: "Procurement manager (demo)" },
    roleCode: "procurement_manager",
    roleName: "Procurement manager",
    tier: 2,
    scope: "BRANCH",
  };
  if (window.sessionStorage.getItem("hh-demo.front-door.session.v1") === null) {
    window.sessionStorage.setItem("hh-demo.front-door.session.v1", JSON.stringify(record));
  }
}

/** Puts the stub's world back to the start. The dev server proxies /_stub to the stub. */
export async function resetStub(request: APIRequestContext): Promise<void> {
  const response = await request.post("/_stub/reset");
  if (!response.ok()) throw new Error(`stub reset failed: ${response.status()}`);
}

/** Signs in through the front door's handoff and lands on the Buying and Stock home. */
export async function openHome(page: Page, request: APIRequestContext): Promise<void> {
  await resetStub(request);
  await page.addInitScript(procurementHandoff);
  await page.goto("/");
  await expect(page.locator("[data-shortfall-row]").first()).toBeVisible();
}

export const test = base;

// The two other seats this surface names: the yard manager (work: receive,
// count) and Payables and Receivables (read). The records ride the same
// front-door key as the procurement handoff above, with the roles' own ids.
export function yardHandoff(): void {
  const part = (value: unknown): string =>
    btoa(JSON.stringify(value)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  const exp = Math.floor(Date.now() / 1000) + 7 * 86_400;
  const token = `${part({ alg: "ES256", typ: "JWT" })}.${part({ sub: "de110000-0000-4000-9000-000000000003", exp })}.c2ln`;
  const record = {
    token,
    tokenExpiresAtMs: Date.now() + 7 * 86_400_000,
    flow: "yard-manager",
    roleLabel: "Yard manager",
    user: { userId: "de110000-0000-4000-9000-000000000003", email: "yard.manager@hh-demo.invalid", name: "Yard manager (demo)" },
    roleCode: "yard_manager",
    roleName: "Yard manager",
    tier: 2,
    scope: "BRANCH",
  };
  if (window.sessionStorage.getItem("hh-demo.front-door.session.v1") === null) {
    window.sessionStorage.setItem("hh-demo.front-door.session.v1", JSON.stringify(record));
  }
}

export function payablesHandoff(): void {
  const part = (value: unknown): string =>
    btoa(JSON.stringify(value)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  const exp = Math.floor(Date.now() / 1000) + 7 * 86_400;
  const token = `${part({ alg: "ES256", typ: "JWT" })}.${part({ sub: "de110000-0000-4000-9000-0000000000a1", exp })}.c2ln`;
  const record = {
    token,
    tokenExpiresAtMs: Date.now() + 7 * 86_400_000,
    flow: "payables-receivables",
    roleLabel: "Payables and Receivables",
    user: { userId: "de110000-0000-4000-9000-0000000000a1", email: "payables.receivables@hh-demo.invalid", name: "Payables and Receivables (demo)" },
    roleCode: "payables_receivables",
    roleName: "Payables and Receivables",
    tier: 2,
    scope: "BRANCH",
  };
  if (window.sessionStorage.getItem("hh-demo.front-door.session.v1") === null) {
    window.sessionStorage.setItem("hh-demo.front-door.session.v1", JSON.stringify(record));
  }
}

/** Switches to one of the app's areas once signed in. */
export async function openArea(page: Page, area: string): Promise<void> {
  await page.locator(`[data-area="${area}"]`).click();
  await expect(page.locator(`[data-area="${area}"][aria-current="true"]`)).toBeVisible();
}

/** Signs in as the procurement manager and lands on the inventory area's lookup. */
export async function openInventory(page: Page, request: APIRequestContext, tab: string): Promise<void> {
  await resetStub(request);
  await page.addInitScript(procurementHandoff);
  await page.goto("/");
  await expect(page.locator("[data-shortfall-row]").first()).toBeVisible();
  await openArea(page, "inventory");
  await page.locator(`[data-inv-tab="${tab}"]`).click();
  await expect(page.locator(`[data-inv-tab="${tab}"][aria-current="true"]`)).toBeVisible();
}

/** Signs in as the procurement manager and lands on the counts area's sheets. */
export async function openCounts(page: Page, request: APIRequestContext, tab: string): Promise<void> {
  await resetStub(request);
  await page.addInitScript(procurementHandoff);
  await page.goto("/");
  await expect(page.locator("[data-shortfall-row]").first()).toBeVisible();
  await openArea(page, "counts");
  await page.locator(`[data-counts-tab="${tab}"]`).click();
  await expect(page.locator(`[data-counts-tab="${tab}"][aria-current="true"]`)).toBeVisible();
}

/** Clears the handoff and re-signs in as another role on the same page. The
 * new record is written directly: an init script would race the role's own
 * still-registered script, which writes first and keeps the old seat. */
export async function switchRole(page: Page, handoff: () => void): Promise<void> {
  await page.evaluate(() => window.sessionStorage.removeItem("hh-demo.front-door.session.v1"));
  await page.evaluate(handoff);
  await page.goto("/");
  await expect(page.locator("[data-shortfall-row]").first()).toBeVisible();
}
