// The Financial Controller's bank reconciliation legs on the Money Desk, walked in a real browser on the stub: the
// accounts and what waits on each; a statement imported from pasted rows or a chosen file, the same rows again
// adding nothing, a file the core cannot read saying what it lacked; the auto match and the hand match to a
// payment or a journal; an undo that is confirmed and keeps its history; a month opened, held until it balances,
// refused in words while lines are unmatched, then signed off and closed; the rules and a new version; every
// refusal in words; the loading, empty and failed states; the phone; and the keyboard.
import { test, expect, openBank, tab, resetStub, controllerHandoff, monthDay, priorMonth, OPERATING, OPERATING_ID, FUEL_CARD, WIRE_PAYMENT, FEE_JOURNAL, CHEQUE_JOURNAL, CLOSING } from "./bankrec-helpers.js";
import { receivablesHandoff } from "./helpers.js";

const row = (page: import("@playwright/test").Page, text: string) => page.locator("[data-line-id]").filter({ hasText: text });

async function chooseLine(page: import("@playwright/test").Page, text: string): Promise<void> {
  await row(page, text).getByRole("button").click();
  await expect(page.locator("[data-line-panel]")).toContainText(text);
}

test("the controller's third area lists the accounts, what waits on each, and the cards as tracking only", async ({ page, request }) => {
  await openBank(page, request);
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Bank reconciliation");
  const cards = page.locator("[data-account-id]");
  await expect(cards.filter({ hasText: OPERATING })).toContainText("7 lines unmatched");
  await expect(cards.filter({ hasText: OPERATING })).toContainText("Bank account · GL 1000 · ending 4410");
  await expect(cards.filter({ hasText: FUEL_CARD })).toContainText("All matched");
  await expect(cards.filter({ hasText: FUEL_CARD })).toContainText("Credit card");
  await expect(page.locator("[data-tracking-only]")).toHaveCount(2);
  await expect(page.locator("[data-month-end]")).toContainText(`Month end · ${OPERATING}`);
  await expect(page.locator("[data-empty-recons]")).toContainText("No month is open for this account.");
  // The other two areas are whole beside it.
  await page.getByRole("button", { name: "Receivables", exact: true }).click();
  await expect(page.locator("[data-card]")).toHaveCount(4);
  await page.getByRole("button", { name: "Bank reconciliation", exact: true }).click();
  await expect(page.locator("[data-account-id]")).toHaveCount(3);
});

test("Payables and Receivables has no bank reconciliation area", async ({ page, request }) => {
  await resetStub(request);
  await page.addInitScript(receivablesHandoff);
  await page.goto("/");
  await expect(page.locator("[data-card]")).toHaveCount(4);
  await expect(page.getByRole("button", { name: "Bank reconciliation" })).toHaveCount(0);
  // The switch holds the ledger as a reader, the receivables desk, and the liens and the calendar, and nothing else.
  await expect(page.locator("nav[aria-label='Money Desk areas'] button")).toHaveText(["Ledger", "Receivables", "Liens", "Compliance"]);
});

test("a statement pasted in imports, the same rows again add nothing, and a file the core cannot read says what it lacked", async ({ page, request }) => {
  await openBank(page, request);
  await tab(page, "import");
  const csv = `Date,Description,Amount,Reference\n${monthDay(27)},YARD SUPPLY CARD PAYMENT,"(1,200.50)",REF-1\n${monthDay(28)},CUSTOMER DEPOSIT,500.00,\n`;
  const box = page.getByRole("textbox", { name: "Statement rows" });
  await box.fill(csv);
  await page.getByRole("button", { name: "Import statement", exact: true }).click();
  const result = page.locator("[data-import-result]");
  await expect(result).toContainText("Imported.");
  await expect(result.locator("[data-parsed]")).toHaveText("2");
  await expect(result.locator("[data-inserted]")).toHaveText("2");
  await expect(result.locator("[data-duplicates]")).toHaveText("0");
  // The same rows again: nothing new, said in words.
  await page.getByRole("button", { name: "Import statement", exact: true }).click();
  await expect(page.locator("[data-import-result]")).toContainText("Nothing new: every line was already here.");
  await expect(page.locator("[data-import-result] [data-inserted]")).toHaveText("0");
  await expect(page.locator("[data-import-result] [data-duplicates]")).toHaveText("2");
  // The typed rows are still in the box after the repaints.
  await expect(box).toHaveValue(csv);
  // A file with no Amount column.
  await box.fill(`Date,Description,Reference\n${monthDay(27)},X,Y\n`);
  await page.getByRole("button", { name: "Import statement", exact: true }).click();
  await expect(page.locator("[data-import-failed]")).toContainText('Not imported: the file has no "Amount" column. The first row must name Date, Description, Amount and Reference.');
  await expect(page.locator("[data-import-failed]")).not.toContainText("bankrec");
  // A date the format does not read.
  await box.fill("Date,Description,Amount,Reference\nJune 4,X,1.00,\n");
  await page.getByRole("button", { name: "Import statement", exact: true }).click();
  await expect(page.locator("[data-import-failed]")).toContainText('row 1 has a date the file format does not read ("June 4")');
  // An amount that is not money.
  await box.fill(`Date,Description,Amount,Reference\n${monthDay(4)},X,12..5,\n`);
  await page.getByRole("button", { name: "Import statement", exact: true }).click();
  await expect(page.locator("[data-import-failed]")).toContainText('row 1 has an amount that is not money ("12..5")');
  // Nothing to import is said before anything is sent.
  await page.getByRole("button", { name: "Clear" }).click();
  await page.getByRole("button", { name: "Import statement", exact: true }).click();
  await expect(page.locator("[data-error]")).toContainText("Choose a statement file, or paste its rows, first.");
});

