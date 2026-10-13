// Item 9 (row 1047 item 6): the deposit and Pay balance story as the clerk, against the demo
// world: a deposit for a credit customer (line one the deposit, line two on account), a deposit for
// the Cash On Demand customer ending on the held card, and Pay balance finding the held order the
// seed plants (DEMO-PLM-D02) and taking the rest to Paid in full. Every figure on the screen is
// held against what the core answers through its own reads.
import {
  expect,
  mint,
  openPayBalance,
  openStep,
  pickCustomer,
  searchAndAdd,
  shot,
  signIn,
  staffGet,
  test,
  type Rung,
} from "./helpers.js";

/** Will-call is a fulfilment that waits: the only kind that takes a deposit. */
async function chooseWillCall(page: import("@playwright/test").Page): Promise<void> {
  await openStep(page, "items");
  await page.getByRole("button", { name: "Will-call" }).click();
}

/** The held card's pay hold, read the way the till reads it: the staff order read. */
async function readPayHold(page: import("@playwright/test").Page, bearer: string, orderId: string) {
  const answer = await staffGet(page, bearer, `/api/v1/orders/${orderId}`);
  expect(answer.status).toBe(200);
  return answer.body["pay_hold"] as {
    held_from_picking: boolean;
    in_process_words?: string;
    owed_cents: number;
    paid_cents: number;
  };
}

test("a deposit for a credit customer: line one the deposit, line two on account, and the ring books it", async ({
  page,
}, testInfo) => {
  await signIn(page, "Sales");
  await pickCustomer(page, "C02", "C02");
  await searchAndAdd(page, "charcoal", "Charcoal");
  await chooseWillCall(page);
  await openStep(page, "payment");
  await page.getByTestId("pay-shape-DEPOSIT_NOW_REST_ON_ACCOUNT").click();
  await page.getByTestId("deposit-panel").getByRole("button", { name: "Cash", exact: true }).click();
  await page.getByTestId("deposit-amount").fill("1.00");
  await expect(page.getByTestId("deposit-line-one")).toContainText("Deposit · Cash");
  await expect(page.getByTestId("deposit-line-two")).toContainText("On account");
  await expect(page.getByTestId("deposit-sentence")).toHaveText(
    "A credit customer: the rest rides their account and is paid from the statement.",
  );
  await expect(page.getByTestId("deposit-commit")).toHaveText("Ring sale with $1.00 deposit");
  await shot(page, "deposits-01-credit-payment", testInfo);

  const posted = page.waitForResponse(
    (response) => response.url().endsWith("/api/v1/pos/sales") && response.request().method() === "POST",
  );
  await page.getByTestId("deposit-commit").click();
  const answer = await posted;
  expect(answer.ok()).toBe(true);
  const sale = (await answer.json()) as Rung;
  expect(sale.deposit_cents).toBe(100);
  await expect(page.getByTestId("sold-panel")).toContainText("Deposit taken");
  // The core's own read: the invoice owes the rest, and a credit customer's order is not held.
  const bearer = await mint(page, "sales");
  const hold = await readPayHold(page, bearer, sale.order_id);
  expect(hold.held_from_picking).toBe(false);
  await expect(page.getByTestId("held-order")).toHaveCount(0);
});

test("a deposit for the Cash On Demand customer ends on the held card, and the core agrees", async ({
  page,
}, testInfo) => {
  await signIn(page, "Sales");
  await pickCustomer(page, "C10", "C10");
  await searchAndAdd(page, "charcoal", "Charcoal");
  await chooseWillCall(page);
  await openStep(page, "payment");
  await expect(page.getByTestId("pay-shape-ALL_ON_ACCOUNT")).toHaveCount(0);
  await page.getByTestId("pay-shape-DEPOSIT_NOW_REST_DUE_LATER").click();
  await page.getByTestId("deposit-panel").getByRole("button", { name: "Cash", exact: true }).click();
  await page.getByTestId("deposit-amount").fill("1.00");
  await expect(page.getByTestId("deposit-line-two")).toContainText("Balance due later");
  await expect(page.getByTestId("deposit-sentence")).toContainText("never cash on delivery");
  await shot(page, "deposits-02-cod-payment", testInfo);

  const posted = page.waitForResponse(
    (response) => response.url().endsWith("/api/v1/pos/sales") && response.request().method() === "POST",
  );
  await page.getByTestId("deposit-commit").click();
  const sale = (await (await posted).json()) as Rung;
  expect(sale.deposit_cents).toBe(100);
  const held = page.getByTestId("held-order");
  await expect(held).toBeVisible();
  await expect(held.getByTestId("held-chip-process")).toHaveText("In process: deposit taken");
  await expect(held.getByTestId("held-chip-hold")).toHaveText("Held from picking");
  await expect(held.getByTestId("held-title")).toHaveText("This order cannot reach picking until the balance is paid.");
  const bearer = await mint(page, "sales");
  const hold = await readPayHold(page, bearer, sale.order_id);
  expect(hold.held_from_picking).toBe(true);
  expect(hold.in_process_words).toBe("In process: deposit taken");
  expect(hold.paid_cents).toBe(100);
  await shot(page, "deposits-03-held-card", testInfo);
});

test("Pay balance finds the held order the seed plants and takes the rest to Paid in full", async ({
  page,
}, testInfo) => {
  await signIn(page, "Sales");
  await openPayBalance(page);
  await page.getByTestId("pay-balance-term").fill("DEMO-PLM-D02");
  await page.getByTestId("pay-balance-find").click();
  const card = page.getByTestId("pay-balance-order");
  await expect(card).toContainText(/Order · DEMO-PLM-D02/i);
  await expect(page.getByTestId("held-chip-hold")).toHaveText("Held from picking");
  const owedText = (await page.getByTestId("pay-balance-owed").innerText()).replace(/\s+/g, " ");
  const owed = owedText.match(/\$([\d,]+\.\d\d)/);
  expect(owed, "Still owed reads a figure").not.toBeNull();
  await expect(page.getByTestId("pay-balance-take")).toHaveText(`Take $${owed?.[1]}`);
  await expect(page.getByTestId("pay-balance-whole-only")).toHaveText(
    "The whole balance: this door takes no part payments.",
  );
  await shot(page, "deposits-04-pay-balance-found", testInfo);

  await page.getByTestId("pay-balance-method-CASH").click();
  const posted = page.waitForResponse(
    (response) => response.url().endsWith("/api/v1/pos/pay-balance") && response.request().method() === "POST",
  );
  await page.getByTestId("pay-balance-take").click();
  const taken = (await (await posted).json()) as { order_id: string; still_owed_cents: number; released_to_picking: boolean };
  expect(taken.still_owed_cents).toBe(0);
  expect(taken.released_to_picking).toBe(true);
  await expect(page.getByTestId("pay-balance-done")).toContainText("DEMO-PLM-D02 reads Paid in full and is released to picking.");
  await expect(page.getByTestId("pay-balance-done")).toContainText("Money trail:");
  // The core's own read: the hold lifted with the last dollar.
  const bearer = await mint(page, "sales");
  const hold = await readPayHold(page, bearer, taken.order_id);
  expect(hold.held_from_picking).toBe(false);
  expect(hold.owed_cents).toBe(0);
  await shot(page, "deposits-05-pay-balance-paid", testInfo);
});
