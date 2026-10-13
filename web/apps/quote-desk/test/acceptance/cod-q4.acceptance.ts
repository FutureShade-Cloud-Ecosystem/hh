// Round 14 item 24 (decision log row 1116): the seeded Cash On Demand quote DEMO-PLM-Q4 walked through the guided
// Convert surface to payment before release, with Will-call, on the real demo core. It asserts the order, the invoice
// and the payment, and that nothing is released unpaid. The seeded quote is consumed by the walk, so run it on a fresh
// world (test/acceptance/README.md). The same story on the stub runs in test/convert-cod-terms.pw.ts.
import { expect, test } from "@playwright/test";

test("DEMO-PLM-Q4 converts through the guided surface with Will-call and the whole payment: order, invoice, payment, nothing released unpaid", async ({ page }) => {
  // Every write the desk makes, in order, and the answers that matter.
  const writes: string[] = [];
  let convert: { order_id: string; order_no: string } | null = null;
  let payment: { order_id: string; order_no: string; invoice_id: string; invoice_no: string; invoice_total_cents: number; payment_id: string; amount_cents: number; balance_cents: number; method: string } | null = null;
  page.on("response", async (response) => {
    const request = response.request();
    if (request.method() === "GET") return;
    const path = new URL(response.url()).pathname;
    writes.push(`${request.method()} ${response.status()} ${path.replace(/[0-9a-f-]{36}/g, "{id}")}`);
    if (path.endsWith("/convert")) convert = (await response.json()) as typeof convert;
    if (path.endsWith("/order-payment")) payment = (await response.json()) as typeof payment;
  });

  await page.goto("/");
  await page.getByRole("button", { name: "Sales manager", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible({ timeout: 30_000 });
  const row = page.getByTestId("qb-list-row").filter({ hasText: "DEMO-PLM-Q4" });
  await expect(row).toHaveCount(1, { timeout: 30_000 });
  await row.click();
  await expect(page.getByTestId("qb-status")).toContainText(/accepted/i);

  // The one way in: the accepted quote's panel sends to the guided surface.
  await page.locator("qd-order-payment").getByTestId("op-convert").click();
  await expect(page.getByTestId("qc-title")).toBeVisible();
  const totalText = await page.getByTestId("qc-total").innerText();
  const total = Number((/\$([\d,]+\.\d{2})/.exec(totalText) ?? [])[1]?.replace(/,/g, ""));
  expect(total).toBeGreaterThan(0);

  await page.getByTestId("qc-next").click();
  await page.getByTestId("qc-fulfil-will-call").click();
  await page.getByTestId("qc-next").click();
  await expect(page.getByTestId("qc-terms")).toContainText("Cash On Demand");
  await expect(page.getByTestId("qc-terms")).toContainText("nothing is released until it is paid");
  await page.getByTestId("qc-next").click();

  // Payment before release: the whole amount, by cash.
  await expect(page.getByTestId("qc-payment")).toContainText("held until it is paid in full");
  await page.getByTestId("qc-pay-amount").fill(total.toFixed(2));
  await page.getByTestId("qc-next").click();
  await expect(page.getByTestId("qc-confirm-step")).toContainText("Will-call");
  await expect(page.getByTestId("qc-confirm-payment")).toContainText("paid in full before it is released");
  await page.getByTestId("qc-confirm").click();
  await expect(page.getByTestId("qc-done-order")).toContainText("created", { timeout: 30_000 });
  await expect(page.getByTestId("qc-done-payment")).toContainText("Balance on the invoice: $0.00");

  // The order.
  expect(convert, "the conversion answered").not.toBeNull();
  await expect(page.getByTestId("qc-done-order")).toContainText(convert!.order_no.replace(/^#/, ""));
  // The invoice and the payment.
  expect(payment, "the payment answered").not.toBeNull();
  expect(payment!.order_id).toBe(convert!.order_id);
  expect(payment!.invoice_no).not.toBe("");
  expect(payment!.invoice_total_cents).toBe(Math.round(total * 100));
  expect(payment!.method).toBe("CASH");
  expect(payment!.amount_cents).toBe(payment!.invoice_total_cents);
  expect(payment!.balance_cents).toBe(0);

  // Nothing released unpaid: the order is made, then the whole payment is taken against its invoice, and the desk made
  // no other write, so no release, pick or dispatch call was ever sent.
  expect(writes.filter((w) => !w.includes("/demo/session"))).toEqual(["POST 201 /api/v1/quotes/{id}/convert", "POST 200 /api/v1/quotes/{id}/order-payment"]);
  const order = await page.request.get(`/api/v1/orders/${convert!.order_id}`, { failOnStatusCode: false });
  if (order.ok()) {
    const body = (await order.json()) as { status?: string; fulfillment?: string };
    expect(body.fulfillment ?? "WILL_CALL").toBe("WILL_CALL");
  }
});
