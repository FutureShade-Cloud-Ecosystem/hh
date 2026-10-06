// The guided Convert to Order surface at #/quotes/<id>/convert (decision log row 993, the row 955
// surfaces rule): five steps, one leg per step, each written red against the parent head (where the
// conversion is one bare button) and green once the surface renders. The stub's accepted quote Q-1003
// is a short line (clear stone, three asked, two at the branch); the seeded ones carry the standings.
import { expect, test, type Page } from "@playwright/test";
import { shot } from "./shot.js";

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

/** Sign in as a role with the route already in the address, the way a link would arrive. */
async function arrive(page: Page, quoteId: string, role = "Sales"): Promise<void> {
  await page.goto(`/#/quotes/${quoteId}/convert`);
  await page.getByRole("button", { name: role, exact: true }).click();
}

async function next(page: Page): Promise<void> {
  await page.getByTestId("qc-next").click();
}

async function state(page: Page): Promise<{ conversions: Array<{ quote_id: string; fulfillment: string | null }>; payments: Array<{ quote_id: string; body: Record<string, unknown>; header: string | null }> }> {
  return (await (await page.request.get("/_stub/state")).json()) as never;
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
  await expect(page.getByTestId("qc-total")).toContainText("CA$");
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
  await expect(page.getByTestId("qc-credit")).toContainText("within");
  await shot(page, "after-convert-standing-step-desktop");
});

test("C a known over-limit customer is told before confirming, and the refusal at confirm is in plain words", async ({ page }) => {
  const id = await seed(page, { status: "ACCEPTED", customer_id: "c-granite", lines: [{ p: "p-wall-std", q: 4 }] });
  await arrive(page, id);
  await next(page);
  await next(page);
  await expect(page.getByTestId("qc-credit")).toContainText("over");
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

test("D the payment step is skippable for a COD account, which is told payment is due on release", async ({ page }) => {
  const id = await seed(page, { status: "ACCEPTED", customer_id: "c-walkin", lines: [{ p: "p-wall-std", q: 4 }] });
  await arrive(page, id);
  await next(page);
  await next(page);
  await expect(page.getByTestId("qc-terms")).toContainText("Cash on delivery");
  await next(page);
  await expect(page.getByTestId("qc-payment")).toBeVisible();
  await expect(page.getByTestId("qc-pay-cod-note")).toContainText("due on release");
  await page.getByTestId("qc-pay-skip").click();
  await expect(page.getByTestId("qc-confirm-step")).toBeVisible();
  await page.getByTestId("qc-confirm").click();
  await expect(page.getByTestId("qc-done-order")).toContainText("SO-");
  expect((await state(page)).payments).toHaveLength(0);
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
  expect(heard).toEqual([{ orderId: "o-q-1003", quoteId: "q-1003" }]);
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
