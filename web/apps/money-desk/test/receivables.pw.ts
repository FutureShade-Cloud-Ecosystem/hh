// The receivables slice walked in a real browser against the stub, held to the
// Money Desk references (hh-sup/design-refs/money-desk): the register with its
// aging tiles and Needs you, an account's open invoices with the payment panel,
// the statement, and the credit holds. The stub is held to the core's shapes and
// sentences by test/stub-contract.test.mjs.
import { test, expect, openDesk, dayFromToday } from "./helpers.js";
import type { Page } from "@playwright/test";

async function openAccount(page: Page, name: string): Promise<void> {
  await page.locator("[data-register-row]", { hasText: name }).first().getByRole("button").click();
  await expect(page.getByRole("heading", { name })).toBeVisible();
  // The row's invoice arrives prefilled once the account has loaded; typing before then would be overwritten.
  await expect(page.locator("[data-field='receive-amount']")).not.toHaveValue("");
}

test("the way in with no handoff offers the role cards", async ({ page, request }) => {
  await request.post("/_stub/reset");
  await page.goto("/");
  await expect(page.locator("[data-flow]")).toHaveCount(2);
  await expect(page.locator("[data-flow]").first()).toContainText("Payables and Receivables");
});

test("the desk opens on the register: four tiles, the rows overdue first, Needs you, and later marked in words", async ({ page, request }) => {
  await openDesk(page, request);
  await expect(page.getByRole("heading", { name: "Receivables", exact: true })).toBeVisible();
  expect(await page.locator("[data-card] [data-card-title]").allTextContents()).toEqual(["Escalation", "Late", "Overdue", "Current"]);
  await expect(page.locator("[data-register-row]")).toHaveCount(9);
  const first = page.locator("[data-register-row]").first();
  await expect(first).toContainText("INV-1001");
  await expect(first).toContainText("104d overdue");
  await expect(first).toContainText("promise open");
  await expect(first).toContainText("104 days ago");
  await expect(page.locator("[data-register-row]", { hasText: "INV-1004" })).toContainText("disputed");
  await expect(page.locator("[data-register-row]", { hasText: "INV-1006" })).toContainText("in 9 days");
  await expect(page.locator("[data-register-foot]")).toContainText("9 invoices, $16,049.50");
  await expect(page.locator("[data-bounced-row]")).toHaveCount(1);
  await expect(page.locator("[data-need-hold]")).toHaveCount(2);
  await expect(page.locator("[data-need-paper]")).toHaveCount(1);
  await expect(page.locator(".tab-later", { hasText: "Payables" })).toContainText("later");
});

test("a tile filters the register to the accounts it counted, and clears", async ({ page, request }) => {
  await openDesk(page, request);
  await page.locator("[data-card='card.escalation']").getByRole("button").click();
  // The tile counted one account, which carries two open invoices.
  await expect(page.locator("[data-register-row]")).toHaveCount(2);
  await expect(page.locator("[data-register-row]").first()).toContainText("Granite Edge Landscaping");
  await page.locator("[data-card='card.escalation']").getByRole("button").click();
  await expect(page.locator("[data-register-row]")).toHaveCount(9);
});

test("the register's filters: overdue, promised, disputed and paid", async ({ page, request }) => {
  await openDesk(page, request);
  const pick = (word: string) => page.getByRole("group", { name: "Register filter" }).getByRole("button", { name: word, exact: true }).click();
  await pick("Overdue");
  await expect(page.locator("[data-register-row]")).toHaveCount(7);
  await pick("Promised");
  await expect(page.locator("[data-register-row]")).toHaveCount(1);
  await pick("Disputed");
  await expect(page.locator("[data-register-row]")).toHaveCount(1);
  await expect(page.locator("[data-register-row]")).toContainText("Open dispute");
  await pick("Paid");
  await expect(page.locator("[data-register-row]")).toHaveCount(1);
  await expect(page.locator("[data-register-row]")).toContainText("INV-1010");
});

