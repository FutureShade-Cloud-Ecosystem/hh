// Shared helpers for the ledger slice's Playwright legs: the front door's
// handoff for the Financial Controller, written the way the front door writes
// it, and the stub's reset.
import { expect, test as base, type APIRequestContext, type Page } from "@playwright/test";

export { expect };

/** Runs in the page before any script: the front door's record for the controller. */
export function controllerHandoff(): void {
  const part = (value: unknown): string =>
    btoa(JSON.stringify(value)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  const exp = Math.floor(Date.now() / 1000) + 7 * 86_400;
  const id = "de110000-0000-4000-9000-000000000008";
  const token = `${part({ alg: "ES256", typ: "JWT" })}.${part({ sub: id, exp })}.c2ln`;
  const record = {
    token,
    tokenExpiresAtMs: Date.now() + 7 * 86_400_000,
    flow: "financial-controller",
    roleLabel: "Financial Controller",
    user: { userId: id, email: "financial.controller@hh-demo.invalid", name: "Financial controller (demo)" },
    roleCode: "financial_controller",
    roleName: "Financial Controller",
    tier: 1,
    scope: "GLOBAL",
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

/** Signs in through the front door's handoff and lands on the approvals queue. */
export async function openApprovals(page: Page, request: APIRequestContext): Promise<void> {
  await resetStub(request);
  await page.addInitScript(controllerHandoff);
  await page.goto("/");
  await expect(page.locator("[data-approval-id]")).toHaveCount(5);
}

export const test = base;

/** Opens a ledger tab by its id and waits for its heading to be the ledger's. */
export async function openTab(page: Page, id: string): Promise<void> {
  await page.locator(`[data-tab-id='${id}']`).click();
  await expect(page.locator(`[data-tab-id='${id}']`)).toHaveAttribute("aria-current", "true");
}

/** Fills one line of the new journal form: the account by its words, and one side. */
export async function fillLine(page: Page, index: number, account: string, side: "debit" | "credit", amount: string, memo = ""): Promise<void> {
  const key = String(index);
  await page.locator(`[data-field="line-account"][data-key="${key}"]`).selectOption({ label: account });
  await page.locator(`[data-field="line-${side}"][data-key="${key}"]`).fill(amount);
  if (memo !== "") await page.locator(`[data-field="line-memo"][data-key="${key}"]`).fill(memo);
}

/** Opens the new journal form and fills a two line journal between two accounts. */
export async function draftJournal(page: Page, memo: string, debit: string, credit: string, amount: string): Promise<void> {
  await openTab(page, "journals");
  await page.getByRole("button", { name: "New journal" }).click();
  await expect(page.locator("[data-composer]")).toBeVisible();
  await page.locator('[data-field="memo"]').fill(memo);
  await fillLine(page, 0, debit, "debit", amount);
  await fillLine(page, 1, credit, "credit", amount);
}

/** Signs in as Payables and Receivables through the handoff and opens the ledger area as a reader. */
export async function openReader(page: Page, request: APIRequestContext): Promise<void> {
  await resetStub(request);
  await page.addInitScript(payRecHandoff);
  await page.goto("/");
  await expect(page.locator("[data-card]")).toHaveCount(4);
  await page.getByRole("button", { name: "Ledger", exact: true }).click();
  await expect(page.locator("[data-tab-id='journals']")).toBeVisible();
}

/** Runs in the page before any script: the front door's record for Payables and Receivables. */
export function payRecHandoff(): void {
  const part = (value: unknown): string =>
    btoa(JSON.stringify(value)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  const exp = Math.floor(Date.now() / 1000) + 7 * 86_400;
  const id = "de110000-0000-4000-9000-000000000007";
  const token = `${part({ alg: "ES256", typ: "JWT" })}.${part({ sub: id, exp })}.c2ln`;
  const record = {
    token,
    tokenExpiresAtMs: Date.now() + 7 * 86_400_000,
    flow: "payables-receivables",
    roleLabel: "Payables and Receivables",
    user: { userId: id, email: "payables.receivables@hh-demo.invalid", name: "Payables and Receivables (demo)" },
    roleCode: "payables_receivables",
    roleName: "Payables and Receivables",
    tier: 2,
    scope: "GLOBAL",
  };
  if (window.sessionStorage.getItem("hh-demo.front-door.session.v1") === null) {
    window.sessionStorage.setItem("hh-demo.front-door.session.v1", JSON.stringify(record));
  }
}
