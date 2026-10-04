// The approvals list in a real browser, against the stubbed harness: Sales
// files a margin floor request through a discount below the floor, the Sales
// manager signs in and sees the request with the server's own detail text,
// approves or refuses it with a reason and the row updates; Sales sees the
// request but a decision attempt is refused in the server's words, verbatim,
// and the decide controls leave the list; a decision on an already decided
// request is the server's 409; and with the caller off the cost list the
// detail text carries no margin and no figure appears anywhere in the panels.
import { expect, test, type Browser, type Page } from "@playwright/test";

const SHOTS = "/home/colton/Desktop/FBHQ/hh-rd2/scratch-app";

async function signIn(page: Page, role = "Sales"): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: role, exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
}

async function openQuote(page: Page, text: string): Promise<void> {
  await page.getByTestId("qb-list-row").filter({ hasText: text }).first().click();
  const detail = page.getByTestId("qb-detail");
  if ((await detail.getAttribute("open")) === null) await page.getByText("Quote detail & admin").click();
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
  await openQuote(page, "Q-1001");
  return page;
}

test("with nothing filed the list says nobody is held up", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-1001");
  await expect(approvals(page).getByTestId("ap-empty")).toHaveText("Nobody is held up.");
  await expect(approvals(page).getByTestId("ap-row")).toHaveCount(0);
});

test("Sales files a request and the Sales manager sees it with the server's detail text and approves it", async ({ page, browser }) => {
  await fileAsSales(page);
  const row = approvals(page).getByTestId("ap-row").first();
  await expect(row).toHaveAttribute("data-status", "PENDING");
  const sales = await row.getByTestId("ap-detail").innerText();
  expect(sales).toMatch(/^Quote Q-1001, line 1, .*, 10% discount, margin [\d.]+% against a floor of [\d.]+%$/);

  const manager = await asManager(browser);
  const mrow = approvals(manager).getByTestId("ap-row").first();
  await expect(mrow.getByTestId("ap-detail")).toHaveText(sales);
  await expect(mrow.getByTestId("ap-status")).toHaveText("Pending");
  await expect(approvals(manager).getByTestId("ap-count")).toHaveText("1 pending");
  await approvals(manager).scrollIntoViewIfNeeded();
  await manager.screenshot({ path: `${SHOTS}/approvals-pending.png` });
  await approvals(manager).getByTestId("ap-reason").fill("Contractor account, fine");
  await mrow.getByRole("button", { name: /^Approve/ }).click();
  await expect(approvals(manager).getByTestId("ap-outcome")).toHaveText("Approved.");
  await expect(mrow.getByTestId("ap-status")).toHaveText("Approved");
  await expect(mrow.getByRole("button", { name: /^Approve/ })).toHaveCount(0);
  await expect(mrow.getByRole("button", { name: /^Refuse/ })).toHaveCount(0);
  await expect(approvals(manager).getByTestId("ap-count")).toHaveText("0 pending");
  await manager.screenshot({ path: `${SHOTS}/approvals-approved.png` });

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

test("Sales sees the request, the decision attempt is refused verbatim and the controls leave", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await fileAsSales(page);
  const row = approvals(page).getByTestId("ap-row").first();
  // The contract carries no may_decide flag, so a pending row offers the controls.
  await expect(row.getByRole("button", { name: /^Approve/ })).toBeVisible();
  await row.getByRole("button", { name: /^Approve/ }).click();
  const said = "identity: deciding a margin floor request is for the branch's Sales manager, the General Manager or the Financial Controller";
  await expect(row.getByTestId("ap-row-error")).toHaveText(said);
  await expect(approvals(page).getByTestId("ap-refused")).toHaveText(said);
  await expect(row.getByRole("button", { name: /^Approve/ })).toHaveCount(0);
  await expect(row.getByRole("button", { name: /^Refuse/ })).toHaveCount(0);
  await expect(approvals(page).getByTestId("ap-reason")).toHaveCount(0);
  await expect(row.getByTestId("ap-status")).toHaveText("Pending");
  // The refusal stands across a re-read.
  await page.getByRole("button", { name: "Refresh" }).first().click().catch(() => undefined);
  await expect(row.getByRole("button", { name: /^Approve/ })).toHaveCount(0);
  await approvals(page).scrollIntoViewIfNeeded();
  await page.screenshot({ path: `${SHOTS}/approvals-refused.png` });
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

test("a caller off the cost list reads no margin anywhere in the panels", async ({ page, request }) => {
  await request.put("/_stub/cost-list", { data: { member: false } });
  await fileAsSales(page);
  const row = approvals(page).getByTestId("ap-row").first();
  const detail = await row.getByTestId("ap-detail").innerText();
  expect(detail).toMatch(/^Quote Q-1001, line 1, .*, 10% discount$/);
  expect(detail).not.toMatch(/margin|floor of/i);
  // The refusal dialog carried no figures either, and the list shows none.
  await paverRefusalHasNoFigures(page);
  const text = await approvals(page).innerText();
  expect(text).not.toMatch(/margin [\d.-]+%|against a floor|cost/i);
});

async function paverRefusalHasNoFigures(page: Page): Promise<void> {
  const paver = edit(page).getByTestId("qe-line").first();
  await paver.locator('[data-field="disc"]').fill("12");
  await paver.getByRole("button", { name: "Save" }).click();
  await expect(dialog(page).getByTestId("fr-dialog")).toBeVisible();
  await expect(dialog(page).getByTestId("fr-figures")).toHaveCount(0);
  const text = await dialog(page).innerText();
  expect(text).not.toMatch(/margin [\d.-]+%|against a floor|cost/i);
  await dialog(page).getByTestId("fr-close").click();
}
