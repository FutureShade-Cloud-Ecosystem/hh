// The counter payment panel in a real browser, against the stubbed harness:
// the cash customer's one action (convert, invoice, pay in full, in one
// door), a credit customer's deposit with the balance the server answered, a
// replay through the same idempotency key after an unknown outcome, each
// refusal in the server's own words (403 role, 400 amounts, 409 the clerk
// wording of an order already invoiced another route, 409 the convert-first
// wording), a double click that pays once, the quote switch while a payment
// is out, and the wrong role offered the controls with the server's refusal
// shown (the app guesses nothing by role). Deterministic throughout: answers
// are held behind gates the tests open, never sleeps.
import { expect, test, type Page } from "@playwright/test";
import { armedHold, hold, settle, signIn, writes } from "./switch-helpers.js";
import { shot } from "./shot";

const pay = (page: Page) => page.locator("qd-order-payment");
const docs = (page: Page) => page.locator("qd-related-docs");

async function openQuote(page: Page, quoteNo: string): Promise<void> {
  await page.getByTestId("qb-list-row").filter({ hasText: quoteNo }).first().click();
  const detail = page.getByTestId("qb-detail");
  if ((await detail.getAttribute("open")) === null) await page.getByText("Quote detail & admin").click();
  await expect(page.getByTestId("qe-box")).toBeVisible();
}

async function openWithPayment(page: Page, quoteNo: string): Promise<void> {
  await openQuote(page, quoteNo);
  await expect(pay(page).getByTestId("op-box")).toBeVisible();
}

/** The builder's own fan-out, dispatched from a panel inside it, so the detail panels re-read as they would after an edit. */
async function dispatchReload(page: Page): Promise<void> {
  await pay(page).evaluate((el) =>
    el.dispatchEvent(new CustomEvent("quote-changed", { detail: { quoteId: "q-2101" }, bubbles: true, composed: true })),
  );
}

/** Type a payment and take it. The amount box and reference box are uncontrolled fields synced from state. */
async function take(page: Page, amount: string, reference = "", tender = "CASH"): Promise<void> {
  await pay(page).locator('[data-field="amount"]').fill(amount);
  if (reference !== "") await pay(page).locator('[data-field="reference"]').fill(reference);
  if (tender !== "CASH") await pay(page).getByTestId("op-tenders").getByRole("button", { name: tender, exact: true }).click();
  await pay(page).getByTestId("op-take").click();
}

/** The idempotency keys of every payment this page has posted, in order. */
async function postedKeys(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const w = window as unknown as { __payKeys?: string[] };
    return [...(w.__payKeys ?? [])];
  });
}

test.beforeEach(async ({ page, request }) => {
  await request.post("/_stub/reset");
  // Every payment post's body is recorded from the page, so key equality is asserted on what left the browser.
  await page.addInitScript(() => {
    const original = window.fetch.bind(window);
    window.fetch = (...args: Parameters<typeof fetch>) => {
      const [input, init] = args;
      if (init?.method === "POST" && String(input).includes("/order-payment")) {
        const body = JSON.parse(String(init.body ?? "{}")) as { idempotency_key?: string };
        const w = window as unknown as { __payKeys?: string[] };
        if (body.idempotency_key !== undefined) w.__payKeys = [...(w.__payKeys ?? []), body.idempotency_key];
      }
      return original(...args);
    };
  });
});

test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

test("the cash customer's one action converts, invoices and pays in full, and the documents appear", async ({ page }) => {
  await signIn(page);
  await openWithPayment(page, "Q-2102");
  await expect(pay(page).getByTestId("op-figures")).toContainText("CA$63.28");
  await take(page, "63.28");
  const receipt = pay(page).getByTestId("op-receipt");
  await expect(receipt).toBeVisible();
  await expect(receipt.getByTestId("op-receipt-amount")).toHaveText("CA$63.28");
  await expect(receipt.getByTestId("op-receipt-balance-on-the-invoice")).toHaveText("CA$0.00");
  await expect(receipt.getByTestId("op-receipt-tender")).toHaveText("Cash");
  await expect(receipt.getByTestId("op-receipt-received-by")).toHaveText("you");
  await expect(receipt.getByTestId("op-receipt-reference-as-recorded")).toHaveText("none given");
  await expect(pay(page).getByTestId("op-converted-note")).toContainText("the order never existed unpaid");
  // The one action changed the quote: every view re-read, so the family shows the order and the paid invoice.
  await expect(docs(page).locator(".chip").filter({ hasText: /Order SO-\d+/ })).toBeVisible();
  await expect(docs(page).locator(".chip").filter({ hasText: /Invoice INV-\d+ · Paid/ })).toBeVisible();
  await expect(page.getByTestId("qe-title")).toContainText("Converted");
  await expect(pay(page).getByTestId("op-error")).toHaveCount(0);
  await shot(pay(page).getByTestId("op-box"), "order-payment-cash-one-action");
});

