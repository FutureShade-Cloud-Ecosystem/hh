// Shared helpers for the bank reconciliation slice's Playwright legs: the controller's way in (the front door's
// handoff, written the way the front door writes it), the stub's reset, and the month the stub's statement covers.
import { expect, test as base, type APIRequestContext, type Page } from "@playwright/test";
import { controllerHandoff, resetStub } from "./gl-helpers.js";

export { expect, controllerHandoff, resetStub };

export const OPERATING = "Gable operating account (demo)";
export const OPERATING_ID = "ba000000-0000-4000-8000-000000000001";
export const FUEL_CARD = "Fleet fuel card (demo)";
// The wire's payment and the fee's journal, as the stub world holds them.
export const WIRE_PAYMENT = "b9000000-0000-4000-8000-000000000004";
export const FEE_JOURNAL = "JE-0004 · Till over/short";
export const CHEQUE_JOURNAL = "JE-0003 · Payment received";
// The statement's closing balance in the stub world: the opening balance plus every line of the month.
export const CLOSING = "25,255.75";

/** The month before today, the way the desk and the stub cut it from the host's own clock. */
export function priorMonth(): { year: number; month: number; words: string } {
  const before = new Date(new Date().getFullYear(), new Date().getMonth() - 1, 1);
  const name = before.toLocaleString("en-US", { month: "long" });
  return { year: before.getFullYear(), month: before.getMonth() + 1, words: `${name} ${before.getFullYear()}` };
}

/** A statement date in the prior month, written the way the bank writes it. */
export function monthDay(day: number): string {
  const { year, month } = priorMonth();
  return `${year}-${String(month).padStart(2, "0")}-${String(day).padStart(2, "0")}`;
}

/** Signs in as the controller through the front door's handoff and opens the bank reconciliation area. */
export async function openBank(page: Page, request: APIRequestContext): Promise<void> {
  await resetStub(request);
  await page.addInitScript(controllerHandoff);
  await page.goto("/");
  await page.getByRole("button", { name: "Bank reconciliation", exact: true }).click();
  await expect(page.locator("[data-account-id]")).toHaveCount(3);
}

export async function tab(page: Page, id: "accounts" | "import" | "match" | "rules"): Promise<void> {
  await page.locator(`[data-tab-id='${id}']`).click();
}

export const test = base;
