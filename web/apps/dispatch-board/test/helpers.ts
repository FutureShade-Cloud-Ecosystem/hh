// Shared helpers for the Playwright legs: the front door's handoff, written the way the front door
// writes it, and the stub's controls.
import { expect, test as base, type APIRequestContext, type Page } from "@playwright/test";

export { expect };

/** Runs in the page before any script: the front door's record, as the minted session. */
export function dispatchHandoff(): void {
  const part = (value: unknown): string =>
    btoa(JSON.stringify(value)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  const exp = Math.floor(Date.now() / 1000) + 7 * 86_400;
  const token = `${part({ alg: "ES256", typ: "JWT" })}.${part({ sub: "d0200000-0000-4000-9000-000000000001", exp })}.c2ln`;
  const record = {
    token,
    tokenExpiresAtMs: Date.now() + 7 * 86_400_000,
    flow: "logistics-manager",
    roleLabel: "Logistics Manager",
    user: { userId: "d0200000-0000-4000-9000-000000000001", email: "dispatch.demo@hh-demo.invalid", name: "Dispatch persona (demo)" },
    roleCode: "logistics_manager",
    roleName: "Logistics Manager",
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

export async function stubPost(request: APIRequestContext, path: string, data: unknown): Promise<void> {
  const response = await request.post(`/_stub/${path}`, { data });
  if (!response.ok()) throw new Error(`stub ${path} failed: ${response.status()}`);
}

/** Signs in through the front door's handoff and lands on the load board. */
export async function openBoard(page: Page, request: APIRequestContext): Promise<void> {
  await resetStub(request);
  await page.addInitScript(dispatchHandoff);
  await page.goto("/");
  await expect(page.locator("[data-card]").first()).toBeVisible();
}

/** The day the stub started on, as YYYY-MM-DD. */
export async function stubToday(request: APIRequestContext): Promise<string> {
  const response = await request.get("/_stub/world");
  return ((await response.json()) as { today: string }).today;
}

export const test = base;
