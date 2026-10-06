// PR 121 round 2 fixes, the panels: a write that was out when another quote opened answers only for the quote it
// began on, and what it would have said never shows on the quote now open. Every test holds one answer back behind
// a gate it opens itself (a delayed route), so the order of events is exact on any machine.
import { expect, test, type Browser, type Page } from "@playwright/test";
import { openDesk, openTab } from "./surface-tabs";
import { gate, row, seed, settle, signIn as signInAs, writes } from "./switch-helpers.js";

// The Sales manager sees every quote in the list, so the probes can move between quotes of either owner.
const signIn = (page: Page, role = "Sales manager") => signInAs(page, role);

/** Open a quote on the surface its panel stands on: Admin (the edit panel), Activity (assist, follow ups) or Stock. */
async function openQuote(page: Page, quoteNo: string, tab: "admin" | "activity" | "stock" = "admin"): Promise<void> {
  await row(page, quoteNo).click();
  await openTab(page, tab);
  await expect(page.getByTestId("qe-title")).toContainText(quoteNo);
}

const edit = (page: Page) => page.locator("qd-quote-edit");
const assist = (page: Page) => page.locator("qd-quote-assist");
const hold = (page: Page) => page.locator("qd-quote-hold-expiry");
const followups = (page: Page) => page.locator("qd-quote-followups");

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

test("a line save with the quantity and the discount changed never sends the discount to the quote opened meanwhile", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-1001");
  const held = gate();
  await page.route(/\/api\/v1\/quotes\/q-1001\/lines\/1$/, async (route) => {
    await held.opened;
    await route.continue();
  });
  const seen = writes(page);
  const line = edit(page).getByTestId("qe-line").first();
  await line.locator('[data-field="qty"]').fill("6");
  await line.locator('[data-field="disc"]').fill("5");
  await line.getByRole("button", { name: "Save" }).click();
  await expect.poll(() => seen.filter((w) => w.startsWith("PATCH")).length).toBe(1);
  await row(page, "Q-1002").click();
  await expect(edit(page).getByTestId("qe-title")).toContainText("Q-1002");
  const answered = page.waitForResponse((r) => r.request().method() === "PATCH" && /\/quotes\/q-1001\/lines\/1$/.test(r.url()));
  held.open();
  await answered;
  await settle(page);
  expect(seen.filter((w) => w.startsWith("PATCH") && !w.includes("/quotes/q-1001/"))).toEqual([]);
  await expect(page.getByTestId("qe-notice")).toHaveText("");
  await expect(page.getByTestId("qe-error")).toHaveText("");
});

test("an add that lands after another quote opened does not clear that quote's add box", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  await openQuote(page, "Q-1001");
  await edit(page).locator('[data-field="add-search"]').fill("edge");
  await edit(page).getByRole("option").first().click();
  await edit(page).locator('[data-field="add-qty"]').fill("2");
  const held = gate();
  await page.route(/\/api\/v1\/quotes\/q-1001\/lines$/, async (route) => {
    if (route.request().method() === "POST") await held.opened;
    await route.continue();
  });
  const asked = page.waitForRequest((r) => r.method() === "POST" && r.url().endsWith("/quotes/q-1001/lines"));
  await edit(page).getByRole("button", { name: /^Add/ }).click();
  await asked;
  await row(page, "Q-1004").click();
  await expect(edit(page).getByTestId("qe-title")).toContainText("Q-1004");
  await edit(page).locator('[data-field="add-qty"]').fill("7");
  await edit(page).locator('[data-field="add-search"]').fill("sand");
  const answered = page.waitForResponse((r) => r.request().method() === "POST" && r.url().endsWith("/quotes/q-1001/lines"));
  held.open();
  await answered;
  await settle(page);
  await expect(edit(page).locator('[data-field="add-qty"]')).toHaveValue("7");
  await expect(edit(page).locator('[data-field="add-search"]')).toHaveValue("sand");
  await expect(page.getByTestId("qe-notice")).toHaveText("");
});

