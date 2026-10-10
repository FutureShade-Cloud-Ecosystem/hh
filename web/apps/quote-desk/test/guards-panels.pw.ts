// PR 121 round 3: one deterministic test for each guard of the panels (edit, history, assist, availability,
// follow-ups, hold expiry, the policy check, the floor dialog, approvals, settings). Each holds one answer back
// behind a delayed route, moves to another quote (or scope, or branch), lets the answer go and asserts none of it
// shows. Take the guard away (test/mutants.json names the exact edit) and the test goes red.
import { expect, test, type Page } from "@playwright/test";
import { armedHold, countRequests, hold, quiet, row, seed, settle, signIn as signInAs, stubBrowserChannels, watchEvent, writes, showList } from "./switch-helpers.js";

/** The compose page under the split-list navigation: the New quote button walks the desk to #/quotes/new. */
async function newQuote(page: Page): Promise<void> {
  await page.getByRole("button", { name: "New quote", exact: true }).click();
  await expect(page.getByTestId("qb-identity")).toBeVisible();
}

const signIn = (page: Page, role = "Sales manager") => signInAs(page, role);

async function openDetail(page: Page): Promise<void> {
  const detail = page.getByTestId("qb-detail");
  if ((await detail.getAttribute("open")) === null) await page.getByText("Quote detail & admin").click();
  await expect(page.getByTestId("qe-box")).toBeVisible();
}

async function openQuote(page: Page, quoteNo: string): Promise<void> {
  await showList(page);
  await row(page, quoteNo).click();
  await openDetail(page);
  await expect(page.getByTestId("qe-title")).toContainText(quoteNo);
}

const edit = (page: Page) => page.locator("qd-quote-edit");
const reloadOf = (page: Page, selector: string) => page.locator(selector).evaluate((el) => (el as unknown as { reload: () => void }).reload());

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

// ── the edit panel ──

test("guard edit read: the quote read for the quote left never shows on the quote now open", async ({ page }) => {
  await signIn(page);
  await showList(page);
  await row(page, "Q-1002").click();
  await openDetail(page);
  // The work area reads the quote first and the panels after it: the work area's read passes, every read after it is held.
  let reads = 0;
  const slow = await hold(page, /\/api\/v1\/quotes\/q-1001$/, { method: "GET", match: () => (reads += 1) > 1 });
  await showList(page);
  await row(page, "Q-1001").click();
  await slow.asked;
  await openQuote(page, "Q-1003");
  await slow.release();
  await settle(page);
  await expect(edit(page).getByTestId("qe-title")).toContainText("Q-1003");
  await expect(edit(page).locator('[data-field="qty"]')).toHaveCount(0);
});

test("guard edit read order: an older read of the quote answered late does not undo a newer one", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-1001");
  await expect(edit(page).getByTestId("qe-line")).toHaveCount(2);
  await quiet(page);
  const gated = await armedHold(page, /\/api\/v1\/quotes\/q-1001$/, "GET", { snapshot: true });
  gated.arm();
  await reloadOf(page, "qd-quote-edit");
  await gated.held.asked;
  gated.disarm();
  // A line is added on the work area: the fan-out reads the quote again and the edit panel shows three lines.
  const scan = page.getByRole("combobox", { name: "Search products by name, code or category" });
  await scan.fill("EDG-08");
  await scan.press("Enter");
  await expect(edit(page).getByTestId("qe-line")).toHaveCount(3);
  await quiet(page);
  await gated.held.release();
  await settle(page);
  await expect(edit(page).getByTestId("qe-line")).toHaveCount(3);
});

test("guard edit discounts read: the discounts of the quote left never show on the quote now open", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick", lines: [{ p: "p-holland-60", q: 3 }, { p: "p-polymer-sand", q: 2 }] });
  await signIn(page);
  await openQuote(page, "Q-1001");
  // Five percent on the sand line of Q-1001, so its terms read carries a discount that Q-1004 does not have.
  const disc = page.getByTestId("qb-line-disc").nth(1);
  await disc.fill("5");
  const landed = page.waitForResponse((r) => r.request().method() !== "GET" && r.url().includes("/api/v1/quotes/"));
  await disc.blur();
  await landed;
  await expect(page.getByTestId("qb-totals")).toContainText("Subtotal");
  await openQuote(page, "Q-1004");
  await quiet(page);
  // Both the work area's and the edit panel's terms reads of Q-1001 are held; the person leaves once both are out.
  const held = await hold(page, /\/api\/v1\/quotes\/q-1001\/terms$/, { method: "GET" });
  await showList(page);
  await row(page, "Q-1001").click();
  await expect.poll(() => held.seen.length).toBeGreaterThanOrEqual(2);
  await openQuote(page, "Q-1004");
  await expect(edit(page).getByTestId("qe-line")).toHaveCount(2);
  await held.release();
  await settle(page);
  await expect(edit(page).getByTestId("qe-line").nth(1).locator('[data-field="disc"]')).toHaveValue("");
});

