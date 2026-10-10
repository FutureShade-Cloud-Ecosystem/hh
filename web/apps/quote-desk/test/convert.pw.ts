// The guided Convert to Order surface at #/quotes/<id>/convert (decision log row 993, the row 955
// surfaces rule): five steps, one leg per step, each written red against the parent head (where the
// conversion is one bare button) and green once the surface renders. The stub's accepted quote Q-1003
// is a short line (clear stone, three asked, two at the branch); the seeded ones carry the standings.
import { expect, test, type Page } from "@playwright/test";
import { shot } from "./shot.js";
import { hold } from "./switch-helpers.js";

test.use({ viewport: { width: 1440, height: 900 } });

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

async function seed(page: Page, body: Record<string, unknown>): Promise<string> {
  const response = await page.request.post("/_stub/seed-quote", { data: body });
  return ((await response.json()) as { id: string }).id;
}

/**
 * Sign in as a role, then walk to the convert route the way a link in the desk would. A sign in
 * always starts at the list (the split list's rule), so the route is walked to, not arrived at.
 */
async function arrive(page: Page, quoteId: string, role = "Sales"): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: role, exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
  await page.evaluate((id) => {
    window.location.hash = `#/quotes/${id}/convert`;
  }, quoteId);
}

/**
 * Two clicks on Confirm in one turn of the page, before it has drawn the button disabled: what a fast
 * double click is, and the case only the surface's own in-flight guard can stop.
 */
async function clickTwice(page: Page): Promise<void> {
  await page.getByTestId("qc-confirm").evaluate((el) => {
    (el as HTMLButtonElement).click();
    (el as HTMLButtonElement).click();
  });
}

async function next(page: Page): Promise<void> {
  await page.getByTestId("qc-next").click();
}

interface Seen {
  conversions: Array<{ quote_id: string; fulfillment: string | null }>;
  payments: Array<{ quote_id: string; body: Record<string, unknown>; header: string | null }>;
  posts: Array<{ kind: string; quote_id: string; key: string | null }>;
}

/** "$1,234.56" in integer cents, read off the screen the way a person reads it. */
function centsOf(text: string): number {
  const match = /(\d[\d,]*)\.(\d{2})/.exec(text);
  if (match === null) throw new Error(`no amount in "${text}"`);
  return Number((match[1] ?? "").replace(/,/g, "")) * 100 + Number(match[2]);
}

async function refuseDoor(page: Page, error: string, count = 1, status = 409): Promise<void> {
  await page.request.put("/_stub/door-refusal", { data: { count, status, error } });
}

/** A cash customer's accepted quote, signed in on the convert route. */
async function arriveCod(page: Page): Promise<string> {
  const id = await seed(page, { status: "ACCEPTED", customer_id: "c-walkin", lines: [{ p: "p-wall-std", q: 4 }] });
  await arrive(page, id);
  return id;
}

async function state(page: Page): Promise<Seen> {
  return (await (await page.request.get("/_stub/state")).json()) as Seen;
}

test("A review lists every line with its quantity and total, the customer, and warns where stock is short", async ({ page }) => {
  await arrive(page, "q-1003");
  const review = page.getByTestId("qc-review");
  await expect(review).toBeVisible();
  await expect(page.getByTestId("qc-customer")).toContainText("Maple Ridge Contracting");
  const lines = page.getByTestId("qc-line");
  await expect(lines).toHaveCount(1);
  await expect(lines.first()).toContainText("Clear Stone");
  await expect(lines.first()).toContainText("3");
  await expect(page.getByTestId("qc-total")).toContainText("$");
  // Two at the branch against three asked: the order will wait, said in plain words.
  await expect(page.getByTestId("qc-line-warning").first()).toContainText("wait on stock");
  await shot(page, "after-convert-review-step-desktop");
});

test("A review shows no warning where the branch covers the line", async ({ page }) => {
  const id = await seed(page, { status: "ACCEPTED", customer_id: "c-fenwick", lines: [{ p: "p-wall-std", q: 4 }] });
  await arrive(page, id);
  await expect(page.getByTestId("qc-line")).toHaveCount(1);
  await expect(page.getByTestId("qc-line-warning")).toHaveCount(0);
});

test("B fulfilment offers Pickup and Will-call, and says delivery is not chosen here", async ({ page }) => {
  await arrive(page, "q-1003");
  await next(page);
  await expect(page.getByTestId("qc-fulfilment")).toBeVisible();
  await expect(page.getByTestId("qc-fulfil-pickup")).toBeVisible();
  await expect(page.getByTestId("qc-fulfil-will-call")).toBeVisible();
  await expect(page.getByTestId("qc-delivery-note")).toContainText("scheduling a stop");
  await page.getByTestId("qc-fulfil-will-call").click();
  await expect(page.getByTestId("qc-fulfil-will-call")).toHaveAttribute("aria-pressed", "true");
  await shot(page, "after-convert-fulfilment-step-desktop");
});

test("C standing renders the terms and the credit status before confirming", async ({ page }) => {
  await arrive(page, "q-1003");
  await next(page);
  await next(page);
  await expect(page.getByTestId("qc-standing")).toBeVisible();
  await expect(page.getByTestId("qc-terms")).toContainText("30 days");
  // The facts are the server's own, said as facts; the screen does no sum of its own.
  await expect(page.getByTestId("qc-credit")).toContainText("$5,000.00");
  await expect(page.getByTestId("qc-credit")).toContainText("$1,250.00");
  await expect(page.getByTestId("qc-credit")).toContainText("the server decides");
  await expect(page.getByTestId("qc-credit")).not.toContainText("within");
  await expect(page.getByTestId("qc-credit")).not.toContainText("over by");
  await shot(page, "after-convert-standing-step-desktop");
});