test("a link minted for one quote does not appear on the next quote opened", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-1001");
  const held = gate();
  await page.route(/\/api\/v1\/quotes\/q-1001\/share-links$/, async (route) => {
    if (route.request().method() === "POST") await held.opened;
    await route.continue();
  });
  const asked = page.waitForRequest((r) => r.method() === "POST" && r.url().endsWith("/quotes/q-1001/share-links"));
  await edit(page).getByRole("button", { name: "Create link to copy" }).click();
  await asked;
  await row(page, "Q-1002").click();
  await expect(edit(page).getByTestId("qe-title")).toContainText("Q-1002");
  const answered = page.waitForResponse((r) => r.request().method() === "POST" && r.url().endsWith("/quotes/q-1001/share-links"));
  held.open();
  await answered;
  await settle(page);
  await expect(edit(page).locator('[data-field="link-url"]')).toHaveCount(0);
  await expect(page.getByTestId("qe-notice")).toHaveText("");
});

test("the assist panel recovers after a quote switch during a read", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-1001", "activity");
  const held = gate();
  await page.route(/\/api\/v1\/quote-assist\/extract$/, async (route) => {
    await held.opened;
    await route.continue();
  });
  const asked = page.waitForRequest((r) => r.url().endsWith("/quote-assist/extract"));
  await assist(page).locator("#jobtext").fill("3 yd clear stone");
  await assist(page).getByRole("button", { name: "Suggest lines from this text" }).click();
  await asked;
  await row(page, "Q-1003").click();
  await expect(edit(page).getByTestId("qe-title")).toContainText("Q-1003");
  const answered = page.waitForResponse((r) => r.url().endsWith("/quote-assist/extract"));
  held.open();
  await answered;
  await settle(page);
  await assist(page).locator("#jobtext").fill("20 sf holland stone");
  const button = assist(page).getByRole("button", { name: "Suggest lines from this text" });
  await expect(button).toBeEnabled();
  await button.click();
  await expect(assist(page).getByTestId("qa-proposal").first()).toBeVisible();
});

test("a confirm loop cut short by a quote switch says how far it got, and writes nothing to the new quote", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-1001", "activity");
  await assist(page).locator("#jobtext").fill("3 yd clear stone, 20 sf holland stone");
  await assist(page).getByRole("button", { name: "Suggest lines from this text" }).click();
  const proposals = assist(page).getByTestId("qa-proposal");
  await expect(proposals).toHaveCount(2);
  for (let i = 0; i < 2; i += 1) await proposals.nth(i).locator('[data-field="tick"]').check();
  const held = gate();
  let posts = 0;
  await page.route(/\/api\/v1\/quotes\/q-1001\/lines$/, async (route) => {
    if (route.request().method() === "POST") {
      posts += 1;
      if (posts === 1) await held.opened;
    }
    await route.continue();
  });
  const seen = writes(page);
  await assist(page).getByTestId("qa-confirm").click();
  await expect.poll(() => posts).toBe(1);
  await row(page, "Q-1003").click();
  await expect(edit(page).getByTestId("qe-title")).toContainText("Q-1003");
  const answered = page.waitForResponse((r) => r.request().method() === "POST" && r.url().endsWith("/quotes/q-1001/lines"));
  held.open();
  await answered;
  await settle(page);
  expect(seen.filter((w) => w.includes("/quotes/q-1003/"))).toEqual([]);
  await expect(assist(page).getByTestId("qa-add-note")).toContainText("moved to another quote");
  // The one add that was out landed on the quote left: the note counts it, and the line is there on return.
  await expect(assist(page).getByTestId("qa-add-note")).toContainText("1 of the 2 selected lines were added to the quote you left");
  await row(page, "Q-1001").click();
  await expect(edit(page).getByTestId("qe-title")).toContainText("Q-1001");
  await expect(edit(page).getByTestId("qe-line")).toHaveCount(3);
});

