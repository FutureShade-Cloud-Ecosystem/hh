// The surfaces round's before and after shots (light theme; desktop 1440x900 and tablet 768x1024). Written only
// when QD_SHOT_DIR names a directory; QD_SHOT_PHASE is "before" or "after" and prefixes every name. The legs
// assert nothing: a shot of a regression has to be takeable where the surface does not exist yet.
import { expect, test, type Browser, type Page } from "@playwright/test";
import { shot } from "./shot.js";

const PHASE = process.env.QD_SHOT_PHASE === "before" ? "before" : "after";
const DESKTOP = { width: 1440, height: 900 };
const TABLET = { width: 768, height: 1024 };

test.skip(!process.env.QD_SHOT_DIR, "shots are written only when QD_SHOT_DIR is set");

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

async function as(browser: Browser, role: "Sales" | "Sales manager", viewport = DESKTOP): Promise<Page> {
  const context = await browser.newContext({ viewport, colorScheme: "light" });
  const page = await context.newPage();
  await page.goto("/");
  await page.getByRole("button", { name: role, exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
  await page.evaluate(() => document.documentElement.setAttribute("data-theme", "light"));
  return page;
}

async function go(page: Page, hash: string): Promise<void> {
  await page.goto(`/${hash}`);
  await page.evaluate(() => document.fonts.ready);
  await page.waitForLoadState("networkidle");
}

/** The quote by its route; where the head has no quote route yet (the before shots) the list row opens it. */
async function openQuote(page: Page): Promise<void> {
  await go(page, "#/quotes/q-1001");
  const context = page.getByTestId("qb-context");
  if (!((await context.innerText()).includes("Q-1001"))) {
    await page.getByTestId("qb-list-row").filter({ hasText: "Q-1001" }).first().click();
    await expect(context).toContainText("Q-1001");
  }
}

async function plant(page: Page, line = 1): Promise<void> {
  await page.request.post("/_stub/plant-approval", { data: { quote_id: "q-1001", line_no: line, requested_by_flow: "sales" } });
}

async function both(name: string, page: Page, only: "before" | "after" | "both" = "both"): Promise<void> {
  if (only !== "both" && only !== PHASE) return;
  await shot(page, `${PHASE}-${name}`);
}

test("shots: the navigation bar", async ({ browser }) => {
  const sales = await as(browser, "Sales");
  await go(sales, "#/quotes");
  await both("topnav-sales-desktop", sales);
  const manager = await as(browser, "Sales manager");
  await plant(manager);
  await go(manager, "#/quotes");
  await manager.reload();
  await manager.evaluate(() => document.documentElement.setAttribute("data-theme", "light"));
  await both("topnav-manager-desktop", manager);
  const tablet = await as(browser, "Sales manager", TABLET);
  await go(tablet, "#/quotes");
  await both("topnav-manager-tablet", tablet);
});

test("shots: the quote page tabs and the retired disclosure", async ({ browser }) => {
  const manager = await as(browser, "Sales manager");
  await openQuote(manager);
  await both("quotetabs-manager-desktop", manager);
  const disclosure = manager.getByTestId("qb-detail");
  if ((await disclosure.count()) > 0) {
    await manager.getByText("Quote detail & admin").click();
    await disclosure.scrollIntoViewIfNeeded();
  }
  await both("no-disclosure-manager-desktop", manager);
  await openQuote(manager);
  await both("build-manager-desktop", manager, "after");
  const sales = await as(browser, "Sales");
  await openQuote(sales);
  await both("build-sales-desktop", sales, "after");
});

test("shots: the approvals route as Sales", async ({ browser }) => {
  const sales = await as(browser, "Sales");
  await go(sales, "#/approvals");
  await both("approvals-route-sales-desktop", sales);
  await go(sales, "#/pricing");
  await both("pricing-route-sales-desktop", sales, "after");
  await go(sales, "#/settings");
  await both("settings-route-sales-desktop", sales, "after");
  await go(sales, "#/quotes/q-1001/admin");
  await both("admin-route-sales-desktop", sales, "after");
});

test("shots: the approval request and its detail", async ({ browser }) => {
  const manager = await as(browser, "Sales manager");
  await plant(manager, 2);
  await go(manager, "#/approvals");
  await manager.reload();
  await manager.evaluate(() => document.documentElement.setAttribute("data-theme", "light"));
  await both("approvals-queue-manager-desktop", manager, "after");
  const open = manager.getByTestId("ap-open");
  if ((await open.count()) > 0) await open.first().click();
  await manager.waitForLoadState("networkidle");
  await both("approval-detail-manager-desktop", manager);
});

test("shots: every other surface", async ({ browser }) => {
  const manager = await as(browser, "Sales manager");
  for (const [name, hash] of [
    ["pricing", "#/pricing"],
    ["settings", "#/settings"],
    ["activity", "#/quotes/q-1001/activity"],
    ["stock", "#/quotes/q-1001/stock"],
    ["admin", "#/quotes/q-1001/admin"],
  ] as const) {
    await go(manager, hash);
    await both(`${name}-manager-desktop`, manager, "after");
  }
  const tablet = await as(browser, "Sales manager", TABLET);
  await go(tablet, "#/quotes/q-1001");
  await both("quotetabs-manager-tablet", tablet, "after");
  const sales = await as(browser, "Sales");
  await go(sales, "#/quotes");
  await both("quotes-sales-desktop", sales, "after");
  for (const [name, hash] of [
    ["activity", "#/quotes/q-1001/activity"],
    ["stock", "#/quotes/q-1001/stock"],
  ] as const) {
    await go(sales, hash);
    await both(`${name}-sales-desktop`, sales, "after");
  }
});
