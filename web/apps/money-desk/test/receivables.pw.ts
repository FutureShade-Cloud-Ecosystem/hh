// The receivables slice walked in a real browser against the stub: the card
// lands on the desk, the aging opens an account, a payment is applied to an
// overdue invoice, a promise to pay is recorded, a payment is marked bounced and
// worked in the queue, and the walled moves are refused in words. The stub is
// held to the core's shapes and sentences by test/stub-contract.test.mjs.
import { test, expect, openDesk, dayFromToday } from "./helpers.js";

test("the way in with no handoff offers this role's own card", async ({ page, request }) => {
  await request.post("/_stub/reset");
  await page.goto("/");
  await expect(page.locator("[data-flow]")).toHaveCount(1);
  await expect(page.locator("[data-flow]")).toContainText("Payables and Receivables");
});

test("the card lands on the desk: four aging cards, the aging, the credit hold and the bounced queue", async ({ page, request }) => {
  await openDesk(page, request);
  await expect(page.getByRole("heading", { name: "Receivables" })).toBeVisible();
  const titles = await page.locator("[data-card] [data-card-title]").allTextContents();
  expect(titles).toEqual(["Escalation", "Late", "Overdue", "Current"]);
  await expect(page.locator("[data-aging-row]")).toHaveCount(5);
  await expect(page.locator("[data-tab='bounced']")).toContainText("1");
});

test("a card filters the aging to the accounts it counted, and clears", async ({ page, request }) => {
  await openDesk(page, request);
  await page.locator("[data-card='card.escalation']").click();
  await expect(page.locator("[data-aging-row]")).toHaveCount(1);
  await expect(page.locator("[data-aging-row]")).toContainText("Granite Edge Landscaping");
  await page.getByRole("button", { name: "Show every account" }).click();
  await expect(page.locator("[data-aging-row]")).toHaveCount(5);
});

