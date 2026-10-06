// Every job its own surface (decision log rows 955 to 957): the desk surfaces
// in the navigation bar under the shared header, the quote surfaces as tabs
// in the quote's title row, a clean "no access" for a route the role cannot
// see, the approval detail, the tablet render, and the retired "Quote detail
// & admin" disclosure. Each leg names the role that sees the surface.
import { expect, test, type APIRequestContext, type Browser, type Page } from "@playwright/test";

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

const DESKTOP = { width: 1440, height: 900 };
const TABLET = { width: 768, height: 1024 };

async function as(browser: Browser, role: "Sales" | "Sales manager", viewport = DESKTOP): Promise<Page> {
  const page = await (await browser.newContext({ viewport })).newPage();
  await page.goto("/");
  await page.getByRole("button", { name: role, exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
  return page;
}

async function plant(request: APIRequestContext, lineNo = 1): Promise<void> {
  const planted = await request.post("/_stub/plant-approval", { data: { quote_id: "q-1001", line_no: lineNo, requested_by_flow: "sales" } });
  expect(planted.ok()).toBe(true);
}

const nav = (page: Page) => page.getByTestId("desk-nav");
const tab = (page: Page, name: string) => page.getByTestId(`desk-tab-${name}`);
const qtab = (page: Page, name: string) => page.getByTestId(`qt-tab-${name}`);

// ── A: the desk navigation bar, role walled ──────────────────────────────────

test("A: Sales sees the desk navigation with Quotes only; the manager tabs are not in the page", async ({ browser }) => {
  const page = await as(browser, "Sales");
  await expect(nav(page)).toBeVisible();
  await expect(tab(page, "quotes")).toBeVisible();
  await expect(tab(page, "quotes")).toHaveAttribute("aria-current", "page");
  for (const name of ["approvals", "pricing", "settings"]) await expect(tab(page, name)).toHaveCount(0);
  await page.close();
});

test("A: the manager sees all four desk tabs, and the Approvals tab carries the pending count", async ({ browser, request }) => {
  await plant(request);
  const page = await as(browser, "Sales manager");
  for (const name of ["quotes", "approvals", "pricing", "settings"]) await expect(tab(page, name)).toBeVisible();
  await expect(tab(page, "approvals")).toHaveText("Approvals 1");
  await tab(page, "pricing").click();
  await expect(page).toHaveURL(/#\/pricing$/);
  await expect(tab(page, "pricing")).toHaveAttribute("aria-current", "page");
  await tab(page, "quotes").click();
  await expect(page).toHaveURL(/#\/quotes$/);
  await expect(tab(page, "quotes")).toHaveAttribute("aria-current", "page");
  await page.close();
});

test("A: the Approvals count falls when a request is decided", async ({ browser, request }) => {
  await plant(request);
  const page = await as(browser, "Sales manager");
  await expect(tab(page, "approvals")).toHaveText("Approvals 1");
  await tab(page, "approvals").click();
  await page.getByTestId("ap-row").first().getByTestId("ap-approve").click();
  await expect(tab(page, "approvals")).toHaveText("Approvals");
  await page.close();
});

// ── B: the quote surfaces as tabs in the title row ───────────────────────────

test("B: the quote page carries Build, Activity, Stock and Admin for the manager, and the tabs route by hash", async ({ browser }) => {
  const page = await as(browser, "Sales manager");
  await page.goto("/#/quotes/q-1001");
  const strip = page.getByTestId("qt-strip");
  await expect(strip).toBeVisible();
  for (const name of ["build", "activity", "stock", "admin"]) await expect(qtab(page, name)).toBeVisible();
  await expect(qtab(page, "build")).toHaveAttribute("aria-current", "page");
  await qtab(page, "activity").click();
  await expect(page).toHaveURL(/#\/quotes\/q-1001\/activity$/);
  await expect(qtab(page, "activity")).toHaveAttribute("aria-current", "page");
  await qtab(page, "stock").click();
  await expect(page).toHaveURL(/#\/quotes\/q-1001\/stock$/);
  await qtab(page, "admin").click();
  await expect(page).toHaveURL(/#\/quotes\/q-1001\/admin$/);
  await page.goBack();
  await expect(qtab(page, "stock")).toHaveAttribute("aria-current", "page");
  await qtab(page, "build").click();
  await expect(page).toHaveURL(/#\/quotes\/q-1001$/);
  // The strip sits in the quote's title row, beside the title.
  const title = (await page.getByTestId("qb-title").boundingBox())!;
  const box = (await strip.boundingBox())!;
  expect(box.y).toBeLessThan(title.y + title.height + 40);
  await page.close();
});

test("B: Sales has Build, Activity and Stock; the Admin tab is not in the page", async ({ browser }) => {
  const page = await as(browser, "Sales");
  await page.goto("/#/quotes/q-1001");
  for (const name of ["build", "activity", "stock"]) await expect(qtab(page, name)).toBeVisible();
  await expect(qtab(page, "admin")).toHaveCount(0);
  await page.close();
});

test("B: each quote surface holds its own panels", async ({ browser }) => {
  const page = await as(browser, "Sales manager");
  await page.goto("/#/quotes/q-1001/activity");
  await expect(page.locator("qd-quote-history")).toBeVisible();
  await expect(page.locator("qd-quote-followups")).toBeVisible();
  await expect(page.locator("qd-quote-availability")).toBeHidden();
  await page.goto("/#/quotes/q-1001/stock");
  await expect(page.locator("qd-quote-availability")).toBeVisible();
  await expect(page.locator("qd-quote-history")).toBeHidden();
  await page.goto("/#/quotes/q-1001/admin");
  await expect(page.locator("qd-quote-edit")).toBeVisible();
  await expect(page.locator("qd-quote-guard")).toBeVisible();
  await expect(page.locator("qd-quote-availability")).toBeHidden();
  await page.close();
});

// ── C: the role wall at every route ──────────────────────────────────────────

for (const hash of ["#/approvals", "#/approvals/fr-q-1001:1", "#/pricing", "#/settings", "#/quotes/q-1001/admin"]) {
  test(`C: Sales visiting ${hash} lands on the no access surface`, async ({ browser, request }) => {
    await plant(request);
    const page = await as(browser, "Sales");
    await page.goto(`/${hash}`);
    await expect(page.getByTestId("no-access")).toBeVisible();
    await expect(page.getByTestId("no-access")).toContainText("not available to your role");
    await expect(page.locator("qd-approvals-queue, qd-approval-detail, qd-pricing-surface, qd-settings-page, qd-quote-edit")).toHaveCount(0);
    await page.close();
  });
}

test("C: the manager reaches every walled route", async ({ browser, request }) => {
  await plant(request);
  const page = await as(browser, "Sales manager");
  await page.goto("/#/approvals");
  await expect(page.locator("qd-approvals-queue")).toBeVisible();
  await page.goto("/#/pricing");
  await expect(page.locator("qd-pricing-surface")).toBeVisible();
  await page.goto("/#/settings");
  await expect(page.locator("qd-settings-page")).toBeVisible();
  for (const section of ["qs-screen", "qa-details", "qr-details"]) await expect(page.getByTestId(section)).toBeVisible();
  await expect(page.getByTestId("no-access")).toHaveCount(0);
  await page.close();
});

test("C: back and forward move between surfaces", async ({ browser }) => {
  const page = await as(browser, "Sales manager");
  await tab(page, "approvals").click();
  await tab(page, "settings").click();
  await expect(page).toHaveURL(/#\/settings$/);
  await page.goBack();
  await expect(page).toHaveURL(/#\/approvals$/);
  await expect(page.locator("qd-approvals-queue")).toBeVisible();
  await page.goForward();
  await expect(page.locator("qd-settings-page")).toBeVisible();
  await page.close();
});

// ── D: the approval request opens its quote ──────────────────────────────────

test("D: an Approvals row opens the request in its quote's context; Approve decides it and returns to the queue", async ({ browser, request }) => {
  await plant(request, 2);
  const page = await as(browser, "Sales manager");
  await tab(page, "approvals").click();
  const row = page.getByTestId("ap-row");
  await expect(row).toHaveCount(1);
  await row.getByTestId("ap-open").click();
  await expect(page).toHaveURL(/#\/approvals\/fr-q-1001%3A2$/);
  const detail = page.locator("qd-approval-detail");
  await expect(detail.getByTestId("ad-quote")).toContainText("Q-1001");
  await expect(detail.getByTestId("ad-customer")).not.toBeEmpty();
  await expect(detail.getByTestId("ad-rep")).not.toBeEmpty();
  const lines = detail.getByTestId("ad-line");
  await expect(lines).toHaveCount(2);
  await expect(detail.locator('[data-testid="ad-line"][data-requested="true"]')).toHaveCount(1);
  await expect(detail.locator('[data-testid="ad-line"][data-requested="true"]')).toHaveAttribute("data-line", "2");
  await expect(detail.getByTestId("ad-line").first()).toHaveAttribute("data-requested", "false");
  await expect(detail.getByTestId("ad-totals")).toContainText("Total");
  await expect(detail.getByTestId("ad-open-quote")).toHaveAttribute("href", "#/quotes/q-1001");
  await detail.getByTestId("ap-reason").fill("Contractor account");
  await detail.getByTestId("ap-approve").click();
  await expect(page).toHaveURL(/#\/approvals$/);
  await expect(page.getByTestId("ap-row").first()).toHaveAttribute("data-status", "APPROVED");
  await expect(tab(page, "approvals")).toHaveText("Approvals");
  await page.close();
});

test("D: Refuse from the detail files the reason and returns to the queue on the next request", async ({ browser, request }) => {
  await plant(request, 1);
  await plant(request, 2);
  const page = await as(browser, "Sales manager");
  await page.goto("/#/approvals/fr-q-1001%3A1");
  const detail = page.locator("qd-approval-detail");
  await expect(detail.getByTestId("ad-quote")).toContainText("Q-1001");
  await detail.getByTestId("ap-reason").fill("Hold the price");
  await detail.getByTestId("ap-refuse").click();
  await expect(page).toHaveURL(/#\/approvals\/fr-q-1001%3A2$/);
  await expect(detail.locator('[data-testid="ad-line"][data-requested="true"]')).toHaveAttribute("data-line", "2");
  const { decisions } = await (await request.get("/_stub/state")).json();
  expect(decisions).toEqual([{ id: "fr-q-1001:1", approve: false, reason: "Hold the price" }]);
  await page.close();
});

test("D: a request the server does not hold says so on the detail", async ({ browser }) => {
  const page = await as(browser, "Sales manager");
  await page.goto("/#/approvals/fr-nothing");
  await expect(page.getByTestId("ad-missing")).toBeVisible();
  await page.close();
});

// ── E: the tablet viewport ───────────────────────────────────────────────────

test("E: at tablet width the manager's navigation carries all four tabs and the quote strip all four quote tabs", async ({ browser }) => {
  const page = await as(browser, "Sales manager", TABLET);
  for (const name of ["quotes", "approvals", "pricing", "settings"]) await expect(tab(page, name)).toBeVisible();
  const box = (await nav(page).boundingBox())!;
  expect(box.x + box.width).toBeLessThanOrEqual(TABLET.width);
  await page.goto("/#/quotes/q-1001");
  for (const name of ["build", "activity", "stock", "admin"]) await expect(qtab(page, name)).toBeVisible();
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth);
  expect(overflow).toBe(true);
  await page.close();
});

// ── G: the disclosure is gone ────────────────────────────────────────────────

test("G: the quote page no longer carries the Quote detail and admin disclosure", async ({ browser }) => {
  const page = await as(browser, "Sales manager");
  await page.goto("/#/quotes/q-1001");
  await expect(page.getByTestId("qb-context")).toContainText("Q-1001");
  await expect(page.getByText("Quote detail & admin")).toHaveCount(0);
  await expect(page.getByTestId("qb-detail")).toHaveCount(0);
  await expect(page.getByTestId("qb-quote-panels")).toHaveCount(0);
  await page.close();
});
