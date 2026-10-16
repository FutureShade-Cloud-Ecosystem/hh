// The receivables office walked in a real browser against the stub: the statement run read back with what went to
// whom, the AP contact, reminders and the paper path, an invoice's send, document, dispute and a payment reversal,
// and the settings. The stub is held to the core's shapes, sentences and gates by test/office-stub-contract.test.mjs.
import { test, expect, openDesk, receivablesHandoff } from "./helpers.js";
import type { APIRequestContext, Page } from "@playwright/test";

/** Signs in as Payables and Receivables (or the controller) on a stub world named by its scenario, and lands on the desk. */
async function openOffice(page: Page, request: APIRequestContext, scenario: "pro" | "empty", who?: "controller"): Promise<void> {
  const answer = await request.post("/_stub/scenario", { data: { name: scenario } });
  expect(answer.ok()).toBe(true);
  await page.addInitScript(receivablesHandoff, who);
  await page.goto("/");
  if (who === "controller") await page.getByRole("button", { name: "Receivables", exact: true }).click();
  await expect(page.locator("[data-card]")).toHaveCount(4);
}

async function openTab(page: Page, name: string): Promise<void> {
  await page.getByRole("navigation", { name: "Money Desk views" }).getByRole("button", { name, exact: true }).click();
}

async function openAccount(page: Page, name: string): Promise<void> {
  await page.locator("[data-register-row]", { hasText: name }).first().getByRole("button").click();
  await expect(page.getByRole("heading", { name })).toBeVisible();
}

async function noSidewaysScroll(page: Page): Promise<void> {
  const wide = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth);
  expect(wide).toBe(false);
}

// ---- statement runs and AP contacts --------------------------------------------------------------------------

test("a statement run is read back with what went to whom, and no address is drawn", async ({ page, request }) => {
  await openDesk(page, request);
  await page.getByRole("button", { name: "Statements", exact: true }).click();
  await expect(page.locator("[data-run-none]")).toBeVisible();
  await page.getByRole("button", { name: "Run for every account" }).first().click();
  await expect(page.locator("[data-run]")).toBeVisible();
  await expect(page.locator("[data-run-mode]")).toHaveText("Month end, every account");
  await expect(page.locator("[data-run-count]")).toHaveText("5");
  await expect(page.locator("[data-run-notices]")).toHaveText("4 statements went to the messaging sink; 1 had no AP contact or was held back.");
  await expect(page.locator("[data-run-row]")).toHaveCount(5);
  await expect(page.locator("[data-run-row]", { hasText: "Granite Edge" }).locator("[data-run-goes]")).toHaveText("Queued as a text to the account's consented number");
  await expect(page.locator("[data-run-how]")).toHaveText("A statement with a balance is queued as a text to the account's consented number when notices are on.");
  await expect(page.locator("[data-run-row]", { hasText: "Cedar Ridge" }).locator("[data-run-goes]")).toHaveText("Not queued");
  await expect(page.locator("[data-run-row]", { hasText: "Cedar Ridge" })).toContainText("Not queued");
  expect(await page.locator("[data-run]").innerText()).not.toMatch(/@|gls-demo/);
});