test("a credit customer's deposit is taken and the balance the server answered is shown", async ({ page }) => {
  await signIn(page);
  await openWithPayment(page, "Q-2101");
  await shot(pay(page).getByTestId("op-box"), "order-payment-form");
  await take(page, "10.00", "AUTH 123", "CARD");
  const receipt = pay(page).getByTestId("op-receipt");
  await expect(receipt.getByTestId("op-receipt-amount")).toHaveText("CA$10.00");
  await expect(receipt.getByTestId("op-receipt-balance-on-the-invoice")).toHaveText("CA$27.06");
  await expect(receipt.getByTestId("op-receipt-tender")).toHaveText("Card");
  await expect(receipt.getByTestId("op-receipt-reference-as-recorded")).toHaveText("AUTH 123");
  await expect(pay(page).getByTestId("op-converted-note")).toHaveCount(0);
  // The figures line names the invoice the server raised, from the family read, never a computed figure.
  await expect(pay(page).getByTestId("op-figures")).toContainText(/Invoice INV-\d+ is Part paid at CA\$37\.06/);
  await shot(pay(page).getByTestId("op-box"), "order-payment-deposit");
  // The dark theme, cycled from Auto through Light to Dark.
  await page.getByTestId("theme-control").click();
  await page.getByTestId("theme-control").click();
  await shot(pay(page).getByTestId("op-box"), "order-payment-deposit-dark");
});

test("an unknown outcome says so plainly and a retry with the SAME key replays the first result", async ({ page }) => {
  await signIn(page);
  await openWithPayment(page, "Q-2101");
  await page.request.put("/_stub/payment-ghost", { data: { ghost: true } });
  await take(page, "10.00", "AUTH 9", "CARD");
  await expect(pay(page).getByTestId("op-unknown")).toContainText("The outcome of that payment is not known");
  await expect(pay(page).getByTestId("op-unknown")).toContainText("safe");
  await expect(pay(page).getByTestId("op-receipt")).toHaveCount(0);
  // The retry: same amount, same method, and the SAME idempotency key, asserted on what left the page.
  await pay(page).getByTestId("op-take").click();
  const receipt = pay(page).getByTestId("op-receipt");
  await expect(receipt).toBeVisible();
  await expect(receipt.getByTestId("op-receipt-amount")).toHaveText("CA$10.00");
  await expect(receipt.getByTestId("op-receipt-balance-on-the-invoice")).toHaveText("CA$27.06");
  await expect(pay(page).getByTestId("op-replayed-note")).toContainText("nothing was posted a second time");
  await expect(pay(page).getByTestId("op-unknown")).toHaveCount(0);
  const keys = await postedKeys(page);
  expect(keys).toHaveLength(2);
  expect(keys[0]).toBe(keys[1]);
});

test("an unknown outcome with no answer at all keeps the key, and the retry posts once", async ({ page }) => {
  await signIn(page);
  await openWithPayment(page, "Q-2101");
  const swallow = async (route: Parameters<Parameters<typeof page.route>[1]>[0]): Promise<void> => {
    if (route.request().method() === "POST") return route.abort();
    return route.continue();
  };
  await page.route(/\/order-payment$/, swallow);
  await take(page, "5.00");
  await expect(pay(page).getByTestId("op-unknown")).toBeVisible();
  await page.unroute(/\/order-payment$/, swallow);
  await pay(page).getByTestId("op-take").click();
  await expect(pay(page).getByTestId("op-receipt-amount")).toHaveText("CA$5.00");
  await expect(pay(page).getByTestId("op-replayed-note")).toHaveCount(0);
  const keys = await postedKeys(page);
  expect(keys).toHaveLength(2);
  expect(keys[0]).toBe(keys[1]);
});