test("guard edit discounts order: an older terms read answered late does not undo a newer one", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-1001");
  const disc = page.getByTestId("qb-line-disc").nth(1);
  await disc.fill("5");
  await disc.blur();
  await expect(edit(page).getByTestId("qe-line").nth(1).locator('[data-field="disc"]')).toHaveValue("5");
  await quiet(page);
  const gated = await armedHold(page, /\/api\/v1\/quotes\/q-1001\/terms$/, "GET", { snapshot: true, times: 1 });
  gated.arm();
  await reloadOf(page, "qd-quote-edit");
  await gated.held.asked;
  gated.disarm();
  await disc.fill("7");
  await disc.blur();
  await expect(edit(page).getByTestId("qe-line").nth(1).locator('[data-field="disc"]')).toHaveValue("7");
  await quiet(page);
  await gated.held.release();
  await settle(page);
  await expect(edit(page).getByTestId("qe-line").nth(1).locator('[data-field="disc"]')).toHaveValue("7");
});

test("guard edit links read: the links of the quote left never show on the quote now open", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-1001");
  await edit(page).getByRole("button", { name: "Create link to copy" }).click();
  await expect(edit(page).getByTestId("qe-link")).toHaveCount(1);
  await openQuote(page, "Q-1002");
  const before = await edit(page).getByTestId("qe-link").count();
  await quiet(page);
  const gated = await armedHold(page, /\/api\/v1\/quotes\/q-1001\/share-links$/, "GET");
  gated.arm();
  await showList(page);
  await row(page, "Q-1001").click();
  await gated.held.asked;
  gated.disarm();
  await openQuote(page, "Q-1002");
  await gated.held.release();
  await settle(page);
  await expect(edit(page).getByTestId("qe-link")).toHaveCount(before);
});

test("guard edit links order: an older links read answered late does not undo a newer one", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-1001");
  await expect(edit(page).getByTestId("qe-link")).toHaveCount(0);
  await quiet(page);
  const gated = await armedHold(page, /\/api\/v1\/quotes\/q-1001\/share-links$/, "GET", { snapshot: true, times: 1 });
  gated.arm();
  await reloadOf(page, "qd-quote-edit");
  await gated.held.asked;
  gated.disarm();
  await edit(page).getByRole("button", { name: "Create link to copy" }).click();
  await expect(edit(page).getByTestId("qe-link")).toHaveCount(1);
  await quiet(page);
  await gated.held.release();
  await settle(page);
  await expect(edit(page).getByTestId("qe-link")).toHaveCount(1);
});

test("guard edit write answer: a refused write that answers after another quote opened shows no error there", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-1001");
  const patch = await hold(page, /\/api\/v1\/quotes\/q-1001\/lines\/2$/, { method: "PATCH", refuse: { status: 500, error: "quote: the line store is down" } });
  const sand = edit(page).getByTestId("qe-line").nth(1);
  await sand.locator('[data-field="qty"]').fill("6");
  await sand.getByRole("button", { name: "Save" }).click();
  await patch.asked;
  await openQuote(page, "Q-1003");
  await patch.release();
  await settle(page);
  await expect(page.getByTestId("qe-error")).toHaveText("");
});

test("guard edit write reload: a save whose re-read answers after another quote opened says nothing and sends nothing more", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-1001");
  const seen = writes(page);
  await quiet(page);
  const gated = await armedHold(page, /\/api\/v1\/quotes\/q-1001$/, "GET");
  const sand = edit(page).getByTestId("qe-line").nth(1);
  await sand.locator('[data-field="qty"]').fill("6");
  await sand.locator('[data-field="disc"]').fill("5");
  gated.arm();
  await sand.getByRole("button", { name: "Save" }).click();
  await gated.held.asked;
  gated.disarm();
  await openQuote(page, "Q-1003");
  const changed = await watchEvent(page, "quote-changed");
  await gated.held.release();
  await settle(page);
  // The quantity went; the discount, which waited behind it, was never sent.
  expect(seen.filter((w) => w.startsWith("PATCH"))).toHaveLength(1);
  await expect(page.getByTestId("qe-notice")).toHaveText("");
  expect(await changed()).toBe(0);
});

