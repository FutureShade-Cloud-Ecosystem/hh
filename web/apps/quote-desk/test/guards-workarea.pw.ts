// PR 121 round 3: one deterministic test for each context guard of the work area. Each test holds one answer back
// behind a delayed route, moves to another quote, lets the answer go and asserts the other quote shows none of it.
// Take the guard away (test/mutants.json names the exact edit) and the test goes red.
import { expect, test } from "@playwright/test";
import { armedHold, countRequests, hold, open, row, seed, settle, signIn, stubBrowserChannels, watchEvent, writes, showList } from "./switch-helpers.js";

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

const fenwickTerms = { customer_po: "PO-SAVED-7" };

test("guard terms read: the terms of the quote just left never land on the quote now open", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick", terms: fenwickTerms });
  await signIn(page);
  const held = await hold(page, /\/quotes\/q-1004\/terms$/, { method: "GET" });
  await showList(page);
  await row(page, "Q-1004").click();
  await held.asked;
  await open(page, "Q-1001");
  await expect(page.getByTestId("qb-line")).toHaveCount(2);
  await held.release();
  await settle(page);
  await expect(page.getByTestId("qb-po")).toHaveValue("");
});

test("guard margin glance: the margin of the quote just left never lands on the quote now open", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  await open(page, "Q-1004");
  await expect(page.getByTestId("qb-margin-pill")).toBeVisible();
  const fenwickMargin = await page.getByTestId("qb-margin-pill").innerText();
  await open(page, "Q-1001");
  await expect(page.getByTestId("qb-margin-pill")).toBeVisible();
  const mapleMargin = await page.getByTestId("qb-margin-pill").innerText();
  expect(fenwickMargin).not.toBe(mapleMargin);
  const held = await hold(page, /\/quotes\/q-1004\/margin-glance$/, { method: "GET" });
  await open(page, "Q-1004");
  await held.asked;
  await open(page, "Q-1001");
  await expect(page.getByTestId("qb-margin-pill")).toHaveText(mapleMargin);
  await held.release();
  await settle(page);
  await expect(page.getByTestId("qb-margin-pill")).toHaveText(mapleMargin);
});

test("guard terms save answer: a terms save that answers after another quote opened leaves that quote's boxes alone", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  await open(page, "Q-1004");
  await expect(page.getByTestId("qb-po")).toBeVisible();
  const held = await hold(page, /\/quotes\/q-1004\/terms$/, { method: "PUT", times: 1 });
  await page.getByTestId("qb-po").fill("PO-TYPED-ON-1004");
  await held.asked;
  // The other quote's own terms read is held too, so nothing but the stale answer could fill its boxes.
  const read = await hold(page, /\/quotes\/q-1001\/terms$/, { method: "GET" });
  await showList(page);
  await row(page, "Q-1001").click();
  await read.asked;
  await expect(page.getByTestId("qb-context")).toContainText("Q-1001");
  await held.release();
  await settle(page);
  await expect(page.getByTestId("qb-po")).toHaveCount(0);
  await read.release();
  await settle(page);
  await expect(page.getByTestId("qb-po")).toHaveValue("");
});

test("guard add line: an add that answers after another quote opened says nothing on that quote", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  await open(page, "Q-1004");
  await expect(page.getByTestId("qb-line")).toHaveCount(1);
  const held = await hold(page, /\/quotes\/q-1004\/lines$/, { method: "POST" });
  const scan = page.getByRole("combobox", { name: "Search products by name, code or category" });
  await scan.fill("PSN-100");
  await scan.press("Enter");
  await held.asked;
  await open(page, "Q-1001");
  await expect(page.getByTestId("qb-line")).toHaveCount(2);
  await held.release();
  await settle(page);
  await expect(page.getByText("Line added.")).toHaveCount(0);
  await expect(page.getByText("Added Polymeric Locking Sand")).toHaveCount(0);
  await expect(page.getByTestId("qb-error")).toHaveText("");
});

test("guard quantity edit: a refused quantity edit that answers after another quote opened shows no error there", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  await open(page, "Q-1004");
  await expect(page.getByTestId("qb-line")).toHaveCount(1);
  const held = await hold(page, /\/quotes\/q-1004\/lines\/1$/, { method: "PATCH", refuse: { status: 500, error: "quote: the line store is down" } });
  await page.getByRole("button", { name: /^Increase quantity/ }).first().click();
  await held.asked;
  await open(page, "Q-1001");
  await expect(page.getByTestId("qb-line")).toHaveCount(2);
  await held.release();
  await settle(page);
  await expect(page.getByTestId("qb-error")).toHaveText("");
});

