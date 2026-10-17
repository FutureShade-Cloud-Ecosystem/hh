// Shared helpers for Manager Home's Playwright legs: the front door's
// handoff, written the way the front door writes it, and the stub's reset
// and scenarios.
import type { APIRequestContext, Page } from "@playwright/test";

/** The yard manager's demo platform user id (the flows table's own row). */
export const YARD_MANAGER_ID = "de110000-0000-4000-9000-000000000003";

/**
 * Runs in the page before any script: the front door's record, as the minted session.
 * Self-contained by necessity: an init script is serialized alone, so nothing outside
 * this function's own body reaches the page.
 */
export function managerHandoff(): void {
  const yardManagerId = "de110000-0000-4000-9000-000000000003";
  const part = (value: unknown): string =>
    btoa(JSON.stringify(value)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  const exp = Math.floor(Date.now() / 1000) + 3600;
  const token = `${part({ alg: "ES256", typ: "JWT" })}.${part({ sub: yardManagerId, exp })}.c2ln`;
  const record = {
    token,
    tokenExpiresAtMs: Date.now() + 3_600_000,
    flow: "yard-manager",
    roleLabel: "Yard manager",
    user: { userId: yardManagerId, email: "yard.manager@hh-demo.invalid", name: "Yard manager (demo)" },
    roleCode: "yard_manager",
    roleName: "Yard manager",
    tier: 2,
    scope: "BRANCH",
  };
  if (window.sessionStorage.getItem("hh-demo.front-door.session.v1") === null) {
    window.sessionStorage.setItem("hh-demo.front-door.session.v1", JSON.stringify(record));
  }
}

/** Puts the stub's world back to the morning: two open shortages, one stale pick, one difference. */
export async function resetStub(request: APIRequestContext): Promise<void> {
  // The dev server proxies /_stub to the stub, so the app's own base URL reaches it.
  const response = await request.post("/_stub/reset");
  if (!response.ok()) throw new Error(`stub reset failed: ${response.status()}`);
}

/** Switches the stub to a named scenario (empty, refused-resolve, unwired-one). */
export async function useScenario(request: APIRequestContext, name: string): Promise<void> {
  const response = await request.post("/_stub/scenario", { data: { name } });
  if (!response.ok()) throw new Error(`stub scenario failed: ${response.status()}`);
}

/** Signs in through the entry card, the demo way in (no handoff). */
export async function signInByCard(page: Page, flow: string): Promise<void> {
  await page.goto("/");
  const card = page.locator(`[data-flow="${flow}"]`);
  await card.click();
  await page.locator("[data-flow]").first().waitFor({ state: "detached" });
}

/**
 * The front door's record for a front-line role (tier three): the same minted
 * session shape, a role the core's guard keeps out of the reporting runs and
 * the commission register. The landing's reporting link must not draw for it.
 */
export function frontLineHandoff(): void {
  const salesId = "de110000-0000-4000-9000-000000000001";
  const part = (value: unknown): string =>
    btoa(JSON.stringify(value)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  const exp = Math.floor(Date.now() / 1000) + 3600;
  const token = `${part({ alg: "ES256", typ: "JWT" })}.${part({ sub: salesId, exp })}.c2ln`;
  const record = {
    token,
    tokenExpiresAtMs: Date.now() + 3_600_000,
    flow: "sales",
    roleLabel: "Sales",
    user: { userId: salesId, email: "sales@hh-demo.invalid", name: "Sales (demo)" },
    roleCode: "inside_sales",
    roleName: "Sales",
    tier: 3,
    scope: "BRANCH",
  };
  if (window.sessionStorage.getItem("hh-demo.front-door.session.v1") === null) {
    window.sessionStorage.setItem("hh-demo.front-door.session.v1", JSON.stringify(record));
  }
}