test("C a known over-limit customer is told before confirming, and the refusal at confirm is in plain words", async ({ page }) => {
  const id = await seed(page, { status: "ACCEPTED", customer_id: "c-granite", lines: [{ p: "p-wall-std", q: 4 }] });
  await arrive(page, id);
  await next(page);
  await next(page);
  // The screen never promises a pass or announces a failure it computed: it shows the server's figures.
  await expect(page.getByTestId("qc-credit")).toContainText("$1,000.00");
  await expect(page.getByTestId("qc-credit")).not.toContainText("over by");
  await expect(page.getByTestId("qc-credit")).not.toContainText("within");
  await next(page);
  await next(page);
  await page.getByTestId("qc-confirm").click();
  const refusal = page.getByTestId("qc-refusal");
  await expect(refusal).toBeVisible();
  await expect(refusal).toContainText("credit limit");
  await expect(refusal).toContainText("Nothing was created");
  await expect(refusal).not.toContainText("quote:");
  await expect(page.getByTestId("qc-done")).toHaveCount(0);
});

test("D a Cash On Demand customer pays the whole amount through the payment door, never a plain convert, never a skip", async ({ page }) => {
  await arriveCod(page);
  const total = centsOf(await page.getByTestId("qc-total").innerText());
  await next(page);
  // The door takes the fulfilment (PR 239), so Will-call is offered for a Cash On Demand customer too.
  await expect(page.getByTestId("qc-fulfil-will-call")).toBeEnabled();
  await expect(page.getByTestId("qc-fulfil-cod-note")).toContainText("created and paid in one action");
  await next(page);
  await expect(page.getByTestId("qc-terms")).toContainText("Cash On Demand");
  await expect(page.getByTestId("qc-terms")).toContainText("nothing is released until it is paid");
  await expect(page.getByTestId("qc-credit")).not.toContainText("No credit check");
  await expect(page.getByTestId("qc-credit-chip")).not.toContainText("No credit check");
  await next(page);
  await expect(page.getByTestId("qc-payment")).toBeVisible();
  await expect(page.getByTestId("qc-pay-cod-note")).toContainText("Cash On Demand");
  await expect(page.getByTestId("qc-pay-cod-note")).toContainText("nothing is released until it is paid");
  await expect(page.getByTestId("qc-pay-due")).toContainText("$");
  expect(centsOf(await page.getByTestId("qc-pay-due").innerText())).toBe(total);
  await expect(page.getByTestId("qc-pay-skip")).toHaveCount(0);
  await expect(page.getByTestId("qc-pay-amount")).toHaveCount(0);
  await shot(page, "after-convert-cod-payment-step-desktop");
  await next(page);
  await expect(page.getByTestId("qc-confirm-step")).toContainText("Cash On Demand");
  await expect(page.getByTestId("qc-confirm")).toContainText("take the payment");
  await page.getByTestId("qc-confirm").click();
  await expect(page.getByTestId("qc-done-order")).toContainText("SO-");
  await expect(page.getByTestId("qc-done-payment")).toContainText("taken");
  await shot(page, "after-convert-cod-done-screen-desktop");
  const seen = await state(page);
  // The one action the real server accepts: no plain convert was ever sent.
  expect(seen.posts.map((p) => p.kind)).toEqual(["order-payment"]);
  expect(seen.payments).toHaveLength(1);
  expect(seen.payments[0]!.body.amount_cents).toBe(total);
  expect(seen.conversions).toHaveLength(1);
});

test("D Cash On Demand is never worded as cash on delivery or due on release", async ({ page }) => {
  await arriveCod(page);
  await next(page);
  await next(page);
  await next(page);
  const text = (await page.locator("body").innerText()).toLowerCase();
  expect(text).not.toContain("cash on delivery");
  expect(text).not.toContain("due on release");
  await expect(page.getByTestId("qc-pay-cod-note")).toContainText("nothing is released until it is paid");
});

test("D a Cash On Demand refusal is said in the server's words, with no credit hold advice and nothing taken", async ({ page }) => {
  await arriveCod(page);
  await refuseDoor(page, "this quote is cancelled; only a converted quote's order takes a payment");
  await next(page);
  await next(page);
  await next(page);
  await next(page);
  await page.getByTestId("qc-confirm").click();
  const refusal = page.getByTestId("qc-refusal");
  await expect(refusal).toContainText("only a converted quote's order takes a payment");
  await expect(refusal).toContainText("Nothing was created and nothing was taken");
  await expect(refusal).not.toContainText("credit hold");
  await expect(refusal).not.toContainText("settle the account");
  await expect(page.getByTestId("qc-done")).toHaveCount(0);
});

test("D when the customer read is unavailable the refusal names Cash On Demand and leads to the payment, not credit advice", async ({ page }) => {
  await page.route(/\/api\/v1\/customers\/[^/]+$/, (route) => route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: "down" }) }));
  await arriveCod(page);
  await next(page);
  await next(page);
  await expect(page.getByTestId("qc-terms")).toContainText("could not be read");
  await next(page);
  await next(page);
  await page.getByTestId("qc-confirm").click();
  // The plain convert is refused by the real gate's COD policy; the surface takes the customer to the whole payment.
  await expect(page.getByTestId("qc-payment")).toBeVisible();
  await expect(page.getByTestId("qc-pay-cod-note")).toContainText("Cash On Demand");
  await expect(page.getByTestId("qc-pay-skip")).toHaveCount(0);
  await expect(page.locator("body")).not.toContainText("credit hold");
  await next(page);
  await page.getByTestId("qc-confirm").click();
  await expect(page.getByTestId("qc-done-order")).toContainText("SO-");
  const kinds = (await state(page)).posts.map((p) => p.kind);
  expect(kinds).toEqual(["convert", "order-payment"]);
});

