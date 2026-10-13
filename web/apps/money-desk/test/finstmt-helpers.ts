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

/** Signs in through the front door's handoff as the controller and opens the Statements area on the income statement. */
export async function openStatements(page: Page, request: APIRequestContext): Promise<void> {
  await resetStub(request);
  await page.addInitScript(controllerHandoff);
  await page.goto("/");
  await expect(page.locator("[data-approval-id]").first()).toBeVisible();
  await page.getByRole("button", { name: "Statements", exact: true }).click();
  await expect(page.locator("[data-statement]")).toBeVisible();
}

export const test = base;