test("an account's AP contact is tagged, replaced and removed, and checked before it is sent", async ({ page, request }) => {
  await openDesk(page, request);
  await openAccount(page, "Cedar Ridge");
  await page.getByRole("button", { name: "Contacts", exact: true }).click();
  await expect(page.locator("[data-contact-line]")).toHaveText("No AP contact is tagged on this account.");

  const sent: string[] = [];
  page.on("request", (r) => {
    if (r.method() === "PUT" && r.url().includes("/ap-contact")) sent.push(r.url());
  });
  await page.locator("[data-field='contact-name']").fill("Accounts payable desk");
  await page.getByRole("button", { name: "Tag the contact" }).click();
  await expect(page.locator("[data-error]")).toContainText("An AP contact needs an email or a phone");
  await page.locator("[data-field='contact-email']").fill("no at sign");
  await page.getByRole("button", { name: "Tag the contact" }).click();
  await expect(page.locator("[data-error]")).toContainText("That AP email address is not an address.");
  expect(sent).toEqual([]);

  await page.locator("[data-field='contact-email']").fill("ap@gls-demo.invalid");
  await page.getByRole("button", { name: "Tag the contact" }).click();
  await expect(page.locator("[data-notice]")).toHaveText("The AP contact is saved. Reminders for this account go to it through the messaging sink.");
  await expect(page.locator("[data-contact-line]")).toHaveText("AP contact on file, Accounts payable desk: email on file, no phone.");
  expect(await page.locator("[data-contact-panel]").innerText()).not.toMatch(/@|gls-demo/);
  expect(await page.locator("[data-field='contact-email']").inputValue()).toBe("");

  await page.locator("[data-field='contact-phone']").fill("5550100");
  await page.getByRole("button", { name: "Replace the contact" }).click();
  await expect(page.locator("[data-contact-line]")).toHaveText("AP contact on file, Accounts payable desk: no email, phone on file.");

  await page.getByRole("button", { name: "Remove the contact" }).click();
  await page.getByRole("button", { name: "Keep it" }).click();
  await expect(page.locator("[data-contact-line]")).toContainText("phone on file");
  await page.getByRole("button", { name: "Remove the contact" }).click();
  await page.getByRole("button", { name: "Remove it now" }).click();
  await expect(page.locator("[data-notice]")).toContainText("The AP contact is removed.");
  await expect(page.locator("[data-contact-line]")).toHaveText("No AP contact is tagged on this account.");
});

test("on a phone the run and the contact fit the screen and work from the keyboard", async ({ page, request }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openDesk(page, request);
  await page.getByRole("button", { name: "Statements", exact: true }).click();
  await page.getByRole("button", { name: "Run for every account" }).first().click();
  await expect(page.locator("[data-run-row]")).toHaveCount(5);
  await noSidewaysScroll(page);
  await page.getByRole("button", { name: "Receivables", exact: true }).last().click();
  await openAccount(page, "Cedar Ridge");
  await page.getByRole("button", { name: "Contacts", exact: true }).click();
  await expect(page.locator("[data-contact-panel]")).toBeVisible();
  await noSidewaysScroll(page);
  await page.locator("[data-field='contact-name']").focus();
  await page.keyboard.press("Tab");
  await expect(page.locator("[data-field='contact-email']")).toBeFocused();
});

// ---- reminders and the paper path ----------------------------------------------------------------------------

async function fillReminder(page: Page, account: string): Promise<void> {
  await page.locator("[data-field='remind-account']").selectOption({ label: account });
  await expect(page.locator("[data-field='remind-invoice']")).toBeEnabled();
}

test("on the base plan a reminder says why nothing goes, and the server's own sentence is what a press shows", async ({ page, request }) => {
  await openDesk(page, request);
  await openTab(page, "Reminders");
  await expect(page.locator("[data-plan]")).toHaveText("Base plan");
  await expect(page.locator("[data-history-empty]")).toHaveText("No reminder has been sent yet.");
  await fillReminder(page, "Granite Edge Landscaping (demo)");
  await expect(page.locator("[data-remind-blocked]")).toHaveText("Reminders are part of accelerated AR, and this dealer is on the base plan.");
  await expect(page.getByRole("button", { name: "Send reminder" })).toHaveAttribute("aria-disabled", "true");
  await page.getByRole("button", { name: "Send reminder" }).click({ force: true });
  await expect(page.locator("[data-error]")).toHaveText("Reminders are part of accelerated AR, this account is on the base plan.");
  await expect(page.locator("[data-history-row]")).toHaveCount(0);
});

