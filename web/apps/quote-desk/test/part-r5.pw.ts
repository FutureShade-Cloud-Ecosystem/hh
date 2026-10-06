// Round 5: the principal's two P2s and the folded P3s (decision row 783). Each test holds an answer back behind a
// gate, so the order of events is exact and never depends on the machine's speed.
import { expect, test, type Page } from "@playwright/test";
import { hold, open, quiet, row, seed, settle, signIn, writes, showList } from "./switch-helpers.js";

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

async function openQuoteDetail(page: Page, quoteNo: string): Promise<void> {
  await showList(page);
  await row(page, quoteNo).click();
  const detail = page.getByTestId("qb-detail");
  if ((await detail.getAttribute("open")) === null) await page.getByText("Quote detail & admin").click();
  await expect(page.getByTestId("qe-title")).toContainText(quoteNo);
}

async function newQuote(page: Page): Promise<void> {
  await page.getByRole("button", { name: "New Quote" }).click();
  await expect(page.getByTestId("qb-title")).toHaveText("New Quote");
}

async function stubState(page: Page): Promise<{ workspaceWrites: { user_id: string; body: unknown }[] }> {
  const response = await page.request.get("/_stub/state");
  return (await response.json()) as { workspaceWrites: { user_id: string; body: unknown }[] };
}