test("a refusal keeps the key, so the corrected retry is the same attempt", async ({ page }) => {
  await signIn(page);
  await openWithPayment(page, "Q-2101");
  // Over the balance: the server's 400 in its own words.
  await take(page, "99.00");
  await expect(pay(page).getByTestId("op-error")).toHaveText("The payment is more than the balance of $37.06");
  await take(page, "37.06");
  await expect(pay(page).getByTestId("op-receipt-amount")).toHaveText("CA$37.06");
  const keys = await postedKeys(page);
  expect(keys).toHaveLength(2);
  expect(keys[0]).toBe(keys[1]);
  await expect(pay(page).getByTestId("op-replayed-note")).toHaveCount(0);
});

test("a new deliberate payment after a taken one mints a fresh key", async ({ page }) => {
  await signIn(page);
  // Seed a second credit quote so the panel's first payment is a deposit and the second is another payment.
  await openWithPayment(page, "Q-2101");
  await take(page, "10.00");
  await expect(pay(page).getByTestId("op-receipt")).toBeVisible();
  await take(page, "10.00");
  const receipt = pay(page).getByTestId("op-receipt");
  await expect(receipt.getByTestId("op-receipt-amount")).toHaveText("CA$10.00");
  await expect(receipt.getByTestId("op-receipt-balance-on-the-invoice")).toHaveText("CA$17.06");
  await expect(pay(page).getByTestId("op-replayed-note")).toHaveCount(0);
  const keys = await postedKeys(page);
  expect(keys).toHaveLength(2);
  expect(keys[0]).not.toBe(keys[1]);
});

test("a double click pays once", async ({ page }) => {
  await signIn(page);
  await openWithPayment(page, "Q-2101");
  await pay(page).locator('[data-field="amount"]').fill("37.06");
  await pay(page).getByTestId("op-take").dblclick();
  await expect(pay(page).getByTestId("op-receipt")).toBeVisible();
  await settle(page);
  const keys = await postedKeys(page);
  expect(keys).toHaveLength(1);
  // One payment, one receipt, no second post behind it.
  await expect(pay(page).getByTestId("op-error")).toHaveCount(0);
});

test("the wrong role is offered the controls and shown the server's refusal; the app guesses nothing by role", async ({ page }) => {
  await signIn(page);
  await page.request.put("/_stub/payment-role", { data: { refused: true } });
  await openWithPayment(page, "Q-2101");
  await expect(pay(page).getByTestId("op-take")).toBeEnabled();
  await take(page, "10.00");
  await expect(pay(page).getByTestId("op-error")).toHaveText("Forbidden");
  await expect(pay(page).getByTestId("op-receipt")).toHaveCount(0);
});

test("an order already invoiced another route is refused with the clerk wording, naming who can record it", async ({ page }) => {
  await signIn(page);
  await page.request.post("/_stub/foreign-invoice", { data: { quote_id: "q-2101" } });
  await openWithPayment(page, "Q-2101");
  await take(page, "37.06");
  await expect(pay(page).getByTestId("op-error")).toContainText("already invoiced as INV-3001 by another route");
  await expect(pay(page).getByTestId("op-error")).toContainText("who record the payment against that invoice");
  await shot(pay(page).getByTestId("op-box"), "order-payment-refused-clerk");
});

test("an accepted quote of a customer with credit is refused with the convert-first wording", async ({ page }) => {
  await signIn(page);
  await openWithPayment(page, "Q-1003");
  await take(page, "10.00");
  await expect(pay(page).getByTestId("op-error")).toContainText("Convert this quote to an order first");
  await expect(pay(page).getByTestId("op-error")).toContainText("converts through the conversion door and then pays");
});

test("a cash customer's one action with the wrong amount is refused in the server's words about the whole total", async ({ page }) => {
  await signIn(page);
  await openWithPayment(page, "Q-2102");
  await take(page, "10.00");
  await expect(pay(page).getByTestId("op-error")).toContainText("A customer with no credit account pays the whole agreed total of $63.28");
  await expect(pay(page).getByTestId("op-receipt")).toHaveCount(0);
  // Nothing was converted by the refused attempt.
  await expect(page.getByTestId("qe-title")).toContainText("Accepted");
});