test("a reminder goes to the AP contact by its role, is stamped on a resend, and lists without an address", async ({ page, request }) => {
  await openOffice(page, request, "pro");
  await openTab(page, "Reminders");
  await expect(page.locator("[data-plan]")).toHaveText("Accelerated AR");
  await fillReminder(page, "Granite Edge Landscaping (demo)");
  await expect(page.locator("[data-remind-line]")).toHaveText("Goes to the AP contact (Accounts payable desk), by email, through the messaging sink.");
  await page.getByRole("button", { name: "Send reminder" }).click();
  await expect(page.locator("[data-notice]")).toHaveText("Reminder for INV-1001 went to the AP contact by email, through the messaging sink.");
  await expect(page.locator("[data-history-row]")).toHaveCount(1);
  await expect(page.locator("[data-history-row]").first()).toContainText("the AP contact, by email");
  await expect(page.locator("[data-history-row]").first()).toContainText("Escalation");
  await expect(page.locator("[data-history-row]").first()).toContainText("Not opened");

  await page.getByRole("checkbox", { name: "Send it again" }).click();
  await page.getByRole("button", { name: "Send reminder" }).click();
  await expect(page.locator("[data-notice]")).toContainText("It was stamped PAST DUE");
  await expect(page.locator("[data-history-row]")).toHaveCount(2);
  await expect(page.locator("[data-history-row]").first()).toContainText("(resend)");

  await page.locator("[data-field='history-account']").selectOption({ label: "Oakline Builders (demo)" });
  await expect(page.locator("[data-history-empty]")).toHaveText("No reminder has been sent to this account yet.");
  expect(await page.locator("main").innerText()).not.toMatch(/@|gls-demo|\.invalid/);
});

test("the channel and the account's contact decide what a reminder would do, and the server's refusal is said as written", async ({ page, request }) => {
  await openOffice(page, request, "pro");
  await openTab(page, "Reminders");
  await fillReminder(page, "Granite Edge Landscaping (demo)");
  await page.getByRole("button", { name: "Text", exact: true }).click();
  await expect(page.locator("[data-remind-blocked]")).toHaveText("Text reminders are switched off for this dealer.");
  await page.getByRole("button", { name: "Email", exact: true }).click();

  await fillReminder(page, "Cedar Ridge Contractors (demo)");
  await expect(page.locator("[data-remind-line]")).toContainText("Nobody is tagged for AP on this account. The account's own address is used only if you confirm it below.");
  await page.getByRole("button", { name: "Send reminder" }).click();
  await expect(page.locator("[data-error]")).toHaveText("Nobody is tagged for AP on this account, confirm the fallback recipient or tag a contact.");
  await expect(page.locator("[data-history-row]")).toHaveCount(0);
  await page.getByRole("checkbox", { name: "Confirm the account's own address" }).click();
  await expect(page.locator("[data-remind-line]")).toContainText("The account's own address will be used, as you confirmed.");
  await page.getByRole("button", { name: "Send reminder" }).click();
  await expect(page.locator("[data-notice]")).toContainText("went to the account's own address, which you confirmed");
  await expect(page.locator("[data-history-row]").first()).toContainText("the account's own address, which you confirmed");
});

test("a reminder whose answer was lost is sent again with the same key and is recorded once", async ({ page, request }) => {
  await openOffice(page, request, "pro");
  await openTab(page, "Reminders");
  await fillReminder(page, "Granite Edge Landscaping (demo)");
  const keys: string[] = [];
  let lost = true;
  await page.route("**/api/v1/ar/reminders", async (route) => {
    if (route.request().method() !== "POST") return route.continue();
    keys.push(JSON.parse(route.request().postData() ?? "{}").idempotency_key);
    if (lost) {
      lost = false;
      await route.fetch();
      return route.abort("connectionreset");
    }
    return route.continue();
  });
  await page.getByRole("button", { name: "Send reminder" }).click();
  await expect(page.locator("[data-error]")).toContainText("could not be reached");
  await page.getByRole("button", { name: "Send reminder" }).click();
  await expect(page.locator("[data-notice]")).toContainText("went to the AP contact");
  expect(keys.length).toBe(2);
  expect(keys[0]).toBe(keys[1]);
  await expect(page.locator("[data-history-row]")).toHaveCount(1);
});