test("guard discount edit: a refused discount edit that answers after another quote opened shows no error there", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  await open(page, "Q-1004");
  await expect(page.getByTestId("qb-line")).toHaveCount(1);
  const held = await hold(page, /\/api\/v1\/quotes\/q-1004\/lines\/1$/, { method: "PATCH", refuse: { status: 500, error: "quote: the line store is down" } });
  const disc = page.getByTestId("qb-line-disc");
  await disc.fill("5");
  await disc.blur();
  await held.asked;
  await open(page, "Q-1001");
  await expect(page.getByTestId("qb-line")).toHaveCount(2);
  await held.release();
  await settle(page);
  await expect(page.getByTestId("qb-error")).toHaveText("");
});

test("guard remove line: a refused remove that answers after another quote opened shows no error there", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  await open(page, "Q-1004");
  await expect(page.getByTestId("qb-line")).toHaveCount(1);
  const held = await hold(page, /\/quotes\/q-1004\/lines\/1$/, { method: "DELETE", refuse: { status: 500, error: "quote: the line store is down" } });
  await page.getByTestId("qb-line-trash").first().click();
  await held.asked;
  await open(page, "Q-1001");
  await expect(page.getByTestId("qb-line")).toHaveCount(2);
  await held.release();
  await settle(page);
  await expect(page.getByTestId("qb-error")).toHaveText("");
});

test("guard validity: a refused expiry change that answers after another quote opened shows no error there", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  await open(page, "Q-1004");
  await expect(page.getByTestId("qb-terms-validity")).toBeVisible();
  const held = await hold(page, /\/quotes\/q-1004$/, { method: "PATCH", refuse: { status: 500, error: "quote: the expiry store is down" } });
  await page.getByTestId("qb-terms-validity").selectOption("60 days");
  await held.asked;
  await open(page, "Q-1001");
  await expect(page.getByTestId("qb-line")).toHaveCount(2);
  await held.release();
  await settle(page);
  await expect(page.getByTestId("qb-error")).toHaveText("");
});

test("guard convert: a conversion that answers after another quote opened shows no order number there", async ({ page }) => {
  const accepted = await seed(page, { customer_id: "c-fenwick", status: "ACCEPTED" });
  expect(accepted).toBe("q-1004");
  await signIn(page);
  await open(page, "Q-1004");
  await expect(page.getByTestId("qb-convert")).toBeVisible();
  const held = await hold(page, /\/quotes\/q-1004\/convert$/, { method: "POST" });
  await page.getByTestId("qb-convert").click();
  await held.asked;
  await open(page, "Q-1001");
  await expect(page.getByTestId("qb-line")).toHaveCount(2);
  await held.release();
  await settle(page);
  await expect(page.getByTestId("qb-convert-done")).toHaveCount(0);
});

test("guard customer search: the account facts found for the quote just left never show on the quote now open", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  const held = await hold(page, /\/customers\/search/, { method: "GET", match: (request) => request.url().includes("Fenwick") });
  await showList(page);
  await row(page, "Q-1004").click();
  await held.asked;
  await open(page, "Q-1001");
  await expect(page.getByTestId("qb-identity-credit")).toBeVisible();
  const mapleCredit = await page.getByTestId("qb-identity").innerText();
  await held.release();
  await settle(page);
  expect(await page.getByTestId("qb-identity").innerText()).toBe(mapleCredit);
  expect(mapleCredit).not.toContain("C02");
});

test("guard add-ons: the suggestions read for the quote just left never show on the quote now open", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  const held = await hold(page, /\/pos\/addon-suggestions/, { method: "GET", match: (request) => request.url().includes("p-holland-60") });
  await showList(page);
  await row(page, "Q-1001").click();
  await held.asked;
  const own = page.waitForResponse((r) => r.url().includes("/pos/addon-suggestions") && !r.url().includes("p-holland-60"));
  await open(page, "Q-1004");
  await expect(page.getByTestId("qb-line")).toHaveCount(1);
  await own;
  await settle(page);
  await held.release();
  await settle(page);
  await expect(page.getByTestId("qb-addon-tile")).toHaveCount(0);
});

test("guard busy release: a send that finishes on the quote just left does not free the busy hold of the quote now open", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  await open(page, "Q-1004");
  const mint = await hold(page, /\/quotes\/q-1004\/share-links$/, { method: "POST" });
  await page.getByTestId("qb-send").click();
  await mint.asked;
  await open(page, "Q-1001");
  await expect(page.getByTestId("qb-line")).toHaveCount(2);
  const edit = await hold(page, /\/quotes\/q-1001\/lines\/1$/, { method: "PATCH" });
  await page.getByRole("button", { name: /^Increase quantity/ }).first().click();
  await edit.asked;
  await expect(page.getByTestId("qb-send")).toBeDisabled();
  await mint.release();
  await settle(page);
  await expect(page.getByTestId("qb-send")).toBeDisabled();
  edit.open();
  await expect(page.getByTestId("qb-send")).toBeEnabled();
});