test("C a credit hold the server reports is shown, and the server's refusal at confirm is in its words", async ({ page }) => {
  const id = await seed(page, { status: "ACCEPTED", customer_id: "c-held", lines: [{ p: "p-wall-std", q: 4 }] });
  await arrive(page, id);
  await next(page);
  await next(page);
  await expect(page.getByTestId("qc-credit")).toContainText("credit hold");
  await expect(page.getByTestId("qc-credit")).toContainText("$120.00");
  await next(page);
  await next(page);
  await page.getByTestId("qc-confirm").click();
  await expect(page.getByTestId("qc-refusal")).toContainText("credit policy");
  await expect(page.getByTestId("qc-refusal")).toContainText("Nothing was created");
});

test("C a customer the standing read calls clear is told the account is in good standing", async ({ page }) => {
  await arrive(page, "q-1003");
  await next(page);
  await next(page);
  await expect(page.getByTestId("qc-credit")).toContainText("good standing");
  await expect(page.getByTestId("qc-credit")).toContainText("the server decides");
});

test("C a customer whose standing read fails is not told the account is clear", async ({ page }) => {
  await page.route(/\/api\/v1\/pos\/account-standing/, (route) => route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: "down" }) }));
  await arrive(page, "q-1003");
  await next(page);
  await next(page);
  await expect(page.getByTestId("qc-credit")).not.toContainText("good standing");
  await expect(page.getByTestId("qc-credit")).toContainText("could not be read");
});

test("D paying fifty dollars cash posts the payment with its idempotency key", async ({ page }) => {
  await arrive(page, "q-1003");
  await next(page);
  await next(page);
  await next(page);
  await page.getByTestId("qc-pay-method").selectOption("CASH");
  await page.getByTestId("qc-pay-amount").fill("50");
  await page.getByTestId("qc-pay-reference").fill("till 2");
  await shot(page, "after-convert-payment-step-desktop");
  await next(page);
  await page.getByTestId("qc-confirm").click();
  await expect(page.getByTestId("qc-done-order")).toContainText("SO-");
  const seen = await state(page);
  expect(seen.payments).toHaveLength(1);
  const posted = seen.payments[0]!;
  expect(posted.body.method).toBe("CASH");
  expect(posted.body.amount_cents).toBe(5000);
  expect(posted.body.reference).toBe("till 2");
  expect(typeof posted.header).toBe("string");
  expect(posted.header).toBe(posted.body.idempotency_key);
});

test("D an amount that is not dollars and cents is refused before anything is sent", async ({ page }) => {
  await arrive(page, "q-1003");
  await next(page);
  await next(page);
  await next(page);
  await page.getByTestId("qc-pay-amount").fill("fifty");
  await next(page);
  await expect(page.getByTestId("qc-payment")).toBeVisible();
  await expect(page.getByTestId("qc-pay-error")).toContainText("not a dollar amount");
});

