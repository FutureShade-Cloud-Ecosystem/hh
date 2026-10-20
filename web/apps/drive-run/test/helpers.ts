// Shared helpers for the Playwright legs: the front door's handoff, written
// the way the front door writes it, and the stub's reset.
import { expect, test as base, type APIRequestContext } from "@playwright/test";

/** Runs in the page before any script: the front door's record, as the minted session. */
export function driverHandoff(): void {
  const part = (value: unknown): string =>
    btoa(JSON.stringify(value)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  // Generous on purpose: a leg may pin the page clock to an instant hours either side of the wall clock.
  const exp = Math.floor(Date.now() / 1000) + 7 * 86_400;
  const token = `${part({ alg: "ES256", typ: "JWT" })}.${part({ sub: "de110000-0000-4000-9000-000000000003", exp })}.c2ln`;
  const record = {
    token,
    tokenExpiresAtMs: Date.now() + 7 * 86_400_000,
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

/**
 * The instant every leg runs at unless it says otherwise: half past midnight
 * UTC, which is the previous evening in the dealer's zone, the hour at which a
 * UTC day and the dealer's day disagree. Built from the host's own date so no
 * calendar literal is written here, and pinned in the page and in the stub
 * alike, so a leg is red on wrong code at any wall hour.
 */
export function eveningInstant(): number {
  const at = new Date();
  at.setUTCHours(0, 30, 0, 0);
  return at.getTime();
}

/** The instant the stub's clock is pinned to right now; resetStub carries it across resets. */
let stubPinnedAt: number | null = null;

/** Puts the stub's world back to the morning: three stops ready on one dispatched run. */
export async function resetStub(request: APIRequestContext, pinTo: number | null = stubPinnedAt): Promise<void> {
  stubPinnedAt = pinTo;
  // The dev server proxies /_stub to the stub, so the app's own base URL reaches it.
  const response = await request.post("/_stub/reset", { data: pinTo === null ? {} : { now: pinTo } });
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

/**
 * Every leg is pinned: the page's clock and the stub's clock both stand at
 * eveningInstant() before the leg starts, whatever the wall clock says. A leg
 * that wants another instant pins its own on top (page.clock.setFixedTime and
 * resetStub with a time).
 */
export const test = base.extend<{ pinned: void }>({
  pinned: [
    async ({ page, request }, use) => {
      await page.clock.install({ time: eveningInstant() });
      await resetStub(request, eveningInstant());
      await use();
    },
    { auto: true },
  ],
});

export { expect };

/** Opens the Field frame's account menu and presses one item (Switch role, Sign out, Brand theme, Light theme). */
export async function accountItem(page: Page, name: string): Promise<void> {
  await page.getByRole("button", { name: /^Account/ }).click();
  await page.getByRole("menuitem", { name, exact: true }).click();
}

/** Runs in the page before any script: the front door's record for a yard manager, who holds Pick, Drive and Messages. */
export function managerHandoff(): void {
  const part = (value: unknown): string =>
    btoa(JSON.stringify(value)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  const exp = Math.floor(Date.now() / 1000) + 7 * 86_400;
  const token = `${part({ alg: "ES256", typ: "JWT" })}.${part({ sub: "d0200000-0000-4000-9000-000000000009", exp })}.c2ln`;
  const record = {
    token,
    tokenExpiresAtMs: Date.now() + 7 * 86_400_000,
    flow: "yard-manager",
    roleLabel: "Yard Manager",
    user: { userId: "d0200000-0000-4000-9000-000000000009", email: "manager@hh-demo.invalid", name: "Yard manager (demo)" },
    roleCode: "yard_manager",
    roleName: "Yard Manager",
    tier: 2,
    scope: "BRANCH",
  };
  if (window.sessionStorage.getItem("hh-demo.front-door.session.v1") === null) {
    window.sessionStorage.setItem("hh-demo.front-door.session.v1", JSON.stringify(record));
  }
}