test("guard edit search: a search answered for an older query never replaces the newer one", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-1001");
  const older = await hold(page, /\/api\/v1\/products\/search/, { method: "GET", times: 1, match: (request) => request.url().includes("q=sand") });
  const box = edit(page).locator('[data-field="add-search"]');
  await box.fill("sand");
  await older.asked;
  await box.fill("edge");
  await expect(edit(page).getByRole("option").first()).toContainText("Edge Restraint");
  await quiet(page);
  await older.release();
  await settle(page);
  await expect(edit(page).getByRole("option").first()).toContainText("Edge Restraint");
  await expect(edit(page).getByRole("option").filter({ hasText: "Polymeric" })).toHaveCount(0);
});

test("guard edit mint answer: a link minted for the quote left shows nowhere on the quote now open", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-1001");
  const post = await hold(page, /\/api\/v1\/quotes\/q-1001\/share-links$/, { method: "POST" });
  await edit(page).getByRole("button", { name: "Create link to copy" }).click();
  await post.asked;
  await openQuote(page, "Q-1002");
  await post.release();
  await settle(page);
  await expect(edit(page).locator('[data-field="link-url"]')).toHaveCount(0);
  await expect(page.getByTestId("qe-notice")).toHaveText("");
});

test("guard edit mint reload: a mint whose re-read answers after another quote opened announces nothing", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-1001");
  await quiet(page);
  const gated = await armedHold(page, /\/api\/v1\/quotes\/q-1001$/, "GET");
  gated.arm();
  await edit(page).getByRole("button", { name: "Create link to copy" }).click();
  await gated.held.asked;
  gated.disarm();
  await openQuote(page, "Q-1002");
  const changed = await watchEvent(page, "quote-changed");
  await gated.held.release();
  await settle(page);
  expect(await changed()).toBe(0);
});

test("guard edit copy: a copy that finishes after another quote opened says nothing there", async ({ page }) => {
  await stubBrowserChannels(page);
  await signIn(page);
  await openQuote(page, "Q-1001");
  await edit(page).getByRole("button", { name: "Create link to copy" }).click();
  await expect(edit(page).getByRole("button", { name: "Copy", exact: true })).toBeVisible();
  await page.evaluate(() => ((window as unknown as { __clipHold: boolean }).__clipHold = true));
  await edit(page).getByRole("button", { name: "Copy", exact: true }).click();
  await expect.poll(() => page.evaluate(() => (window as unknown as { __copied: string[] }).__copied.length)).toBe(1);
  await openQuote(page, "Q-1002");
  await page.evaluate(() => (window as unknown as { __clipRelease: () => void }).__clipRelease());
  await settle(page);
  await expect(page.getByTestId("qe-notice")).toHaveText("");
});

test("guard edit copy refused: a copy that fails after another quote opened says nothing there", async ({ page }) => {
  await stubBrowserChannels(page);
  await signIn(page);
  await openQuote(page, "Q-1001");
  await edit(page).getByRole("button", { name: "Create link to copy" }).click();
  await expect(edit(page).getByRole("button", { name: "Copy", exact: true })).toBeVisible();
  await page.evaluate(() => ((window as unknown as { __clipHold: boolean }).__clipHold = true));
  await edit(page).getByRole("button", { name: "Copy", exact: true }).click();
  await expect.poll(() => page.evaluate(() => (window as unknown as { __copied: string[] }).__copied.length)).toBe(1);
  await openQuote(page, "Q-1002");
  await page.evaluate(() => (window as unknown as { __clipReject: () => void }).__clipReject());
  await settle(page);
  await expect(page.getByTestId("qe-error")).toHaveText("");
});

test("guard edit compare: a refused comparison for the quote left shows no error on the quote now open", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-1001");
  const post = await hold(page, /\/api\/v1\/quotes\/q-1001\/lines\/\d+\/compare$/, { method: "POST", refuse: { status: 500, error: "quote: the comparison is down" } });
  await edit(page).getByRole("button", { name: "Compare" }).first().click();
  await post.asked;
  await openQuote(page, "Q-1003");
  await post.release();
  await settle(page);
  await expect(page.getByTestId("qe-error")).toHaveText("");
});

// ── the history panel ──