// ── the guards after an await that is not itself a guarded call: each holds one step of a chain back, switches
// quotes, lets it go, and asserts the chain stopped (no write sent, no notice, no quote-changed announced). ──

test("guard scan waits for opening: a scan that waited for the quote being opened is dropped when another quote opens", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  const seen = writes(page);
  const reads = await hold(page, /\/api\/v1\/quotes\/q-1004$/, { method: "GET" });
  await showList(page);
  await row(page, "Q-1004").click();
  await reads.asked;
  await page.locator("qd-quote-workarea").evaluate((el) => {
    el.shadowRoot?.dispatchEvent(
      new CustomEvent("pos-add-product", { detail: { product: { id: "p-polymer-sand", base_uom: "bag", name: "Polymeric Locking Sand" } }, bubbles: true, composed: true }),
    );
  });
  await open(page, "Q-1001");
  await expect(page.getByTestId("qb-line")).toHaveCount(2);
  // A line of the quote now open is already being said: the scan that is dropped must leave it alone.
  const qty = page.locator("qd-quote-workarea").locator('[data-field="qty"]').first();
  await qty.fill("0");
  await qty.blur();
  await expect(page.getByTestId("qb-error")).toContainText("Quantity must be a positive number.");
  await reads.release();
  await settle(page);
  expect(seen.filter((w) => w.includes("/lines"))).toEqual([]);
  await expect(page.getByTestId("qb-error")).toContainText("Quantity must be a positive number.");
  // The sender is the last line of defence and would refuse this write; the screen of the quote now open still must not be told of it.
  await expect(page.getByText(/was not sent, because you moved/)).toHaveCount(0);
});

test("guard cart change after terms save: an add whose terms save answers after another quote opened announces nothing", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  await open(page, "Q-1004");
  await expect(page.getByTestId("qb-line")).toHaveCount(1);
  await expect(page.getByTestId("qb-po")).toBeVisible();
  // The save is refused, so its late answer announces nothing itself (a success that lands late does, and the open views then refresh on their own).
  const put = await hold(page, /\/api\/v1\/quotes\/q-1004\/terms$/, { method: "PUT", refuse: { status: 500, error: "quote: the terms store is down" } });
  const scan = page.getByRole("combobox", { name: "Search products by name, code or category" });
  await scan.fill("PSN-100");
  await scan.press("Enter");
  await put.asked;
  await open(page, "Q-1001");
  await settle(page);
  const changed = await watchEvent(page, "quote-changed");
  const readsOfOpen = countRequests(page, /^GET .*\/api\/v1\/quotes\/q-1001$/);
  await put.release();
  await settle(page);
  expect(await changed()).toBe(0);
  // The chain stopped: it did not go on to re-read the quote now open.
  expect(readsOfOpen()).toBe(0);
});

test("guard cart change after read: an add whose re-read answers after another quote opened announces nothing", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  await open(page, "Q-1004");
  await expect(page.getByTestId("qb-line")).toHaveCount(1);
  const gated = await armedHold(page, /\/api\/v1\/quotes\/q-1004$/, "GET");
  gated.arm();
  const scan = page.getByRole("combobox", { name: "Search products by name, code or category" });
  await scan.fill("PSN-100");
  await scan.press("Enter");
  await gated.held.asked;
  gated.disarm();
  await open(page, "Q-1001");
  const changed = await watchEvent(page, "quote-changed");
  await gated.held.release();
  await settle(page);
  expect(await changed()).toBe(0);
});

test("guard terms save reload: a terms save whose re-read answers after another quote opened announces nothing", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  await open(page, "Q-1004");
  await expect(page.getByTestId("qb-po")).toBeVisible();
  const gated = await armedHold(page, /\/api\/v1\/quotes\/q-1004$/, "GET");
  gated.arm();
  await page.getByTestId("qb-po").fill("PO-RELOAD");
  await gated.held.asked;
  gated.disarm();
  await open(page, "Q-1001");
  const changed = await watchEvent(page, "quote-changed");
  await gated.held.release();
  await settle(page);
  expect(await changed()).toBe(0);
});