test("a statement file chosen in the browser fills the rows and imports to the card account", async ({ page, request }) => {
  await openBank(page, request);
  await tab(page, "import");
  await page.locator("[data-field='br-account']").selectOption({ label: FUEL_CARD });
  await expect(page.getByText("Transaction Date, Description, Amount, Reference")).toBeVisible();
  const csv = `Transaction Date,Description,Amount,Reference\n${monthDay(6)},FUEL STOP,(88.10),F-1\n${monthDay(7)},FUEL STOP,(91.45),F-2\n`;
  await page.locator("[data-field='br-file']").setInputFiles({ name: "fuel.csv", mimeType: "text/csv", buffer: Buffer.from(csv) });
  await expect(page.getByRole("textbox", { name: "Statement rows" })).toHaveValue(csv);
  await page.getByRole("button", { name: "Import statement", exact: true }).click();
  await expect(page.locator("[data-import-result]")).toContainText("fuel.csv");
  await expect(page.locator("[data-import-result] [data-inserted]")).toHaveText("2");
  await page.getByRole("button", { name: "Go to matching" }).click();
  await expect(page.locator("[data-line-id]")).toHaveCount(2);
  await expect(page.locator("[data-line-id]").first()).toContainText("FUEL STOP");
});

test("auto match pairs the card, lodgement and batch lines and leaves the rest for the hand", async ({ page, request }) => {
  await openBank(page, request);
  await tab(page, "match");
  await expect(page.locator("[data-line-id]")).toHaveCount(7);
  await page.getByRole("button", { name: "Run auto match" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Auto match paired 4 lines, and posted their clearing entries. 2 credits stay unmatched: match them by hand.");
  // The list is the unmatched ones: the cheque deposit, the wire and the fee (a debit auto match never touches).
  await expect(page.locator("[data-line-id]")).toHaveCount(3);
  await expect(row(page, "CHEQUE DEPOSIT")).toContainText("Unmatched");
  await expect(row(page, "MONTHLY SERVICE FEE")).toContainText("-$45.00");
  await page.getByRole("button", { name: "Matched", exact: true }).click();
  await expect(page.locator("[data-line-id]")).toHaveCount(4);
  await expect(row(page, "CARD SETTLEMENT BATCH 0142")).toContainText("$1,280.00");
  await expect(row(page, "RUN BATCH SETTLEMENT")).toContainText("$1,150.00");
  await expect(row(page, "CASH LODGEMENT DEPOSIT")).toContainText("Matched");
  // A second run finds nothing new, and says so.
  await page.getByRole("button", { name: "Run auto match" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Auto match found nothing new to pair. 2 credits stay unmatched.");
  // The account cards follow.
  await tab(page, "accounts");
  await expect(page.locator("[data-account-id]").filter({ hasText: OPERATING })).toContainText("3 lines unmatched");
});

test("auto match can be held to a window of dates, and a window that ends before it starts is refused in the browser", async ({ page, request }) => {
  await openBank(page, request);
  await tab(page, "match");
  await page.locator("[data-field='br-from']").fill(monthDay(1));
  await page.locator("[data-field='br-to']").fill(monthDay(6));
  await page.getByRole("button", { name: "Run auto match" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Auto match paired 2 lines");
  await page.locator("[data-field='br-from']").fill(monthDay(20));
  await page.locator("[data-field='br-to']").fill(monthDay(10));
  await page.getByRole("button", { name: "Run auto match" }).click();
  await expect(page.locator("[data-error]")).toContainText("The end date is before the start date.");
});

test("a line matched by hand to a payment leaves the unmatched list, and the refusals say what to do", async ({ page, request }) => {
  await openBank(page, request);
  await tab(page, "match");
  await chooseLine(page, "WIRE FROM CONTRACTOR");
  const panel = page.locator("[data-line-panel]");
  await expect(panel.locator("[data-history-empty]")).toContainText("This line has never been matched.");
  await expect(panel.getByRole("radio", { name: /Payment/ })).toHaveAttribute("aria-checked", "true");
  // Nothing typed: refused in the browser.
  await panel.getByRole("button", { name: "Match line" }).click();
  await expect(page.locator("[data-error]")).toContainText("Paste the payment id the line clears against.");
  // Not an id: refused in the browser, in words.
  await panel.getByRole("textbox", { name: "Payment id" }).fill("pay-1");
  await panel.getByRole("button", { name: "Match line" }).click();
  await expect(page.locator("[data-error]")).toContainText("That is not a payment id.");
  // An id the core does not hold as a received payment: the core's 409, in words.
  await panel.getByRole("textbox", { name: "Payment id" }).fill("b9000000-0000-4000-8000-0000000000ff");
  await panel.getByRole("button", { name: "Match line" }).click();
  await expect(page.locator("[data-error]")).toContainText("That payment cannot be found, so it cannot be deposited.");
  await expect(page.locator("[data-line-panel]")).toBeVisible();
  // The real payment.
  await panel.getByRole("textbox", { name: "Payment id" }).fill(WIRE_PAYMENT);
  await panel.getByRole("button", { name: "Match line" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Matched. The clearing entry posted to the ledger.");
  await expect(row(page, "WIRE FROM CONTRACTOR")).toHaveCount(0);
  // A real payment that is no longer waiting (just matched): the sentence names it by its NUMBER, never its id.
  await chooseLine(page, "MONTHLY SERVICE FEE");
  await panel.getByRole("textbox", { name: "Payment id" }).fill(WIRE_PAYMENT);
  await panel.getByRole("button", { name: "Match line" }).click();
  await expect(page.locator("[data-error]")).toContainText("Payment PAY-000104 is not waiting to be deposited");
  await expect(page.locator("[data-error]")).not.toContainText(WIRE_PAYMENT);
  await page.getByRole("button", { name: "Matched", exact: true }).click();
  await expect(row(page, "WIRE FROM CONTRACTOR")).toContainText("Matched");
});

test("a line matched by hand to a posted journal posts nothing new", async ({ page, request }) => {
  await openBank(page, request);
  await tab(page, "match");
  await chooseLine(page, "MONTHLY SERVICE FEE");
  const panel = page.locator("[data-line-panel]");
  await panel.getByRole("radio", { name: /Journal/ }).click();
  await panel.getByRole("button", { name: "Match line" }).click();
  await expect(page.locator("[data-error]")).toContainText("Pick the journal the line clears against.");
  await panel.getByRole("combobox", { name: "Journal" }).selectOption({ label: FEE_JOURNAL });
  await panel.getByRole("button", { name: "Match line" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Matched. No new entry was posted: the line is paired to something already in the ledger.");
  await expect(row(page, "MONTHLY SERVICE FEE")).toHaveCount(0);
});

test("an undo is confirmed, returns the line to unmatched and keeps the history", async ({ page, request }) => {
  await openBank(page, request);
  await tab(page, "match");
  await page.getByRole("button", { name: "Run auto match" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Auto match paired 4 lines");
  await page.getByRole("button", { name: "Matched", exact: true }).click();
  await chooseLine(page, "CARD SETTLEMENT BATCH 0143");
  const panel = page.locator("[data-line-panel]");
  await expect(panel.locator("[data-history]")).toContainText("Automatic · Payment · $980.00 · in force");
  await expect(panel.locator("[data-live-match]")).toContainText("Matched by a rule to a payment.");
  await panel.getByRole("button", { name: "Undo match" }).click();
  await expect(panel.locator("[data-undo-note]")).toContainText("Undoing returns the line to unmatched.");
  // Keeping the match changes nothing.
  await panel.getByRole("button", { name: "Keep the match" }).click();
  await expect(panel.getByRole("button", { name: "Undo match" })).toBeVisible();
  await panel.getByRole("button", { name: "Undo match" }).click();
  await panel.getByRole("button", { name: "Confirm undo" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Match undone. The line is unmatched again.");
  await expect(row(page, "CARD SETTLEMENT BATCH 0143")).toHaveCount(0);
  await page.getByRole("button", { name: "Unmatched", exact: true }).click();
  await chooseLine(page, "CARD SETTLEMENT BATCH 0143");
  await expect(page.locator("[data-line-panel] [data-history]")).toContainText("Automatic · Payment · $980.00 · undone");
  // The line can be matched again by hand: the payment was released.
  await expect(page.locator("[data-line-panel]").getByRole("button", { name: "Match line" })).toBeVisible();
});

test("a month opens with the difference in words, sign off is held until it is zero, and the refusal for unmatched lines is in words", async ({ page, request }) => {
  await openBank(page, request);
  const prior = priorMonth();
  await page.getByRole("textbox", { name: "Closing balance on the statement" }).fill(CLOSING);
  await page.getByRole("button", { name: "Open month" }).click();
  await expect(page.locator("[data-notice]")).toContainText(`${prior.words} is open`);
  const month = page.locator("[data-recon-id]");
  await expect(month).toContainText(prior.words);
  await expect(month).toContainText("$25,255.75");
  await expect(month).toContainText("Off by $6,160.50");
  await expect(month).toContainText("Open");
  await expect(month.getByRole("button", { name: "Sign off" })).toBeDisabled();
  await expect(month.locator("[data-held]")).toContainText("Held: the statement and the ledger must agree first.");
  // Match what can be matched, then refresh: the figures move, the month is balanced, two lines are still unmatched.
  await tab(page, "match");
  await page.getByRole("button", { name: "Run auto match" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Auto match paired 4 lines");
  await chooseLine(page, "WIRE FROM CONTRACTOR");
  await page.locator("[data-line-panel]").getByRole("textbox", { name: "Payment id" }).fill(WIRE_PAYMENT);
  await page.locator("[data-line-panel]").getByRole("button", { name: "Match line" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Matched.");
  await tab(page, "accounts");
  // Until it is refreshed the month still shows the old difference.
  await expect(page.locator("[data-recon-id]")).toContainText("Off by $6,160.50");
  await page.locator("[data-recon-id]").getByRole("button", { name: "Refresh figures" }).click();
  await expect(page.locator("[data-recon-id]")).toContainText("Balanced");
  const signOff = page.locator("[data-recon-id]").getByRole("button", { name: "Sign off" });
  await expect(signOff).toBeEnabled();
  await signOff.click();
  await page.getByRole("button", { name: "Confirm sign off" }).click();
  await expect(page.locator("[data-error]")).toContainText(`Not signed off: 2 statement lines are still unmatched for ${prior.words}. Match them, or refresh the figures if you just did.`);
  await expect(page.locator("[data-recon-id]")).toContainText("Open");
});

test("matching the last lines lets the month sign off, and a signed off month is closed", async ({ page, request }) => {
  await openBank(page, request);
  const prior = priorMonth();
  await page.getByRole("textbox", { name: "Closing balance on the statement" }).fill(CLOSING);
  await page.getByRole("button", { name: "Open month" }).click();
  await tab(page, "match");
  await page.getByRole("button", { name: "Run auto match" }).click();
  for (const [text, how] of [
    ["WIRE FROM CONTRACTOR", "payment"],
    ["CHEQUE DEPOSIT", "cheque"],
    ["MONTHLY SERVICE FEE", "fee"],
  ] as const) {
    await chooseLine(page, text);
    const panel = page.locator("[data-line-panel]");
    if (how === "payment") {
      await panel.getByRole("textbox", { name: "Payment id" }).fill(WIRE_PAYMENT);
    } else {
      await panel.getByRole("radio", { name: /Journal/ }).click();
      await panel.getByRole("combobox", { name: "Journal" }).selectOption({ label: how === "fee" ? FEE_JOURNAL : CHEQUE_JOURNAL });
    }
    await panel.getByRole("button", { name: "Match line" }).click();
    await expect(page.locator("[data-notice]")).toContainText("Matched.");
  }
  await expect(page.locator("[data-empty-lines]")).toContainText("No lines here.");
  await tab(page, "accounts");
  await expect(page.locator("[data-account-id]").filter({ hasText: OPERATING })).toContainText("All matched");
  await page.locator("[data-recon-id]").getByRole("button", { name: "Refresh figures" }).click();
  await expect(page.locator("[data-recon-id]")).toContainText("Balanced");
  await page.locator("[data-recon-id]").getByRole("button", { name: "Sign off" }).click();
  // Keeping it open changes nothing.
  await page.getByRole("button", { name: "Keep open" }).click();
  await expect(page.locator("[data-recon-id]")).toContainText("Open");
  await page.locator("[data-recon-id]").getByRole("button", { name: "Sign off" }).click();
  await page.getByRole("button", { name: "Confirm sign off" }).click();
  await expect(page.locator("[data-notice]")).toContainText(`${prior.words} is signed off for ${OPERATING}. Its figures no longer change.`);
  await expect(page.locator("[data-recon-id]")).toContainText("Signed off");
  await expect(page.locator("[data-recon-id]").getByRole("button")).toHaveCount(0);
  // Opening the closed month again is refused in words.
  await page.getByRole("textbox", { name: "Closing balance on the statement" }).fill(CLOSING);
  await page.getByRole("button", { name: "Open month" }).click();
  await expect(page.locator("[data-error]")).toContainText(`${prior.words} is already signed off for this account, so its figures no longer change.`);
});

test("a zero closing balance is not sent quietly: the core's replacement is said", async ({ page, request }) => {
  await openBank(page, request);
  await page.getByRole("textbox", { name: "Closing balance on the statement" }).fill("0.00");
  await page.getByRole("button", { name: "Open month" }).click();
  await expect(page.locator("[data-notice]")).toContainText("You entered a zero balance, and the core replaced it with $6,755.75, the net of the month's statement lines.");
  await expect(page.locator("[data-recon-id]")).toContainText("$6,755.75");
});

test("a blank or invalid closing balance is refused in the browser and nothing is sent", async ({ page, request }) => {
  await openBank(page, request);
  const sent: string[] = [];
  page.on("request", (r) => {
    if (r.method() === "POST" && r.url().includes("/api/v1/bankrec/")) sent.push(r.url());
  });
  await page.getByRole("button", { name: "Open month" }).click();
  await expect(page.locator("[data-error]")).toContainText("Enter the closing balance from the statement, in dollars and cents, such as 25,255.75.");
  const balance = page.getByRole("textbox", { name: "Closing balance on the statement" });
  await balance.fill("25.255,75");
  await expect(page.locator("[data-balance-hint]")).toContainText("Enter dollars and cents, such as 25,255.75.");
  await page.getByRole("button", { name: "Open month" }).click();
  await expect(page.locator("[data-error]")).toContainText("Enter the closing balance from the statement");
  await balance.fill("1.005");
  await page.getByRole("button", { name: "Open month" }).click();
  await expect(page.locator("[data-error]")).toContainText("Enter the closing balance from the statement");
  await page.getByRole("textbox", { name: "Year" }).fill("31");
  await balance.fill("10.00");
  await page.getByRole("button", { name: "Open month" }).click();
  await expect(page.locator("[data-error]")).toContainText("Enter the year as four digits");
  expect(sent).toEqual([]);
  // A negative balance, in brackets, is money.
  await page.getByRole("textbox", { name: "Year" }).fill(String(priorMonth().year));
  await balance.fill("(1,200.50)");
  await expect(page.locator("[data-balance-hint]")).toHaveCount(0);
  await page.getByRole("button", { name: "Open month" }).click();
  await expect(page.locator("[data-recon-id]")).toContainText("-$1,200.50");
});

test("the rules list what is in use, and approving a new version retires the old one", async ({ page, request }) => {
  await openBank(page, request);
  await tab(page, "rules");
  const rules = page.getByRole("table", { name: "Match rules" });
  await expect(rules.getByRole("row")).toHaveCount(4);
  await expect(rules).toContainText("Card settlement, reference exact");
  await expect(rules).toContainText("Description %RUN%, within 3 days, fee split allowed");
  await expect(rules.getByRole("row", { name: /run-settlement-fuzzy/ })).toContainText("In use");
  await page.locator("[data-rule-id]").filter({ hasText: "run-settlement-fuzzy" }).getByRole("button", { name: "Start a new version from this" }).click();
  const form = page.locator("[data-rule-form]");
  await expect(form.getByRole("textbox", { name: "Code" })).toHaveValue("run-settlement-fuzzy");
  await expect(form.getByRole("textbox", { name: "Description contains" })).toHaveValue("RUN");
  await expect(form.getByRole("textbox", { name: "Date window" })).toHaveValue("3");
  await expect(form.getByRole("combobox", { name: "Allow a fee split" })).toHaveValue("Yes");
  await form.getByRole("textbox", { name: "Date window" }).fill("5");
  await form.getByRole("button", { name: "Approve version" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Version 2 of run-settlement-fuzzy is in use. The version before it is retired.");
  await expect(rules.getByRole("row")).toHaveCount(5);
  await expect(page.locator("[data-rule-id]").filter({ hasText: "run-settlement-fuzzy" }).filter({ hasText: "v1" })).toContainText("Retired");
  await expect(page.locator("[data-rule-id]").filter({ hasText: "run-settlement-fuzzy" }).filter({ hasText: "v2" })).toContainText("within 5 days");
  // A new rule of the exact kind, with a tolerance.
  await form.getByRole("textbox", { name: "Code" }).fill("gateway-near");
  await form.getByRole("textbox", { name: "Name" }).fill("Card settlement within a dollar");
  await form.getByRole("combobox", { name: "Kind" }).selectOption({ label: "Card settlement, reference exact" });
  await expect(form.getByRole("textbox", { name: "Amount tolerance" })).toBeVisible();
  await form.getByRole("textbox", { name: "Amount tolerance" }).fill("1.00");
  await form.getByRole("textbox", { name: "Priority" }).fill("15");
  await form.getByRole("button", { name: "Approve version" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Version 1 of gateway-near is in use.");
  await expect(page.locator("[data-rule-id]").filter({ hasText: "gateway-near" })).toContainText("Within $1.00");
});

test("a rule needs a code, a sensible priority, a tolerance that is money, and a description for the batch kind", async ({ page, request }) => {
  await openBank(page, request);
  await tab(page, "rules");
  const form = page.locator("[data-rule-form]");
  await form.getByRole("button", { name: "Approve version" }).click();
  await expect(page.locator("[data-error]")).toContainText("Give the rule a code");
  await form.getByRole("textbox", { name: "Code" }).fill("x");
  await form.getByRole("textbox", { name: "Priority" }).fill("0");
  await form.getByRole("button", { name: "Approve version" }).click();
  await expect(page.locator("[data-error]")).toContainText("Priority is a whole number from 1");
  await form.getByRole("textbox", { name: "Priority" }).fill("5");
  await form.getByRole("textbox", { name: "Amount tolerance" }).fill("-1");
  await form.getByRole("button", { name: "Approve version" }).click();
  await expect(page.locator("[data-error]")).toContainText("Enter the tolerance in dollars and cents");
  await form.getByRole("combobox", { name: "Kind" }).selectOption({ label: "Batch settlement, description and date window" });
  await form.getByRole("button", { name: "Approve version" }).click();
  await expect(page.locator("[data-error]")).toContainText("Say what the statement description contains");
});

test("a refused write names who may do it, never the bare gate word", async ({ page, request }) => {
  await openBank(page, request);
  const gate = (route: import("@playwright/test").Route) => route.fulfill({ status: 403, contentType: "application/json", body: JSON.stringify({ error: "Forbidden" }) });
  await page.route("**/api/v1/bankrec/reconciliations", gate);
  await page.getByRole("textbox", { name: "Closing balance on the statement" }).fill(CLOSING);
  await page.getByRole("button", { name: "Open month" }).click();
  await expect(page.locator("[data-error]")).toContainText("Opening a month is for the Financial Controller or a manager. Sign out and in again");
  await expect(page.locator("[data-error]")).not.toContainText("Forbidden");
  await page.route("**/api/v1/bankrec/match", gate);
  await tab(page, "match");
  await page.getByRole("button", { name: "Run auto match" }).click();
  await expect(page.locator("[data-error]")).toContainText("Matching is for the Financial Controller or a manager.");
  await page.route("**/api/v1/bankrec/imports", gate);
  await tab(page, "import");
  await page.getByRole("textbox", { name: "Statement rows" }).fill("Date,Description,Amount,Reference\n");
  await page.getByRole("button", { name: "Import statement", exact: true }).click();
  await expect(page.locator("[data-error]")).toContainText("Importing a statement is for the Financial Controller or a manager.");
  await page.route("**/api/v1/bankrec/rules", (route) => (route.request().method() === "POST" ? gate(route) : route.fallback()));
  await tab(page, "rules");
  await page.locator("[data-rule-form]").getByRole("textbox", { name: "Code" }).fill("gateway-ref-exact");
  await page.locator("[data-rule-form]").getByRole("button", { name: "Approve version" }).click();
  await expect(page.locator("[data-error]")).toContainText("Approving a match rule is for the Financial Controller.");
});

test("an undo of a reversed CARD deposit goes through, so a wrong match can be corrected", async ({ page, request }) => {
  await openBank(page, request);
  await tab(page, "match");
  await page.getByRole("button", { name: "Run auto match" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Auto match paired 4 lines");
  // The payments are reversed behind the desk's back (the stub's control stands for the payments module).
  const reversed = await request.post("/_stub/reverse-payment", { data: { all: true } });
  expect(reversed.ok()).toBeTruthy();
  await page.getByRole("button", { name: "Matched", exact: true }).click();
  await chooseLine(page, "CARD SETTLEMENT BATCH 0143");
  const panel = page.locator("[data-line-panel]");
  await panel.getByRole("button", { name: "Undo match" }).click();
  await panel.getByRole("button", { name: "Confirm undo" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Match undone. The line is unmatched again.");
});

test("an undo of a deposit whose payment was reversed is refused in words, for a payment that is not a card, and the line stays matched", async ({ page, request }) => {
  await openBank(page, request);
  await tab(page, "match");
  // The wire payment (not a card) is matched by hand, then reversed behind the desk's back.
  await chooseLine(page, "WIRE FROM CONTRACTOR");
  await page.locator("[data-line-panel]").getByRole("textbox", { name: "Payment id" }).fill(WIRE_PAYMENT);
  await page.locator("[data-line-panel]").getByRole("button", { name: "Match line" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Matched. The clearing entry posted to the ledger.");
  const reversed = await request.post("/_stub/reverse-payment", { data: { all: true } });
  expect(reversed.ok()).toBeTruthy();
  await page.getByRole("button", { name: "Matched", exact: true }).click();
  await chooseLine(page, "WIRE FROM CONTRACTOR");
  const panel = page.locator("[data-line-panel]");
  await panel.getByRole("button", { name: "Undo match" }).click();
  await panel.getByRole("button", { name: "Confirm undo" }).click();
  await expect(page.locator("[data-error]")).toContainText("This deposit cannot be undone: the payment was reversed, and a reversed cheque or cash payment cannot be matched again.");
  // Nothing changed: the line is still matched and its history still reads in force.
  await expect(panel.locator("[data-history]")).toContainText("in force");
  await expect(panel.getByRole("button", { name: "Undo match" })).toBeVisible();
});

test("a refused sign off and a refused undo are said in words and change nothing", async ({ page, request }) => {
  await openBank(page, request);
  await page.getByRole("textbox", { name: "Closing balance on the statement" }).fill(CLOSING);
  await page.getByRole("button", { name: "Open month" }).click();
  await page.route("**/api/v1/bankrec/reconciliations/*/complete", (route) =>
    route.fulfill({ status: 403, contentType: "application/json", body: JSON.stringify({ error: "Forbidden" }) }),
  );
  // Make the month signable on the stub's own terms first, so the browser's hold does not stand in the way.
  await page.route("**/api/v1/bankrec/reconciliations?*", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify([{ id: "b5000000-0000-4000-8000-000000000001", bank_account_id: OPERATING_ID, year: priorMonth().year, month: priorMonth().month, statement_balance_cents: 100, gl_balance_cents: 100, variance_cents: 0, status: "OPEN" }]),
    }),
  );
  await page.locator("[data-account-id]").filter({ hasText: FUEL_CARD }).click();
  await page.locator("[data-account-id]").filter({ hasText: OPERATING }).click();
  await page.locator("[data-recon-id]").getByRole("button", { name: "Sign off" }).click();
  await page.getByRole("button", { name: "Confirm sign off" }).click();
  await expect(page.locator("[data-error]")).toContainText("Signing off a month is for the Financial Controller.");
  await expect(page.locator("[data-recon-id]")).toContainText("Open");
  // An undo the core says was already done.
  await page.route("**/api/v1/bankrec/unmatch", (route) =>
    route.fulfill({ status: 409, contentType: "application/json", body: JSON.stringify({ error: "match x is not live (already unmatched)" }) }),
  );
  await tab(page, "match");
  await page.getByRole("button", { name: "Run auto match" }).click();
  await page.getByRole("button", { name: "Matched", exact: true }).click();
  await chooseLine(page, "CARD SETTLEMENT BATCH 0142");
  await page.locator("[data-line-panel]").getByRole("button", { name: "Undo match" }).click();
  await page.getByRole("button", { name: "Confirm undo" }).click();
  await expect(page.locator("[data-error]")).toContainText("That match was already undone. Reload to see where the line stands.");
});

test("while the accounts load the screen says so, a failed load offers Try again, and an empty list says so", async ({ page, request }) => {
  await resetStub(request);
  await page.addInitScript(controllerHandoff);
  let calls = 0;
  await page.route("**/api/v1/bankrec/accounts", async (route) => {
    calls += 1;
    if (calls === 1) {
      await new Promise((r) => setTimeout(r, 700));
      return route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: "internal error" }) });
    }
    if (calls === 2) return route.fulfill({ status: 200, contentType: "application/json", body: "[]" });
    return route.fallback();
  });
  await page.goto("/");
  await page.getByRole("button", { name: "Bank reconciliation", exact: true }).click();
  await expect(page.locator("[data-loading]")).toContainText("Loading the bank accounts.");
  await expect(page.locator("[data-error]")).toContainText("The bank reconciliation service did not answer. Reload to try again.");
  await expect(page.locator("[data-account-id]")).toHaveCount(0);
  await page.getByRole("button", { name: "Try again" }).click();
  await expect(page.locator("[data-empty-accounts]")).toContainText("No bank or card accounts are set up.");
  await expect(page.locator("[data-month-end]")).toContainText("Pick an account to open a month.");
  await page.getByRole("button", { name: "Receivables", exact: true }).click();
  await page.getByRole("button", { name: "Bank reconciliation", exact: true }).click();
});

test("the core's bare array is read, and a page of items or anything else is refused, never half read", async ({ page, request }) => {
  await resetStub(request);
  await page.addInitScript(controllerHandoff);
  const account = { id: OPERATING_ID, name: OPERATING, account_type: "BANK", gl_account_code: "1000", institution: "Harbor Bank (demo)", last4: "4410", is_active: true, tracking_only: false, unmatched_lines: 0 };
  let shape = "array";
  const answers: Record<string, unknown> = { array: [account], page: { items: [account], next_cursor: null }, garbage: { items: "x" } };
  await page.route("**/api/v1/bankrec/accounts", (route) =>
    route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(answers[shape]) }),
  );
  await page.goto("/");
  await page.getByRole("button", { name: "Bank reconciliation", exact: true }).click();
  await expect(page.locator("[data-account-id]")).toHaveCount(1);
  for (const refused of ["page", "garbage"]) {
    shape = refused;
    await page.getByRole("button", { name: "Receivables", exact: true }).click();
    await page.reload();
    await page.getByRole("button", { name: "Bank reconciliation", exact: true }).click();
    await expect(page.locator("[data-error]")).toContainText("The accounts were not readable.");
    await expect(page.locator("[data-account-id]")).toHaveCount(0);
  }
});

test("a second press while a write is in flight sends nothing more", async ({ page, request }) => {
  await openBank(page, request);
  await tab(page, "match");
  let posts = 0;
  await page.route("**/api/v1/bankrec/match", async (route) => {
    posts += 1;
    await new Promise((r) => setTimeout(r, 500));
    return route.fallback();
  });
  const run = page.getByRole("button", { name: "Run auto match" });
  await run.click();
  await run.click({ force: true });
  await expect(page.locator("[data-notice]")).toContainText("Auto match paired 4 lines");
  expect(posts).toBe(1);
});

test("more than a page of lines shows more on request and follows the core's cursor to the end", async ({ page, request }) => {
  await openBank(page, request);
  await tab(page, "import");
  const rows = Array.from({ length: 60 }, (_, i) => `${monthDay(10)},ROLL ${String(i).padStart(2, "0")},${i + 1}.00,R-${i}`);
  await page.getByRole("textbox", { name: "Statement rows" }).fill(`Date,Description,Amount,Reference\n${rows.join("\n")}\n`);
  await page.getByRole("button", { name: "Import statement", exact: true }).click();
  await expect(page.locator("[data-import-result] [data-inserted]")).toHaveText("60");
  await page.getByRole("button", { name: "Go to matching" }).click();
  await expect(page.locator("[data-line-id]")).toHaveCount(50);
  await page.getByRole("button", { name: "Show more lines" }).click();
  await expect(page.locator("[data-line-id]")).toHaveCount(67);
  await expect(page.locator("[data-lines-end]")).toHaveText("67 lines shown.");
  await expect(page.getByRole("button", { name: "Show more lines" })).toHaveCount(0);
});

test("the filters narrow the lines and an empty filter says so in words", async ({ page, request }) => {
  await openBank(page, request);
  await tab(page, "match");
  await expect(page.getByRole("button", { name: "Unmatched", exact: true })).toHaveAttribute("aria-pressed", "true");
  await page.getByRole("button", { name: "Matched", exact: true }).click();
  await expect(page.locator("[data-empty-lines]")).toContainText("Nothing is in this state.");
  await page.getByRole("button", { name: "Excluded", exact: true }).click();
  await expect(page.locator("[data-empty-lines]")).toBeVisible();
  await page.getByRole("button", { name: "All", exact: true }).click();
  await expect(page.locator("[data-line-id]")).toHaveCount(7);
  // An excluded or already matched line offers no hand match.
  await page.locator("[data-field='br-account']").selectOption({ label: FUEL_CARD });
  await expect(page.locator("[data-empty-lines]")).toContainText("Import a statement for this account first.");
});

test("on a phone the tables give way to cards and no screen scrolls the page sideways", async ({ page, request }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openBank(page, request);
  const sideways = () => page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth);
  expect(await sideways()).toBe(false);
  await page.getByRole("textbox", { name: "Closing balance on the statement" }).fill(CLOSING);
  await page.getByRole("button", { name: "Open month" }).click();
  await expect(page.locator("[data-recon-card]")).toBeVisible();
  await expect(page.locator("[data-recon-id]")).toBeHidden();
  await expect(page.locator("[data-recon-card]")).toContainText("Off by $6,160.50");
  expect(await sideways()).toBe(false);
  await tab(page, "match");
  await expect(page.locator("[data-line-card]")).toHaveCount(7);
  await expect(page.locator("[data-line-id]").first()).toBeHidden();
  expect(await sideways()).toBe(false);
  await page.locator("[data-line-card]").filter({ hasText: "WIRE FROM CONTRACTOR" }).getByRole("button", { name: "Match by hand" }).click();
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBeGreaterThan(300);
  await expect(page.locator("[data-line-panel]")).toBeInViewport();
  expect(await sideways()).toBe(false);
  await tab(page, "rules");
  await expect(page.locator("[data-rule-card]")).toHaveCount(3);
  expect(await sideways()).toBe(false);
  await tab(page, "import");
  expect(await sideways()).toBe(false);
});

test("the tabs and the controls work from the keyboard, and focus is visible", async ({ page, request }) => {
  await openBank(page, request);
  const importTab = page.locator("[data-tab-id='import']");
  await importTab.focus();
  // Keyboard modality first: a focus ring belongs to the keyboard, not to a script's focus call.
  await page.keyboard.press("Tab");
  await page.keyboard.press("Shift+Tab");
  const outline = await importTab.evaluate((el) => getComputedStyle(el).outlineStyle);
  expect(outline).not.toBe("none");
  await page.keyboard.press("Enter");
  await expect(page.locator("[data-br-import]")).toBeVisible();
  // The account select is reachable and changes by keyboard.
  const select = page.locator("[data-field='br-account']");
  await select.focus();
  await page.keyboard.press("ArrowDown");
  await expect(select.locator("option:checked")).toHaveText(FUEL_CARD);
  // The match tab by Space, the first line's action by Enter.
  await page.locator("[data-tab-id='accounts']").focus();
  await page.keyboard.press("Space");
  await expect(page.locator("[data-br-accounts]")).toBeVisible();
  await page.locator("[data-account-id]").filter({ hasText: OPERATING }).focus();
  await page.keyboard.press("Enter");
  await page.locator("[data-tab-id='match']").focus();
  await page.keyboard.press("Enter");
  await page.locator("[data-line-id]").first().getByRole("button").focus();
  await page.keyboard.press("Enter");
  await expect(page.locator("[data-line-panel]")).toBeVisible();
});