test("guard history read: the changes of the quote left never show under the quote now open", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-1001");
  const sand = edit(page).getByTestId("qe-line").nth(1);
  await sand.locator('[data-field="qty"]').fill("6");
  await sand.getByRole("button", { name: "Save" }).click();
  await expect(page.locator("qd-quote-history").getByTestId("qh-group")).toHaveCount(1);
  await openQuote(page, "Q-1003");
  await expect(page.locator("qd-quote-history")).toContainText("No changes yet.");
  await quiet(page);
  const gated = await armedHold(page, /\/api\/v1\/quotes\/q-1001\/edits$/, "GET");
  gated.arm();
  await showList(page);
  await row(page, "Q-1001").click();
  await gated.held.asked;
  gated.disarm();
  await openQuote(page, "Q-1003");
  await gated.held.release();
  await settle(page);
  await expect(page.locator("qd-quote-history").getByTestId("qh-group")).toHaveCount(0);
});

test("guard history order: an older read answered late does not undo a newer one", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-1001");
  await expect(page.locator("qd-quote-history")).toContainText("No changes yet.");
  await quiet(page);
  const gated = await armedHold(page, /\/api\/v1\/quotes\/q-1001\/edits$/, "GET", { snapshot: true, times: 1 });
  gated.arm();
  await reloadOf(page, "qd-quote-history");
  await gated.held.asked;
  gated.disarm();
  const sand = edit(page).getByTestId("qe-line").nth(1);
  await sand.locator('[data-field="qty"]').fill("6");
  await sand.getByRole("button", { name: "Save" }).click();
  await expect(page.locator("qd-quote-history").getByTestId("qh-group")).toHaveCount(1);
  await quiet(page);
  await gated.held.release();
  await settle(page);
  await expect(page.locator("qd-quote-history").getByTestId("qh-group")).toHaveCount(1);
});

// ── the assist panel ──

const assist = (page: Page) => page.locator("qd-quote-assist");
const READ = { name: "Suggest lines from this text" };

test("guard assist read: proposals read for the quote left never show on the quote now open", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-1001");
  const post = await hold(page, /\/api\/v1\/quote-assist\/extract$/, { method: "POST" });
  await assist(page).locator("#jobtext").fill("3 yd clear stone");
  await assist(page).getByRole("button", READ).click();
  await post.asked;
  await openQuote(page, "Q-1003");
  await post.release();
  await settle(page);
  await expect(assist(page).getByTestId("qa-proposal")).toHaveCount(0);
});

test("guard assist reading flag: a read that ends on the quote left does not free the flag of a read on the quote open", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-1001");
  const first = await hold(page, /\/api\/v1\/quote-assist\/extract$/, { method: "POST", times: 1 });
  await assist(page).locator("#jobtext").fill("3 yd clear stone");
  await assist(page).getByRole("button", READ).click();
  await first.asked;
  await openQuote(page, "Q-1003");
  const second = await hold(page, /\/api\/v1\/quote-assist\/extract$/, { method: "POST", match: () => true });
  await assist(page).locator("#jobtext").fill("20 sf holland stone");
  await assist(page).getByRole("button", READ).click();
  await second.asked;
  await expect(assist(page).locator('[data-action="read"]')).toBeDisabled();
  await first.release();
  await settle(page);
  await expect(assist(page).locator('[data-action="read"]')).toBeDisabled();
  await second.release();
  await expect(assist(page).getByTestId("qa-proposal").first()).toBeVisible();
});

test("guard assist adding flag: an add that ends on the quote left does not free the flag of an add on the quote open", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-1001");
  await assist(page).locator("#jobtext").fill("3 yd clear stone");
  await assist(page).getByRole("button", READ).click();
  await assist(page).getByTestId("qa-proposal").first().locator('[data-field="tick"]').check();
  const first = await hold(page, /\/api\/v1\/quotes\/q-1001\/lines$/, { method: "POST" });
  await assist(page).getByTestId("qa-confirm").click();
  await first.asked;
  await openQuote(page, "Q-1003");
  await assist(page).locator("#jobtext").fill("20 sf holland stone");
  await assist(page).getByRole("button", READ).click();
  await assist(page).getByTestId("qa-proposal").first().locator('[data-field="tick"]').check();
  const second = await hold(page, /\/api\/v1\/quotes\/q-1003\/lines$/, { method: "POST" });
  await assist(page).getByTestId("qa-confirm").click();
  await second.asked;
  await expect(assist(page).getByTestId("qa-confirm")).toBeDisabled();
  await first.release();
  await settle(page);
  await expect(assist(page).getByTestId("qa-confirm")).toBeDisabled();
  await second.release();
});

