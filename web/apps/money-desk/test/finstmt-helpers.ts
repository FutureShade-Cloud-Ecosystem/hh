// Shared helpers for the statements slice's Playwright legs: the front door's handoff for the Financial
// Controller (the same record the ledger legs write), the stub's reset, and the way into the Statements area.
import { expect, test as base, type APIRequestContext, type Page } from "@playwright/test";
import { controllerHandoff } from "./gl-helpers.js";

export { expect, controllerHandoff };

/** Puts the stub's world back to the start. The dev server proxies /_stub to the stub. */
export async function resetStub(request: APIRequestContext): Promise<void> {
  const response = await request.post("/_stub/reset");
  if (!response.ok()) throw new Error(`stub reset failed: ${response.status()}`);
}

/** The fiscal year the stub's books end in, cut from the host's own clock by the stub. */
export async function stubYear(request: APIRequestContext): Promise<number> {
  const response = await request.get("/_stub/finstmt-calls");
  return ((await response.json()) as { year: number }).year;
}

/** How many calls the statements routes have answered since the last reset. */
export async function stubCalls(request: APIRequestContext): Promise<number> {
  const response = await request.get("/_stub/finstmt-calls");
  return ((await response.json()) as { calls: number }).calls;
}

/**
 * Runs in the page before any script: the front door's record for the controller's role code with the
 * subject of another stub account, so the Statements area is shown and the core's own gates and branch fence
 * answer for that account. The manager holds one branch; the payables and receivables account is a manager
 * who does not hold the admin gate.
 */
export function handoffFor(account: { id: string }): void {
  const part = (value: unknown): string =>
    btoa(JSON.stringify(value)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  const exp = Math.floor(Date.now() / 1000) + 7 * 86_400;
  const token = `${part({ alg: "ES256", typ: "JWT" })}.${part({ sub: account.id, exp })}.c2ln`;
  const record = {
    token,
    tokenExpiresAtMs: Date.now() + 7 * 86_400_000,
    flow: "financial-controller",
    roleLabel: "Financial Controller",
    user: { userId: account.id, email: "financial.controller@hh-demo.invalid", name: "Financial controller (demo)" },
    roleCode: "financial_controller",
    roleName: "Financial Controller",
    tier: 1,
    scope: "GLOBAL",
  };
  if (window.sessionStorage.getItem("hh-demo.front-door.session.v1") === null) {
    window.sessionStorage.setItem("hh-demo.front-door.session.v1", JSON.stringify(record));
  }
}

export const MANAGER = { id: "de110000-0000-4000-9000-000000000002" };
export const PAYREC = { id: "de110000-0000-4000-9000-000000000007" };

/** Signs in through the front door's handoff and opens the Statements area, on the income statement or on its refusal. */
export async function openStatementsAs(page: Page, request: APIRequestContext, account: { id: string }): Promise<void> {
  await resetStub(request);
  await page.addInitScript(handoffFor, account);
  await page.goto("/");
  await expect(page.getByRole("button", { name: "Statements", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Statements", exact: true }).click();
}

/** Signs in through the front door's handoff as the controller and opens the Statements area on the income statement. */
export async function openStatements(page: Page, request: APIRequestContext): Promise<void> {
  await resetStub(request);
  await page.addInitScript(controllerHandoff);
  await page.goto("/");
  await expect(page.getByRole("button", { name: "Statements", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Statements", exact: true }).click();
  await expect(page.locator("[data-statement]")).toBeVisible();
}

export const test = base;
