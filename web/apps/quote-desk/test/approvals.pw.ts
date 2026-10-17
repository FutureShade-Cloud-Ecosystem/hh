// The approvals list in a real browser, against the stubbed harness: Sales
// files a margin floor request through a discount below the floor, the Sales
// manager signs in and sees the request with the server's own detail text,
// approves or refuses it with a reason and the row updates; Sales sees the
// request but a decision attempt is refused in the server's words, verbatim,
// and the decide controls leave the list; a decision on an already decided
// request is the server's 409; and with the caller off the cost list the
// detail text carries no margin and no figure appears anywhere in the panels.
import { expect, test, type APIRequestContext, type Browser, type Page } from "@playwright/test";
import { shot } from "./shot";
import { showList } from "./switch-helpers.js";

async function signIn(page: Page, role = "Sales"): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: role, exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
}

async function openQuote(page: Page, text: string): Promise<void> {
  await showList(page);
  await page.getByTestId("qb-list-row").filter({ hasText: text }).first().click();
  await expect(page.getByTestId("qb-context")).toContainText(text);
  const detail = page.getByTestId("qb-detail");
  if ((await detail.getAttribute("open")) === null) await page.getByText("Quote detail & admin").click();
}

/**
 * The requester's way to the approvals view under the split-list
 * navigation: the quote page's disclosure carries the Approvals way in,
 * which walks the desk to the list page's approvals view.
 */
async function openApprovals(page: Page, text = "Q-1001"): Promise<void> {
  await openQuote(page, text);
  await page.getByRole("button", { name: "Approvals", exact: true }).click();
  await expect(page.getByTestId("ap-box")).toBeVisible();
}

const approvals = (page: Page) => page.locator("qd-approvals");
const dialog = (page: Page) => page.locator("qd-floor-refusal");
const edit = (page: Page) => page.locator("qd-quote-edit");

/** Sales asks for a discount below the floor on line 1 of the draft and files the request through the dialog. */
async function fileAsSales(page: Page, disc = "10"): Promise<void> {
  await signIn(page);
  await openQuote(page, "Q-1001");
  const paver = edit(page).getByTestId("qe-line").first();
  await paver.locator('[data-field="disc"]').fill(disc);
  await paver.getByRole("button", { name: "Save" }).click();
  await expect(dialog(page).getByTestId("fr-dialog")).toBeVisible();
  await dialog(page).getByTestId("fr-request").click();
  await expect(dialog(page).getByTestId("fr-request-state")).toContainText("pending");
  await dialog(page).getByTestId("fr-close").click();
  // The approvals view is the list page's own content now: walk to it
  // through the disclosure's way in.
  await page.getByRole("button", { name: "Approvals", exact: true }).click();
  await expect(page.getByTestId("ap-box")).toBeVisible();
  await expect(approvals(page).getByTestId("ap-row")).toHaveCount(1);
}

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

async function asManager(browser: Browser): Promise<Page> {
  const page = await (await browser.newContext({ viewport: { width: 1440, height: 900 } })).newPage();
  await signIn(page, "Sales manager");
  // The Sales manager's own way in: the list page's tabs carry Approvals.
  await page.getByTestId("qd-list-tabs").getByRole("button", { name: "Approvals", exact: true }).click();
  await expect(page.getByTestId("ap-box")).toBeVisible();
  return page;
}

test("with nothing filed the list says nobody is held up", async ({ page }) => {
  await signIn(page);
  await openApprovals(page);
  await expect(approvals(page).getByTestId("ap-empty")).toHaveText("Nobody is held up.");
  await expect(approvals(page).getByTestId("ap-row")).toHaveCount(0);
});

test("Sales files a request and the Sales manager sees it with the server's detail text and approves it", async ({ page, browser }) => {
  await fileAsSales(page);
  const row = approvals(page).getByTestId("ap-row").first();
  await expect(row).toHaveAttribute("data-status", "PENDING");
  const sales = await row.getByTestId("ap-detail").innerText();
  expect(sales).toMatch(/^quote Q-1001 line 1: 10 percent discount, resulting margin [\d.]+% against the [\d.]+% floor$/);
  expect(sales, "no id in the sentence").not.toMatch(/[0-9a-f]{8}-[0-9a-f]{4}-/);

  const manager = await asManager(browser);
  const mrow = approvals(manager).getByTestId("ap-row").first();
  await expect(mrow.getByTestId("ap-detail")).toHaveText(sales);
  await expect(mrow.getByTestId("ap-status")).toHaveText("Pending");
  await expect(approvals(manager).getByTestId("ap-count")).toHaveText("1 pending");
  await approvals(manager).scrollIntoViewIfNeeded();
  await shot(manager, "approvals-pending");
  await approvals(manager).getByTestId("ap-reason").fill("Contractor account, fine");
  await mrow.getByRole("button", { name: /^Approve/ }).click();
  await expect(approvals(manager).getByTestId("ap-outcome")).toHaveText("Approved.");
  await expect(mrow.getByTestId("ap-status")).toHaveText("Approved");
  await expect(mrow.getByRole("button", { name: /^Approve/ })).toHaveCount(0);
  await expect(mrow.getByRole("button", { name: /^Refuse/ })).toHaveCount(0);
  await expect(approvals(manager).getByTestId("ap-count")).toHaveText("0 pending");
  await shot(manager, "approvals-approved");

  await manager.close();
});