test("aging to payment applied: Record payment on a row opens the account with the payment ready, and the server's balance shows", async ({ page, request }) => {
  await openDesk(page, request);
  await page.locator("[data-register-row]", { hasText: "INV-1002" }).getByRole("button", { name: "Record payment" }).click();
  await expect(page.getByRole("heading", { name: "Granite Edge Landscaping (demo)" })).toBeVisible();
  await expect(page.locator("[data-hold-banner]")).toContainText("on hold: Returned payment");
  await expect(page.locator("[data-field='receive-amount']")).toHaveValue("960.00");
  await expect(page.locator("[data-applied]")).toHaveText("$960.00 of $960.00");
  await page.locator("[data-field='receive-amount']").fill("100.00");
  await page.locator("[data-field='apply'][data-key]").nth(1).fill("100.00");
  await page.locator("[data-field='receive-reference']").fill("Cheque 1042");
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-notice]")).toContainText("PAY-0101 $100.00 on INV-1002, balance now $860.00");
  await expect(page.locator("[data-invoice-row]", { hasText: "INV-1002" })).toContainText("$860.00");
  await expect(page.locator("[data-hold-stays]")).toContainText("The hold stays");
});

test("one payment across two invoices: oldest first fills the apply column and posts once per invoice", async ({ page, request }) => {
  await openDesk(page, request);
  await openAccount(page, "Granite Edge Landscaping (demo)");
  await page.locator("[data-field='receive-amount']").fill("5000.00");
  await page.getByRole("button", { name: "Oldest first" }).click();
  await expect(page.locator("[data-allocation]")).toContainText("INV-1001 · closes");
  await expect(page.locator("[data-allocation]")).toContainText("INV-1002 · part");
  await expect(page.locator("[data-applied]")).toHaveText("$5,000.00 of $5,000.00");
  await expect(page.locator("[data-unapplied]")).toContainText("unapplied $0.00");
  await page.locator("[data-field='receive-reference']").fill("Cheque 4471");
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-notice]")).toContainText("PAY-0101 $4,825.00 on INV-1001, balance now $0.00");
  await expect(page.locator("[data-notice]")).toContainText("PAY-0102 $175.00 on INV-1002, balance now $785.00");
  await expect(page.locator("[data-invoice-row]")).toHaveCount(1);
});

test("a payment that does not add up is stopped in words before anything is sent", async ({ page, request }) => {
  await openDesk(page, request);
  await openAccount(page, "Granite Edge Landscaping (demo)");
  await page.getByRole("button", { name: "Clear" }).first().click();
  const reference = page.locator("[data-field='receive-reference']");
  await reference.fill("x");
  await page.locator("[data-field='receive-amount']").fill("100.00");
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-error]")).toContainText("Apply the payment to at least one invoice");
  await page.locator("[data-field='apply']").nth(1).fill("960.01");
  await page.locator("[data-field='receive-amount']").fill("960.01");
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-error]")).toHaveText("That is more than the balance of INV-1002, $960.00.");
  await page.locator("[data-field='apply']").nth(1).fill("50.00");
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-error]")).toContainText("$910.01 of the payment is not applied");
  await expect(page.locator("[data-error]")).toContainText("later");
});

test("a promise to pay is recorded, and a second is refused in the core's words", async ({ page, request }) => {
  await openDesk(page, request);
  await openAccount(page, "Oakline Builders (demo)");
  await page.getByRole("button", { name: "Record a promise" }).click();
  const fill = async () => {
    await page.locator("[data-field='promise-invoice']").selectOption("INV-1003");
    await page.locator("[data-field='promise-amount']").fill("500.00");
    await page.locator("[data-field='promise-date']").fill(dayFromToday(7));
    await page.locator("[data-field='promise-by']").fill("Accounts payable");
    await page.locator("[data-field='promise-note']").fill("Cheque goes out Friday");
    await page.getByRole("button", { name: "Record promise", exact: true }).click();
  };
  await fill();
  await expect(page.locator("[data-notice]")).toContainText("Promise recorded on INV-1003: $500.00");
  await expect(page.locator("[data-promise]")).toContainText(dayFromToday(7));
  await page.getByRole("button", { name: "Back to payment" }).click();
  await page.getByRole("button", { name: "Record a promise" }).click();
  await fill();
  await expect(page.locator("[data-error]")).toHaveText("This invoice already carries a live promise; settle it before recording another.");
});

