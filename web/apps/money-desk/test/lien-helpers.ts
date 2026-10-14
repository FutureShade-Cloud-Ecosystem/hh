// Shared helpers for the liens and compliance slices' Playwright legs: the front door's handoff for each role that
// holds the areas (written the way the front door writes it), the stub's reset, and the way into each area.
import { expect, test as base, type APIRequestContext, type Page } from "@playwright/test";
import { resetStub } from "./gl-helpers.js";

export { expect, resetStub };

export const GM_ID = "de110000-0000-4000-9000-0000000000a9";
export const CONTROLLER_ID = "de110000-0000-4000-9000-000000000008";
export const PAYREC_ID = "de110000-0000-4000-9000-000000000007";
export const JOB1 = "10b00000-0000-4000-8000-000000000001";
export const JOB4 = "10b00000-0000-4000-8000-000000000004";
export const JOB5 = "10b00000-0000-4000-8000-000000000005";
export const COCKPIT = "00000000-0000-4000-9000-0000000000d6";

/** A day this many days from the host's own today, written the way the core writes a date. */
export function dayFrom(days: number): string {
  return new Date(Date.now() + days * 86_400_000).toISOString().slice(0, 10);
}

export type Who = "general_manager" | "controller" | "payrec";

/** Runs in the page before any script: the front door's record for one role. */
export function handoffFor(who: string): void {
  const part = (value: unknown): string => btoa(JSON.stringify(value)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  const exp = Math.floor(Date.now() / 1000) + 7 * 86_400;
  const roles = {
    general_manager: { id: "de110000-0000-4000-9000-0000000000a9", flow: "general-manager", label: "General Manager", code: "general_manager", tier: 1, mail: "general.manager" },
    controller: { id: "de110000-0000-4000-9000-000000000008", flow: "financial-controller", label: "Financial Controller", code: "financial_controller", tier: 1, mail: "financial.controller" },
    payrec: { id: "de110000-0000-4000-9000-000000000007", flow: "payables-receivables", label: "Payables and Receivables", code: "payables_receivables", tier: 2, mail: "payables.receivables" },
  } as const;
  const r = roles[who as Who];
  const token = `${part({ alg: "ES256", typ: "JWT" })}.${part({ sub: r.id, exp })}.c2ln`;
  const record = {
    token,
    tokenExpiresAtMs: Date.now() + 7 * 86_400_000,
    flow: r.flow,
    roleLabel: r.label,
    user: { userId: r.id, email: `${r.mail}@hh-demo.invalid`, name: `${r.label} (demo)` },
    roleCode: r.code,
    roleName: r.label,
    tier: r.tier,
    scope: "GLOBAL",
  };
  if (window.sessionStorage.getItem("hh-demo.front-door.session.v1") === null) {
    window.sessionStorage.setItem("hh-demo.front-door.session.v1", JSON.stringify(record));
  }
}

/** Signs in as a role through the handoff. The General Manager lands on Liens; the others take one tap on the area switch. */
export async function openLiens(page: Page, request: APIRequestContext, who: Who = "controller"): Promise<void> {
  await resetStub(request);
  await page.addInitScript(handoffFor, who);
  await page.goto("/");
  if (who !== "general_manager") await page.getByRole("button", { name: "Liens", exact: true }).click();
  await expect(page.locator("[data-liens]")).toBeVisible();
  await expect(page.locator("[data-row='triage']:visible").first()).toBeVisible();
}

export async function openCompliance(page: Page, request: APIRequestContext, who: Who = "controller"): Promise<void> {
  await resetStub(request);
  await page.addInitScript(handoffFor, who);
  await page.goto("/");
  await page.getByRole("button", { name: "Compliance", exact: true }).click();
  await expect(page.locator("[data-compliance]")).toBeVisible();
  await expect(page.locator("[data-row='deadline']:visible").first()).toBeVisible();
}

export async function liensTab(page: Page, id: "triage" | "jobs" | "holdbacks" | "documents"): Promise<void> {
  await page.locator(`[data-liens] [data-tab-id='${id}']`).click();
}

export async function complianceTab(page: Page, id: "upcoming" | "calendar" | "review"): Promise<void> {
  await page.locator(`[data-compliance] [data-tab-id='${id}']`).click();
}

/** The page does not scroll sideways: nothing is wider than the window. */
export async function noSidewaysScroll(page: Page): Promise<void> {
  const widths = await page.evaluate(() => ({ doc: document.documentElement.scrollWidth, win: window.innerWidth }));
  expect(widths.doc).toBeLessThanOrEqual(widths.win);
}

export const test = base;