test("a payment that lands after the person left says nothing on the quote now open, and is said on return", async ({ page }) => {
  await signIn(page);
  await openWithPayment(page, "Q-2101");
  const held = await hold(page, /\/api\/v1\/quotes\/q-2101\/order-payment$/, { method: "POST", snapshot: true });
  await take(page, "10.00");
  await held.asked;
  // The payment is out; the person opens another quote.
  await page.getByTestId("qb-list-row").filter({ hasText: "Q-1001" }).first().click();
  await expect(page.getByTestId("qe-title")).toContainText("Q-1001");
  await held.release();
  await settle(page);
  // The draft quote never grows a receipt, an error or the payment box at all.
  await expect(page.locator("qd-order-payment").getByTestId("op-box")).toHaveCount(0);
  // Coming back to the quote that was paid: the note says the receipt went with the answer, and the documents show the invoice.
  await page.getByTestId("qb-list-row").filter({ hasText: "Q-2101" }).first().click();
  await expect(pay(page).getByTestId("op-notice")).toContainText("A payment was taken on this quote while you were away");
  await expect(docs(page).locator(".chip").filter({ hasText: /Invoice INV-\d+ · Part paid/ })).toBeVisible();
});

test("a payment refused while the person is on another quote says nothing there", async ({ page }) => {
  await signIn(page);
  await openWithPayment(page, "Q-2101");
  const held = await hold(page, /\/api\/v1\/quotes\/q-2101\/order-payment$/, {
    method: "POST",
    refuse: { status: 400, error: "the payment is more than the balance of $37.06" },
  });
  await take(page, "99.00");
  await held.asked;
  // The other quote is one the payment panel is offered on, so a late refusal would have somewhere to land.
  await page.getByTestId("qb-list-row").filter({ hasText: "Q-2102" }).first().click();
  await expect(page.getByTestId("qe-title")).toContainText("Q-2102");
  await held.release();
  await settle(page);
  await expect(pay(page).getByTestId("op-error")).toHaveCount(0);
  await expect(pay(page).getByTestId("op-unknown")).toHaveCount(0);
});

test("guard payment read: a read for the quote left never lands on the one open now", async ({ page }) => {
  await signIn(page);
  await openWithPayment(page, "Q-2101");
  // The gate is armed before the read it holds is asked for: a reload of the open quote's panels.
  // Both panels' family reads are held (the payment's and the document family's), so whichever
  // lands late is covered and the catch does not depend on which one asked first.
  const gated = await armedHold(page, /\/api\/v1\/quotes\/q-2101\/related-documents$/, "GET", { times: 2, snapshot: true });
  gated.arm();
  await dispatchReload(page);
  await expect.poll(() => gated.held.seen.length).toBe(2);
  await page.getByTestId("qb-list-row").filter({ hasText: "Q-1001" }).first().click();
  await expect(page.getByTestId("qe-title")).toContainText("Q-1001");
  await gated.held.release();
  await settle(page);
  // The draft quote never grows the payment controls of the converted one.
  await expect(page.locator("qd-order-payment").getByTestId("op-box")).toHaveCount(0);
  await expect(docs(page).locator(".chip").filter({ hasText: "SO-2001" })).toHaveCount(0);
});

test("guard payment read order: an older read of the quote answered late never lands over a newer one", async ({ page }) => {
  await signIn(page);
  await openWithPayment(page, "Q-2101");
  const gated = await armedHold(page, /\/api\/v1\/quotes\/q-2101\/related-documents$/, "GET", {
    times: 2,
    refuse: { status: 500, error: "internal" },
  });
  gated.arm();
  await dispatchReload(page);
  await expect.poll(() => gated.held.seen.length).toBe(2);
  // A newer read of the same quote begins and succeeds while the older ones are still out.
  await dispatchReload(page);
  await expect(pay(page).getByTestId("op-figures")).toContainText("CA$37.06");
  await gated.held.release();
  await settle(page);
  await expect(pay(page).getByTestId("op-unread")).toHaveCount(0);
  await expect(pay(page).getByTestId("op-figures")).toContainText("CA$37.06");
  await expect(docs(page).getByTestId("rd-error")).toHaveCount(0);
});

