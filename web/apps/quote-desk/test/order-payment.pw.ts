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
import { armedHold, countRequests, hold, settle, showList, signIn, writes } from "./switch-helpers.js";
import { shot } from "./shot";

const pay = (page: Page) => page.locator("qd-order-payment");
const docs = (page: Page) => page.locator("qd-related-docs");

async function openQuote(page: Page, quoteNo: string): Promise<void> {
  await showList(page);
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

/** Record the payment posts' keys from a page. A second tab of the context needs its own watcher. */
function watchKeys(page: Page): void {
  page.addInitScript(() => {
    const w = window as unknown as { __payKeys?: string[] };
    if (w.__payKeys !== undefined) return;
    const original = window.fetch.bind(window);
    window.fetch = (...args: Parameters<typeof fetch>) => {
      const [input, init] = args;
      if (init?.method === "POST" && String(input).includes("/order-payment")) {
        const body = JSON.parse(String(init.body ?? "{}")) as { idempotency_key?: string };
        const seen = window as unknown as { __payKeys?: string[] };
        if (body.idempotency_key !== undefined) seen.__payKeys = [...(seen.__payKeys ?? []), body.idempotency_key];
      }
      return original(...args);
    };
  });
}

test.beforeEach(async ({ page, request }) => {
  await request.post("/_stub/reset");
  // Every payment post's body is recorded from the page, so key equality is asserted on what left the browser.
  watchKeys(page);
});

test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

test("a credit customer's deposit is taken and the balance the server answered is shown", async ({ page }) => {
  await signIn(page);
  await openWithPayment(page, "Q-2101");
  await shot(pay(page).getByTestId("op-box"), "order-payment-form");
  await take(page, "10.00", "AUTH 123", "CARD");
  const receipt = pay(page).getByTestId("op-receipt");
  await expect(receipt.getByTestId("op-receipt-amount")).toHaveText("$10.00");
  await expect(receipt.getByTestId("op-receipt-balance-on-the-invoice")).toHaveText("$27.06");
  await expect(receipt.getByTestId("op-receipt-tender")).toHaveText("Card");
  await expect(receipt.getByTestId("op-receipt-reference-as-recorded")).toHaveText("AUTH 123");
  await expect(pay(page).getByTestId("op-converted-note")).toHaveCount(0);
  // The figures line names the invoice the server raised, from the family read, never a computed figure.
  await expect(pay(page).getByTestId("op-figures")).toContainText(/Invoice INV-\d+ is Part paid at \$37\.06/);
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
  await expect(receipt.getByTestId("op-receipt-amount")).toHaveText("$10.00");
  await expect(receipt.getByTestId("op-receipt-balance-on-the-invoice")).toHaveText("$27.06");
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
  await expect(pay(page).getByTestId("op-receipt-amount")).toHaveText("$5.00");
  await expect(pay(page).getByTestId("op-replayed-note")).toHaveCount(0);
  const keys = await postedKeys(page);
  expect(keys).toHaveLength(2);
  expect(keys[0]).toBe(keys[1]);
});

test("a refusal keeps the key, so the corrected retry is the same attempt", async ({ page }) => {
  await signIn(page);
  await openWithPayment(page, "Q-2101");
  await take(page, "10.00");
  await expect(pay(page).getByTestId("op-receipt")).toBeVisible();
  // Reload: the desk no longer holds the receipt that answered the balance, and the family read
  // carries the part-paid state but not the figure, so the balance is the server's to name.
  await page.reload();
  await signIn(page);
  await openWithPayment(page, "Q-2101");
  await expect(pay(page).getByTestId("op-figures")).toContainText("not carried by this read");
  // Over the balance: the server's 400 in its own words.
  await take(page, "99.00");
  await expect(pay(page).getByTestId("op-error")).toHaveText("The payment is more than the balance of $27.06");
  await take(page, "27.06");
  await expect(pay(page).getByTestId("op-receipt-amount")).toHaveText("$27.06");
  const keys = await postedKeys(page);
  expect(keys).toHaveLength(2);
  expect(keys[0]).toBe(keys[1]);
  await expect(pay(page).getByTestId("op-replayed-note")).toHaveCount(0);
});

test("the figures name the balance the reads carry, and a payment over it is refused before anything is sent", async ({ page }) => {
  await signIn(page);
  await openWithPayment(page, "Q-2101");
  // No invoice yet: the door raises the order's agreed invoice, whose opening balance is the agreed total.
  await expect(pay(page).getByTestId("op-figures")).toContainText("balance $37.06");
  const seen = writes(page);
  await take(page, "99.00");
  await expect(pay(page).getByTestId("op-error")).toHaveText("The payment is more than the balance of $37.06");
  expect(seen.filter((line) => line.includes("order-payment"))).toEqual([]);
  // After a deposit, the balance the receipt answered is the live one the reads carry.
  await take(page, "10.00");
  await expect(pay(page).getByTestId("op-receipt-balance-on-the-invoice")).toHaveText("$27.06");
  await expect(pay(page).getByTestId("op-figures")).toContainText("balance $27.06");
  await take(page, "30.00");
  await expect(pay(page).getByTestId("op-error")).toHaveText("The payment is more than the balance of $27.06");
});

test("a new deliberate payment after a taken one mints a fresh key", async ({ page }) => {
  await signIn(page);
  // Seed a second credit quote so the panel's first payment is a deposit and the second is another payment.
  await openWithPayment(page, "Q-2101");
  await take(page, "10.00");
  await expect(pay(page).getByTestId("op-receipt")).toBeVisible();
  await take(page, "10.00");
  const receipt = pay(page).getByTestId("op-receipt");
  await expect(receipt.getByTestId("op-receipt-amount")).toHaveText("$10.00");
  await expect(receipt.getByTestId("op-receipt-balance-on-the-invoice")).toHaveText("$17.06");
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
  // The gate's body is the single word "Forbidden": the desk says what that means and what to do.
  await expect(pay(page).getByTestId("op-error")).toHaveText("Taking a payment on this order is not open to your login. Ask a manager or the counter.");
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

test("a payment that lands after the person left says nothing on the quote now open, and is said on return", async ({ page }) => {
  await signIn(page);
  await openWithPayment(page, "Q-2101");
  const held = await hold(page, /\/api\/v1\/quotes\/q-2101\/order-payment$/, { method: "POST", snapshot: true });
  await take(page, "10.00");
  await held.asked;
  // The payment is out; the person opens another quote the payment panel is offered on, so a
  // receipt, an error or a note leaking onto it has somewhere to land and would be seen.
  await showList(page);
  await page.getByTestId("qb-list-row").filter({ hasText: "Q-2102" }).first().click();
  await expect(page.getByTestId("qe-title")).toContainText("Q-2102");
  await held.release();
  await settle(page);
  await expect(pay(page).getByTestId("op-receipt")).toHaveCount(0);
  await expect(pay(page).getByTestId("op-error")).toHaveCount(0);
  await expect(pay(page).getByTestId("op-unknown")).toHaveCount(0);
  await expect(pay(page).getByTestId("op-notice")).toHaveCount(0);
  // Coming back to the quote that was paid: the note says the receipt is below, with the
  // server's own figures, and the documents show the invoice.
  await showList(page);
  await page.getByTestId("qb-list-row").filter({ hasText: "Q-2101" }).first().click();
  await expect(pay(page).getByTestId("op-notice")).toContainText("A payment was taken on this quote while you were away");
  await expect(pay(page).getByTestId("op-receipt-amount")).toHaveText("$10.00");
  await expect(pay(page).getByTestId("op-receipt-balance-on-the-invoice")).toHaveText("$27.06");
  await expect(docs(page).locator(".chip").filter({ hasText: /Invoice INV-\d+ · Part paid/ })).toBeVisible();
});

test("a payment refused while the person is on another quote says nothing there", async ({ page }) => {
  await signIn(page);
  await openWithPayment(page, "Q-2101");
  // A role refusal, which the desk cannot predict and so always sends.
  const held = await hold(page, /\/api\/v1\/quotes\/q-2101\/order-payment$/, {
    method: "POST",
    refuse: { status: 403, error: "Forbidden" },
  });
  await take(page, "10.00");
  await held.asked;
  // The other quote is one the payment panel is offered on, so a late refusal would have somewhere to land.
  await showList(page);
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
  const gated = await armedHold(page, /\/api\/v1\/quotes\/q-2101\/related-documents$/, "GET", { times: 1, snapshot: true });
  gated.arm();
  await dispatchReload(page);
  await expect.poll(() => gated.held.seen.length).toBe(1);
  await showList(page);
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
    times: 1,
    refuse: { status: 500, error: "internal" },
  });
  gated.arm();
  await dispatchReload(page);
  await expect.poll(() => gated.held.seen.length).toBe(1);
  // A newer read of the same quote (a new share cycle) begins and succeeds while the older one is still out.
  await dispatchReload(page);
  await expect(pay(page).getByTestId("op-figures")).toContainText("$37.06");
  await gated.held.release();
  await settle(page);
  await expect(pay(page).getByTestId("op-unread")).toHaveCount(0);
  await expect(pay(page).getByTestId("op-figures")).toContainText("$37.06");
  await expect(docs(page).getByTestId("rd-error")).toHaveCount(0);
});

test("a payment whose outcome is not known is kept for its quote across switches: the note is there on return and the retry reuses the SAME key", async ({ page }) => {
  await signIn(page);
  await openWithPayment(page, "Q-2101");
  await page.request.put("/_stub/payment-ghost", { data: { ghost: true } });
  await take(page, "10.00", "AUTH 9", "CARD");
  await expect(pay(page).getByTestId("op-unknown")).toContainText("not known");
  // The person opens another quote and comes back.
  await showList(page);
  await page.getByTestId("qb-list-row").filter({ hasText: "Q-1001" }).first().click();
  await expect(page.getByTestId("qe-title")).toContainText("Q-1001");
  await showList(page);
  await page.getByTestId("qb-list-row").filter({ hasText: "Q-2101" }).first().click();
  await expect(pay(page).getByTestId("op-box")).toBeVisible();
  // The not-known note survived the switch, the attempt's figures are still held, and the retry is the SAME attempt.
  await expect(pay(page).getByTestId("op-unknown")).toContainText("not known");
  await expect(pay(page).getByTestId("op-take")).toBeEnabled();
  await pay(page).getByTestId("op-take").click();
  const receipt = pay(page).getByTestId("op-receipt");
  await expect(receipt.getByTestId("op-receipt-amount")).toHaveText("$10.00");
  await expect(receipt.getByTestId("op-receipt-balance-on-the-invoice")).toHaveText("$27.06");
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
  await showList(page);
  await page.getByTestId("qb-list-row").filter({ hasText: "Q-1001" }).first().click();
  await expect(page.getByTestId("qe-title")).toContainText("Q-1001");
  await showList(page);
  await page.getByTestId("qb-list-row").filter({ hasText: "Q-2101" }).first().click();
  await expect(pay(page).getByTestId("op-box")).toBeVisible();
  // The attempt is still out for this quote: the button is held and the fields are locked to it, so no second key can leave.
  await expect(pay(page).getByTestId("op-take")).toBeDisabled();
  await expect(pay(page).locator('[data-field="amount"]')).toBeDisabled();
  await held.release();
  await settle(page);
  const receipt = pay(page).getByTestId("op-receipt");
  await expect(receipt.getByTestId("op-receipt-amount")).toHaveText("$10.00");
  await expect(receipt.getByTestId("op-receipt-balance-on-the-invoice")).toHaveText("$27.06");
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
  await showList(page);
  await page.getByTestId("qb-list-row").filter({ hasText: "Q-2102" }).first().click();
  await expect(page.getByTestId("qe-title")).toContainText("Q-2102");
  await held.release();
  await settle(page);
  await expect(pay(page).getByTestId("op-unknown")).toHaveCount(0);
  await expect(pay(page).getByTestId("op-error")).toHaveCount(0);
  await expect(pay(page).getByTestId("op-receipt")).toHaveCount(0);
  // Back on the quote the payment belongs to: the not-known note is there, the fields are held, and the retry is the same attempt.
  await showList(page);
  await page.getByTestId("qb-list-row").filter({ hasText: "Q-2101" }).first().click();
  await expect(pay(page).getByTestId("op-unknown")).toContainText("not known");
  await expect(pay(page).locator('[data-field="amount"]')).toBeDisabled();
  await pay(page).getByTestId("op-take").click();
  await expect(pay(page).getByTestId("op-receipt-amount")).toHaveText("$10.00");
  const keys = await postedKeys(page);
  expect(keys).toHaveLength(2);
  expect(keys[0]).toBe(keys[1]);
});

test("New Quote keeps the payment seam mounted, so a payment that lands while compose is open is on its attempt when the person returns", async ({ page }) => {
  await signIn(page);
  await openWithPayment(page, "Q-2101");
  const held = await hold(page, /\/api\/v1\/quotes\/q-2101\/order-payment$/, { method: "POST", snapshot: true });
  await take(page, "10.00");
  await held.asked;
  await page.getByRole("button", { name: "New Quote" }).click();
  await expect(page.getByTestId("qb-title")).toHaveText("New Quote");
  // The seam's panels stay mounted through compose (they render nothing for no quote), so the
  // payment still out keeps its element, its context and its per quote record.
  await expect(page.locator("qd-order-payment")).toHaveCount(1);
  await expect(page.locator("qd-related-docs")).toHaveCount(1);
  await held.release();
  await settle(page);
  // Back on the quote that was paid: the note is there, the receipt is there, and only one
  // payment ever left the browser.
  await showList(page);
  await page.getByTestId("qb-list-row").filter({ hasText: "Q-2101" }).first().click();
  await expect(pay(page).getByTestId("op-notice")).toContainText("A payment was taken on this quote while you were away");
  const receipt = pay(page).getByTestId("op-receipt");
  await expect(receipt.getByTestId("op-receipt-amount")).toHaveText("$10.00");
  await expect(receipt.getByTestId("op-receipt-balance-on-the-invoice")).toHaveText("$27.06");
  const keys = await postedKeys(page);
  expect(keys).toHaveLength(1);
});

test("a ghosted 10 cash cannot become a 5 card: while the outcome is not known the fields are locked to the attempt, and the way through is the retry or a fresh payment", async ({ page }) => {
  await signIn(page);
  await openWithPayment(page, "Q-2101");
  await page.request.put("/_stub/payment-ghost", { data: { ghost: true } });
  await take(page, "10.00", "AUTH 9", "CARD");
  await expect(pay(page).getByTestId("op-unknown")).toContainText("not known");
  // The reviewer's sequence: the cashier tries to change the payment to 5 CARD while the
  // outcome is not known. The form does not offer the change: the fields are locked to the
  // attempt, so no post can carry one key with two different payments.
  await expect(pay(page).locator('[data-field="amount"]')).toBeDisabled();
  await expect(pay(page).getByTestId("op-tenders").getByRole("button", { name: "CASH", exact: true })).toHaveAttribute("aria-disabled", "true");
  // The retry is the same attempt: the same key replays the ghost's payment.
  await pay(page).getByTestId("op-take").click();
  await expect(pay(page).getByTestId("op-receipt-amount")).toHaveText("$10.00");
  await expect(pay(page).getByTestId("op-receipt-balance-on-the-invoice")).toHaveText("$27.06");
  let keys = await postedKeys(page);
  expect(keys).toHaveLength(2);
  expect(keys[0]).toBe(keys[1]);
  // A new deliberate payment after the taken one mints a fresh key and may be 5 CARD.
  await page.request.put("/_stub/payment-ghost", { data: { ghost: false } });
  await take(page, "5.00", "", "CARD");
  await expect(pay(page).getByTestId("op-receipt-amount")).toHaveText("$5.00");
  await expect(pay(page).getByTestId("op-receipt-balance-on-the-invoice")).toHaveText("$22.06");
  keys = await postedKeys(page);
  expect(keys).toHaveLength(3);
  expect(keys[2]).not.toBe(keys[0]);
});

test("a 409 that says the key took a different payment holds the attempt instead of looping: the first payment's figures stay and a new payment starts only after a fresh read", async ({ page }) => {
  await signIn(page);
  await openWithPayment(page, "Q-2101");
  await page.request.put("/_stub/payment-key-crossed", { data: { crossed: true } });
  await take(page, "10.00", "AUTH 1", "CARD");
  // The server's own sentence, said plainly.
  await expect(pay(page).getByTestId("op-error")).toHaveText("This idempotency key took a different payment; use a new key for a new payment");
  // The first attempt's details stay visible and locked: the amount box holds the 10.00 that was sent.
  await expect(pay(page).locator('[data-field="amount"]')).toBeDisabled();
  await expect(pay(page).locator('[data-field="amount"]')).toHaveValue("10.00");
  await expect(pay(page).getByTestId("op-start-new")).toBeVisible();
  // A retry is still offered and still refused in the same words: no second payment is taken.
  await pay(page).getByTestId("op-take").click();
  await expect(pay(page).getByTestId("op-error")).toHaveText("This idempotency key took a different payment; use a new key for a new payment");
  await expect(pay(page).getByTestId("op-receipt")).toHaveCount(0);
  let keys = await postedKeys(page);
  expect(keys).toHaveLength(2);
  expect(keys[0]).toBe(keys[1]);
  // The way forward: start a new payment, which re-reads the family first, so what landed (the
  // crossed key's CARD 5.00) is on the screen before the form unlocks.
  await pay(page).getByTestId("op-start-new").click();
  await expect(pay(page).getByTestId("op-figures")).toContainText("Part paid");
  await expect(pay(page).getByTestId("op-start-new")).toHaveCount(0);
  await expect(pay(page).getByTestId("op-error")).toHaveCount(0);
  await expect(pay(page).locator('[data-field="amount"]')).toBeEnabled();
  await page.request.put("/_stub/payment-key-crossed", { data: { crossed: false } });
  await take(page, "5.00", "", "CARD");
  await expect(pay(page).getByTestId("op-receipt-amount")).toHaveText("$5.00");
  keys = await postedKeys(page);
  expect(keys).toHaveLength(3);
  expect(keys[2]).not.toBe(keys[0]);
});

test("a decimal comma is refused in words that teach the point, never sent, and the parsed figure is shown beside the button before it is pressed", async ({ page }) => {
  await signIn(page);
  await openWithPayment(page, "Q-2101");
  const seen = writes(page);
  for (const bad of ["0,50", "12,50", "1,5", "1 5"]) {
    await pay(page).locator('[data-field="amount"]').fill(bad);
    await pay(page).getByTestId("op-take").click();
    await expect(pay(page).getByTestId("op-error")).toHaveText("Use a point for cents, for example 12.50.");
    await expect(pay(page).getByTestId("op-preview")).toHaveCount(0);
  }
  expect(seen.filter((line) => line.includes("order-payment"))).toEqual([]);
  // The parsed figure is on the screen before Take payment is pressed, in the receipt's own money word.
  await pay(page).locator('[data-field="amount"]').fill("12.50");
  await expect(pay(page).getByTestId("op-preview")).toHaveText("Taking $12.50 by Cash");
  await pay(page).getByTestId("op-tenders").getByRole("button", { name: "CARD", exact: true }).click();
  await expect(pay(page).getByTestId("op-preview")).toHaveText("Taking $12.50 by Card");
  // An amount no counter can name exactly is refused in words too.
  await pay(page).locator('[data-field="amount"]').fill("99999999999999999999");
  await pay(page).getByTestId("op-take").click();
  await expect(pay(page).getByTestId("op-error")).toHaveText("The amount is larger than this desk can send.");
});

test("the payment form follows the till's tile language: a 2x2 grid of icon tiles, full height controls, a capped reference with its count, and the chosen tile visible in flight", async ({ page }) => {
  await signIn(page);
  await openWithPayment(page, "Q-2101");
  const tiles = pay(page).getByTestId("op-tenders").getByRole("button");
  await expect(tiles).toHaveCount(4);
  // A 2x2 grid: the first two tiles share a row, the third starts the second.
  const group = (await pay(page).getByTestId("op-tenders").boundingBox())!;
  const cash = (await tiles.nth(0).boundingBox())!;
  const card = (await tiles.nth(1).boundingBox())!;
  const cheque = (await tiles.nth(2).boundingBox())!;
  expect(cash.y).toBe(card.y);
  expect(cheque.y).toBeGreaterThan(cash.y);
  expect(cash.width).toBeLessThan(group.width * 0.6);
  // Every tile is full height and carries the till's drawn glyph.
  for (let i = 0; i < 4; i += 1) {
    expect((await tiles.nth(i).boundingBox())!.height).toBeGreaterThanOrEqual(38);
    await expect(tiles.nth(i).locator("svg")).toHaveCount(1);
  }
  // The amount box and the Take payment action are at least as tall as a tile.
  expect((await pay(page).locator('[data-field="amount"]').boundingBox())!.height).toBeGreaterThanOrEqual(38);
  expect((await pay(page).getByTestId("op-take").boundingBox())!.height).toBeGreaterThanOrEqual(38);
  // The reference is capped at the server's own 80 characters, with the count beside the box.
  await expect(pay(page).locator('[data-field="reference"]')).toHaveAttribute("maxlength", "80");
  await pay(page).locator('[data-field="reference"]').fill("AUTH 123");
  await expect(pay(page).getByTestId("op-ref-count")).toHaveText("8/80");
  // The chosen tile stays visible while the payment is out: its selection is a fact being
  // carried, so it keeps its ink and its pressed state rather than greying away.
  const held = await hold(page, /\/api\/v1\/quotes\/q-2101\/order-payment$/, { method: "POST", snapshot: true });
  await pay(page).locator('[data-field="amount"]').fill("10.00");
  await pay(page).getByTestId("op-take").click();
  await held.asked;
  const chosen = pay(page).getByTestId("op-tenders").getByRole("button", { name: "CASH", exact: true });
  await expect(chosen).toHaveClass(/active/);
  await expect(chosen).toHaveAttribute("aria-pressed", "true");
  await held.release();
  await settle(page);
  await expect(pay(page).getByTestId("op-receipt")).toBeVisible();
});

test("a paid invoice shows a Paid in full state in place of the form", async ({ page }) => {
  await signIn(page);
  await openWithPayment(page, "Q-2101");
  await take(page, "37.06");
  await expect(pay(page).getByTestId("op-receipt-balance-on-the-invoice")).toHaveText("$0.00");
  await expect(pay(page).getByTestId("op-paid")).toContainText("Paid in full");
  // No live amount, no tender, no Take payment when nothing is owed.
  await expect(pay(page).getByTestId("op-take")).toHaveCount(0);
  await expect(pay(page).locator('[data-field="amount"]')).toHaveCount(0);
  await expect(pay(page).getByTestId("op-tenders")).toHaveCount(0);
  // The receipt of the payment that settled it stays.
  await expect(pay(page).getByTestId("op-receipt-amount")).toHaveText("$37.06");
  await expect(pay(page).getByTestId("op-receipt-earlier")).toHaveCount(0);
});

test("the receipt of an earlier payment is marked as that, never as the result of a refusal under it", async ({ page }) => {
  await signIn(page);
  await openWithPayment(page, "Q-2101");
  await take(page, "10.00");
  await expect(pay(page).getByTestId("op-receipt-amount")).toHaveText("$10.00");
  await expect(pay(page).getByTestId("op-receipt-earlier")).toHaveCount(0);
  // A second attempt, refused before it is sent: the receipt stays, marked as the earlier
  // payment's, so it cannot read as this refusal's result.
  await take(page, "30.00");
  await expect(pay(page).getByTestId("op-error")).toHaveText("The payment is more than the balance of $27.06");
  await expect(pay(page).getByTestId("op-receipt")).toBeVisible();
  await expect(pay(page).getByTestId("op-receipt-earlier")).toContainText("earlier payment");
  await expect(pay(page).getByTestId("op-receipt-amount")).toHaveText("$10.00");
  // A taken payment replaces the mark with its own receipt.
  await take(page, "17.06");
  await expect(pay(page).getByTestId("op-receipt-amount")).toHaveText("$17.06");
  await expect(pay(page).getByTestId("op-receipt-earlier")).toHaveCount(0);
});

test("the form does not render over an unread family: the read failure is shown with a retry", async ({ page }) => {
  await signIn(page);
  await openWithPayment(page, "Q-2101");
  // Both panels' family reads are refused, so the figures behind the payment form are not known.
  const gated = await armedHold(page, /\/api\/v1\/quotes\/q-2101\/related-documents$/, "GET", { times: 1, refuse: { status: 500, error: "internal" } });
  gated.arm();
  await dispatchReload(page);
  await expect.poll(() => gated.held.seen.length).toBe(1);
  await gated.held.release();
  await settle(page);
  await expect(pay(page).getByTestId("op-unread")).toBeVisible();
  // No controls over a guess: no amount, no tender, no Take payment.
  await expect(pay(page).getByTestId("op-take")).toHaveCount(0);
  await expect(pay(page).locator('[data-field="amount"]')).toHaveCount(0);
  await expect(pay(page).getByTestId("op-tenders")).toHaveCount(0);
  await expect(pay(page).getByTestId("op-figures")).toHaveCount(0);
  // The retry re-reads, and when the read answers the form is back.
  gated.disarm();
  await pay(page).getByTestId("op-retry-read").click();
  await expect(pay(page).getByTestId("op-unread")).toHaveCount(0);
  await expect(pay(page).getByTestId("op-take")).toBeVisible();
  await gated.held.release();
  await settle(page);
  await expect(pay(page).getByTestId("op-figures")).toContainText("$37.06");
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

test("a mixed grouping is rejected on input with the grouping words, never sent", async ({ page }) => {
  await signIn(page);
  await openWithPayment(page, "Q-2101");
  const seen = writes(page);
  await pay(page).locator('[data-field="amount"]').fill("1,000 000");
  await expect(pay(page).getByTestId("op-preview")).toHaveCount(0);
  await pay(page).getByTestId("op-take").click();
  await expect(pay(page).getByTestId("op-error")).toHaveText("Type the digits and use one separator every three digits, like 1,000.50.");
  expect(seen.filter((line) => line.includes("order-payment"))).toEqual([]);
  // One kind of separator throughout is still read as the figure it is.
  await pay(page).locator('[data-field="amount"]').fill("1,000,000.00");
  await expect(pay(page).getByTestId("op-preview")).toHaveText("Taking $1,000,000.00 by Cash");
});

test("Start a new payment unlocks only over a read that landed: a read superseded while it was out keeps the attempt held", async ({ page }) => {
  await signIn(page);
  await openWithPayment(page, "Q-2101");
  await page.request.put("/_stub/payment-key-crossed", { data: { crossed: true } });
  await take(page, "10.00", "AUTH 1", "CARD");
  await expect(pay(page).getByTestId("op-start-new")).toBeVisible();
  // The way out starts with a read of the quote and its family; the read is held behind a gate.
  const gated = await armedHold(page, /\/api\/v1\/quotes\/q-2101\/related-documents$/, "GET", { times: 2, snapshot: true });
  gated.arm();
  await pay(page).getByTestId("op-start-new").click();
  await expect.poll(() => gated.held.seen.length).toBe(1);
  // A newer read of the same quote begins while the way out's read is still out (the builder's fan
  // out re-reads the seam's panels): it supersedes the way out's read.
  await pay(page).evaluate((el) =>
    el.dispatchEvent(new CustomEvent("quote-changed", { detail: { quoteId: "q-2101" }, bubbles: true, composed: true })),
  );
  await expect.poll(() => gated.held.seen.length).toBe(2);
  gated.held.release();
  await settle(page);
  // The way out's read never landed (a newer read answered first), so the attempt is still held:
  // the form stays locked to it, and the way out is offered again.
  await expect(pay(page).getByTestId("op-start-new")).toBeVisible();
  await expect(pay(page).locator('[data-field="amount"]')).toBeDisabled();
  await expect(pay(page).locator('[data-field="amount"]')).toHaveValue("10.00");
});

test("a credit that moved the order to Paid while the desk held the old Part paid balance is caught: a refusal re-reads and the panel shows Paid", async ({ page, context }) => {
  await signIn(page);
  await openWithPayment(page, "Q-2101");
  await take(page, "10.00");
  await expect(pay(page).getByTestId("op-figures")).toContainText("balance $27.06");
  // On another tab, a credit of the whole rest moves the order to Paid; this desk's only balance
  // figure is its last receipt's, and nothing crosses the tabs to tell it.
  const other = await context.newPage();
  await signIn(other);
  await openWithPayment(other, "Q-2101");
  const credit = await hold(other, /\/api\/v1\/quotes\/q-2101\/order-payment$/, { method: "POST" });
  await take(other, "27.06");
  await credit.asked;
  await credit.release();
  await expect(pay(other).getByTestId("op-receipt-balance-on-the-invoice")).toHaveText("$0.00");
  // The desk still shows the figures of its last read: Part paid, the receipt's balance.
  await expect(pay(page).getByTestId("op-figures")).toContainText("balance $27.06");
  // The desk tries the receipt's figure: the server refuses it in its own words, and the desk
  // answers the refusal with a re-read, so the panel shows what the server now holds: Paid.
  await take(page, "27.06");
  await expect(pay(page).getByTestId("op-error")).toContainText("more than the balance");
  await expect(pay(page).getByTestId("op-paid")).toContainText("Paid in full");
  await expect(pay(page).getByTestId("op-take")).toHaveCount(0);
  await other.close();
});

test("the take path's re-read is its own share cycle: it asks the server again, so the family read count grows and the server's Paid is shown", async ({ page, context }) => {
  await signIn(page);
  const reads = countRequests(page, /GET .*\/quotes\/q-2101\/related-documents$/);
  await openWithPayment(page, "Q-2101");
  await expect(pay(page).getByTestId("op-figures")).toContainText("balance $37.06");
  await take(page, "10.00");
  await expect(pay(page).getByTestId("op-figures")).toContainText("balance $27.06");
  // Another tab credits the whole rest, so the server moves the order to Paid off this desk's screen.
  const other = await context.newPage();
  await signIn(other);
  await openWithPayment(other, "Q-2101");
  await take(other, "27.06");
  await expect(pay(other).getByTestId("op-receipt-balance-on-the-invoice")).toHaveText("$0.00");
  await other.close();
  // The desk still holds its receipt's Part paid balance. It sends the stale figure and the server
  // refuses it; the refusal's re-read must be a FRESH family read (its own share cycle, never the
  // cycle's cached answer the payment was made inside): the count of related documents GETs this
  // page made grows by at least one (the cycle's read plus the take path's fresh one, two at least
  // over the whole leg), and the panel shows what the server now holds: Paid.
  const before = reads();
  await take(page, "27.06");
  await expect(pay(page).getByTestId("op-error")).toContainText("more than the balance");
  await expect(pay(page).getByTestId("op-paid")).toContainText("Paid in full");
  expect(reads() - before).toBeGreaterThanOrEqual(1);
  expect(reads()).toBeGreaterThanOrEqual(2);
});

test("the seam makes one related documents read per read cycle, shared by the family and the payment panels", async ({ page }) => {
  await signIn(page);
  const reads = countRequests(page, /GET .*\/quotes\/q-2101\/related-documents$/);
  await openWithPayment(page, "Q-2101");
  await expect(pay(page).getByTestId("op-figures")).toContainText("$37.06");
  await expect(docs(page).getByTestId("rd-box")).toBeVisible();
  // One quote open, one family read: the two panels share the cycle's read.
  expect(reads()).toBe(1);
  // An edit's fan out is a new cycle: exactly one more read, not joined to the old answer.
  await dispatchReload(page);
  await settle(page);
  expect(reads()).toBe(2);
});

test("the stub door says the core's words for another payment on a paid order: The order is paid in full.", async ({ request }) => {
  // The door is a staff route: the stub's own mint shape, sales' account and the demo org header.
  const claims = Buffer.from(JSON.stringify({ iss: "hh-demo-stub", sub: "demo-account-sales", aud: "staff" }), "utf8").toString("base64url");
  const bearer = `Bearer ${Buffer.from(JSON.stringify({ alg: "ES256", typ: "JWT" }), "utf8").toString("base64url")}.${claims}.c3R1Yi1zaWduYXR1cmUtYnl0ZXM`;
  const headers = { authorization: bearer, "x-org": "hh-demo" };
  // The first payment converts the cash quote and takes the whole total; the order is paid.
  const first = await request.post("/api/v1/quotes/q-2102/order-payment", {
    headers,
    data: { method: "CASH", amount_cents: 6328, reference: "", idempotency_key: "stub-paid-full-1" },
  });
  expect(first.ok()).toBeTruthy();
  // Another payment on the paid order, a new key: the core says the order is paid, never a balance of $0.00.
  const second = await request.post("/api/v1/quotes/q-2102/order-payment", {
    headers,
    data: { method: "CASH", amount_cents: 1000, reference: "", idempotency_key: "stub-paid-full-2" },
  });
  expect(second.status()).toBe(400);
  expect(await second.json()).toMatchObject({ error: "The order is paid in full." });
});

test("a payment still out at sign out is kept as a known attempt for the next sign in, so it cannot be taken twice", async ({ page, context }) => {
  await signIn(page);
  await openWithPayment(page, "Q-2101");
  const held = await hold(page, /\/api\/v1\/quotes\/q-2101\/order-payment$/, { method: "POST" });
  await take(page, "10.00", "AUTH 9", "CARD");
  await held.asked;
  // The person signs out while the payment is out: the sign out waits a bound for it, goes on
  // without the answer, and the entry page says the payment is kept, not dropped.
  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toBeVisible({ timeout: 15000 });
  await expect(page.locator("p.notice")).toContainText("kept as a known attempt");
  // The answer lands after the session ended: the server took the payment, nobody heard it.
  await held.release();
  // Another tab rings a deliberate sale of its own meanwhile; the kept attempt's answer must still
  // be the first outcome, not a second row behind it.
  const other = await context.newPage();
  watchKeys(other);
  await signIn(other);
  await openWithPayment(other, "Q-2101");
  await take(other, "5.00", "", "CARD");
  await expect(pay(other).getByTestId("op-receipt-amount")).toHaveText("$5.00");
  // The next sign in on the page the payment was kept on meets it as a known attempt (the entry
  // the sign out landed on: no reload, so the kept attempt is the same page's memory, as kept).
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
  await openWithPayment(page, "Q-2101");
  await expect(pay(page).getByTestId("op-unknown")).toContainText("not known");
  await expect(pay(page).locator('[data-field="amount"]')).toBeDisabled();
  await expect(pay(page).locator('[data-field="amount"]')).toHaveValue("10.00");
  // The retry is the kept attempt: the same key, so the server replays the first result.
  await pay(page).getByTestId("op-take").click();
  const receipt = pay(page).getByTestId("op-receipt");
  await expect(receipt.getByTestId("op-receipt-amount")).toHaveText("$10.00");
  // One ghost payment, not two: the balance carries the ghost's 10 and the tab's deliberate 5,
  // and the replay note says nothing was posted a second time.
  await expect(receipt.getByTestId("op-receipt-balance-on-the-invoice")).toHaveText("$22.06");
  await expect(pay(page).getByTestId("op-replayed-note")).toContainText("nothing was posted a second time");
  const first = await postedKeys(page);
  expect(first).toHaveLength(2);
  expect(first[0]).toBe(first[1]);
  await other.close();
});

test("sign out waits for a payment that lands inside the bound and says on the entry page that it was taken", async ({ page }) => {
  await signIn(page);
  await openWithPayment(page, "Q-2101");
  const held = await hold(page, /\/api\/v1\/quotes\/q-2101\/order-payment$/, { method: "POST" });
  await take(page, "10.00", "AUTH 9", "CARD");
  await held.asked;
  await page.getByRole("button", { name: "Sign out" }).click();
  // The flush is waiting on the payment: a release inside the bound is heard before the session
  // ends, and the person who is leaving is told the payment was taken, with its figures.
  await held.release();
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toBeVisible({ timeout: 15000 });
  await expect(page.locator("p.notice")).toContainText("was taken");
  await expect(page.locator("p.notice")).toContainText("$10.00");
});

test("a retry is marked in flight: returning to the quote while it is out holds the button, so two retries never race", async ({ page }) => {
  await signIn(page);
  await openWithPayment(page, "Q-2101");
  await page.request.put("/_stub/payment-ghost", { data: { ghost: true } });
  await take(page, "10.00", "AUTH 9", "CARD");
  await expect(pay(page).getByTestId("op-unknown")).toContainText("not known");
  // The retry POST is held: while it is out, the attempt is in flight wherever the person looks.
  const retry = await hold(page, /\/api\/v1\/quotes\/q-2101\/order-payment$/, { method: "POST" });
  await pay(page).getByTestId("op-take").click();
  await retry.asked;
  await showList(page);
  await page.getByTestId("qb-list-row").filter({ hasText: "Q-1001" }).first().click();
  await expect(page.getByTestId("qe-title")).toContainText("Q-1001");
  await showList(page);
  await page.getByTestId("qb-list-row").filter({ hasText: "Q-2101" }).first().click();
  await expect(pay(page).getByTestId("op-box")).toBeVisible();
  // The attempt is still out for this quote: the button is held on return, so no third POST can
  // be opened beside the retry that is still out.
  await expect(pay(page).getByTestId("op-take")).toBeDisabled();
  await retry.release();
  await settle(page);
  const receipt = pay(page).getByTestId("op-receipt");
  await expect(receipt.getByTestId("op-receipt-amount")).toHaveText("$10.00");
  await expect(receipt.getByTestId("op-receipt-balance-on-the-invoice")).toHaveText("$27.06");
  const keys = await postedKeys(page);
  expect(keys).toHaveLength(2);
  expect(keys[0]).toBe(keys[1]);
});

test("Paid in full is not said beside an attempt whose outcome is not known: the owed nothing is said with the attempt's status, and the retry stays offered", async ({ page }) => {
  await signIn(page);
  await openWithPayment(page, "Q-2101");
  await page.request.put("/_stub/payment-ghost", { data: { ghost: true } });
  await take(page, "37.06");
  await expect(pay(page).getByTestId("op-unknown")).toContainText("not known");
  // The ghost took the full amount, so the server's invoice is paid; a switch away and back is the
  // re-read that shows it. The attempt is still open, so the paid state is not said beside it: the
  // panel says the owed nothing with the attempt's status, and keeps the retry that asks the server
  // for the payment's receipt.
  await showList(page);
  await page.getByTestId("qb-list-row").filter({ hasText: "Q-1001" }).first().click();
  await expect(page.getByTestId("qe-title")).toContainText("Q-1001");
  await showList(page);
  await page.getByTestId("qb-list-row").filter({ hasText: "Q-2101" }).first().click();
  await expect(pay(page).getByTestId("op-box")).toBeVisible();
  await expect(pay(page).getByTestId("op-unknown")).toContainText("not known");
  await expect(pay(page).getByTestId("op-paid")).toHaveCount(0);
  await expect(pay(page).getByTestId("op-take")).toBeEnabled();
  await expect(pay(page).getByTestId("op-paid-unknown")).toContainText("nothing is owed");
  await expect(pay(page).getByTestId("op-paid-unknown")).toContainText("not known");
  // The retry fetches the receipt by replay and ends the attempt; only then is Paid in full said.
  await pay(page).getByTestId("op-take").click();
  await expect(pay(page).getByTestId("op-receipt-amount")).toHaveText("$37.06");
  await expect(pay(page).getByTestId("op-paid")).toContainText("Paid in full");
  await expect(pay(page).getByTestId("op-paid-unknown")).toHaveCount(0);
});