test("a re-hold answer that lands after another quote opened says nothing on that quote", async ({ page, request }) => {
  await request.post("/_stub/hold-lapse", { data: { quote_id: "q-1001", line_no: 2 } });
  await signIn(page);
  await openQuote(page, "Q-1001", "stock");
  const held = gate();
  await page.route(/\/api\/v1\/quotes\/q-1001\/holds\/resync$/, async (route) => {
    await held.opened;
    await route.continue();
  });
  const asked = page.waitForRequest((r) => r.url().endsWith("/quotes/q-1001/holds/resync"));
  await hold(page).getByRole("button", { name: "Re-hold these lines" }).click();
  await asked;
  await row(page, "Q-1003").click();
  await expect(edit(page).getByTestId("qe-title")).toContainText("Q-1003");
  const answered = page.waitForResponse((r) => r.url().endsWith("/quotes/q-1001/holds/resync"));
  held.open();
  await answered;
  await settle(page);
  await expect(hold(page).getByTestId("he-notice")).toHaveCount(0);
  await expect(hold(page).getByTestId("he-box")).toHaveCount(0);
});

test("a follow-ups stop that lands after another quote opened says nothing and lays no plan on that quote", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-1002", "activity");
  const held = gate();
  await page.route(/\/api\/v1\/quotes\/q-1002\/followups\/disable$/, async (route) => {
    await held.opened;
    await route.continue();
  });
  const asked = page.waitForRequest((r) => r.url().endsWith("/quotes/q-1002/followups/disable"));
  await followups(page).getByRole("button", { name: "Stop follow-ups" }).click();
  await asked;
  await row(page, "Q-1001").click();
  await expect(edit(page).getByTestId("qe-title")).toContainText("Q-1001");
  const answered = page.waitForResponse((r) => r.url().endsWith("/quotes/q-1002/followups/disable"));
  held.open();
  await answered;
  await settle(page);
  await expect(followups(page).getByTestId("fu-notice")).toHaveCount(0);
  await expect(followups(page).getByRole("button", { name: "Restart follow-ups" })).toHaveCount(0);
});

test("a text share answer that lands after another quote opened says nothing on that quote", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-1002", "activity");
  const held = gate();
  await page.route(/\/api\/v1\/quotes\/q-1002\/sms-share$/, async (route) => {
    await held.opened;
    await route.continue();
  });
  const asked = page.waitForRequest((r) => r.url().endsWith("/quotes/q-1002/sms-share"));
  await followups(page).getByRole("button", { name: "Text the quote link" }).click();
  await asked;
  await row(page, "Q-1001").click();
  await expect(edit(page).getByTestId("qe-title")).toContainText("Q-1001");
  const answered = page.waitForResponse((r) => r.url().endsWith("/quotes/q-1002/sms-share"));
  held.open();
  await answered;
  await settle(page);
  await expect(followups(page).getByTestId("fu-error")).toHaveCount(0);
  await expect(followups(page).getByTestId("fu-notice")).toHaveCount(0);
});

test("the availability panel drops a stale error when another quote opens", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-1003", "stock");
  const held = gate();
  await page.route(/\/api\/v1\/quote-availability\/[^/]+\/branches$/, async (route) => {
    await held.opened;
    await route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: "quote: the branch stock read is down" }) });
  });
  const panel = page.locator("qd-quote-availability");
  const asked = page.waitForRequest((r) => /\/quote-availability\/[^/]+\/branches$/.test(r.url()));
  await panel.getByRole("button", { name: "Other branches" }).first().click();
  await asked;
  await row(page, "Q-1001").click();
  await expect(edit(page).getByTestId("qe-title")).toContainText("Q-1001");
  held.open();
  await settle(page);
  await expect(panel.getByTestId("av-error")).toHaveCount(0);
});

async function plantAndOpenAsManager(browser: Browser, page: Page): Promise<Page> {
  // Two requests, so a box that clears is told apart from a box that left with its row.
  for (const lineNo of [1, 2]) await page.request.post("/_stub/plant-approval", { data: { quote_id: "q-1001", line_no: lineNo, disc_pct: 10, requested_by_flow: "sales" } });
  const manager = await (await browser.newContext({ viewport: { width: 1440, height: 900 } })).newPage();
  await signIn(manager, "Sales manager");
  await openDesk(manager, "approvals");
  await expect(manager.locator("qd-approvals").getByTestId("ap-row")).toHaveCount(2);
  return manager;
}