test("guard validity reload: a validity change whose re-read answers after another quote opened announces nothing", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  await open(page, "Q-1004");
  await expect(page.getByTestId("qb-terms-validity")).toBeVisible();
  const gated = await armedHold(page, /\/api\/v1\/quotes\/q-1004$/, "GET");
  gated.arm();
  await page.getByTestId("qb-terms-validity").selectOption("60 days");
  await gated.held.asked;
  gated.disarm();
  await open(page, "Q-1001");
  const changed = await watchEvent(page, "quote-changed");
  await gated.held.release();
  await settle(page);
  expect(await changed()).toBe(0);
});

test("guard convert reload: a conversion whose re-read answers after another quote opened announces nothing", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick", status: "ACCEPTED" });
  await signIn(page);
  await open(page, "Q-1004");
  await expect(page.getByTestId("qb-convert")).toBeVisible();
  const gated = await armedHold(page, /\/api\/v1\/quotes\/q-1004$/, "GET");
  gated.arm();
  await page.getByTestId("qb-convert").click();
  await gated.held.asked;
  gated.disarm();
  await open(page, "Q-1001");
  const changed = await watchEvent(page, "quote-changed");
  await gated.held.release();
  await settle(page);
  expect(await changed()).toBe(0);
  await expect(page.getByTestId("qb-convert-done")).toHaveCount(0);
});

test("guard copy link clipboard: a copy that finishes after another quote opened says nothing there", async ({ page }) => {
  await stubBrowserChannels(page);
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  await open(page, "Q-1004");
  await page.evaluate(() => ((window as unknown as { __clipHold: boolean }).__clipHold = true));
  await page.getByTestId("qb-copy-link").click();
  await expect.poll(() => page.evaluate(() => (window as unknown as { __copied: string[] }).__copied.length)).toBe(1);
  await open(page, "Q-1001");
  const changed = await watchEvent(page, "quote-changed");
  await page.evaluate(() => (window as unknown as { __clipRelease: () => void }).__clipRelease());
  await settle(page);
  await expect(page.getByTestId("qb-notice")).toHaveText("");
  expect(await changed()).toBe(0);
});

test("guard copy link reload: a copy whose re-read answers after another quote opened announces nothing", async ({ page }) => {
  await stubBrowserChannels(page);
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  await open(page, "Q-1004");
  const gated = await armedHold(page, /\/api\/v1\/quotes\/q-1004$/, "GET");
  gated.arm();
  await page.getByTestId("qb-copy-link").click();
  await gated.held.asked;
  gated.disarm();
  await open(page, "Q-1001");
  const changed = await watchEvent(page, "quote-changed");
  await gated.held.release();
  await settle(page);
  expect(await changed()).toBe(0);
});

async function errorOnOpenQuote(page: import("@playwright/test").Page): Promise<void> {
  // A refused quantity edit puts a sentence on the quote now open, which a late catch must not blank.
  await page.route(/\/api\/v1\/quotes\/q-1001\/lines\/1$/, (route) =>
    route.request().method() === "PATCH"
      ? route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: "quote: the line store is down" }) })
      : route.continue(),
  );
  await page.getByRole("button", { name: /^Increase quantity/ }).first().click();
  await expect(page.getByTestId("qb-error")).not.toHaveText("");
}

test("guard copy link error: a link that could not be made for the quote left leaves the sentence on the quote open", async ({ page }) => {
  await stubBrowserChannels(page);
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  await open(page, "Q-1004");
  const mint = await hold(page, /\/api\/v1\/quotes\/q-1004\/share-links$/, { method: "POST" });
  await page.getByTestId("qb-copy-link").click();
  await mint.asked;
  await open(page, "Q-1001");
  await errorOnOpenQuote(page);
  const said = await page.getByTestId("qb-error").innerText();
  await mint.release();
  await settle(page);
  await expect(page.getByTestId("qb-error")).toHaveText(said);
});

test("guard preview error: a preview that could not be made for the quote left leaves the sentence on the quote open", async ({ page }) => {
  await stubBrowserChannels(page);
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  await open(page, "Q-1004");
  const mint = await hold(page, /\/api\/v1\/quotes\/q-1004\/share-links$/, { method: "POST" });
  await page.getByTestId("qb-preview").click();
  await mint.asked;
  await open(page, "Q-1001");
  await errorOnOpenQuote(page);
  const said = await page.getByTestId("qb-error").innerText();
  await mint.release();
  await settle(page);
  await expect(page.getByTestId("qb-error")).toHaveText(said);
  expect(await page.evaluate(() => (window as unknown as { __opened: string[] }).__opened)).toEqual([]);
});

