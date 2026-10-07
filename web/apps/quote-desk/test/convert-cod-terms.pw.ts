// Round 13: a Cash On Demand customer with zero-day terms and a credit line beside them (the demo's Dibbits account) is
// told in Convert's own steps what the Dibbits rule is (decision log row 1047 item 6): the order is held until it is paid
// in full, a deposit puts it in process, and taking nothing now stays a choice only with that consequence stated.
import { expect, test } from "@playwright/test";

test.use({ viewport: { width: 1440, height: 900 } });
test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

async function toPayment(page: import("@playwright/test").Page, customer: string): Promise<void> {
  const seeded = await page.request.post("/_stub/seed-quote", { data: { status: "ACCEPTED", customer_id: customer, lines: [{ p: "p-wall-std", q: 4 }] } });
  const id = ((await seeded.json()) as { id: string }).id;
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
  await page.evaluate((quote) => {
    window.location.hash = `#/quotes/${quote}/convert`;
  }, id);
  await expect(page.getByTestId("qc-review")).toBeVisible();
  for (let step = 0; step < 3; step += 1) await page.getByTestId("qc-next").click();
  await expect(page.getByTestId("qc-payment")).toBeVisible();
}

test("a Cash On Demand customer's Payment step says the order is held until paid in full and a deposit puts it in process", async ({ page }) => {
  await toPayment(page, "c-cod-terms");
  const payment = page.getByTestId("qc-payment");
  await expect(payment).not.toContainText("(optional)");
  await expect(payment).not.toContainText("Skip payment");
  const note = payment.getByTestId("qc-pay-cod-terms-note");
  await expect(note).toContainText("held until it is paid in full");
  await expect(note).toContainText("A deposit puts it in process");
  await expect(payment.getByTestId("qc-pay-skip")).toHaveText("Take nothing now, hold the order");
});

test("taking nothing now for a Cash On Demand customer is confirmed with its consequence, not as 'None'", async ({ page }) => {
  await toPayment(page, "c-cod-terms");
  await page.getByTestId("qc-pay-skip").click();
  const confirm = page.getByTestId("qc-confirm-step");
  await expect(confirm).toBeVisible();
  await expect(confirm.getByTestId("qc-confirm-payment")).toContainText("Nothing now. The order will be held until it is paid in full.");
  await expect(confirm).not.toContainText(/Payment now\s*None/);
});

test("a deposit for a Cash On Demand customer is confirmed as putting the order in process, the rest due before release", async ({ page }) => {
  await toPayment(page, "c-cod-terms");
  await page.getByTestId("qc-pay-amount").fill("10.00");
  await page.getByTestId("qc-next").click();
  const line = page.getByTestId("qc-confirm-step").getByTestId("qc-confirm-payment");
  await expect(line).toContainText("$10.00");
  await expect(line).toContainText("puts the order in process");
  await expect(line).toContainText("paid in full before it is released");
});

test("an account customer's Payment step is unchanged: optional, with Skip payment", async ({ page }) => {
  await toPayment(page, "c-fenwick");
  await expect(page.getByTestId("qc-payment")).toContainText("(optional)");
  await expect(page.getByTestId("qc-pay-skip")).toHaveText("Skip payment");
  await expect(page.getByTestId("qc-pay-cod-terms-note")).toHaveCount(0);
});