test("guard assist gaps read: the suggestions for the quote left never show on the quote now open", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-1003");
  await expect(assist(page).getByTestId("qa-gaps")).toBeVisible();
  await expect(assist(page).getByTestId("qa-gaps")).toContainText("Checked 1 line");
  await quiet(page);
  const gated = await armedHold(page, /\/api\/v1\/quote-assist\/scope-gaps/, "GET");
  gated.arm();
  await showList(page);
  await row(page, "Q-1001").click();
  await gated.held.asked;
  gated.disarm();
  await openQuote(page, "Q-1003");
  await gated.held.release();
  await settle(page);
  await expect(assist(page).getByTestId("qa-gaps")).toContainText("Checked 1 line");
  await expect(assist(page).getByTestId("qa-gaps")).not.toContainText("Granular base for the paved area");
});

test("guard assist gaps order: an older suggestions read answered late does not undo a newer one", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-1001");
  await expect(assist(page).getByTestId("qa-gaps")).toContainText("Granular base for the paved area");
  await quiet(page);
  const gated = await armedHold(page, /\/api\/v1\/quote-assist\/scope-gaps/, "GET", { snapshot: true, times: 1 });
  gated.arm();
  await reloadOf(page, "qd-quote-assist");
  await gated.held.asked;
  gated.disarm();
  // Clear stone is the base: adding it from the work area makes the base suggestion go away.
  const scan = page.getByRole("combobox", { name: "Search products by name, code or category" });
  await scan.fill("GRA-CL");
  await scan.press("Enter");
  await expect(assist(page).getByTestId("qa-gaps")).not.toContainText("Granular base for the paved area");
  await quiet(page);
  await gated.held.release();
  await settle(page);
  await expect(assist(page).getByTestId("qa-gaps")).not.toContainText("Granular base for the paved area");
});

// ── availability, follow-ups, hold expiry ──

test("guard availability read: the stock read for the quote left never shows on the quote now open", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-1003");
  const panel = page.locator("qd-quote-availability");
  await expect(panel.getByTestId("av-chip").first()).toBeVisible();
  const own = await panel.getByTestId("av-box").innerText();
  await quiet(page);
  const gated = await armedHold(page, /\/api\/v1\/quotes\/q-1002\/availability$/, "GET");
  gated.arm();
  await showList(page);
  await row(page, "Q-1002").click();
  await gated.held.asked;
  gated.disarm();
  await openQuote(page, "Q-1003");
  await expect(panel.getByTestId("av-box")).toHaveText(own);
  await gated.held.release();
  await settle(page);
  await expect(panel.getByTestId("av-box")).toHaveText(own);
});

test("guard follow-ups read: the plan of the quote left never shows on the quote now open", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-1001");
  const panel = page.locator("qd-quote-followups");
  await expect(panel.getByTestId("fu-row")).toHaveCount(0);
  await quiet(page);
  const gated = await armedHold(page, /\/api\/v1\/quotes\/q-1002\/followups$/, "GET");
  gated.arm();
  await showList(page);
  await row(page, "Q-1002").click();
  await gated.held.asked;
  gated.disarm();
  await openQuote(page, "Q-1001");
  await gated.held.release();
  await settle(page);
  await expect(panel.getByTestId("fu-row")).toHaveCount(0);
});

test("guard follow-ups text reload: a text share whose re-read answers after another quote opened announces nothing", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-1002");
  // The text leg is queued (the stub's own answer is a refusal, which has no re-read after it).
  await page.route(/\/api\/v1\/quotes\/q-1002\/sms-share$/, (route) => route.fulfill({ status: 200, contentType: "application/json", body: "{}" }));
  await quiet(page);
  const gated = await armedHold(page, /\/api\/v1\/quotes\/q-1002\/followups$/, "GET");
  gated.arm();
  await page.locator("qd-quote-followups").getByRole("button", { name: "Text the quote link" }).click();
  await gated.held.asked;
  gated.disarm();
  await openQuote(page, "Q-1001");
  const changed = await watchEvent(page, "quote-changed");
  await gated.held.release();
  await settle(page);
  expect(await changed()).toBe(0);
  await expect(page.locator("qd-quote-followups").getByTestId("fu-notice")).toHaveCount(0);
});

test("guard hold expiry read: the lapse of the quote left never shows on the quote now open", async ({ page, request }) => {
  await request.post("/_stub/hold-lapse", { data: { quote_id: "q-1001", line_no: 2 } });
  await signIn(page);
  await openQuote(page, "Q-1003");
  const panel = page.locator("qd-quote-hold-expiry");
  await expect(panel.getByTestId("he-headline")).toHaveCount(0);
  await quiet(page);
  const gated = await armedHold(page, /\/api\/v1\/quotes\/q-1001\/hold-expiry$/, "GET");
  gated.arm();
  await showList(page);
  await row(page, "Q-1001").click();
  await gated.held.asked;
  gated.disarm();
  await openQuote(page, "Q-1003");
  await gated.held.release();
  await settle(page);
  await expect(panel.getByTestId("he-headline")).toHaveCount(0);
});