test("P2-1 a queued layout save is never sent under the next person's session, and their desk resumes their own quote", async ({ page }) => {
  await signIn(page, "Sales");
  await open(page, "Q-1001");
  await quiet(page);
  // The resume reference save of the quote just opened is held on its way out; the next open queues another behind it.
  const first = await hold(page, /\/surfaces\/quotes\.builder\/my-workspace$/, { method: "PUT", times: 1 });
  await open(page, "Q-1003");
  await first.asked;
  await open(page, "Q-1001");
  // Sign out cannot wait for the held save: the bounded wait gives up and the session ends with the save still out.
  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toBeVisible({ timeout: 10000 });
  await page.getByRole("button", { name: "Sales manager", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
  await quiet(page);
  const before = (await stubState(page)).workspaceWrites.filter((w) => w.user_id === "demo-account-sales-manager");
  // The held save answers under the person it was sent for: the queued one behind it must never go out as the manager.
  await first.release();
  await settle(page);
  const after = (await stubState(page)).workspaceWrites.filter((w) => w.user_id === "demo-account-sales-manager");
  expect(after).toEqual(before);
  expect(after).toHaveLength(0);
  // The manager's own desk is what their own row says: Sales' last quote is not resumed for them.
  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toBeVisible({ timeout: 10000 });
  await page.getByRole("button", { name: "Sales manager", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
  await expect(page.getByTestId("qb-context")).not.toContainText("Q-10");
});

test("P3-1 one terms PUT per quote at a time: a whole row PUT from an older edit cannot erase a later edit", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  await open(page, "Q-1004");
  await expect(page.getByTestId("qb-po")).toBeVisible();
  const put1 = await hold(page, /\/quotes\/q-1004\/terms$/, { method: "PUT", times: 1 });
  await page.getByTestId("qb-po").fill("PO-FIRST");
  await put1.asked;
  // A second edit of the same row is made while the first save is still out.
  const put2 = await hold(page, /\/quotes\/q-1004\/terms$/, { method: "PUT", times: 1 });
  await page.getByTestId("qb-contact").fill("Site contact");
  // The edit's own save has now been asked for by the debounce, whether it went out beside the first (unguarded) or
  // waits behind it (the queue): past the debounce with nothing asked, it is waiting its turn.
  await Promise.race([put2.asked, page.waitForTimeout(1500)]);
  // The later save's gate is opened first, so if it were out beside the older one the older row would land last and
  // erase the later edit; sent in order, the later row is the one that lands last.
  await put2.release();
  await put1.release();
  await settle(page);
  // Reopening the quote shows what the server holds: both edits stand.
  await open(page, "Q-1001");
  await open(page, "Q-1004");
  await expect(page.getByTestId("qb-po")).toHaveValue("PO-FIRST");
  await expect(page.getByTestId("qb-contact")).toHaveValue("Site contact");
});

const followups = (page: Page) => page.locator("qd-quote-followups");

test("P2-2 leaving through New Quote: a follow-ups stop that lands while its panel is gone shows OFF on return", async ({ page }) => {
  await signIn(page, "Sales manager");
  await openQuoteDetail(page, "Q-1002");
  await expect(followups(page).getByTestId("fu-summary")).toContainText("Follow-ups are on");
  const disable = await hold(page, /\/quotes\/q-1002\/followups\/disable$/, { method: "POST", times: 1 });
  await followups(page).getByRole("button", { name: "Stop follow-ups" }).click();
  await disable.asked;
  await newQuote(page);
  // The quote is opened again while the stop is still out: the panel on screen is not the one that made the stop.
  await openQuoteDetail(page, "Q-1002");
  await expect(followups(page).getByTestId("fu-summary")).toContainText("Follow-ups are on");
  await disable.release();
  await expect(followups(page).getByTestId("fu-summary")).toContainText("Follow-ups are OFF");
});

test("P2-2 leaving through New Quote: a quantity that lands while its panel is gone shows the server's value and says the discount half", async ({ page }) => {
  await signIn(page, "Sales manager");
  await openQuoteDetail(page, "Q-1001");
  const line = page.locator("qd-quote-edit").getByTestId("qe-line").first();
  await line.locator('[data-field="qty"]').fill("3");
  await line.locator('[data-field="disc"]').fill("4");
  const patch = await hold(page, /\/quotes\/q-1001\/lines\/1$/, { method: "PATCH", times: 1, snapshot: true });
  await line.getByRole("button", { name: "Save" }).click();
  await patch.asked;
  await newQuote(page);
  await patch.release();
  await settle(page);
  await openQuoteDetail(page, "Q-1001");
  await expect(page.locator("qd-quote-edit").getByTestId("qe-line").first().locator('[data-field="qty"]')).toHaveValue("3");
  await expect(page.getByTestId("qe-notice")).toContainText("the discount you also changed was not sent");
});

test("P2-2 leaving through New Quote: a link minted while its panel was gone is said on return", async ({ page }) => {
  await signIn(page, "Sales manager");
  await openQuoteDetail(page, "Q-1001");
  const mint = await hold(page, /\/quotes\/q-1001\/share-links$/, { method: "POST", times: 1 });
  await page.locator("qd-quote-edit").getByRole("button", { name: "Create link to copy" }).click();
  await mint.asked;
  await newQuote(page);
  await mint.release();
  await settle(page);
  await openQuoteDetail(page, "Q-1001");
  await expect(page.getByTestId("qe-notice")).toContainText("A share link was made on this quote while you were away");
});

async function announce(page: Page, detail: Record<string, string>): Promise<void> {
  await page.locator("qd-quote-workarea").evaluate((el, d) => {
    el.shadowRoot?.dispatchEvent(new CustomEvent("quote-changed", { detail: d, bubbles: true, composed: true }));
  }, detail);
}

test("P3-3 the builder reads the changed quote's own id: another quote's change re-reads the list, not the open quote", async ({ page }) => {
  await signIn(page, "Sales manager");
  await open(page, "Q-1001");
  await quiet(page);
  const openQuoteReads: string[] = [];
  let listReads = 0;
  page.on("request", (request) => {
    if (/\/api\/v1\/quotes\/q-1001/.test(request.url())) openQuoteReads.push(request.url());
    if (request.method() === "GET" && /\/api\/v1\/quotes(\?|$)/.test(request.url())) listReads += 1;
  });
  // The announcement names another quote; the decoy field carries the open one, so a builder reading any other
  // field, or none and reloading every view regardless, would re-read the open quote's screens.
  await announce(page, { quoteId: "q-1003", ref: "q-1001" });
  await settle(page);
  expect(openQuoteReads).toEqual([]);
  expect(listReads).toBeGreaterThan(0);
  // An announcement naming the open quote does re-read its screens, so the id is what decides.
  await announce(page, { quoteId: "q-1001", ref: "q-1003" });
  await settle(page);
  expect(openQuoteReads.length).toBeGreaterThan(0);
});