test("the paper path closes a flag with a note, and the Needs you list follows", async ({ page, request }) => {
  await openDesk(page, request);
  await expect(page.locator("[data-need-paper]")).toHaveCount(1);
  await openTab(page, "Reminders");
  await expect(page.locator("[data-paper-row]")).toHaveCount(1);
  await expect(page.locator("[data-paper-row]")).toContainText("Summit Patio Co (demo)");
  await expect(page.locator("[data-paper-row]")).toContainText("3 reminders in 45 days, none opened");
  await page.getByRole("button", { name: "Moved to paper" }).click();
  await page.getByRole("button", { name: "Move to paper" }).click();
  await expect(page.locator("[data-error]")).toHaveText("Say why in a few words: the next person to open this account reads it, not the flag.");
  await expect(page.locator("[data-paper-row]")).toHaveCount(1);
  await page.locator("[data-field='paper-note']").fill("Moved to paper statements by phone");
  await page.getByRole("button", { name: "Move to paper" }).click();
  await expect(page.locator("[data-notice]")).toContainText("marked as moved to paper");
  await expect(page.locator("[data-paper-empty]")).toHaveText("No account is flagged.");
  await openTab(page, "Receivables");
  await expect(page.locator("[data-need-paper]")).toHaveCount(0);
});

test("the sweep raises the account that ignored three reminders", async ({ page, request }) => {
  await openOffice(page, request, "pro");
  await openTab(page, "Reminders");
  await fillReminder(page, "Riverstone Paving (demo)");
  for (let i = 1; i <= 3; i += 1) {
    if (i > 1) await page.getByRole("checkbox", { name: "Send it again" }).click();
    await page.getByRole("button", { name: "Send reminder" }).click();
    await expect(page.locator("[data-history-row]")).toHaveCount(i);
  }
  await page.getByRole("button", { name: "Run the sweep" }).click();
  await expect(page.locator("[data-notice]")).toContainText("raised 1 new flag.");
  await expect(page.locator("[data-paper-row]")).toHaveCount(2);
  await expect(page.locator("[data-paper-row]").first()).toContainText("Riverstone Paving (demo)");
  await page.getByRole("button", { name: "Run the sweep" }).click();
  await expect(page.locator("[data-notice]")).toContainText("raised 0 new flags.");
  await page.getByRole("button", { name: "Dismiss", exact: true }).first().click();
  await page.locator("[data-field='paper-note']").fill("Phoned the desk");
  await page.getByRole("button", { name: "Dismiss the flag" }).click();
  await expect(page.locator("[data-paper-row]")).toHaveCount(1);
});

test("on a phone the reminders screen fits, the history becomes cards, and the form works from the keyboard", async ({ page, request }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openOffice(page, request, "pro");
  await openTab(page, "Reminders");
  await fillReminder(page, "Granite Edge Landscaping (demo)");
  await page.getByRole("button", { name: "Send reminder" }).click();
  await expect(page.locator("[data-history-row]")).toHaveCount(1);
  await expect(page.locator("[data-history] thead")).toBeHidden();
  await noSidewaysScroll(page);
  await page.locator("[data-field='remind-account']").focus();
  await page.keyboard.press("Tab");
  await expect(page.locator("[data-field='remind-invoice']")).toBeFocused();
});

// ---- an invoice: send, document, dispute, and a payment reversal -------------------------------------------------------

async function openInvoice(page: Page, account: string, invoice: string): Promise<void> {
  await openAccount(page, account);
  await page.locator("[data-invoice-row]", { hasText: invoice }).getByRole("button", { name: invoice }).click();
  await expect(page.locator("[data-invoice-panel]")).toContainText(`Invoice ${invoice}`);
}

test("an invoice is sent through the messaging sink, and an account with no contact email is refused in the server's words", async ({ page, request }) => {
  await openDesk(page, request);
  await openInvoice(page, "Granite Edge Landscaping (demo)", "INV-1002");
  await page.getByRole("button", { name: "Send the invoice" }).click();
  await expect(page.locator("[data-notice]")).toHaveText("The invoice went to the account's contact through the messaging sink.");
  expect(await page.locator("[data-invoice-panel]").innerText()).not.toMatch(/@|gls-demo|\.invalid/);
  await page.getByRole("button", { name: "‹ Receivables" }).click();
  await openInvoice(page, "Oakline Builders (demo)", "INV-1003");
  await page.getByRole("button", { name: "Send the invoice" }).click();
  await expect(page.locator("[data-error]")).toHaveText("Oakline Builders (demo) has no contact email on the account.");
});