test("the Sales manager can refuse with a reason and the row reads Rejected", async ({ page, browser }) => {
  await fileAsSales(page);
  const manager = await asManager(browser);
  const mrow = approvals(manager).getByTestId("ap-row").first();
  await approvals(manager).getByTestId("ap-reason").fill("Hold the price");
  await mrow.getByRole("button", { name: /^Refuse/ }).click();
  await expect(approvals(manager).getByTestId("ap-outcome")).toHaveText("Refused.");
  await expect(mrow.getByTestId("ap-status")).toHaveText("Rejected");
  await expect(mrow.getByRole("button", { name: /^Approve/ })).toHaveCount(0);
  await manager.close();
});

test("Sales sees the request it filed with no decide; on a colleague's request the attempt is refused verbatim and that row's controls leave", async ({ page, request }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await fileAsSales(page);
  const own = approvals(page).getByTestId("ap-row").first();
  // The session carries the caller's own id, so the row the caller filed offers no decide.
  await expect(own.getByRole("button", { name: /^Approve/ })).toHaveCount(0);
  await expect(own.getByTestId("ap-own")).toBeVisible();
  await plant(request, 2, "other");
  await signIn(page);
  await openApprovals(page);
  const row = approvals(page).getByTestId("ap-row").filter({ hasNot: page.getByTestId("ap-own") }).first();
  // The contract carries no may_decide flag, so a colleague's pending row offers the controls.
  await expect(row.getByRole("button", { name: /^Approve/ })).toBeVisible();
  await row.getByRole("button", { name: /^Approve/ }).click();
  const said = "identity: deciding a margin floor request is for the branch's Sales manager, the General Manager or the Financial Controller";
  await expect(row.getByTestId("ap-row-error")).toHaveText(said);
  await expect(row.getByRole("button", { name: /^Approve/ })).toHaveCount(0);
  await expect(row.getByRole("button", { name: /^Refuse/ })).toHaveCount(0);
  await expect(approvals(page).getByTestId("ap-reason")).toHaveCount(0);
  await expect(row.getByTestId("ap-status")).toHaveText("Pending");
  await approvals(page).scrollIntoViewIfNeeded();
  await shot(page, "approvals-refused");
});

test("a decision on a request somebody else already decided is the server's 409, verbatim", async ({ page, browser }) => {
  await fileAsSales(page);
  const first = await asManager(browser);
  const second = await asManager(browser);
  await expect(approvals(second).getByTestId("ap-row")).toHaveCount(1);
  await approvals(first).getByTestId("ap-row").first().getByRole("button", { name: /^Approve/ }).click();
  await expect(approvals(first).getByTestId("ap-outcome")).toHaveText("Approved.");
  // The second list is stale and still offers the controls; the server says no.
  const stale = approvals(second).getByTestId("ap-row").first();
  await stale.getByRole("button", { name: /^Refuse/ }).click();
  await expect(stale.getByTestId("ap-row-error")).toHaveText("identity: this request has already been decided");
  // It re-read: the row now says Approved and offers nothing, and a 409 is not a refusal of the caller.
  await expect(stale.getByTestId("ap-status")).toHaveText("Approved");
  await expect(approvals(second).getByTestId("ap-refused")).toHaveCount(0);
  await first.close();
  await second.close();
});

test("a caller off the cost list: the refusal dialog carries no figures, the filer's own card keeps the server's detail (the core's qd1c)", async ({ page, request }) => {
  await request.put("/_stub/cost-list", { data: { member: false } });
  await fileAsSales(page);
  const row = approvals(page).getByTestId("ap-row").first();
  // The core builds the detail from its own figures and the filer reads it in the filer's own card.
  const detail = await row.getByTestId("ap-detail").innerText();
  expect(detail).toMatch(/^quote Q-1001 line 1: 10 percent discount, resulting margin [\d.]+% against the [\d.]+% floor$/);
  expect(detail, "no id in the sentence").not.toMatch(/[0-9a-f]{8}-[0-9a-f]{4}-/);
  // The refusal dialog carries no figures for a caller off the list.
  await paverRefusalHasNoFigures(page);
});

