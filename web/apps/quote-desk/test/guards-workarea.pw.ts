// PR 121 round 3: one deterministic test for each context guard of the work area. Each test holds one answer back
// behind a delayed route, moves to another quote, lets the answer go and asserts the other quote shows none of it.
// Take the guard away (test/mutants.json names the exact edit) and the test goes red.
import { expect, test } from "@playwright/test";
import { hold, open, row, seed, settle, signIn } from "./switch-helpers.js";

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
  await row(page, "Q-1004").click();
  await held.asked;
  await open(page, "Q-1001");
  await expect(page.getByTestId("qb-line")).toHaveCount(2);
  held.open();
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
  held.open();
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
  await row(page, "Q-1001").click();
  await read.asked;
  await expect(page.getByTestId("qb-context")).toContainText("Q-1001");
  held.open();
  await settle(page);
  await expect(page.getByTestId("qb-po")).toHaveCount(0);
  read.open();
  await settle(page);
  await expect(page.getByTestId("qb-po")).toHaveValue("");
});

test("guard add line: an add that answers after another quote opened says nothing on that quote", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  await open(page, "Q-1004");
  await expect(page.getByTestId("qb-line")).toHaveCount(1);
  const held = await hold(page, /\/quotes\/q-1004\/lines$/, { method: "POST" });
  const scan = page.getByTestId("pos-scan-input");
  await scan.fill("PSN-100");
  await scan.press("Enter");
  await held.asked;
  await open(page, "Q-1001");
  await expect(page.getByTestId("qb-line")).toHaveCount(2);
  held.open();
  await settle(page);
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
  held.open();
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
  held.open();
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
  held.open();
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
  held.open();
  await settle(page);
  await expect(page.getByTestId("qb-convert-done")).toHaveCount(0);
});

test("guard customer search: the account facts found for the quote just left never show on the quote now open", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  const held = await hold(page, /\/customers\/search/, { method: "GET", match: (request) => request.url().includes("Fenwick") });
  await row(page, "Q-1004").click();
  await held.asked;
  await open(page, "Q-1001");
  await expect(page.getByTestId("qb-identity-credit")).toBeVisible();
  const mapleCredit = await page.getByTestId("qb-identity").innerText();
  held.open();
  await settle(page);
  expect(await page.getByTestId("qb-identity").innerText()).toBe(mapleCredit);
  expect(mapleCredit).not.toContain("C02");
});

test("guard add-ons: the suggestions read for the quote just left never show on the quote now open", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  const held = await hold(page, /\/pos\/addon-suggestions/, { method: "GET", match: (request) => request.url().includes("p-holland-60") });
  await row(page, "Q-1001").click();
  await held.asked;
  const own = page.waitForResponse((r) => r.url().includes("/pos/addon-suggestions") && !r.url().includes("p-holland-60"));
  await open(page, "Q-1004");
  await expect(page.getByTestId("qb-line")).toHaveCount(1);
  await own;
  await settle(page);
  held.open();
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
  mint.open();
  await settle(page);
  await expect(page.getByTestId("qb-send")).toBeDisabled();
  edit.open();
  await expect(page.getByTestId("qb-send")).toBeEnabled();
});