test("an invoice document opens in a frame that can run nothing, and closes", async ({ page, request }) => {
  await openDesk(page, request);
  await openInvoice(page, "Granite Edge Landscaping (demo)", "INV-1002");
  await page.getByRole("button", { name: "View the document" }).click();
  await expect(page.locator("[data-doc-slot] iframe")).toHaveAttribute("sandbox", "");
  await expect(page.frameLocator("[data-doc-slot] iframe").locator("h1")).toHaveText("Invoice INV-1002");
  await page.getByRole("button", { name: "Close the document" }).click();
  await expect(page.locator("[data-doc-slot] iframe")).toHaveCount(0);
});

test("a dispute is checked, opened, shown on the invoice and the register, and closed with an outcome", async ({ page, request }) => {
  await openDesk(page, request);
  await openInvoice(page, "Granite Edge Landscaping (demo)", "INV-1002");
  await page.getByRole("button", { name: "Open a dispute" }).click();
  await page.getByRole("button", { name: "Open the dispute" }).click();
  await expect(page.locator("[data-error]")).toHaveText("Choose why the customer disputes it.");
  await page.locator("[data-field='dispute-reason']").selectOption("DAMAGED");
  await page.locator("[data-field='dispute-amount']").fill("960.01");
  await page.getByRole("button", { name: "Open the dispute" }).click();
  await expect(page.locator("[data-error]")).toHaveText("That is more than the invoice balance of $960.00.");
  await page.locator("[data-field='dispute-amount']").fill("100.00");
  await page.locator("[data-field='dispute-note']").fill("Two pallets arrived cracked");
  await page.getByRole("button", { name: "Open the dispute" }).click();
  await expect(page.locator("[data-notice]")).toContainText("The dispute is open.");
  await expect(page.locator("[data-dispute]")).toContainText("Open: $100.00, Two pallets arrived cracked");
  await expect(page.locator("[data-invoice-panel] .pill", { hasText: "disputed" })).toBeVisible();

  await page.getByRole("button", { name: "Close the dispute" }).click();
  await page.getByRole("button", { name: "Close the dispute" }).last().click();
  await expect(page.locator("[data-error]")).toHaveText("Choose how the dispute ended.");
  await page.locator("[data-field='dispute-outcome']").selectOption("CREDITED");
  await page.getByRole("button", { name: "Close the dispute" }).last().click();
  await expect(page.locator("[data-notice]")).toContainText("The dispute is closed");
  await expect(page.locator("[data-dispute-closed]")).toHaveText("Last dispute: Damaged, closed as Credited.");
  await expect(page.getByRole("button", { name: "Open a dispute" })).toBeVisible();
});

test("a payment is reversed with a reason, and the invoice is owing what it was", async ({ page, request }) => {
  await openDesk(page, request);
  await openAccount(page, "Riverstone Paving (demo)");
  // The form fills the amount with what is owing once the account has loaded; typing before that is overwritten.
  await expect(page.locator("[data-field='receive-amount']")).not.toHaveValue("");
  await page.locator("[data-field='receive-amount']").fill("250.00");
  await page.locator("[data-field='receive-reference']").fill("Cheque 7782");
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-notice]")).toContainText("PAY-0101 applied");
  await page.locator("[data-invoice-row]", { hasText: "INV-1005" }).getByRole("button", { name: "INV-1005" }).click();
  await expect(page.locator("[data-invoice-balance]")).toHaveText("$637.50");
  await page.locator("[data-payment-row]").getByRole("button", { name: "Reverse" }).click();
  await page.getByRole("button", { name: "Confirm reversed" }).click();
  await expect(page.locator("[data-error]")).toHaveText("Enter why the payment is being reversed, such as keyed on the wrong account.");
  await page.locator("[data-field='reverse-reason']").fill("Keyed on the wrong account");
  await page.getByRole("button", { name: "Confirm reversed" }).click();
  await expect(page.locator("[data-notice]")).toHaveText("Payment PAY-0101 is reversed as a void, the same day. $250.00 is owing again on 1 invoice.");
  await expect(page.locator("[data-invoice-balance]")).toHaveText("$887.50");
  await expect(page.locator("[data-payment-row]", { hasText: "REV-" })).toContainText("Reversal");
  await expect(page.locator("[data-payment-row]", { hasText: "REV-" }).getByRole("button")).toHaveCount(0);
  await page.locator("[data-payment-row]", { hasText: "PAY-0101" }).getByRole("button", { name: "Reverse" }).click();
  await page.locator("[data-field='reverse-reason']").fill("Again");
  await page.getByRole("button", { name: "Confirm reversed" }).click();
  await expect(page.locator("[data-error]")).toHaveText("This payment has already been reversed.");
});