test("a typed approval reason survives a 503, and clears on a 409, on a 403 and on success", async ({ page, browser }) => {
  const manager = await plantAndOpenAsManager(browser, page);
  const reason = manager.locator("qd-approvals").getByTestId("ap-reason");
  const statuses = [503, 409, 403];
  await manager.route(/\/api\/v1\/staff\/approvals\/[^/]+\/decide$/, async (route) => {
    const status = statuses.shift();
    if (status === undefined) {
      await route.continue();
      return;
    }
    await route.fulfill({ status, contentType: "application/json", body: JSON.stringify({ error: `identity: refused with ${status}` }) });
  });
  const approve = () => manager.locator("qd-approvals").getByTestId("ap-approve").first().click();
  await reason.fill("Customer is a repeat buyer");
  await approve();
  await expect(manager.locator("qd-approvals").getByTestId("ap-row-error")).toContainText("503");
  await expect(reason).toHaveValue("Customer is a repeat buyer");
  // A clash with another decision ends this try on the row: the reason typed for it is not left for the next row.
  await approve();
  await expect(manager.locator("qd-approvals").getByTestId("ap-row-error")).toContainText("409");
  await expect(reason).toHaveValue("");
  await reason.fill("Customer is a repeat buyer");
  await approve();
  await expect(manager.locator("qd-approvals").getByTestId("ap-row-error")).toContainText("403");
  await expect(reason).toHaveValue("");
  await manager.context().close();
});

test("a typed approval reason clears once the decision is made", async ({ page, browser }) => {
  const manager = await plantAndOpenAsManager(browser, page);
  const reason = manager.locator("qd-approvals").getByTestId("ap-reason");
  await reason.fill("Customer is a repeat buyer");
  await manager.locator("qd-approvals").getByTestId("ap-approve").first().click();
  await expect(manager.locator("qd-approvals").getByTestId("ap-outcome")).toHaveText("Approved.");
  await expect(manager.locator("qd-approvals").getByTestId("ap-approve")).toHaveCount(1);
  await expect(reason).toHaveValue("");
  await manager.context().close();
});

test("an approvals read that answers late never overwrites the newer read, and a failed read does not keep the old rows", async ({ page, browser }) => {
  const manager = await plantAndOpenAsManager(browser, page);
  const panel = manager.locator("qd-approvals");
  const first = gate();
  let reads = 0;
  await manager.route(/\/api\/v1\/staff\/approvals(\?.*)?$/, async (route) => {
    reads += 1;
    if (reads === 1) {
      await first.opened;
      await route.continue();
      return;
    }
    if (reads === 2) {
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ items: [] }) });
      return;
    }
    if (reads === 3) {
      await route.continue();
      return;
    }
    await route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: "identity: the approvals store is down" }) });
  });
  await panel.evaluate((el) => {
    (el as unknown as { reload: () => void }).reload();
    (el as unknown as { reload: () => void }).reload();
  });
  await expect(panel.getByTestId("ap-empty")).toBeVisible();
  const late = manager.waitForResponse((r) => r.url().includes("/staff/approvals"));
  first.open();
  await late;
  await settle(manager);
  await expect(panel.getByTestId("ap-row")).toHaveCount(0);
  await expect(panel.getByTestId("ap-empty")).toBeVisible();
  // A read that lands brings the rows back; one that then fails shows the failure and not the rows of the read before it.
  await panel.evaluate((el) => (el as unknown as { reload: () => void }).reload());
  await expect(panel.getByTestId("ap-row")).toHaveCount(2);
  await panel.evaluate((el) => (el as unknown as { reload: () => void }).reload());
  await expect(panel.getByTestId("ap-error")).toBeVisible();
  // A failed read says nothing of how many are pending: no "0 pending" beside the failure.
  await expect(panel.getByTestId("ap-count")).toHaveCount(0);
  await expect(panel.getByTestId("ap-row")).toHaveCount(0);
  await manager.context().close();
});