test("guard hold expiry reload: a re-hold whose re-read answers after another quote opened announces nothing", async ({ page, request }) => {
  await request.post("/_stub/hold-lapse", { data: { quote_id: "q-1001", line_no: 2 } });
  await signIn(page);
  await openQuote(page, "Q-1001");
  const panel = page.locator("qd-quote-hold-expiry");
  await expect(panel.getByRole("button", { name: "Re-hold these lines" })).toBeVisible();
  await quiet(page);
  const gated = await armedHold(page, /\/api\/v1\/quotes\/q-1001\/hold-expiry$/, "GET");
  gated.arm();
  await panel.getByRole("button", { name: "Re-hold these lines" }).click();
  await gated.held.asked;
  gated.disarm();
  await openQuote(page, "Q-1003");
  const changed = await watchEvent(page, "quote-changed");
  await gated.held.release();
  await settle(page);
  expect(await changed()).toBe(0);
  await expect(panel.getByTestId("he-notice")).toHaveCount(0);
});

// ── the quote list, the new quote form and the customer picker ──

test("guard list order: an older read of the list answered late does not undo a newer one", async ({ page, request }) => {
  await signIn(page);
  await expect(row(page, "Q-1001")).toBeVisible();
  await quiet(page);
  const gated = await armedHold(page, /\/api\/v1\/quotes(\?.*)?$/, "GET", { snapshot: true, times: 1 });
  gated.arm();
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  await gated.held.asked;
  gated.disarm();
  await seed(page, { customer_id: "c-fenwick" });
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  await expect(row(page, "Q-1004")).toBeVisible();
  await quiet(page);
  await gated.held.release();
  await settle(page);
  await expect(row(page, "Q-1004")).toBeVisible();
  void request;
});

test("guard list paging: a page asked for before the list was read again is not added to the new list", async ({ page, request }) => {
  await request.put("/_stub/quote-page-size", { data: { size: 1 } });
  await signIn(page);
  await expect(page.getByTestId("qb-list-row")).toHaveCount(1);
  await quiet(page);
  const gated = await armedHold(page, /\/api\/v1\/quotes\?.*cursor=/, "GET", { snapshot: true, times: 1 });
  gated.arm();
  await page.getByTestId("qb-list-more").click();
  await gated.held.asked;
  gated.disarm();
  // An edit elsewhere on the page reads the list again while the page is out (the Refresh button is held while it loads).
  await page.getByTestId("qb-list").evaluate((el) => el.dispatchEvent(new CustomEvent("quote-changed", { detail: { quoteId: "q-1001" }, bubbles: true, composed: true })));
  await expect(page.getByTestId("qb-list-row")).toHaveCount(1);
  await quiet(page);
  await gated.held.release();
  await settle(page);
  await expect(page.getByTestId("qb-list-row")).toHaveCount(1);
  await request.put("/_stub/quote-page-size", { data: { size: 0 } });
});

test("guard create selection: a quote made while another was opened does not take the screen from it", async ({ page }) => {
  await signIn(page);
  // The compose page is where a new quote is made now: the pick IS the
  // create (there is no form under the list any more).
  await newQuote(page);
  const picker = page.getByTestId("qb-identity").locator("hos-customer-picker");
  const post = await hold(page, /\/api\/v1\/quotes$/, { method: "POST" });
  await picker.getByPlaceholder("Search name or account #…").fill("Fenwick");
  await picker.getByRole("option", { name: /Fenwick Builders/ }).click();
  // The first line makes the quote: the create is held, and the person walks to another quote while it is out.
  const box = page.getByRole("combobox", { name: "Search products by name, code or category" });
  await box.fill("PSN-100");
  await box.press("Enter");
  await post.asked;
  await showList(page);
  await row(page, "Q-1001").click();
  await expect(page.getByTestId("qb-context")).toContainText("Q-1001");
  await post.release();
  await settle(page);
  await expect(page.getByTestId("qb-context")).toContainText("Q-1001");
  // The made quote joins the list, one page over.
  await page.getByTestId("qb-all-quotes").click();
  await expect(row(page, "Q-1004")).toBeVisible();
});