test("on a phone the invoice's dispute form and the reversal fit the screen", async ({ page, request }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openDesk(page, request);
  await openInvoice(page, "Granite Edge Landscaping (demo)", "INV-1002");
  await page.getByRole("button", { name: "Open a dispute" }).click();
  await expect(page.locator("[data-dispute-form]")).toBeVisible();
  await noSidewaysScroll(page);
  await page.getByRole("button", { name: "View the document" }).click();
  await expect(page.locator("[data-doc-slot] iframe")).toBeVisible();
  await noSidewaysScroll(page);
});

// ---- settings, holds a returned payment placed, and the dispatch gate -------------------------------------------------------

test("Payables and Receivables reads the rules, and every change is held and says who may make it", async ({ page, request }) => {
  await openDesk(page, request);
  await openTab(page, "Settings");
  await expect(page.locator("[data-field='set-warn']")).toHaveValue("60");
  await expect(page.locator("[data-field='set-escalate']")).toHaveValue("90");
  await expect(page.locator("[data-field='set-tier']")).toHaveValue("BASE");
  await expect(page.locator("[data-field='set-fee']")).toHaveValue("35.00");
  await expect(page.locator("[data-field='set-warn']")).toBeDisabled();
  await expect(page.locator("[data-settings-hold] [data-locked]")).toContainText("for the Financial Controller or the General Manager");
  await expect(page.locator("[data-settings-history] [data-locked]")).toBeVisible();
  await page.getByRole("button", { name: "Save the rules" }).click({ force: true });
  await expect(page.locator("[data-error]")).toHaveText("Changing receivables rules is for the Financial Controller or the General Manager. Payables and Receivables can read the rules the desk runs on.");
  await page.getByRole("button", { name: "Save the fee" }).click({ force: true });
  await expect(page.locator("[data-error]")).toContainText("The returned payment fee and its hold are for the Financial Controller or the General Manager to change.");
  await page.getByRole("checkbox", { name: "Email reminders" }).click({ force: true });
  await expect(page.getByRole("checkbox", { name: "Email reminders" })).toHaveAttribute("aria-checked", "true");
});

test("the controller changes the rules, checked first, and each change is on the record", async ({ page, request }) => {
  await openDesk(page, request, "controller");
  await openTab(page, "Settings");
  await expect(page.locator("[data-settings-history] [data-history-none]")).toHaveText("No rule has been changed yet.");
  await page.locator("[data-field='set-warn']").fill("95");
  await page.getByRole("button", { name: "Save the rules" }).click();
  await expect(page.locator("[data-error]")).toHaveText("Overdue warning (95) must be fewer than escalation (90).");
  await page.locator("[data-field='set-warn']").fill("55");
  await page.locator("[data-field='set-cadence']").fill("10");
  await page.getByRole("checkbox", { name: "Text reminders" }).click();
  await page.locator("[data-field='set-tier']").selectOption("PRO");
  await page.getByRole("button", { name: "Save the rules" }).click();
  await expect(page.locator("[data-notice]")).toContainText("The receivables rules are saved.");
  await expect(page.locator("[data-change-row]")).toHaveCount(4);
  await expect(page.locator("[data-change-row]", { hasText: "Days between reminders" })).toContainText("7");
  await expect(page.locator("[data-change-row]", { hasText: "Days between reminders" })).toContainText("10");
  await expect(page.locator("[data-change-row]", { hasText: "Text reminders" })).toContainText("Off");
  await expect(page.locator("[data-change-row]", { hasText: "Plan" })).toContainText("Accelerated AR");
  await openTab(page, "Reminders");
  await expect(page.locator("[data-plan]")).toHaveText("Accelerated AR");
});

