// Shared helpers for the Playwright legs: the front door's handoff, written
// the way the front door writes it, and the stub's reset.
import type { APIRequestContext } from "@playwright/test";

/** Runs in the page before any script: the front door's record, as the minted session. */
export function driverHandoff(): void {
  const part = (value: unknown): string =>
    btoa(JSON.stringify(value)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  const exp = Math.floor(Date.now() / 1000) + 3600;
  const token = `${part({ alg: "ES256", typ: "JWT" })}.${part({ sub: "de110000-0000-4000-9000-000000000003", exp })}.c2ln`;
  const record = {
    token,
    tokenExpiresAtMs: Date.now() + 3_600_000,
    flow: "driver",
    roleLabel: "Driver",
    user: { userId: "de110000-0000-4000-9000-000000000003", email: "driver@hh-demo.invalid", name: "Driver (demo)" },
    roleCode: "driver",
    roleName: "Driver",
    tier: 3,
    scope: "BRANCH",
  };
  if (window.sessionStorage.getItem("hh-demo.front-door.session.v1") === null) {
    window.sessionStorage.setItem("hh-demo.front-door.session.v1", JSON.stringify(record));
  }
}

/** Puts the stub's world back to the morning: three stops ready on one dispatched run. */
export async function resetStub(request: APIRequestContext): Promise<void> {
  // The dev server proxies /_stub to the stub, so the app's own base URL reaches it.
  const response = await request.post("/_stub/reset");
  if (!response.ok()) throw new Error(`stub reset failed: ${response.status()}`);
}

/** Switches the stub to a named scenario (empty, not-driver, run-planned, photo-fails). */
export async function useScenario(request: APIRequestContext, name: string): Promise<void> {
  const response = await request.post("/_stub/scenario", { data: { name } });
  if (!response.ok()) throw new Error(`stub scenario failed: ${response.status()}`);
}

export interface LoggedCall {
  method: string;
  path: string;
  query: Record<string, string>;
  bearer: boolean;
  org: string;
  contentType: string;
  bytes?: number;
  body: Record<string, unknown> | null;
}

/** The calls the stub saw on the dispatch routes, in order. */
export async function stubLog(request: APIRequestContext): Promise<LoggedCall[]> {
  const response = await request.get("/_stub/log");
  return (await response.json()) as LoggedCall[];
}

/** The dealer's day the way the core names it for the demo dealer: the house zone. */
export function houseDay(at: Date = new Date()): string {
  return new Intl.DateTimeFormat("en-CA", {
    timeZone: "America/Toronto",
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
  }).format(at);
}

import type { Page } from "@playwright/test";

/** A tiny, valid PNG (one pixel): a stand-in for a camera photo that needs no shrinking. */
export const TINY_PNG = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==",
  "base64",
);

/** Opens stop 1 and moves it loaded, then en route: the state proof of delivery starts from. */
export async function takeFirstStopEnRoute(page: Page): Promise<void> {
  await page.goto("/");
  await page.locator("[data-stop]").first().click();
  await page.getByRole("button", { name: "Mark loaded" }).click();
  await page.getByRole("button", { name: "Mark en route" }).waitFor();
  await page.getByRole("button", { name: "Mark en route" }).click();
  await page.getByRole("button", { name: "Delivered", exact: true }).waitFor();
}

/** From the run sheet: take stop `n` (1 based) loaded, en route and delivered, and come back to the run sheet. */
export async function deliverStop(page: Page, n: number): Promise<void> {
  await page.locator("[data-stop]").nth(n - 1).click();
  await page.getByRole("button", { name: "Mark loaded" }).click();
  await page.getByRole("button", { name: "Mark en route" }).click();
  await page.getByRole("button", { name: "Delivered", exact: true }).click();
  await page.getByLabel("Full name of the person receiving").fill("Receiving clerk");
  await page.getByRole("checkbox").check();
  await page.getByRole("button", { name: "Sign for this delivery" }).click();
  await page.locator("[data-stop-status]").filter({ hasText: "Delivered" }).waitFor();
  await page.getByRole("button", { name: "Back to my run" }).click();
}

/** From the run sheet: take stop `n` en route, then record it as not delivered with a reason. */
export async function failStop(page: Page, n: number, reason: string): Promise<void> {
  await page.locator("[data-stop]").nth(n - 1).click();
  await page.getByRole("button", { name: "Mark loaded" }).click();
  await page.getByRole("button", { name: "Mark en route" }).click();
  await page.getByRole("button", { name: "Could not deliver" }).click();
  await page.getByLabel("What stopped this delivery?").fill(reason);
  await page.getByRole("button", { name: "Could not deliver" }).click();
  await page.locator("[data-stop-status]").filter({ hasText: "Not delivered" }).waitFor();
  await page.getByRole("button", { name: "Back to my run" }).click();
}
