// The approvals queue in a real browser, against the stubbed harness: Sales
// files a margin floor request through a discount below the floor on the work
// area, the Sales manager opens the Approvals surface (its own page, decision
// log row 955) and sees the request with the server's own detail text,
// approves or refuses it with a reason and the row updates; a decision on an
// already decided request is the server's 409; and with the caller off the
// cost list the detail text carries no margin and no figure appears anywhere
// in the dialog. Sales has no Approvals surface: the 403 a Sales caller used
// to meet on a row is now met at the wall (surfaces.pw.ts).
import { expect, test, type APIRequestContext, type Browser, type Page } from "@playwright/test";
import { shot } from "./shot";
import { openDesk } from "./surface-tabs";

async function signIn(page: Page, role = "Sales"): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: role, exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
}

/** The Approvals surface, the Sales manager's own page (decision log row 955): the queue is no panel under a quote. */
async function openQuote(page: Page, text: string): Promise<void> {
  await page.getByTestId("qb-list-row").filter({ hasText: text }).first().click();
  await expect(page.getByTestId("qb-context")).toContainText(text);
}

async function openQueue(page: Page): Promise<void> {
  await openDesk(page, "approvals");
  await expect(page.getByTestId("ap-box")).toBeVisible();
}

const approvals = (page: Page) => page.locator("qd-approvals");
const dialog = (page: Page) => page.locator("qd-floor-refusal");

/** Sales asks for a discount below the floor on the first line of the draft (the work area's own box) and files the request through the dialog. */
async function fileAsSales(page: Page, disc = "10"): Promise<void> {
  await signIn(page);
  await openQuote(page, "Q-1001");
  const box = page.getByTestId("qb-line-disc").first();
  await box.fill(disc);
  await box.blur();
  await expect(dialog(page).getByTestId("fr-dialog")).toBeVisible();
  await dialog(page).getByTestId("fr-request").click();
  await expect(dialog(page).getByTestId("fr-request-state")).toContainText("pending");
  await dialog(page).getByTestId("fr-close").click();
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
  await openQueue(page);
  return page;
}

test("with nothing filed the list says nobody is held up", async ({ browser }) => {
  const manager = await asManager(browser);
  await expect(approvals(manager).getByTestId("ap-empty")).toHaveText("Nobody is held up.");
  await expect(approvals(manager).getByTestId("ap-row")).toHaveCount(0);
  await manager.close();
});

test("Sales files a request and the Sales manager sees it with the server's detail text and approves it", async ({ page, browser }) => {
  await fileAsSales(page);

  const manager = await asManager(browser);
  const mrow = approvals(manager).getByTestId("ap-row").first();
  await expect(mrow).toHaveAttribute("data-status", "PENDING");
  await expect(mrow.getByTestId("ap-detail")).toHaveText(/^Quote Q-1001, line 1, .*, 10% discount, margin [\d.]+% against a floor of [\d.]+%$/);
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
  // The refusal dialog carries no figures for a caller off the list.
  await paverRefusalHasNoFigures(page);
});

async function paverRefusalHasNoFigures(page: Page): Promise<void> {
  const box = page.getByTestId("qb-line-disc").first();
  await box.fill("12");
  await box.blur();
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