test("the register settings are saved with their tones and read back", async ({ page, request }) => {
  await openDesk(page, request, "controller");
  await openTab(page, "Settings");
  await page.locator("[data-field='set-duesoon']").fill("0");
  await page.getByRole("button", { name: "Save the register settings" }).click();
  await expect(page.locator("[data-error]")).toHaveText("The due soon window must be between 1 and 90 days.");
  await page.locator("[data-field='set-duesoon']").fill("10");
  await page.getByRole("button", { name: "Firm", exact: true }).click();
  await page.getByRole("button", { name: "Save the register settings" }).click();
  await expect(page.locator("[data-notice]")).toHaveText("The register settings are saved.");
  await openTab(page, "Statements");
  await openTab(page, "Settings");
  await expect(page.locator("[data-field='set-duesoon']")).toHaveValue("10");
  await expect(page.getByRole("button", { name: "Firm", exact: true })).toHaveAttribute("aria-pressed", "false");
  await expect(page.getByRole("button", { name: "Final", exact: true })).toHaveAttribute("aria-pressed", "true");
});

test("the returned payment fee is saved in dollars and is the fee the next bounce is charged", async ({ page, request }) => {
  await openDesk(page, request, "controller");
  await openTab(page, "Settings");
  await page.locator("[data-field='set-fee']").fill("35.001");
  await page.getByRole("button", { name: "Save the fee" }).click();
  await expect(page.locator("[data-error]")).toHaveText("Enter the fee as dollars and cents, such as 35.00.");
  await page.locator("[data-field='set-fee']").fill("60.00");
  await page.getByRole("button", { name: "Save the fee" }).click();
  await expect(page.locator("[data-notice]")).toContainText("The returned payment fee is saved.");
  await openTab(page, "Receivables");
  await openAccount(page, "Riverstone Paving (demo)");
  await page.locator("[data-field='receive-amount']").fill("250.00");
  await page.locator("[data-field='receive-reference']").fill("Cheque 7783");
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-notice]")).toContainText("applied");
  await page.locator("[data-invoice-row]", { hasText: "INV-1005" }).getByRole("button", { name: "INV-1005" }).click();
  await page.locator("[data-payment-row]").getByRole("button", { name: "Mark bounced" }).click();
  await page.locator("[data-field='bounce-reason']").fill("Cheque returned NSF");
  await page.getByRole("button", { name: "Confirm bounced" }).click();
  await expect(page.locator("[data-notice]")).toContainText("the bank fee is $60.00");
});

test("the automatic hold switch shows what it would apply, asks twice, and says when it cannot be changed", async ({ page, request }) => {
  await openDesk(page, request, "controller");
  await openTab(page, "Settings");
  await expect(page.locator("[data-hold-state]")).toHaveText("Off");
  await expect(page.locator("[data-settings-hold]")).toContainText("Automatic holds are off.");
  await expect(page.locator("[data-settings-hold]")).toContainText("Aged receivables: held past 30 days overdue once $5,000.00 or more is in that age.");
  await page.getByRole("button", { name: "Turn automatic holds on" }).click();
  await expect(page.locator("[data-hold-confirm]")).toContainText("lets the sweep stop accounts from transacting by itself");
  await page.getByRole("button", { name: "Cancel" }).click();
  await expect(page.locator("[data-hold-state]")).toHaveText("Off");
  await page.getByRole("button", { name: "Turn automatic holds on" }).click();
  await page.locator("[data-field='set-hold-reason']").fill("Ready for the demo");
  await page.getByRole("button", { name: "Turn on", exact: true }).click();
  await expect(page.locator("[data-notice]")).toHaveText("Automatic holds are on.");
  await expect(page.locator("[data-hold-state]")).toHaveText("On");
});