test("guard picker search: a customer search answered for an older word never replaces the newer list", async ({ page }) => {
  await signIn(page);
  await newQuote(page);
  const picker = page.getByTestId("qb-identity").locator("hos-customer-picker");
  const older = await hold(page, /\/api\/v1\/customers\/search/, { method: "GET", times: 1, match: (request) => request.url().includes("q=Maple") });
  await picker.getByPlaceholder("Search name or account #…").fill("Maple");
  await older.asked;
  await picker.getByPlaceholder("Search name or account #…").fill("Fenwick");
  await expect(picker.getByRole("option", { name: /Fenwick Builders/ })).toBeVisible();
  await quiet(page);
  await older.release();
  await settle(page);
  await expect(picker.getByRole("option", { name: /Fenwick Builders/ })).toBeVisible();
  await expect(picker.getByRole("option", { name: /Maple Ridge/ })).toHaveCount(0);
});

// ── the settings surface (guarded by scope, branch and profile rather than by a quote) ──

const surface = (page: Page) => page.locator("qd-surface-settings");

async function openScreenSettings(page: Page): Promise<void> {
  // The disclosure lives on the quote page under the split-list
  // navigation: open a quote first, then the detail disclosure.
  await showList(page);
  await row(page, "Q-1001").click();
  await expect(page.getByTestId("qb-context")).toBeVisible();
  await page.getByText("Quote detail & admin").click();
  await page.locator("qd-quote-settings").getByTestId("qs-screen").locator("summary").click();
  await expect(page.getByTestId("qs-policy")).toBeVisible();
}

async function generalManager(page: Page): Promise<void> {
  await page.request.put("/_stub/general-manager", { data: { member: true } });
  await signIn(page);
  await openScreenSettings(page);
}

test.afterEach(async ({ request }) => {
  await request.put("/_stub/general-manager", { data: { member: false } });
});

test("guard policy order: a policy read for the scope before, answered late, does not undo the newer one", async ({ page }) => {
  await generalManager(page);
  await page.getByTestId("qs-validity-days").fill("55");
  await page.getByTestId("qs-policy-save").click();
  await expect(page.getByTestId("qs-notice")).toContainText("Quote settings saved");
  await quiet(page);
  const gated = await armedHold(page, /\/api\/v1\/quotes\/policy/, "GET", { snapshot: true, times: 1 });
  gated.arm();
  await page.getByTestId("qs-scope").selectOption("dealer");
  await gated.held.asked;
  gated.disarm();
  await page.getByTestId("qs-scope").selectOption("branch");
  await expect(page.getByTestId("qs-validity-days")).toHaveValue("55");
  await quiet(page);
  await gated.held.release();
  await settle(page);
  await expect(page.getByTestId("qs-validity-days")).toHaveValue("55");
});

test("guard policy edit: a read that answers while the form is being edited does not overwrite the edit", async ({ page }) => {
  await generalManager(page);
  await page.getByTestId("qs-validity-days").fill("77");
  const read = page.waitForResponse((r) => r.request().method() === "GET" && r.url().includes("/quotes/policy"));
  await surface(page).evaluate((el) => (el as unknown as { reload: () => void }).reload());
  await read;
  await settle(page);
  await expect(page.getByTestId("qs-validity-days")).toHaveValue("77");
});

test("guard policy unread: a failed read takes the form of the scope before down", async ({ page }) => {
  await generalManager(page);
  await expect(page.getByTestId("qs-validity-days")).toBeVisible();
  await page.route("**/api/v1/quotes/policy**", async (route) => {
    if (route.request().method() === "GET" && !new URL(route.request().url()).searchParams.has("branch_id")) {
      await route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: "quote: the policy store is down" }) });
      return;
    }
    await route.continue();
  });
  await page.getByTestId("qs-scope").selectOption("dealer");
  await expect(page.getByTestId("qs-policy-unread")).toBeVisible();
  await expect(page.getByTestId("qs-validity-days")).toHaveCount(0);
});

test("guard policy wrong form: a form loaded for another scope is never saved to this one", async ({ page }) => {
  await generalManager(page);
  await expect(page.getByTestId("qs-validity-days")).toBeVisible();
  const seen = writes(page);
  await surface(page).evaluate((el) => {
    (el as unknown as { formKey: string }).formKey = "dealer";
  });
  await page.getByTestId("qs-policy-save").click();
  await expect(page.getByTestId("qs-error")).toContainText("another scope");
  expect(seen.filter((w) => w.startsWith("PUT") && w.includes("/quotes/policy"))).toEqual([]);
});

test("guard policy save answer: a save that answers after the scope moved on fills nothing", async ({ page }) => {
  await generalManager(page);
  await page.getByTestId("qs-validity-days").fill("55");
  const put = await hold(page, /\/api\/v1\/quotes\/policy/, { method: "PUT" });
  await page.getByTestId("qs-policy-save").click();
  await put.asked;
  await page.getByTestId("qs-scope").selectOption("dealer");
  await expect(page.getByTestId("qs-validity-days")).toHaveValue("30");
  await put.release();
  await settle(page);
  await expect(page.getByTestId("qs-validity-days")).toHaveValue("30");
  await expect(page.getByTestId("qs-notice")).toHaveCount(0);
});