test("a payment whose outcome is not known is kept for its quote across switches: the note is there on return and the retry reuses the SAME key", async ({ page }) => {
  await signIn(page);
  await openWithPayment(page, "Q-2101");
  await page.request.put("/_stub/payment-ghost", { data: { ghost: true } });
  await take(page, "10.00", "AUTH 9", "CARD");
  await expect(pay(page).getByTestId("op-unknown")).toContainText("not known");
  // The person opens another quote and comes back.
  await page.getByTestId("qb-list-row").filter({ hasText: "Q-1001" }).first().click();
  await expect(page.getByTestId("qe-title")).toContainText("Q-1001");
  await page.getByTestId("qb-list-row").filter({ hasText: "Q-2101" }).first().click();
  await expect(pay(page).getByTestId("op-box")).toBeVisible();
  // The not-known note survived the switch, the attempt's figures are still held, and the retry is the SAME attempt.
  await expect(pay(page).getByTestId("op-unknown")).toContainText("not known");
  await expect(pay(page).getByTestId("op-take")).toBeEnabled();
  await pay(page).getByTestId("op-take").click();
  const receipt = pay(page).getByTestId("op-receipt");
  await expect(receipt.getByTestId("op-receipt-amount")).toHaveText("CA$10.00");
  await expect(receipt.getByTestId("op-receipt-balance-on-the-invoice")).toHaveText("CA$27.06");
  await expect(pay(page).getByTestId("op-replayed-note")).toContainText("nothing was posted a second time");
  const keys = await postedKeys(page);
  expect(keys).toHaveLength(2);
  expect(keys[0]).toBe(keys[1]);
});

test("a payment still out for its quote keeps the controls held on return, and lands once", async ({ page }) => {
  await signIn(page);
  await openWithPayment(page, "Q-2101");
  const held = await hold(page, /\/api\/v1\/quotes\/q-2101\/order-payment$/, { method: "POST", snapshot: true });
  await take(page, "10.00");
  await held.asked;
  await page.getByTestId("qb-list-row").filter({ hasText: "Q-1001" }).first().click();
  await expect(page.getByTestId("qe-title")).toContainText("Q-1001");
  await page.getByTestId("qb-list-row").filter({ hasText: "Q-2101" }).first().click();
  await expect(pay(page).getByTestId("op-box")).toBeVisible();
  // The attempt is still out for this quote: the button is held and the fields are locked to it, so no second key can leave.
  await expect(pay(page).getByTestId("op-take")).toBeDisabled();
  await expect(pay(page).locator('[data-field="amount"]')).toBeDisabled();
  await held.release();
  await settle(page);
  const receipt = pay(page).getByTestId("op-receipt");
  await expect(receipt.getByTestId("op-receipt-amount")).toHaveText("CA$10.00");
  await expect(receipt.getByTestId("op-receipt-balance-on-the-invoice")).toHaveText("CA$27.06");
  const keys = await postedKeys(page);
  expect(keys).toHaveLength(1);
});

test("a late answer with no readable outcome is recorded for its own quote only", async ({ page }) => {
  await signIn(page);
  await openWithPayment(page, "Q-2101");
  const held = await hold(page, /\/api\/v1\/quotes\/q-2101\/order-payment$/, { method: "POST", times: 1, refuse: { status: 500, error: "internal" } });
  await take(page, "10.00");
  await held.asked;
  // The other quote is one the payment panel is offered on, so an unknown note leaking there has somewhere to land.
  await page.getByTestId("qb-list-row").filter({ hasText: "Q-2102" }).first().click();
  await expect(page.getByTestId("qe-title")).toContainText("Q-2102");
  await held.release();
  await settle(page);
  await expect(pay(page).getByTestId("op-unknown")).toHaveCount(0);
  await expect(pay(page).getByTestId("op-error")).toHaveCount(0);
  await expect(pay(page).getByTestId("op-receipt")).toHaveCount(0);
  // Back on the quote the payment belongs to: the not-known note is there, the fields are held, and the retry is the same attempt.
  await page.getByTestId("qb-list-row").filter({ hasText: "Q-2101" }).first().click();
  await expect(pay(page).getByTestId("op-unknown")).toContainText("not known");
  await expect(pay(page).locator('[data-field="amount"]')).toBeDisabled();
  await pay(page).getByTestId("op-take").click();
  await expect(pay(page).getByTestId("op-receipt-amount")).toHaveText("CA$10.00");
  const keys = await postedKeys(page);
  expect(keys).toHaveLength(2);
  expect(keys[0]).toBe(keys[1]);
});

test("an amount that is not a number this desk can send is refused before any post", async ({ page }) => {
  await signIn(page);
  await openWithPayment(page, "Q-2101");
  const seen = writes(page);
  await pay(page).locator('[data-field="amount"]').fill("1.005");
  await pay(page).getByTestId("op-take").click();
  await expect(pay(page).getByTestId("op-error")).toContainText("not a number this desk can send");
  expect(seen.filter((line) => line.includes("order-payment"))).toEqual([]);
});
