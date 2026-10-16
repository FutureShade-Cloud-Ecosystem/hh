// Shared helpers for the Playwright legs: the front door's handoff, written
// the way the front door writes it, and the stub's reset.
import { expect, test as base, type APIRequestContext, type Page } from "@playwright/test";

export { expect };

/** Runs in the page before any script: the front door's record, as the minted session. */
export function receivablesHandoff(who?: "controller"): void {
  const part = (value: unknown): string =>
    btoa(JSON.stringify(value)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  const exp = Math.floor(Date.now() / 1000) + 7 * 86_400;
  const c = who === "controller";
  const id = c ? "de110000-0000-4000-9000-000000000008" : "de110000-0000-4000-9000-000000000007";
  const token = `${part({ alg: "ES256", typ: "JWT" })}.${part({ sub: id, exp })}.c2ln`;
  const record = {
    token,
    tokenExpiresAtMs: Date.now() + 7 * 86_400_000,
    flow: c ? "financial-controller" : "payables-receivables",
    roleLabel: c ? "Financial Controller" : "Payables and Receivables",
    user: { userId: id, email: c ? "financial.controller@hh-demo.invalid" : "payables.receivables@hh-demo.invalid", name: c ? "Financial Controller (demo)" : "Payables and Receivables (demo)" },
    roleCode: c ? "financial_controller" : "payables_receivables",
    roleName: c ? "Financial Controller" : "Payables and Receivables",
    tier: c ? 1 : 2,
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

/** Signs in through the front door's handoff and lands on the receivables desk. */
export async function openDesk(page: Page, request: APIRequestContext, who?: "controller"): Promise<void> {
  await resetStub(request);
  await page.addInitScript(receivablesHandoff, who);
  await page.goto("/");
  // The controller lands on the ledger; the receivables desk is one tap away.
  if (who === "controller") await page.getByRole("button", { name: "Receivables", exact: true }).click();
  await page.getByRole("button", { name: "Register", exact: true }).click();
  await expect(page.locator("[data-card]")).toHaveCount(4);
}

/** A date this many days from today, in the dealer's zone, built from the host's own clock. */
export function dayFromToday(days: number): string {
  const at = new Date();
  at.setUTCDate(at.getUTCDate() + days);
  return new Intl.DateTimeFormat("en-CA", { timeZone: "America/Toronto", year: "numeric", month: "2-digit", day: "2-digit" }).format(at);
}

export const test = base;
