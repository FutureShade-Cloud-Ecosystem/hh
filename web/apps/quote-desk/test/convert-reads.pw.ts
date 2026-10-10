// Round 10 item 2 (rows 1042, 1051, 1092): Convert to Order uses PR 239's reads. The account standing's own
// `standing`, the customer by id for the limit and the balance, Will-call on the payment door for a Cash On Demand
// customer, and the payment grant before payment is offered. Each leg was red on the parent head.
import { expect, test, type Page } from "@playwright/test";

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

function reads(page: Page): string[] {
  const seen: string[] = [];
  page.on("request", (r) => {
    if (r.method() === "GET") seen.push(new URL(r.url()).pathname + new URL(r.url()).search);
  });
  return seen;
}

async function arrive(page: Page, quoteId: string): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
  await page.evaluate((id) => {
    window.location.hash = `#/quotes/${id}/convert`;
  }, quoteId);
}

const next = (page: Page) => page.getByTestId("qc-next").click();

async function state(page: Page): Promise<{ posts: Array<{ kind: string; fulfillment: string | null }>; conversions: Array<{ fulfillment: string | null }> }> {
  return (await (await page.request.get("/_stub/state")).json()) as never;
}

test("the standing step reads the customer by id and the account standing, never the name search", async ({ page }) => {
  const seen = reads(page);
  await arrive(page, "q-1003");
  await next(page);
  await next(page);
  await expect(page.getByTestId("qc-terms")).toContainText("30 days");
  await expect(page.getByTestId("qc-credit")).toContainText(/\$5,000\.00/);
  await expect(page.getByTestId("qc-credit")).toContainText(/\$1,250\.00/);
  await expect(page.getByTestId("qc-credit")).toContainText("good standing");
  expect(seen.filter((p) => /\/api\/v1\/customers\/c0000000-0000-4000-9000-000000000001$/.test(p))).toHaveLength(1);
  expect(seen.filter((p) => p.startsWith("/api/v1/pos/account-standing"))).toHaveLength(1);
  expect(seen.filter((p) => p.startsWith("/api/v1/customers/search"))).toEqual([]);
  await expect(page.locator("body")).not.toContainText("not reported");
});

test("a past due account says how much is past due and since when", async ({ page }) => {
  const id = await seed(page, { status: "ACCEPTED", customer_id: "c-pastdue", lines: [{ p: "p-wall-std", q: 4 }] });
  await arrive(page, id);
  await next(page);
  await next(page);
  await expect(page.getByTestId("qc-credit")).toContainText("past due");
  await expect(page.getByTestId("qc-credit")).toContainText(/\$450\.00/);
  await expect(page.getByTestId("qc-credit-chip")).toContainText("Past due");
});

test("a customer the read cannot find says so, not that the terms are unreported by a guess", async ({ page }) => {
  await page.route(/\/api\/v1\/customers\/[^/]+$/, (route) => route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: "down" }) }));
  await arrive(page, "q-1003");
  await next(page);
  await next(page);
  await expect(page.getByTestId("qc-terms")).toContainText("could not be read");
});

test("a Cash On Demand customer may choose Will-call, and the payment door carries it", async ({ page }) => {
  const id = await seed(page, { status: "ACCEPTED", customer_id: "c-walkin", lines: [{ p: "p-wall-std", q: 4 }] });
  await arrive(page, id);
  await next(page);
  await expect(page.getByTestId("qc-fulfil-will-call")).toBeEnabled();
  await page.getByTestId("qc-fulfil-will-call").click();
  await expect(page.getByTestId("qc-fulfil-will-call")).toHaveAttribute("aria-pressed", "true");
  await next(page);
  await next(page);
  await next(page);
  await expect(page.getByTestId("qc-confirm-step")).toContainText("Will-call");
  await page.getByTestId("qc-confirm").click();
  await expect(page.getByTestId("qc-done-order")).toContainText("SO-");
  const seen = await state(page);
  expect(seen.posts.map((p) => [p.kind, p.fulfillment])).toEqual([["order-payment", "WILL_CALL"]]);
});

test("the payment grant is read before payment is offered, and a role without it is told in words", async ({ page }) => {
  await page.request.put("/_stub/payment-grant", { data: { refused: true } });
  const seen = reads(page);
  await arrive(page, "q-1003");
  await next(page);
  await next(page);
  await next(page);
  await expect(page.getByTestId("qc-payment")).toBeVisible();
  expect(seen.filter((p) => p.startsWith("/api/v1/pos/payment-grant"))).toHaveLength(1);
  await expect(page.getByTestId("qc-pay-no-grant")).toContainText("may not take a payment");
  await expect(page.getByTestId("qc-pay-amount")).toHaveCount(0);
  // An account customer still converts, with no payment.
  await next(page);
  await page.getByTestId("qc-confirm").click();
  await expect(page.getByTestId("qc-done-order")).toContainText("SO-");
  expect((await state(page)).posts.map((p) => p.kind)).toEqual(["convert"]);
});

test("a Cash On Demand customer cannot be converted by a role that may not take payment", async ({ page }) => {
  await page.request.put("/_stub/payment-grant", { data: { refused: true } });
  const id = await seed(page, { status: "ACCEPTED", customer_id: "c-walkin", lines: [{ p: "p-wall-std", q: 4 }] });
  await arrive(page, id);
  await next(page);
  await next(page);
  await next(page);
  await expect(page.getByTestId("qc-pay-no-grant")).toContainText("Cash On Demand");
  await expect(page.getByTestId("qc-next")).toHaveCount(0);
});
