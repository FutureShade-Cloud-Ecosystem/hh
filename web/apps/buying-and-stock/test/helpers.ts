// Shared helpers for the Playwright legs: the front door's handoff, written
// the way the front door writes it, and the stub's reset.
import { expect, test as base, type APIRequestContext, type Page } from "@playwright/test";

export { expect };

/** Runs in the page before any script: the front door's record, as the minted session. */
export function procurementHandoff(): void {
  const part = (value: unknown): string =>
    btoa(JSON.stringify(value)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  const exp = Math.floor(Date.now() / 1000) + 7 * 86_400;
  const token = `${part({ alg: "ES256", typ: "JWT" })}.${part({ sub: "de110000-0000-4000-9000-000000000008", exp })}.c2ln`;
  const record = {
    token,
    tokenExpiresAtMs: Date.now() + 7 * 86_400_000,
    flow: "procurement-manager",
    roleLabel: "Procurement manager",
    user: { userId: "de110000-0000-4000-9000-000000000008", email: "procurement.manager@hh-demo.invalid", name: "Procurement manager (demo)" },
    roleCode: "procurement_manager",
    roleName: "Procurement manager",
    tier: 2,
    scope: "BRANCH",
  };
  if (window.sessionStorage.getItem("hh-demo.front-door.session.v1") === null) {
    window.sessionStorage.setItem("hh-demo.front-door.session.v1", JSON.stringify(record));
  }
}

/** Runs in the page before any script: the front door's record for another seat, given the role code or a seat with its own account id (passed to addInitScript). */
export function roleHandoff(seat: string | { roleCode: string; userId: string }): void {
  const roleCode = typeof seat === "string" ? seat : seat.roleCode;
  const userId = typeof seat === "string" ? "de110000-0000-4000-9000-000000000009" : seat.userId;
  const part = (value: unknown): string =>
    btoa(JSON.stringify(value)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  const exp = Math.floor(Date.now() / 1000) + 7 * 86_400;
  const token = `${part({ alg: "ES256", typ: "JWT" })}.${part({ sub: userId, exp })}.c2ln`;
  const record = {
    token,
    tokenExpiresAtMs: Date.now() + 7 * 86_400_000,
    flow: roleCode,
    roleLabel: roleCode,
    user: { userId, email: `${roleCode}@hh-demo.invalid`, name: `${roleCode} (demo)` },
    roleCode,
    roleName: roleCode,
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

/** Signs in through the front door's handoff and lands on the Buying and Stock home. */
export async function openHome(page: Page, request: APIRequestContext): Promise<void> {
  await resetStub(request);
  await page.addInitScript(procurementHandoff);
  await page.goto("/");
  await expect(page.locator("[data-shortfall-row]").first()).toBeVisible();
}

/**
 * Every visible text node's contrast against the colour it really sits on (backgrounds composited up the
 * tree, shadow roots crossed). Returns "text: ratio" for each under 4.5 (3 for large text). Disabled controls
 * are exempt, as the standard has it.
 */
export async function lowContrast(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const canvas = document.createElement("canvas");
    canvas.width = canvas.height = 1;
    const ctx = canvas.getContext("2d", { willReadFrequently: true })!;
    const rgba = (css: string): [number, number, number, number] => {
      ctx.clearRect(0, 0, 1, 1);
      ctx.fillStyle = "#000";
      ctx.fillStyle = css;
      ctx.fillRect(0, 0, 1, 1);
      const d = ctx.getImageData(0, 0, 1, 1).data;
      return [d[0] ?? 0, d[1] ?? 0, d[2] ?? 0, (d[3] ?? 0) / 255];
    };
    const over = (top: number[], under: number[]): number[] => {
      const a = top[3] ?? 1;
      return [0, 1, 2].map((i) => (top[i] ?? 0) * a + (under[i] ?? 0) * (1 - a)).concat([1]);
    };
    const lum = (c: number[]): number => {
      const f = (v: number): number => {
        const x = v / 255;
        return x <= 0.03928 ? x / 12.92 : ((x + 0.055) / 1.055) ** 2.4;
      };
      return 0.2126 * f(c[0] ?? 0) + 0.7152 * f(c[1] ?? 0) + 0.0722 * f(c[2] ?? 0);
    };
    const parentOf = (e: Element): Element | null => e.parentElement ?? ((e.getRootNode() as ShadowRoot).host ?? null);
    const backdrop = (e: Element): number[] => {
      const chain: number[][] = [];
      for (let n: Element | null = e; n !== null; n = parentOf(n)) chain.push(rgba(getComputedStyle(n).backgroundColor));
      let base = [255, 255, 255, 1];
      for (let i = chain.length - 1; i >= 0; i -= 1) base = over(chain[i] as number[], base);
      return base;
    };
    const out: string[] = [];
    const walk = (root: Document | ShadowRoot | Element): void => {
      const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
      for (let node = walker.nextNode(); node !== null; node = walker.nextNode()) {
        const text = (node.textContent ?? "").trim();
        const el = node.parentElement;
        if (text === "" || el === null || el.closest("[disabled]") !== null) continue;
        if (el.closest("style, script") !== null) continue;
        const cs = getComputedStyle(el);
        if (cs.visibility === "hidden" || cs.display === "none") continue;
        const bg = backdrop(el);
        const fg = over(rgba(cs.color), bg);
        const l1 = lum(fg);
        const l2 = lum(bg);
        const ratio = (Math.max(l1, l2) + 0.05) / (Math.min(l1, l2) + 0.05);
        const size = parseFloat(cs.fontSize);
        const large = size >= 24 || (size >= 18.66 && Number(cs.fontWeight) >= 700);
        if (ratio < (large ? 3 : 4.5)) out.push(`${text.slice(0, 40)}: ${ratio.toFixed(2)}`);
      }
      root.querySelectorAll("*").forEach((n) => {
        if (n.shadowRoot !== null) walk(n.shadowRoot);
      });
    };
    walk(document);
    return out;
  });
}

export const test = base;

/** Switches the stub's caller to another role (payables-receivables, super-admin); reset puts it back. */
export async function stubRole(request: APIRequestContext, role: string): Promise<void> {
  const response = await request.post("/_stub/role", { data: { role } });
  if (!response.ok()) throw new Error(`stub role failed: ${response.status()}`);
}

/** Signs in and opens one of the purchasing areas by its tab. */
export async function openArea(page: Page, request: APIRequestContext, name: string): Promise<void> {
  await openHome(page, request);
  await page.locator("nav.areas").getByRole("button", { name }).click();
}

/** The page never scrolls sideways: nothing is wider than the window. */
export async function noSidewaysScroll(page: Page): Promise<void> {
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(overflow, "the page scrolls sideways").toBeLessThanOrEqual(0);
}

/** Signs in as another seat (its role code in the session, its role at the stub's wall) and opens an area. */
export async function openAreaAs(page: Page, request: APIRequestContext, seat: { code: string; stub: string }, name: string): Promise<void> {
  await resetStub(request);
  await stubRole(request, seat.stub);
  await page.addInitScript(roleHandoff, seat.code);
  await page.goto("/");
  await expect(page.locator("[data-shortfall-row]").first()).toBeVisible();
  await page.locator("nav.areas").getByRole("button", { name }).click();
}

export const AP = { code: "payables_receivables", stub: "payables-receivables" };
export const SUPER = { code: "super_admin", stub: "super-admin" };

// The two other seats this surface names: the yard manager (work: receive, count) and Payables and
// Receivables (read), each with its own demo account id (the counts gate reads the token's sub, and
// the stub's seat follows it). Passed to roleHandoff as the init script's argument.
export const YARD_SEAT = { roleCode: "yard_manager", userId: "de110000-0000-4000-9000-000000000003" };
export const PAYABLES_SEAT = { roleCode: "payables_receivables", userId: "de110000-0000-4000-9000-0000000000a1" };
// The sales seat the stock lookup is drawn for (change 5 slice S4, ruling R1): read only.
export const INSIDE_SEAT = { roleCode: "inside_sales", userId: "de110000-0000-4000-9000-0000000000a2" };

/** Switches to one of the app's areas once signed in, by its id (the tabs' data-area-id). */
export async function showArea(page: Page, id: string): Promise<void> {
  await page.locator(`nav.areas [data-area-id="${id}"]`).click();
  await expect(page.locator(`nav.areas [data-area-id="${id}"][aria-current="true"]`)).toBeVisible();
}

/** Signs in as the procurement manager and lands on the inventory area's lookup. */
export async function openInventory(page: Page, request: APIRequestContext, tab: string): Promise<void> {
  await resetStub(request);
  await page.addInitScript(procurementHandoff);
  await page.goto("/");
  await expect(page.locator("[data-shortfall-row]").first()).toBeVisible();
  await showArea(page, "inventory");
  await page.locator(`[data-inv-tab="${tab}"]`).click();
  await expect(page.locator(`[data-inv-tab="${tab}"][aria-current="true"]`)).toBeVisible();
}

/** Signs in as the procurement manager and lands on the counts area's sheets. */
export async function openCounts(page: Page, request: APIRequestContext, tab: string): Promise<void> {
  await resetStub(request);
  await page.addInitScript(procurementHandoff);
  await page.goto("/");
  await expect(page.locator("[data-shortfall-row]").first()).toBeVisible();
  await showArea(page, "counts");
  await page.locator(`[data-counts-tab="${tab}"]`).click();
  await expect(page.locator(`[data-counts-tab="${tab}"][aria-current="true"]`)).toBeVisible();
}

/** Clears the handoff and re-signs in as another role on the same page. The
 * new record is written directly: an init script would race the role's own
 * still-registered script, which writes first and keeps the old seat. */
export async function switchRole(page: Page, handoff: (...args: never[]) => void, arg?: unknown): Promise<void> {
  await page.evaluate(() => window.sessionStorage.removeItem("hh-demo.front-door.session.v1"));
  await page.evaluate(handoff as (arg: unknown) => void, arg);
  await page.goto("/");
  await expect(page.locator("[data-shortfall-row]").first()).toBeVisible();
}