test("aging to payment applied: an overdue invoice takes a payment and the server's balance shows", async ({ page, request }) => {
  await openDesk(page, request);
  await page.locator("[data-aging-row]", { hasText: "Granite Edge Landscaping" }).getByRole("button", { name: "Open account" }).click();
  await expect(page.getByRole("heading", { name: "Granite Edge Landscaping" })).toBeVisible();
  const row = page.locator("[data-invoice-row]", { hasText: "INV-1002" });
  await expect(row).toContainText("Overdue");
  await expect(row).toContainText("$960.00");
  await row.getByRole("button", { name: "Open invoice" }).click();
  await page.locator("[data-field='pay-amount']").fill("100.00");
  await page.locator("[data-field='pay-reference']").fill("Cheque 1042");
  await page.getByRole("button", { name: "Apply payment" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Payment PAY-0101 applied");
  await expect(page.locator("[data-notice]")).toContainText("$860.00");
  await expect(page.locator("[data-invoice-row]", { hasText: "INV-1002" })).toContainText("$860.00");
  // The invoice's earlier payment came back from the bank and stays on its record.
  await expect(page.locator("[data-payment-row]")).toHaveCount(2);
  await expect(page.locator("[data-payment-row]", { hasText: "PAY-0101" })).toContainText("Received");
  await expect(page.locator("[data-payment-row]", { hasText: "PAY-0001" })).toContainText("Bounced");
});

test("a payment over the balance is stopped in words before it is sent", async ({ page, request }) => {
  await openDesk(page, request);
  await page.locator("[data-aging-row]", { hasText: "Granite Edge Landscaping" }).getByRole("button", { name: "Open account" }).click();
  await page.locator("[data-invoice-row]", { hasText: "INV-1002" }).getByRole("button", { name: "Open invoice" }).click();
  await page.locator("[data-field='pay-amount']").fill("960.01");
  await page.locator("[data-field='pay-reference']").fill("x");
  await page.getByRole("button", { name: "Apply payment" }).click();
  await expect(page.locator("[data-error]")).toHaveText("That is more than the invoice balance of $960.00.");
});

test("a promise to pay is recorded, and a second one is refused in the core's words", async ({ page, request }) => {
  await openDesk(page, request);
  await page.locator("[data-aging-row]", { hasText: "Oakline Builders" }).getByRole("button", { name: "Open account" }).click();
  await page.locator("[data-invoice-row]", { hasText: "INV-1003" }).getByRole("button", { name: "Open invoice" }).click();
  const fill = async () => {
    await page.locator("[data-field='promise-amount']").fill("500.00");
    await page.locator("[data-field='promise-date']").fill(dayFromToday(7));
    await page.locator("[data-field='promise-by']").fill("Accounts payable");
    await page.locator("[data-field='promise-note']").fill("Cheque goes out Friday");
    await page.getByRole("button", { name: "Record promise" }).click();
  };
  await fill();
  await expect(page.locator("[data-notice]")).toContainText("Promise recorded");
  await expect(page.locator("[data-promise]")).toContainText("$500.00");
  await expect(page.locator("[data-promise]")).toContainText(dayFromToday(7));
  // A second promise on the same invoice is the core's refusal, in the core's words.
  await fill();
  await expect(page.locator("[data-error]")).toHaveText("This invoice already carries a live promise; settle it before recording another.");
});

test("a payment is marked bounced, and worked in the queue", async ({ page, request }) => {
  await openDesk(page, request);
  await page.locator("[data-aging-row]", { hasText: "Riverstone Paving" }).getByRole("button", { name: "Open account" }).click();
  await page.locator("[data-invoice-row]", { hasText: "INV-1005" }).getByRole("button", { name: "Open invoice" }).click();
  await page.locator("[data-field='pay-amount']").fill("250.00");
  await page.locator("[data-field='pay-reference']").fill("Cheque 7781");
  await page.getByRole("button", { name: "Apply payment" }).click();
  await expect(page.locator("[data-payment-row]")).toHaveCount(1);
  await page.locator("[data-payment-row]").getByRole("button", { name: "Mark bounced" }).click();
  await page.locator("[data-field='bounce-reason']").fill("Cheque returned NSF");
  await page.getByRole("button", { name: "Confirm bounced" }).click();
  await expect(page.locator("[data-notice]")).toContainText("marked bounced");
  await expect(page.locator("[data-notice]")).toContainText("$250.00");
  await expect(page.locator("[data-payment-row]", { hasText: "PAY-0101" })).toContainText("Bounced");

  await page.getByRole("button", { name: "Back to receivables" }).click();
  await page.locator("[data-tab='bounced']").click();
  await expect(page.locator("[data-bounced-row]")).toHaveCount(2);
  const mine = page.locator("[data-bounced-row]", { hasText: "Riverstone Paving" });
  await mine.getByRole("button", { name: "Work it" }).click();
  await expect(page.locator("[data-bounced-work]")).toContainText("INV-1005");
  await expect(page.locator("[data-bounced-work]")).toContainText("$250.00");
  await page.locator("[data-bounced-work]").getByRole("button", { name: "Open the account" }).click();
  await expect(page.getByRole("heading", { name: "Riverstone Paving" })).toBeVisible();
  await expect(page.locator("[data-invoice-row]", { hasText: "INV-1005" })).toContainText("$887.50");
});

test("the bounced queue the demo world starts with is shown", async ({ page, request }) => {
  await openDesk(page, request);
  await page.locator("[data-tab='bounced']").click();
  await expect(page.locator("[data-bounced-row]")).toHaveCount(1);
  await expect(page.locator("[data-bounced-row]")).toContainText("PAY-0001");
  await expect(page.locator("[data-bounced-row]")).toContainText("$960.00");
});

test("releasing a credit hold is refused in words that name who may do it", async ({ page, request }) => {
  await openDesk(page, request);
  await page.locator("[data-tab='holds']").click();
  await expect(page.locator("[data-hold-row]")).toHaveCount(1);
  await page.locator("[data-field='release-reason']").fill("Paid in full");
  await page.getByRole("button", { name: "Release hold" }).click();
  const words = page.locator("[data-error]");
  await expect(words).toContainText("Financial Controller");
  await expect(words).toContainText("General Manager");
  await expect(words).not.toContainText("Forbidden");
  await expect(page.locator("[data-hold-row]")).toHaveCount(1);
});

test("a statement is run for an account and read back", async ({ page, request }) => {
  await openDesk(page, request);
  await page.locator("[data-aging-row]", { hasText: "Summit Patio Co" }).getByRole("button", { name: "Open account" }).click();
  await expect(page.locator("[data-statement]")).toContainText("No statement has been run for this account yet.");
  await page.getByRole("button", { name: "Run a statement" }).click();
  await expect(page.locator("[data-statement]")).toContainText("Closing balance");
  await expect(page.locator("[data-statement]")).toContainText("$623.00");
});