async function paverRefusalHasNoFigures(page: Page): Promise<void> {
  await openQuote(page, "Q-1001");
  const paver = edit(page).getByTestId("qe-line").first();
  await paver.locator('[data-field="disc"]').fill("12");
  await paver.getByRole("button", { name: "Save" }).click();
  await expect(dialog(page).getByTestId("fr-dialog")).toBeVisible();
  await expect(dialog(page).getByTestId("fr-figures")).toHaveCount(0);
  const text = await dialog(page).innerText();
  expect(text).not.toMatch(/margin [\d.-]+%|against a floor|cost/i);
  await dialog(page).getByTestId("fr-close").click();
}

// ── PR 121 round 1: the list against the core's own shape, and per row locks ──

async function plant(request: APIRequestContext, lineNo: number, flow: string): Promise<void> {
  const planted = await request.post("/_stub/plant-approval", { data: { quote_id: "q-1001", line_no: lineNo, requested_by_flow: flow } });
  expect(planted.ok()).toBe(true);
}

for (const shape of ["page", "bare", "paged"]) {
  test(`the list reads the ${shape} shape: every request is shown (the contract says a page, the core writes a bare array)`, async ({ browser, request }) => {
    await request.put("/_stub/approvals-shape", { data: { shape } });
    await plant(request, 1, "sales");
    await plant(request, 2, "sales");
    const manager = await asManager(browser);
    await expect(approvals(manager).getByTestId("ap-row")).toHaveCount(2);
    await expect(approvals(manager).getByTestId("ap-error")).toHaveCount(0);
    await expect(approvals(manager).getByTestId("ap-count")).toHaveText("2 pending");
    await manager.close();
  });
}

test("one 403 locks only its own row: the other rows keep their decide controls", async ({ page, request }) => {
  await plant(request, 1, "other");
  await plant(request, 2, "other");
  await signIn(page);
  await openApprovals(page);
  const rows = approvals(page).getByTestId("ap-row");
  await expect(rows).toHaveCount(2);
  await rows.nth(0).getByRole("button", { name: /^Approve/ }).click();
  await expect(rows.nth(0).getByTestId("ap-row-error")).toContainText("deciding a margin floor request");
  await expect(rows.nth(0).getByRole("button", { name: /^Approve/ })).toHaveCount(0);
  await expect(rows.nth(1).getByRole("button", { name: /^Approve/ })).toBeVisible();
  await expect(rows.nth(1).getByRole("button", { name: /^Refuse/ })).toBeVisible();
});

test("a request the caller filed offers no decide, a colleague's does", async ({ browser, request }) => {
  await plant(request, 1, "sales-manager");
  await plant(request, 2, "sales");
  const manager = await asManager(browser);
  const rows = approvals(manager).getByTestId("ap-row");
  await expect(rows).toHaveCount(2);
  await expect(rows.nth(0).getByRole("button", { name: /^Approve/ })).toHaveCount(0);
  await expect(rows.nth(0).getByTestId("ap-own")).toBeVisible();
  await expect(rows.nth(1).getByRole("button", { name: /^Approve/ })).toBeVisible();
  await manager.close();
});

test("a decision's reason does not carry to the next decision", async ({ browser, request }) => {
  await plant(request, 1, "sales");
  await plant(request, 2, "sales");
  const manager = await asManager(browser);
  const rows = approvals(manager).getByTestId("ap-row");
  await approvals(manager).getByTestId("ap-reason").fill("Long standing contractor, approve this one only");
  await rows.nth(0).getByRole("button", { name: /^Approve/ }).click();
  await expect(approvals(manager).getByTestId("ap-outcome")).toHaveText("Approved.");
  await expect(approvals(manager).getByTestId("ap-reason")).toHaveValue("");
  await rows.nth(1).getByRole("button", { name: /^Refuse/ }).click();
  await expect(rows.nth(1).getByTestId("ap-status")).toHaveText("Rejected");
  const ledger = (await (await request.get("/_stub/state")).json()).decisions as Array<{ approve: boolean; reason: string }>;
  expect(ledger).toHaveLength(2);
  expect(ledger[0]?.reason).toBe("Long standing contractor, approve this one only");
  expect(ledger[1]?.reason).toBe("");
  await manager.close();
});