test("guard send error: a send that could not be made for the quote left leaves the sentence on the quote open", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  await open(page, "Q-1004");
  const mint = await hold(page, /\/api\/v1\/quotes\/q-1004\/share-links$/, { method: "POST" });
  await page.getByTestId("qb-send").click();
  await mint.asked;
  await open(page, "Q-1001");
  await errorOnOpenQuote(page);
  const said = await page.getByTestId("qb-error").innerText();
  await mint.release();
  await settle(page);
  await expect(page.getByTestId("qb-error")).toHaveText(said);
});

test("guard preview reload: a preview whose re-read answers after another quote opened announces nothing", async ({ page }) => {
  await stubBrowserChannels(page);
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  await open(page, "Q-1004");
  const gated = await armedHold(page, /\/api\/v1\/quotes\/q-1004$/, "GET");
  gated.arm();
  await page.getByTestId("qb-preview").click();
  await gated.held.asked;
  gated.disarm();
  await open(page, "Q-1001");
  const changed = await watchEvent(page, "quote-changed");
  await gated.held.release();
  await settle(page);
  expect(await changed()).toBe(0);
});

test("guard send terms save: a send whose terms save answers after another quote opened mints no link", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  await open(page, "Q-1004");
  await expect(page.getByTestId("qb-po")).toBeVisible();
  const seen = writes(page);
  const put = await hold(page, /\/api\/v1\/quotes\/q-1004\/terms$/, { method: "PUT", times: 1 });
  await page.getByTestId("qb-po").fill("PO-BEFORE-SEND");
  await page.getByTestId("qb-send").click();
  await put.asked;
  await open(page, "Q-1001");
  await put.release();
  await settle(page);
  expect(seen.filter((w) => w.includes("/share-links") || w.includes("/sms-share"))).toEqual([]);
  // The sender is the last line of defence and would refuse this write; the screen of the quote now open still must not be told of it.
  await expect(page.getByText(/was not sent, because you moved/)).toHaveCount(0);
});

test("guard send reload: a send whose re-read answers after another quote opened announces nothing", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  await open(page, "Q-1004");
  const gated = await armedHold(page, /\/api\/v1\/quotes\/q-1004$/, "GET");
  gated.arm();
  await page.getByTestId("qb-send").click();
  await gated.held.asked;
  gated.disarm();
  await open(page, "Q-1001");
  const changed = await watchEvent(page, "quote-changed");
  await gated.held.release();
  await settle(page);
  expect(await changed()).toBe(0);
  await expect(page.getByTestId("qb-notice")).toHaveText("");
});

test("guard customer search error: a failed account search for the quote left does not clear the card of the quote open", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  const held = await hold(page, /\/customers\/search/, { method: "GET", match: (request) => request.url().includes("Fenwick"), refuse: { status: 500, error: "customers: the account search is down" } });
  await showList(page);
  await row(page, "Q-1004").click();
  await held.asked;
  await open(page, "Q-1001");
  await expect(page.getByTestId("qb-identity-credit")).toBeVisible();
  await held.release();
  await settle(page);
  await expect(page.getByTestId("qb-identity-credit")).toBeVisible();
});

test("guard add-ons error: a failed suggestions read for the quote left does not hide the strip of the quote open", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick", lines: [{ p: "p-holland-60", q: 5 }] });
  await signIn(page);
  const held = await hold(page, /\/pos\/addon-suggestions/, {
    method: "GET",
    match: (request) => request.url().endsWith("product_ids=p-holland-60"),
    refuse: { status: 500, error: "pos: the suggestions are down" },
  });
  await showList(page);
  await row(page, "Q-1004").click();
  await held.asked;
  await open(page, "Q-1001");
  await expect(page.getByTestId("qb-addon-tile").first()).toBeVisible();
  await held.release();
  await settle(page);
  await expect(page.getByTestId("qb-addon-tile").first()).toBeVisible();
});

test("guard reload flush wait: a read waiting for a terms save is dropped when the person leaves again", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  await open(page, "Q-1004");
  await expect(page.getByTestId("qb-po")).toBeVisible();
  const put = await hold(page, /\/api\/v1\/quotes\/q-1004\/terms$/, { method: "PUT" });
  await page.getByTestId("qb-po").fill("PO-TYPED-ON-1004");
  await showList(page);
  await row(page, "Q-1001").click();
  await put.asked;
  // Back on the quote whose save is out: its read waits. The person leaves again before the save answers.
  await showList(page);
  await row(page, "Q-1004").click();
  await expect(page.getByTestId("qb-context")).toContainText("Q-1004");
  await open(page, "Q-1001");
  await settle(page);
  const reads = countRequests(page, /^GET .*\/api\/v1\/quotes\/q-1004\/terms$/);
  await put.release();
  await settle(page);
  expect(reads()).toBe(0);
});