test("when the switch is not wired it is shown and cannot be changed", async ({ page, request }) => {
  await request.post("/_stub/scenario", { data: { name: "unwired" } });
  const who: "controller" = "controller";
  await page.addInitScript(receivablesHandoff, who);
  await page.goto("/");
  await page.getByRole("button", { name: "Receivables", exact: true }).click();
  await expect(page.locator("[data-card]")).toHaveCount(4);
  await openTab(page, "Settings");
  await expect(page.locator("[data-settings-hold]")).toContainText("The switch is not wired on this server, so it is shown but cannot be changed.");
  await expect(page.getByRole("button", { name: /Turn automatic holds/ })).toHaveCount(0);
});

test("a hold a returned payment placed names its payment and is lifted through the till's path", async ({ page, request }) => {
  await openDesk(page, request, "controller");
  await openTab(page, "Credit holds");
  await page.locator("[data-hold-row]", { hasText: "Granite" }).getByRole("button", { name: "Lift..." }).click();
  await expect(page.locator("[data-hold-source]")).toHaveText("Payment PAY-0001, returned by the bank");
  await page.locator("[data-field='release-reason']").fill("Replacement cheque cleared");
  await page.getByRole("button", { name: "Lift hold" }).click();
  await expect(page.locator("[data-notice]")).toContainText("The credit hold is lifted.");
  await expect(page.locator("[data-hold-row]")).toHaveCount(2);
});

test("an invoice says whether its order may go out, and why not", async ({ page, request }) => {
  await openDesk(page, request);
  await openInvoice(page, "Granite Edge Landscaping (demo)", "INV-1001");
  await page.getByRole("button", { name: "Can the order go out?" }).click();
  await expect(page.locator("[data-error]")).toHaveText("Order SO-2001 is stopped: ACCOUNT ON HOLD, a payment was returned; the office must clear this before delivery.");
  await page.getByRole("button", { name: "‹ Receivables" }).click();
  await openInvoice(page, "Riverstone Paving (demo)", "INV-1005");
  await page.getByRole("button", { name: "Can the order go out?" }).click();
  await expect(page.locator("[data-notice]")).toHaveText("Order SO-2005 may go out: nothing on the account stops it.");
});

test("on a phone the settings fit the screen and work from the keyboard", async ({ page, request }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openDesk(page, request, "controller");
  await openTab(page, "Settings");
  await expect(page.locator("[data-settings-rules]")).toBeVisible();
  await noSidewaysScroll(page);
  await page.locator("[data-field='set-warn']").focus();
  await page.keyboard.press("Tab");
  await expect(page.locator("[data-field='set-escalate']")).toBeFocused();
});

test("the credit sweep only reports while automatic holds are off, and places a hold once they are on", async ({ page, request }) => {
  await openDesk(page, request, "controller");
  await openTab(page, "Credit holds");
  await page.locator("[data-hold-row]", { hasText: "Granite" }).getByRole("button", { name: "Lift..." }).click();
  await page.locator("[data-field='release-reason']").fill("Replacement cheque cleared");
  await page.getByRole("button", { name: "Lift hold" }).click();
  await expect(page.locator("[data-hold-row]")).toHaveCount(2);
  await page.getByRole("button", { name: "Run the sweep" }).click();
  await expect(page.locator("[data-notice]")).toHaveText("The sweep checked 5 accounts. It would hold 1 and placed none, because automatic holds are off.");
  await expect(page.locator("[data-hold-row]")).toHaveCount(2);
  await openTab(page, "Settings");
  await page.getByRole("button", { name: "Turn automatic holds on" }).click();
  await page.getByRole("button", { name: "Turn on", exact: true }).click();
  await expect(page.locator("[data-hold-state]")).toHaveText("On");
  await openTab(page, "Credit holds");
  await page.getByRole("button", { name: "Run the sweep" }).click();
  await expect(page.locator("[data-notice]")).toHaveText("The sweep checked 5 accounts and placed 1 hold.");
  await expect(page.locator("[data-hold-row]")).toHaveCount(3);
  await expect(page.locator("[data-hold-row]", { hasText: "Granite" })).toContainText("auto");
});