test("a payment is marked bounced from its invoice, and worked from the queue", async ({ page, request }) => {
  await openDesk(page, request);
  await openAccount(page, "Riverstone Paving (demo)");
  await page.locator("[data-field='receive-amount']").fill("250.00");
  await page.locator("[data-field='apply']").nth(0).fill("250.00");
  await page.locator("[data-field='receive-reference']").fill("Cheque 7781");
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-notice]")).toContainText("PAY-0101 $250.00 on INV-1005");
  await page.locator("[data-invoice-row]", { hasText: "INV-1005" }).getByRole("button", { name: "INV-1005" }).click();
  await page.locator("[data-payment-row]").getByRole("button", { name: "Mark bounced" }).click();
  await page.locator("[data-field='bounce-reason']").fill("Cheque returned NSF");
  await page.getByRole("button", { name: "Confirm bounced" }).click();
  await expect(page.locator("[data-notice]")).toContainText("marked bounced");
  await expect(page.locator("[data-notice]")).toContainText("$250.00");
  await expect(page.locator("[data-payment-row]")).toContainText("Bounced");

  await page.getByRole("button", { name: "‹ Receivables" }).click();
  await expect(page.locator("[data-bounced-row]")).toHaveCount(2);
  await page.locator("[data-bounced-row]", { hasText: "Riverstone" }).getByRole("button").click();
  const work = page.locator("[data-bounced-work]");
  await expect(work).toContainText("INV-1005");
  await expect(work).toContainText("$250.00");
  await work.getByRole("radio", { name: /Promise to pay/ }).click();
  await work.getByRole("button", { name: "Record promise" }).click();
  await expect(page.getByRole("heading", { name: "Riverstone Paving (demo)" })).toBeVisible();
  await expect(page.locator("[data-promise-form]")).toBeVisible();
});