test("E confirm runs the convert with the chosen fulfilment and the done screen names the order", async ({ page }) => {
  await arrive(page, "q-1003");
  await next(page);
  await page.getByTestId("qc-fulfil-will-call").click();
  await next(page);
  await next(page);
  await next(page);
  await expect(page.getByTestId("qc-confirm-step")).toBeVisible();
  await expect(page.getByTestId("qc-confirm-step")).toContainText("Will-call");
  expect((await state(page)).conversions).toHaveLength(0);
  await page.getByTestId("qc-confirm").click();
  await expect(page.getByTestId("qc-done")).toBeVisible();
  await expect(page.getByTestId("qc-done-order")).toContainText("Order SO-");
  await expect(page.getByTestId("qc-done-order")).toContainText("created");
  await expect(page.getByTestId("qc-done")).toContainText("converted");
  expect((await state(page)).conversions).toEqual([{ quote_id: "q-1003", fulfillment: "WILL_CALL" }]);
  await shot(page, "after-convert-done-screen-desktop");
  await page.getByTestId("qc-back-to-quotes").click();
  await expect(page).toHaveURL(/#\/quotes$/);
  await expect(page.getByTestId("qb-list")).toBeVisible();
});

test("E the surface announces quote-converted with the order and the quote", async ({ page }) => {
  await arrive(page, "q-1003");
  await expect(page.getByTestId("qc-review")).toBeVisible();
  await page.evaluate(() => {
    const host = document.querySelector("qd-app-root")!.shadowRoot!.querySelector("qd-quote-builder")!.shadowRoot!.querySelector("quote-convert")!;
    (window as unknown as { __converted: unknown[] }).__converted = [];
    host.addEventListener("quote-converted", (event) => (window as unknown as { __converted: unknown[] }).__converted.push((event as CustomEvent).detail));
  });
  await next(page);
  await next(page);
  await next(page);
  await next(page);
  await page.getByTestId("qc-confirm").click();
  await expect(page.getByTestId("qc-done")).toBeVisible();
  const heard = await page.evaluate(() => (window as unknown as { __converted: unknown[] }).__converted);
  expect(heard).toEqual([{ orderId: "o-2002", quoteId: "q-1003" }]);
});

test("F a Sales caller whose role does not carry the convert action cannot reach the route", async ({ page, request }) => {
  await request.put("/_stub/convert-wall", { data: { on: true } });
  await arrive(page, "q-1003");
  await expect(page.getByTestId("qc-no-access")).toBeVisible();
  await expect(page.getByTestId("qc-no-access")).toContainText("does not include converting quotes");
  await expect(page.getByTestId("qc-review")).toHaveCount(0);
  await expect(page.getByTestId("qc-next")).toHaveCount(0);
  await shot(page, "after-convert-sales-no-access-desktop");
});

test("F a quote that cannot convert yet says so and offers no steps", async ({ page }) => {
  await arrive(page, "q-1001");
  await expect(page.getByTestId("qc-not-ready")).toContainText("only an accepted quote");
  await expect(page.getByTestId("qc-next")).toHaveCount(0);
});

test("guard convert load order: a stock read that answers after another quote's route opened never shows on it", async ({ page }) => {
  const other = await seed(page, { status: "ACCEPTED", customer_id: "c-fenwick", lines: [{ p: "p-wall-std", q: 4 }] });
  const held = await hold(page, /\/api\/v1\/quotes\/q-1003\/availability$/, { method: "GET" });
  await arrive(page, "q-1003");
  await held.asked;
  await page.evaluate((id) => {
    window.location.hash = `#/quotes/${id}/convert`;
  }, other);
  await expect(page.getByTestId("qc-line")).toContainText("Wall");
  await held.release();
  // The late answer is for the clear stone line of the quote left; the first line here is the wall stone.
  await expect(page.getByTestId("qc-line")).toHaveCount(1);
  await expect(page.getByTestId("qc-line-warning")).toHaveCount(0);
});

test("guard convert answer: a conversion answered after another quote's route opened leaves that route's screen alone", async ({ page }) => {
  const other = await seed(page, { status: "ACCEPTED", customer_id: "c-fenwick", lines: [{ p: "p-wall-std", q: 4 }] });
  await arrive(page, "q-1003");
  await next(page);
  await next(page);
  await next(page);
  await next(page);
  const held = await hold(page, /\/api\/v1\/quotes\/q-1003\/convert$/, { method: "POST" });
  await page.getByTestId("qc-confirm").click();
  await held.asked;
  await page.evaluate((id) => {
    window.location.hash = `#/quotes/${id}/convert`;
  }, other);
  await expect(page.getByTestId("qc-review")).toBeVisible();
  await held.release();
  await expect(page.getByTestId("qc-review")).toBeVisible();
  await expect(page.getByTestId("qc-done")).toHaveCount(0);
  expect((await state(page)).conversions).toEqual([{ quote_id: "q-1003", fulfillment: "PICKUP" }]);
});

// ── the idempotency the surface relies on ──────────────────────────────────────

test("idempotency: a double click on Confirm posts the conversion once", async ({ page }) => {
  await arrive(page, "q-1003");
  await next(page);
  await next(page);
  await next(page);
  await next(page);
  const held = await hold(page, /\/api\/v1\/quotes\/q-1003\/convert$/, { method: "POST" });
  await clickTwice(page);
  await held.asked;
  await held.release();
  await expect(page.getByTestId("qc-done")).toBeVisible();
  expect((await state(page)).posts.filter((p) => p.kind === "convert")).toHaveLength(1);
});

test("idempotency: a double click on Confirm posts the Cash On Demand payment once", async ({ page }) => {
  await arriveCod(page);
  await next(page);
  await next(page);
  await next(page);
  await next(page);
  const held = await hold(page, /\/order-payment$/, { method: "POST" });
  await clickTwice(page);
  await held.asked;
  await held.release();
  await expect(page.getByTestId("qc-done")).toBeVisible();
  expect((await state(page)).posts.filter((p) => p.kind === "order-payment")).toHaveLength(1);
});

test("idempotency: a payment whose answer was lost is retried under the same key and is taken once", async ({ page }) => {
  await arrive(page, "q-1003");
  await next(page);
  await next(page);
  await next(page);
  await page.getByTestId("qc-pay-amount").fill("50");
  await next(page);
  // The server takes the payment and the answer is lost on the way back.
  let lost = true;
  await page.route(/\/order-payment$/, async (route) => {
    if (!lost) {
      await route.continue();
      return;
    }
    lost = false;
    await route.fetch();
    await route.abort();
  });
  await page.getByTestId("qc-confirm").click();
  await expect(page.getByTestId("qc-pay-note")).toContainText("could not confirm whether the payment");
  await expect(page.getByTestId("qc-pay-note")).not.toContainText("Nothing was charged");
  await expect(page.getByTestId("qc-done-order")).toContainText("SO-");
  await page.getByTestId("qc-retry-payment").click();
  await expect(page.getByTestId("qc-done-payment")).toContainText("taken");
  const seen = await state(page);
  const posted = seen.posts.filter((p) => p.kind === "order-payment");
  expect(posted).toHaveLength(2);
  expect(posted[0]!.key).not.toBeNull();
  expect(posted[1]!.key).toBe(posted[0]!.key);
  expect(seen.payments).toHaveLength(1);
});

/** The order made, a payment of 50 planned, and the payment door answering as `answer` after (or without) the write. */
async function payAfterLostAnswer(page: Page, answer: "drop" | "502"): Promise<void> {
  await arrive(page, "q-1003");
  await next(page);
  await next(page);
  await next(page);
  await page.getByTestId("qc-pay-amount").fill("50");
  await next(page);
  let lost = true;
  await page.route(/\/order-payment$/, async (route) => {
    if (!lost) {
      await route.continue();
      return;
    }
    lost = false;
    await route.fetch();
    if (answer === "drop") await route.abort();
    else await route.fulfill({ status: 502, contentType: "application/json", body: JSON.stringify({ error: "bad gateway" }) });
  });
  await page.getByTestId("qc-confirm").click();
}

for (const answer of ["drop", "502"] as const) {
  test(`unknown payment outcome (${answer}): the words say it must be checked, never "Nothing was charged", and the retry reuses the key`, async ({ page }) => {
    await payAfterLostAnswer(page, answer);
    const note = page.getByTestId("qc-pay-note");
    await expect(note).toContainText("could not confirm whether the payment of $50.00 was taken");
    await expect(note).toContainText("balance");
    await expect(note).not.toContainText("Nothing was charged");
    await expect(note).not.toContainText("counter");
    await page.getByTestId("qc-retry-payment").click();
    await expect(page.getByTestId("qc-done-payment")).toContainText("taken");
    const seen = await state(page);
    const posted = seen.posts.filter((p) => p.kind === "order-payment");
    expect(posted).toHaveLength(2);
    expect(posted[0]!.key).not.toBeNull();
    expect(posted[1]!.key).toBe(posted[0]!.key);
    expect(seen.payments).toHaveLength(1);
  });
}

for (const answer of ["drop", "502"] as const) {
  test(`unknown convert outcome (${answer}) with multi accept ON: the words say not known yet, the retry sends the same key, and one order is made`, async ({ page }) => {
    // The dealer's switch ON: without a key a second convert makes a second order (DIB-491).
    await page.request.put("/_stub/multi-accept", { data: { on: true } });
    await arrive(page, "q-1003");
    await next(page);
    await next(page);
    await next(page);
    await next(page);
    let lost = true;
    await page.route(/\/api\/v1\/quotes\/q-1003\/convert$/, async (route) => {
      if (!lost) {
        await route.continue();
        return;
      }
      lost = false;
      await route.fetch();
      if (answer === "drop") await route.abort();
      else await route.fulfill({ status: 502, contentType: "application/json", body: JSON.stringify({ error: "bad gateway" }) });
    });
    await page.getByTestId("qc-confirm").click();
    const refusal = page.getByTestId("qc-refusal");
    await expect(refusal).toContainText("not known yet");
    await expect(refusal).toContainText("reload");
    await expect(refusal).not.toContainText("Nothing was created");
    await expect(refusal).not.toContainText("converts a quote only once");
    // The retry: the same key, so the core answers the first order and makes no second.
    await page.getByTestId("qc-confirm").click();
    await expect(page.getByTestId("qc-done-order")).toContainText("SO-");
    const seen = await state(page);
    const posted = seen.posts.filter((p) => p.kind === "convert");
    expect(posted).toHaveLength(2);
    expect(posted[0]!.key).not.toBeNull();
    expect(posted[1]!.key).toBe(posted[0]!.key);
    expect(seen.conversions).toHaveLength(1);
  });
}

// ── a lost convert answer holds the attempt open (PR 296 round 4, the Opus P1) ──

test("a lost convert answer holds the fulfilment and the key: presses make one order (multi accept ON)", async ({ page }) => {
  await page.request.put("/_stub/multi-accept", { data: { on: true } });
  await arrive(page, "q-1003");
  await next(page);
  await next(page);
  await next(page);
  await next(page);
  let lost = true;
  await page.route(/\/api\/v1\/quotes\/q-1003\/convert$/, async (route) => {
    if (!lost) {
      await route.continue();
      return;
    }
    lost = false;
    await route.fetch();
    await route.abort();
  });
  await page.getByTestId("qc-confirm").click();
  await expect(page.getByTestId("qc-refusal")).toContainText("not known yet");
  // Back to the fulfilment step: the attempt whose answer was lost is still open, so the choice is
  // held as it was sent and a change is not taken.
  await page.getByTestId("qc-back").click();
  await page.getByTestId("qc-back").click();
  await page.getByTestId("qc-back").click();
  await expect(page.getByTestId("qc-fulfilment")).toBeVisible();
  await expect(page.getByTestId("qc-fulfil-held")).toContainText("held as it was sent");
  await expect(page.getByTestId("qc-fulfil-will-call")).toBeDisabled();
  await expect(page.getByTestId("qc-fulfil-pickup")).toBeDisabled();
  await expect(page.getByTestId("qc-fulfil-pickup")).toHaveAttribute("aria-pressed", "true");
  // Presses on: the same key and the same fulfilment go, so the stub answers the first order.
  await next(page);
  await next(page);
  await next(page);
  await page.getByTestId("qc-confirm").click();
  await expect(page.getByTestId("qc-done-order")).toContainText("SO-");
  const seen = await state(page);
  const posted = seen.posts.filter((p) => p.kind === "convert");
  expect(posted).toHaveLength(2);
  expect(posted[0]!.key).not.toBeNull();
  expect(posted[1]!.key).toBe(posted[0]!.key);
  expect(seen.conversions).toHaveLength(1);
  expect(seen.conversions[0]!.fulfillment).toBe("PICKUP");
});

test("a key reuse refusal after a lost answer shows the earlier order and never the use a new key words", async ({ page }) => {
  await page.request.put("/_stub/multi-accept", { data: { on: true } });
  await arrive(page, "q-1003");
  await next(page);
  await next(page);
  await next(page);
  await next(page);
  const keys: Array<string | undefined> = [];
  let step = 0;
  await page.route(/\/api\/v1\/quotes\/q-1003\/convert$/, async (route) => {
    keys.push(route.request().headers()["idempotency-key"]);
    step += 1;
    if (step === 1) {
      await route.fetch();
      await route.abort();
      return;
    }
    if (step === 2) {
      // The core's ErrConvertKeyReused, word for word.
      await route.fulfill({ status: 409, contentType: "application/json", body: JSON.stringify({ error: "quote: this idempotency key was used for a different conversion; use a new key for a new conversion" }) });
      return;
    }
    await route.continue();
  });
  await page.getByTestId("qc-confirm").click();
  await expect(page.getByTestId("qc-refusal")).toContainText("not known yet");
  await page.getByTestId("qc-confirm").click();
  await expect(page.getByTestId("qc-done-earlier")).toContainText("An order was already created for this quote");
  await expect(page.getByTestId("qc-done-order")).toContainText("SO-");
  const screen = await page.locator("body").innerText();
  expect(screen).not.toContain("use a new key");
  expect(screen).not.toContain("different conversion");
  // One order stands, and no fresh key was minted from the refusal.
  const seen = await state(page);
  expect(seen.conversions).toHaveLength(1);
  expect(keys).toHaveLength(2);
  expect(keys[0]).toBeTruthy();
  expect(keys[1]).toBe(keys[0]);
});

test("a reload of a converted quote names its order and offers no second convert (multi accept ON)", async ({ page }) => {
  await page.request.put("/_stub/multi-accept", { data: { on: true } });
  await arrive(page, "q-1003");
  await next(page);
  await next(page);
  await next(page);
  await next(page);
  await page.getByTestId("qc-confirm").click();
  await expect(page.getByTestId("qc-done-order")).toContainText("SO-");
  const orderNo = /SO-\d+/.exec(await page.getByTestId("qc-done-order").innerText())![0];
  await page.reload();
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  // A sign in always starts at the list (the split list's rule), so the route is walked to again.
  await expect(page.getByTestId("qb-list")).toBeVisible();
  await page.evaluate((id) => {
    window.location.hash = `#/quotes/${id}/convert`;
  }, "q-1003");
  const stop = page.getByTestId("qc-not-ready");
  await expect(stop).toContainText("An order was already created for this quote");
  await expect(stop).toContainText(orderNo);
  await expect(page.getByTestId("qc-review")).toHaveCount(0);
  await expect(page.getByTestId("qc-next")).toHaveCount(0);
  await expect(page.getByTestId("qc-confirm")).toHaveCount(0);
  expect((await state(page)).conversions).toHaveLength(1);
});

test("an unworded 4xx payment refusal says the outcome is unknown, never 'Nothing was charged', and the retry reuses the key", async ({ page }) => {
  await arrive(page, "q-1003");
  await next(page);
  await next(page);
  await next(page);
  await page.getByTestId("qc-pay-amount").fill("50");
  await next(page);
  let bare = true;
  const keys: Array<string | undefined> = [];
  await page.route(/\/order-payment$/, async (route) => {
    keys.push(route.request().headers()["idempotency-key"]);
    if (bare) {
      bare = false;
      await route.fulfill({ status: 400, contentType: "text/plain", body: "" });
      return;
    }
    await route.continue();
  });
  await page.getByTestId("qc-confirm").click();
  const note = page.getByTestId("qc-pay-note");
  await expect(note).toContainText("could not confirm whether the payment of $50.00 was taken");
  await expect(note).not.toContainText("Nothing was charged");
  await expect(note).not.toContainText("counter");
  await page.getByTestId("qc-retry-payment").click();
  await expect(page.getByTestId("qc-done-payment")).toContainText("taken");
  const seen = await state(page);
  const posted = seen.posts.filter((p) => p.kind === "order-payment");
  // The bare answer never reached the stub; the retry carries the key the first attempt used.
  expect(posted).toHaveLength(1);
  expect(keys).toHaveLength(2);
  expect(keys[0]).toBeTruthy();
  expect(keys[1]).toBe(keys[0]);
  expect(seen.payments).toHaveLength(1);
});

test("an unworded 4xx convert refusal says the outcome is not known yet and the retry reuses the key (multi accept ON)", async ({ page }) => {
  await page.request.put("/_stub/multi-accept", { data: { on: true } });
  await arrive(page, "q-1003");
  await next(page);
  await next(page);
  await next(page);
  await next(page);
  let bare = true;
  const keys: Array<string | undefined> = [];
  await page.route(/\/api\/v1\/quotes\/q-1003\/convert$/, async (route) => {
    keys.push(route.request().headers()["idempotency-key"]);
    if (bare) {
      bare = false;
      await route.fulfill({ status: 400, contentType: "text/plain", body: "" });
      return;
    }
    await route.continue();
  });
  await page.getByTestId("qc-confirm").click();
  const refusal = page.getByTestId("qc-refusal");
  await expect(refusal).toContainText("not known yet");
  await expect(refusal).not.toContainText("Nothing was created");
  await page.getByTestId("qc-confirm").click();
  await expect(page.getByTestId("qc-done-order")).toContainText("SO-");
  const seen = await state(page);
  // The bare answer never reached the stub; the retry carries the key the first attempt used.
  const posted = seen.posts.filter((p) => p.kind === "convert");
  expect(posted).toHaveLength(1);
  expect(keys).toHaveLength(2);
  expect(keys[0]).toBeTruthy();
  expect(keys[1]).toBe(keys[0]);
  expect(seen.conversions).toHaveLength(1);
});

test("a fresh press of Confirm after a definite refusal mints a new convert key", async ({ page }) => {
  await arrive(page, "q-1003");
  await next(page);
  await next(page);
  await next(page);
  await next(page);
  const keys: Array<string | undefined> = [];
  await page.route(/\/api\/v1\/quotes\/q-1003\/convert$/, async (route) => {
    keys.push(route.request().headers()["idempotency-key"]);
    if (keys.length === 1) {
      await route.fulfill({ status: 409, contentType: "application/json", body: JSON.stringify({ error: "quote: this quote is sent, only an accepted quote converts" }) });
      return;
    }
    await route.continue();
  });
  await page.getByTestId("qc-confirm").click();
  await expect(page.getByTestId("qc-refusal")).toContainText("Nothing was created");
  await page.getByTestId("qc-confirm").click();
  await expect(page.getByTestId("qc-done-order")).toContainText("SO-");
  expect(keys).toHaveLength(2);
  expect(keys[0]).toBeTruthy();
  expect(keys[1]).toBeTruthy();
  expect(keys[1]).not.toBe(keys[0]);
});

test("idempotency: the same intended payment keeps its key, a fresh intended payment gets a new one", async ({ page }) => {
  await arriveCod(page);
  await refuseDoor(page, "the server could not take this just now", 2, 500);
  await next(page);
  await next(page);
  await next(page);
  await next(page);
  await page.getByTestId("qc-confirm").click();
  await expect(page.getByTestId("qc-refusal")).toBeVisible();
  // The same payment tried again: the same key.
  await page.getByTestId("qc-confirm").click();
  await expect(page.getByTestId("qc-refusal")).toBeVisible();
  await expect.poll(async () => (await state(page)).posts.length).toBe(2);
  // A different intended payment (another tender): a new key.
  await page.getByTestId("qc-back").click();
  await page.getByTestId("qc-pay-method").selectOption("CARD");
  await next(page);
  await page.getByTestId("qc-confirm").click();
  await expect(page.getByTestId("qc-done-order")).toContainText("SO-");
  const keys = (await state(page)).posts.map((p) => p.key);
  expect(keys).toHaveLength(3);
  expect(keys[1]).toBe(keys[0]);
  expect(keys[2]).not.toBe(keys[0]);
  expect((await state(page)).payments[0]!.body.method).toBe("CARD");
});

// ── leaving while the conversion is in flight ──────────────────────────────────

test("a payment typed for the order is still taken, and said, when the person leaves while the conversion is out", async ({ page }) => {
  const other = await seed(page, { status: "ACCEPTED", customer_id: "c-fenwick", lines: [{ p: "p-wall-std", q: 4 }] });
  await arrive(page, "q-1003");
  await next(page);
  await next(page);
  await next(page);
  await page.getByTestId("qc-pay-amount").fill("50");
  await next(page);
  const held = await hold(page, /\/api\/v1\/quotes\/q-1003\/convert$/, { method: "POST" });
  await page.getByTestId("qc-confirm").click();
  await held.asked;
  await page.evaluate((id) => {
    window.location.hash = `#/quotes/${id}/convert`;
  }, other);
  await expect(page.getByTestId("qc-review")).toBeVisible();
  await held.release();
  await expect(page.getByTestId("qb-convert-notice")).toContainText("payment of $50.00 was taken");
  await expect(page.getByTestId("qb-convert-notice")).toContainText("SO-");
  const seen = await state(page);
  expect(seen.payments).toHaveLength(1);
  expect(seen.payments[0]!.body.amount_cents).toBe(5000);
  // The screen the person landed on is not the old quote's done screen.
  await expect(page.getByTestId("qc-done")).toHaveCount(0);
});

test("a payment that cannot be taken after the person left is said plainly, not dropped", async ({ page }) => {
  const other = await seed(page, { status: "ACCEPTED", customer_id: "c-fenwick", lines: [{ p: "p-wall-std", q: 4 }] });
  await arrive(page, "q-1003");
  await next(page);
  await next(page);
  await next(page);
  await page.getByTestId("qc-pay-amount").fill("50");
  await next(page);
  await refuseDoor(page, "the payments desk is closed");
  const held = await hold(page, /\/api\/v1\/quotes\/q-1003\/convert$/, { method: "POST" });
  await page.getByTestId("qc-confirm").click();
  await held.asked;
  await page.evaluate((id) => {
    window.location.hash = `#/quotes/${id}/convert`;
  }, other);
  await expect(page.getByTestId("qc-review")).toBeVisible();
  await held.release();
  await expect(page.getByTestId("qb-convert-notice")).toContainText("was not taken");
  await expect(page.getByTestId("qb-convert-notice")).toContainText("Nothing was charged");
  await expect(page.getByTestId("qb-convert-notice")).toContainText("SO-");
  expect((await state(page)).payments).toHaveLength(0);
});

test("guard convert cod answer: a Cash On Demand action answered after the person left is said, and no screen is touched", async ({ page }) => {
  const other = await seed(page, { status: "ACCEPTED", customer_id: "c-fenwick", lines: [{ p: "p-wall-std", q: 4 }] });
  const id = await arriveCod(page);
  await next(page);
  await next(page);
  await next(page);
  await next(page);
  const held = await hold(page, /\/order-payment$/, { method: "POST" });
  await page.getByTestId("qc-confirm").click();
  await held.asked;
  await page.evaluate((quote) => {
    window.location.hash = `#/quotes/${quote}/convert`;
  }, other);
  await expect(page.getByTestId("qc-review")).toBeVisible();
  await held.release();
  await expect(page.getByTestId("qb-convert-notice")).toContainText("Cash On Demand payment");
  await expect(page.getByTestId("qb-convert-notice")).toContainText("was taken");
  await expect(page.getByTestId("qc-review")).toBeVisible();
  await expect(page.getByTestId("qc-done")).toHaveCount(0);
  expect((await state(page)).payments.map((p) => p.quote_id)).toEqual([id]);
});

test("guard convert payment: a payment out after the conversion when the person leaves is said, taken", async ({ page }) => {
  const other = await seed(page, { status: "ACCEPTED", customer_id: "c-fenwick", lines: [{ p: "p-wall-std", q: 4 }] });
  await arrive(page, "q-1003");
  await next(page);
  await next(page);
  await next(page);
  await page.getByTestId("qc-pay-amount").fill("50");
  await next(page);
  const held = await hold(page, /\/order-payment$/, { method: "POST" });
  await page.getByTestId("qc-confirm").click();
  await expect(page.getByTestId("qc-done-order")).toContainText("SO-");
  await held.asked;
  await page.evaluate((id) => {
    window.location.hash = `#/quotes/${id}/convert`;
  }, other);
  await expect(page.getByTestId("qc-review")).toBeVisible();
  await held.release();
  await expect(page.getByTestId("qb-convert-notice")).toContainText("payment of $50.00 was taken");
  await expect(page.getByTestId("qb-convert-notice")).toContainText("SO-");
  await expect(page.getByTestId("qc-done")).toHaveCount(0);
  expect((await state(page)).payments).toHaveLength(1);
});

test("guard convert payment: a payment refused after the person left is said as not taken", async ({ page }) => {
  const other = await seed(page, { status: "ACCEPTED", customer_id: "c-fenwick", lines: [{ p: "p-wall-std", q: 4 }] });
  await arrive(page, "q-1003");
  await next(page);
  await next(page);
  await next(page);
  await page.getByTestId("qc-pay-amount").fill("50");
  await next(page);
  await refuseDoor(page, "the payments desk is closed");
  const held = await hold(page, /\/order-payment$/, { method: "POST" });
  await page.getByTestId("qc-confirm").click();
  await held.asked;
  await page.evaluate((id) => {
    window.location.hash = `#/quotes/${id}/convert`;
  }, other);
  await expect(page.getByTestId("qc-review")).toBeVisible();
  await held.release();
  await expect(page.getByTestId("qb-convert-notice")).toContainText("was not taken");
  await expect(page.getByTestId("qb-convert-notice")).toContainText("payments desk is closed");
  await expect(page.getByTestId("qb-convert-notice")).toContainText("SO-");
  expect((await state(page)).payments).toHaveLength(0);
});

test("idempotency: a Cash On Demand action committed with its answer lost is retried under the same key and taken once", async ({ page }) => {
  await arriveCod(page);
  await next(page);
  await next(page);
  await next(page);
  await next(page);
  let lost = true;
  await page.route(/\/order-payment$/, async (route) => {
    if (!lost) {
      await route.continue();
      return;
    }
    lost = false;
    await route.fetch();
    await route.abort();
  });
  await page.getByTestId("qc-confirm").click();
  await expect(page.getByTestId("qc-refusal")).toContainText("could not confirm");
  await expect(page.getByTestId("qc-done")).toHaveCount(0);
  await page.getByTestId("qc-confirm").click();
  await expect(page.getByTestId("qc-done-order")).toContainText("SO-");
  const seen = await state(page);
  const posted = seen.posts.filter((p) => p.kind === "order-payment");
  expect(posted).toHaveLength(2);
  expect(posted[1]!.key).toBe(posted[0]!.key);
  expect(seen.payments).toHaveLength(1);
  expect(seen.conversions).toHaveLength(1);
});

test("a Cash On Demand refusal tells the person to reload in case the quote changed since the page opened", async ({ page }) => {
  await arriveCod(page);
  await refuseDoor(page, "a customer with no credit account pays the whole agreed total of $999.00", 1, 400);
  await next(page);
  await next(page);
  await next(page);
  await next(page);
  await page.getByTestId("qc-confirm").click();
  await expect(page.getByTestId("qc-refusal")).toContainText("pays the whole agreed total");
  await expect(page.getByTestId("qc-refusal")).toContainText("reload");
});

test("a conversion that lands after the surface was removed still refreshes the desk's list", async ({ page }) => {
  await arriveCod(page);
  await next(page);
  await next(page);
  await next(page);
  await next(page);
  const held = await hold(page, /\/order-payment$/, { method: "POST" });
  await page.getByTestId("qc-confirm").click();
  await held.asked;
  await page.evaluate(() => {
    window.location.hash = "#/quotes";
  });
  await expect(page.getByTestId("qc-host")).toHaveCount(0);
  let lists = 0;
  page.on("request", (r) => {
    if (r.method() === "GET" && /\/api\/v1\/quotes(\?|$)/.test(r.url())) lists += 1;
  });
  await held.release();
  await expect(page.getByTestId("qb-convert-notice")).toBeVisible();
  await expect.poll(() => lists).toBeGreaterThan(0);
});

test("the convert route sits in the desk's history: back reaches the list, forward the convert screen", async ({ page }) => {
  await arrive(page, "q-1003");
  await expect(page.getByTestId("qc-review")).toBeVisible();
  await page.goBack();
  await expect(page).toHaveURL(/#\/quotes$/);
  await expect(page.getByTestId("qb-list")).toBeVisible();
  await expect(page.getByTestId("qc-host")).toHaveCount(0);
  await page.goForward();
  await expect(page.getByTestId("qc-review")).toBeVisible();
});
