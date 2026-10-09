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
    flow: "procurement",
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