test("the bounced payment the demo world starts with: what happened, and a reminder sent from the queue", async ({ page, request }) => {
  await openDesk(page, request);
  await page.locator("[data-bounced-row]").getByRole("button").click();
  const work = page.locator("[data-bounced-work]");
  await expect(work).toContainText("PAY-0001");
  await expect(work).toContainText("INV-1002");
  await expect(work).toContainText("fee $35.00");
  await work.getByRole("radio", { name: /Send a reminder/ }).click();
  await work.getByRole("button", { name: "Send reminder" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Reminder sent for 2 invoices");
});

test("Remind on an account and the paper path item in Needs you chase what is overdue", async ({ page, request }) => {
  await openDesk(page, request);
  await page.locator("[data-need-paper]").getByRole("button").click();
  await expect(page.locator("[data-notice]")).toContainText("Reminder sent for 1 invoice");
  await openAccount(page, "Granite Edge Landscaping (demo)");
  await page.getByRole("button", { name: "Remind", exact: true }).click();
  await expect(page.locator("[data-notice]")).toContainText("Reminder sent for 2 invoices");
});

test("Record a payment on the home asks which account", async ({ page, request }) => {
  await openDesk(page, request);
  await page.getByRole("button", { name: "Record a payment", exact: true }).click();
  await page.locator("[data-field='picker-account']").selectOption("Summit Patio Co (demo)");
  await page.getByRole("button", { name: "Open account" }).click();
  await expect(page.getByRole("heading", { name: "Summit Patio Co (demo)" })).toBeVisible();
  await expect(page.locator("[data-receive]")).toBeVisible();
});

test("Download CSV hands over the register", async ({ page, request }) => {
  await openDesk(page, request);
  const download = page.waitForEvent("download");
  await page.getByRole("button", { name: "Download CSV" }).click();
  expect((await download).suggestedFilename()).toBe("receivables.csv");
});

test("credit holds: the sweep's holds are listed, lifting is held for this role and says who may, place a hold reads as later", async ({ page, request }) => {
  await openDesk(page, request);
  await page.getByRole("button", { name: "Credit holds", exact: true }).click();
  await expect(page.locator("[data-hold-row]")).toHaveCount(3);
  await expect(page.locator("[data-hold-row]", { hasText: "Cedar Ridge" })).toContainText("override in force");
  await expect(page.locator("[data-hold-row]", { hasText: "Oakline" })).toContainText("auto");
  const lift = page.locator("[data-hold-row]", { hasText: "Oakline" }).getByRole("button", { name: "Lift..." });
  await expect(lift).toHaveAttribute("aria-disabled", "true");
  await lift.click({ force: true });
  const words = page.locator("[data-error]");
  await expect(words).toContainText("Financial Controller");
  await expect(words).toContainText("General Manager");
  await expect(words).not.toContainText("Forbidden");
  await expect(page.locator("[data-held-words]")).toContainText("held for this role");
  await expect(page.locator("[data-hold-row]")).toHaveCount(3);
  await page.getByRole("button", { name: /Place a hold/ }).click();
  await expect(page.locator("[data-hold-panel]")).toContainText("later");
  await expect(page.locator("[data-hold-panel]")).toContainText("no route for it yet");
});

test("the controller lifts a hold with a reason, and overrides another", async ({ page, request }) => {
  await openDesk(page, request, "controller");
  await page.getByRole("button", { name: "Credit holds", exact: true }).click();
  await page.locator("[data-hold-row]", { hasText: "Oakline" }).getByRole("button", { name: "Lift..." }).click();
  await page.locator("[data-field='release-reason']").fill("Payment applied; promise open");
  await page.getByRole("button", { name: "Lift hold" }).click();
  await expect(page.locator("[data-notice]")).toContainText("The credit hold is lifted.");
  await expect(page.locator("[data-hold-row]")).toHaveCount(2);
  await page.locator("[data-hold-row]", { hasText: "Granite" }).getByRole("button", { name: "Override..." }).click();
  await page.getByRole("radio", { name: /Override instead/ }).click();
  await page.locator("[data-field='release-reason']").fill("Deposit is coming");
  await page.getByRole("button", { name: "Override hold" }).click();
  await expect(page.locator("[data-notice]")).toContainText("until the override expires");
  await expect(page.locator("[data-hold-row]", { hasText: "Granite" })).toContainText("override in force");
});

test("the statement: none yet, run it, read the aging strip, send it; PDF reads as later", async ({ page, request }) => {
  await openDesk(page, request);
  await openAccount(page, "Summit Patio Co (demo)");
  await page.getByRole("button", { name: "Statement", exact: true }).click();
  await expect(page.locator("[data-statement]")).toContainText("No statement has been run for this account yet.");
  await page.getByRole("button", { name: "Run a statement" }).click();
  await expect(page.locator("[data-closing]")).toContainText("$623.00");
  await expect(page.locator("[data-statement]")).toContainText("1 to 30");
  await page.getByRole("button", { name: "Send to the AP contact" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Statement queued to the AP contact");
  await page.getByRole("button", { name: /Download PDF/ }).click({ force: true });
  await expect(page.locator("[data-error]")).toContainText("later");
  await expect(page.locator(".tab-later", { hasText: "Activity" })).toContainText("later");
  await expect(page.locator(".tab-later", { hasText: "Contacts" })).toContainText("later");
});

test("the Statements tab lists every account and runs them all", async ({ page, request }) => {
  await openDesk(page, request);
  await page.getByRole("button", { name: "Statements", exact: true }).click();
  await expect(page.locator("[data-statement-row]")).toHaveCount(5);
  await expect(page.locator("[data-statement-row]").first()).toContainText("None yet");
  await page.getByRole("button", { name: "Run for every account" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Statements run: 5 accounts");
  await expect(page.locator("[data-statement-row]").first()).toContainText("As of");
});