test("guard policy refusal: a refusal of a save on the scope still on screen is said there", async ({ page }) => {
  await generalManager(page);
  await page.getByTestId("qs-validity-days").fill("55");
  const put = await hold(page, /\/api\/v1\/quotes\/policy/, { method: "PUT", refuse: { status: 500, error: "quote: the policy store is down" } });
  await page.getByTestId("qs-policy-save").click();
  await put.asked;
  await put.release();
  await expect(page.getByTestId("qs-error")).toContainText("policy store is down");
  // It is the scope on screen: the sentence is the plain refusal, not the one for settings left behind.
  await expect(page.getByTestId("qs-error")).not.toContainText("you left");
  await expect(page.getByTestId("qs-error")).not.toContainText("were not saved");
});

test("guard policy lock: a refusal locks the row it was for and no other", async ({ page }) => {
  await signIn(page);
  await openScreenSettings(page);
  const own = await surface(page).evaluate((el) => (el as unknown as { branchId: string }).branchId);
  const setBranch = (id: string) =>
    surface(page).evaluate((el, value) => {
      (el as unknown as { branchId: string }).branchId = value;
    }, id);
  await setBranch("another-branch");
  await expect(page.getByTestId("qs-policy-save")).toBeEnabled();
  const put = await hold(page, /\/api\/v1\/quotes\/policy/, { method: "PUT", refuse: { status: 403, error: "quote: that branch is not one your role reaches" } });
  await page.getByTestId("qs-policy-save").click();
  await put.asked;
  await setBranch(own);
  await put.release();
  await settle(page);
  // The refusal was for the other branch: this one is not locked.
  await expect(page.getByTestId("qs-policy-lock")).toHaveCount(0);
  await expect(page.getByTestId("qs-policy-save")).toBeEnabled();
  await setBranch("another-branch");
  await expect(page.getByTestId("qs-policy-lock")).toContainText("not one your role reaches");
});

test("guard items order: a layout read for the profile before, answered late, does not undo the newer one", async ({ page }) => {
  await generalManager(page);
  const items = page.getByTestId("qs-items");
  await items.getByLabel("Show Photo").uncheck();
  await items.getByTestId("qs-items-save").click();
  await expect(page.getByTestId("qs-notice")).toContainText("Screen layout saved for the counter profile");
  await expect(items.getByLabel("Show Photo")).not.toBeChecked();
  await page.getByTestId("qs-profile").selectOption("yard");
  await expect(items.getByLabel("Show Photo")).toBeChecked();
  await quiet(page);
  const gated = await armedHold(page, /\/api\/v1\/surfaces\/[^/]+\/config/, "GET", { snapshot: true, times: 1 });
  gated.arm();
  await page.getByTestId("qs-profile").selectOption("counter");
  await gated.held.asked;
  gated.disarm();
  await page.getByTestId("qs-profile").selectOption("yard");
  await expect(items.getByLabel("Show Photo")).toBeChecked();
  await quiet(page);
  await gated.held.release();
  await settle(page);
  await expect(items.getByLabel("Show Photo")).toBeChecked();
});

test("guard items drafts: a re-read keeps the layout changes the person has begun", async ({ page }) => {
  await generalManager(page);
  const items = page.getByTestId("qs-items");
  await items.getByLabel("Show Photo").uncheck();
  const read = page.waitForResponse((r) => r.request().method() === "GET" && r.url().includes("/surfaces/"));
  await surface(page).evaluate((el) => (el as unknown as { reload: () => void }).reload());
  await read;
  await settle(page);
  await expect(items.getByLabel("Show Photo")).not.toBeChecked();
});

test("guard items save answer: a layout save that answers after the profile moved on says nothing there", async ({ page }) => {
  await generalManager(page);
  const items = page.getByTestId("qs-items");
  const put = await hold(page, /\/api\/v1\/surfaces\/[^/]+\/items$/, { method: "PUT" });
  await items.getByTestId("qs-items-save").click();
  await put.asked;
  // The select is held while a save is out, so the profile is moved the way a script would move it.
  await surface(page).evaluate((el) => {
    (el as unknown as { profile: string }).profile = "yard";
  });
  const changed = await watchEvent(page, "quote-changed");
  await put.release();
  await settle(page);
  await expect(page.getByTestId("qs-notice")).toHaveCount(0);
  expect(await changed()).toBeGreaterThan(0);
});
