// The Financial Controller's books on the Money Desk, walked in a real browser on the stub: the chart of
// accounts, a new journal (posted below the threshold, parked at it), void and reverse behind their review,
// the recurring templates, the fiscal periods with close, reopen and the year end close, the trial balance by
// month with its export, the receivables tie out, the BisTrack check, the policy, and Payables and
// Receivables reading the same ledger with every write held. Each destructive step is a review that names
// what it will change before anything is sent.
import { test, expect, openApprovals, openTab, fillLine, draftJournal, openReader } from "./gl-helpers.js";

const SEPT_JOURNAL_ROW = (n: number): string => `JE-${String(n).padStart(4, "0")}`;

test("the chart lists every account with its status in words, and adds an account the journals can use", async ({ page, request }) => {
  await openApprovals(page, request);
  await openTab(page, "chart");
  const table = page.getByRole("table", { name: "Accounts" });
  await expect(table.getByRole("row", { name: /1000 Cash/ })).toContainText("Active");
  await expect(table).toContainText("Retained Earnings");
  await page.locator('[data-field="account-code"]').fill("6100");
  await page.locator('[data-field="account-name"]').fill("Freight out");
  await page.locator('[data-field="account-type"]').selectOption("EXPENSE");
  await page.getByRole("button", { name: "Add account" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Account 6100 Freight out is added");
  await expect(table.getByRole("row", { name: /6100 Freight out/ })).toContainText("Expense");
  // The form is empty again, and a second 6100 is refused in the core's words.
  await expect(page.locator('[data-field="account-code"]')).toHaveValue("");
  await page.locator('[data-field="account-code"]').fill("6100");
  await page.locator('[data-field="account-name"]').fill("Again");
  await page.getByRole("button", { name: "Add account" }).click();
  await expect(page.locator("[data-error]")).toContainText("already exists");
  await expect(page.locator("[data-error]")).not.toContainText("Gl:");
  // The new account is a choice on the journal form.
  await draftJournal(page, "Freight", "6100 · Freight out", "1000 · Cash", "10.00");
});

test("deactivating an account is a review that names what it keeps and what it ends, and cancel changes nothing", async ({ page, request }) => {
  await openApprovals(page, request);
  await openTab(page, "chart");
  await page.getByRole("button", { name: "Deactivate 5200 Inventory Adjustments" }).click();
  const confirm = page.locator("[data-confirm]");
  await expect(confirm).toContainText("Deactivate 5200 Inventory Adjustments?");
  await expect(confirm).toContainText("no route to turn an account back on");
  await expect(confirm).toBeFocused();
  await confirm.getByRole("button", { name: "Keep it as it is" }).click();
  await expect(page.locator("[data-confirm]")).toHaveCount(0);
  await expect(page.locator('[data-account="5200"]')).toContainText("Active");
  // 5200 sits on a parked draft, so the server refuses and the review stays open with its words.
  await page.getByRole("button", { name: "Deactivate 5200 Inventory Adjustments" }).click();
  await page.locator("[data-confirm]").getByRole("button", { name: "Deactivate 5200" }).click();
  await expect(page.locator("[data-confirm-error]")).toContainText("referenced by DRAFT journal(s); cannot deactivate");
  await page.locator("[data-confirm]").getByRole("button", { name: "Keep it as it is" }).click();
  // 1100 is free of drafts: it deactivates and reads Inactive.
  await page.getByRole("button", { name: "Deactivate 1100 Accounts Receivable" }).click();
  await page.locator("[data-confirm]").getByRole("button", { name: "Deactivate 1100" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Account 1100 Accounts Receivable is deactivated");
  await expect(page.locator('[data-account="1100"]')).toContainText("Inactive");
});

test("a journal below the threshold posts after a review, the register shows it posted and the trial balance carries it", async ({ page, request }) => {
  await openApprovals(page, request);
  await openTab(page, "trial");
  const before = await page.locator("[data-total-debit]").innerText();
  await draftJournal(page, "Freight accrual", "5200 · Inventory Adjustments", "1000 · Cash", "120.00");
  await page.getByRole("button", { name: "Review" }).click();
  const review = page.locator("[data-review]");
  await expect(review).toContainText("Post this journal?");
  await expect(review.getByRole("table", { name: "Lines to post" })).toContainText("$120.00");
  await expect(review.locator("[data-threshold]")).toContainText("$5,000.00");
  await expect(review).toBeFocused();
  // Back to editing keeps what was typed.
  await review.getByRole("button", { name: "Back to editing" }).click();
  await expect(page.locator('[data-field="memo"]')).toHaveValue("Freight accrual");
  await page.getByRole("button", { name: "Review" }).click();
  await page.locator("[data-review]").getByRole("button", { name: "Post journal" }).click();
  await expect(page.locator("[data-notice]")).toContainText(`Posted as ${SEPT_JOURNAL_ROW(7)}`);
  await expect(page.getByRole("table", { name: "Journal register" }).getByRole("row", { name: /JE-0007/ })).toContainText("Posted");
  await expect(page.locator("[data-composer]")).toHaveCount(0);
  await openTab(page, "trial");
  await expect(page.locator("[data-total-debit]")).not.toHaveText(before);
  await expect(page.locator("[data-trial]")).toContainText("In balance");
});

test("a journal at the threshold parks, files its request in the approvals queue, and says so", async ({ page, request }) => {
  await openApprovals(page, request);
  await draftJournal(page, "Roof for the second yard", "5200 · Inventory Adjustments", "1000 · Cash", "5,000.00");
  await page.getByRole("button", { name: "Review" }).click();
  await page.locator("[data-review]").getByRole("button", { name: "Post journal" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Parked as JE-0007");
  await expect(page.locator("[data-notice]")).toContainText("a second approver posts it");
  await expect(page.getByRole("table", { name: "Journal register" }).getByRole("row", { name: /JE-0007/ })).toContainText("Pending approval");
  await expect(page.locator("[data-tab-id='approvals'] .gl-count")).toHaveText("6");
});

test("the form checks each line before anything is sent: a memo, one side, a real amount", async ({ page, request }) => {
  await openApprovals(page, request);
  const sent: string[] = [];
  page.on("request", (r) => {
    if (r.method() === "POST" && r.url().includes("/api/v1/gl/journals")) sent.push(r.url());
  });
  await openTab(page, "journals");
  await page.getByRole("button", { name: "New journal" }).click();
  await page.getByRole("button", { name: "Review" }).click();
  await expect(page.locator("[data-compose-error]")).toContainText("add a memo");
  await page.locator('[data-field="memo"]').fill("Check");
  await page.getByRole("button", { name: "Review" }).click();
  await expect(page.locator("[data-compose-error]")).toContainText("Line 1: choose an account");
  await fillLine(page, 0, "5200 · Inventory Adjustments", "debit", "12.345");
  await fillLine(page, 1, "1000 · Cash", "credit", "12.34");
  await page.getByRole("button", { name: "Review" }).click();
  await expect(page.locator("[data-compose-error]")).toContainText("Line 1: the debit is not an amount");
  await page.locator('[data-field="line-debit"][data-key="0"]').fill("12.34");
  await page.locator('[data-field="line-credit"][data-key="0"]').fill("12.34");
  await page.getByRole("button", { name: "Review" }).click();
  await expect(page.locator("[data-compose-error]")).toContainText("Line 1: fill exactly one of debit or credit");
  expect(sent).toEqual([]);
});

test("an unbalanced journal is refused by the server in words, nothing is saved, and the form keeps what was typed", async ({ page, request }) => {
  await openApprovals(page, request);
  await draftJournal(page, "Off by a dollar", "5200 · Inventory Adjustments", "1000 · Cash", "10.00");
  await page.locator('[data-field="line-credit"][data-key="1"]').fill("9.00");
  await page.getByRole("button", { name: "Review" }).click();
  await page.locator("[data-review]").getByRole("button", { name: "Post journal" }).click();
  await expect(page.locator("[data-compose-error]")).toContainText("Debits and credits are not equal, so nothing was saved");
  await expect(page.locator('[data-field="memo"]')).toHaveValue("Off by a dollar");
  await expect(page.locator('[data-field="line-credit"][data-key="1"]')).toHaveValue("9.00");
  await openTab(page, "journals");
  await expect(page.getByRole("table", { name: "Journal register" }).getByRole("row", { name: /JE-0007/ })).toHaveCount(0);
});

test("a lost answer to a post says it may or may not have landed, and the desk never sends it a second time", async ({ page, request }) => {
  await openApprovals(page, request);
  let posts = 0;
  await page.route("**/api/v1/gl/journals", (route) => {
    if (route.request().method() !== "POST") return route.continue();
    posts += 1;
    return route.abort("failed");
  });
  await draftJournal(page, "Lost answer", "5200 · Inventory Adjustments", "1000 · Cash", "10.00");
  await page.getByRole("button", { name: "Review" }).click();
  await page.locator("[data-review]").getByRole("button", { name: "Post journal" }).click();
  await expect(page.locator("[data-compose-error]")).toContainText("may or may not have been saved");
  await expect(page.locator("[data-compose-error]")).toContainText("look at the register before you press again");
  expect(posts).toBe(1);
  // Posting again takes a new review, and the form kept what was typed.
  await expect(page.locator("[data-review]")).toHaveCount(0);
  await expect(page.locator('[data-field="memo"]')).toHaveValue("Lost answer");
});

test("a post refused at the gate names who may post, never the bare gate word", async ({ page, request }) => {
  await openApprovals(page, request);
  await page.route("**/api/v1/gl/journals", (route) =>
    route.request().method() === "POST"
      ? route.fulfill({ status: 403, contentType: "application/json", body: JSON.stringify({ error: "Forbidden" }) })
      : route.continue(),
  );
  await draftJournal(page, "Walled", "5200 · Inventory Adjustments", "1000 · Cash", "10.00");
  await page.getByRole("button", { name: "Review" }).click();
  await page.locator("[data-review]").getByRole("button", { name: "Post journal" }).click();
  const alert = page.locator("[data-compose-error]");
  await expect(alert).toContainText("To post a journal is for the Financial Controller or the General Manager");
  await expect(alert).not.toContainText("Forbidden");
});

test("reversing a posted journal is a review that lists its lines, cancel changes nothing, and the reversal links back", async ({ page, request }) => {
  await openApprovals(page, request);
  await openTab(page, "journals");
  await page.getByRole("table", { name: "Journal register" }).getByRole("button", { name: "JE-0004" }).click();
  await page.getByRole("button", { name: "Reverse", exact: true }).click();
  const confirm = page.locator("[data-confirm]");
  await expect(confirm).toContainText("Reverse JE-0004?");
  await expect(confirm).toContainText("every line's side swapped");
  await expect(confirm).toContainText("stays posted");
  await expect(confirm.getByRole("table", { name: "Lines of the journal being reversed" })).toContainText("$25.00");
  await expect(confirm).toBeFocused();
  await confirm.getByRole("button", { name: "Keep it as it is" }).click();
  await expect(page.getByRole("table", { name: "Journal register" }).getByRole("row", { name: /JE-0007/ })).toHaveCount(0);
  await page.getByRole("button", { name: "Reverse", exact: true }).click();
  await page.locator("[data-confirm]").getByRole("button", { name: "Reverse JE-0004" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Reversed JE-0004. JE-0007 carries the opposite lines");
  await expect(page.locator("[data-journal-panel]")).toContainText("JE-0007");
  await expect(page.locator("[data-reverses]")).toHaveText("JE-0004");
  // The original stays posted; a posted journal cannot be voided and a second reversal is a new journal.
  await expect(page.getByRole("table", { name: "Journal register" }).getByRole("row", { name: /JE-0004/ })).toContainText("Posted");
});

test("voiding a draft is a review, and the register then reads it Void", async ({ page, request }) => {
  await openApprovals(page, request);
  await openTab(page, "journals");
  await page.getByRole("table", { name: "Journal register" }).getByRole("button", { name: "JE-0006" }).click();
  await page.getByRole("button", { name: "Void", exact: true }).click();
  const confirm = page.locator("[data-confirm]");
  await expect(confirm).toContainText("Void JE-0006?");
  await expect(confirm).toContainText("has not touched the ledger");
  await confirm.getByRole("button", { name: "Void JE-0006" }).click();
  await expect(page.locator("[data-notice]")).toContainText("JE-0006 is voided");
  await expect(page.getByRole("table", { name: "Journal register" }).getByRole("row", { name: /JE-0006/ })).toContainText("Void");
});

test("approving a draft on its own route is refused to its maker in the core's words", async ({ page, request }) => {
  await openApprovals(page, request);
  await openTab(page, "journals");
  await page.locator("[data-template]").first().getByRole("button", { name: /^Run/ }).click();
  await expect(page.locator("[data-notice]")).toContainText("Draft JE-0007 added from Yard lease accrual");
  await expect(page.locator("[data-journal-panel]")).toContainText("Draft");
  await page.getByRole("button", { name: "Approve here" }).click();
  await expect(page.locator("[data-error]")).toContainText("You made this journal, so you cannot approve it");
});

test("the recurring templates list what they post and run into a draft that never posts by itself", async ({ page, request }) => {
  await openApprovals(page, request);
  await openTab(page, "journals");
  const recurring = page.locator("[data-recurring]");
  await expect(recurring).toContainText("Yard lease accrual");
  await expect(recurring).toContainText("Insurance prepaid release");
  await expect(recurring).toContainText("never posts by itself");
  await recurring.getByRole("button", { name: "Run Insurance prepaid release" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Draft JE-0007 added from Insurance prepaid release");
  await expect(page.getByRole("table", { name: "Journal register" }).getByRole("row", { name: /JE-0007/ })).toContainText("Pending approval");
});

test("closing a period names the period and what changes, then the period reads Closed and reopens only with a reason", async ({ page, request }) => {
  await openApprovals(page, request);
  await openTab(page, "periods");
  const periods = page.getByRole("table", { name: "Periods" });
  await expect(periods.getByRole("row", { name: /August 2026/ })).toContainText("Open");
  await expect(periods.getByRole("row", { name: /July 2026/ })).toContainText("Closed");
  await page.getByRole("button", { name: "Close August 2026" }).click();
  const confirm = page.locator("[data-confirm]");
  await expect(confirm).toContainText("Close August 2026?");
  await expect(confirm).toContainText("No journal can post into August 2026 until the Financial Controller reopens it");
  await expect(confirm.locator("[data-parked]")).toContainText("2 parked journals wait in this period");
  await expect(confirm).toBeFocused();
  await confirm.getByRole("button", { name: "Keep it as it is" }).click();
  await expect(periods.getByRole("row", { name: /August 2026/ })).toContainText("Open");
  await page.getByRole("button", { name: "Close August 2026" }).click();
  await page.locator('[data-field="reason"]').fill("August is reconciled");
  await page.locator("[data-confirm]").getByRole("button", { name: "Close August 2026" }).click();
  await expect(page.locator("[data-notice]")).toContainText("August 2026 is closed");
  await expect(periods.getByRole("row", { name: /August 2026/ })).toContainText("Closed");
  // Reopen needs a reason: empty is refused in words without a request, then a reason reopens it.
  await page.getByRole("button", { name: "Reopen August 2026" }).click();
  await page.locator("[data-confirm]").getByRole("button", { name: "Reopen August 2026" }).click();
  await expect(page.locator("[data-confirm-error]")).toContainText("A reason is required");
  await page.locator('[data-field="reason"]').fill("A late supplier invoice");
  await page.locator("[data-confirm]").getByRole("button", { name: "Reopen August 2026" }).click();
  await expect(page.locator("[data-notice]")).toContainText("August 2026 is open again. Your reason is on record");
  await expect(periods.getByRole("row", { name: /August 2026/ })).toContainText("Open");
});

test("a journal dated into a closed month is refused in words that say who can reopen it", async ({ page, request }) => {
  await openApprovals(page, request);
  await openTab(page, "periods");
  await page.getByRole("button", { name: "Close September 2026" }).click();
  await page.locator("[data-confirm]").getByRole("button", { name: "Close September 2026" }).click();
  await expect(page.locator("[data-notice]")).toContainText("September 2026 is closed");
  await draftJournal(page, "Too late", "5200 · Inventory Adjustments", "1000 · Cash", "10.00");
  await page.getByRole("button", { name: "Review" }).click();
  await page.locator("[data-review]").getByRole("button", { name: "Post journal" }).click();
  await expect(page.locator("[data-compose-error]")).toContainText("That month is closed, so nothing was saved. The Financial Controller can reopen it with a reason");
});

test("the year end close is a review naming the year, retained earnings and the periods it seals, and reports the server's net income", async ({ page, request }) => {
  await openApprovals(page, request);
  await openTab(page, "periods");
  await page.getByRole("button", { name: "Close fiscal year 2026" }).click();
  const confirm = page.locator("[data-confirm]");
  await expect(confirm).toContainText("Close fiscal year 2026?");
  await expect(confirm).toContainText("3900 Retained Earnings");
  await expect(confirm).toContainText("Every open period of 2026 is closed");
  await confirm.getByRole("button", { name: "Keep it as it is" }).click();
  await expect(page.getByRole("table", { name: "Periods" }).getByRole("row", { name: /December 2026/ })).toContainText("Open");
  await page.getByRole("button", { name: "Close fiscal year 2026" }).click();
  await page.locator("[data-confirm]").getByRole("button", { name: "Close fiscal year 2026" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Fiscal year 2026 is closed. Net income of $12,025.00 moved to Retained Earnings");
  await expect(page.getByRole("table", { name: "Periods" }).getByRole("row", { name: /December 2026/ })).toContainText("Closed");
  await expect(page.getByRole("button", { name: "Close fiscal year 2026" })).toHaveCount(0);
  await openTab(page, "journals");
  await expect(page.getByRole("table", { name: "Journal register" }).getByRole("row", { name: /JE-0007/ })).toContainText("Posted");
});

test("the trial balance takes a month, shows that month alone, and the export downloads the rows on screen", async ({ page, request }) => {
  await openApprovals(page, request);
  await openTab(page, "trial");
  await page.getByRole("button", { name: "Sep", exact: true }).click();
  await expect(page.locator("[data-trial]")).toContainText("September 2026");
  await expect(page.locator("[data-trial]")).toContainText("No posted journals yet.");
  await page.getByRole("button", { name: "Aug", exact: true }).click();
  await expect(page.locator("[data-trial]")).toContainText("August 2026");
  await expect(page.locator("[data-total-debit]")).toHaveText("$37,025.00");
  const [download] = await Promise.all([page.waitForEvent("download"), page.getByRole("button", { name: "Download the August export" }).click()]);
  expect(download.suggestedFilename()).toBe("gl-export-2026-month-08.csv");
  const { readFile } = await import("node:fs/promises");
  const text = await readFile(await download.path(), "utf8");
  expect(text).toContain("# PARITY DRAFT");
  expect(text).toContain("entry_no,posted_at,account_code,account_name,debit,credit,memo");
  expect(text).toContain("Opening balances");
  await expect(page.locator("[data-notice]")).toContainText("a draft in the parity format, not a filing");
  await page.getByRole("button", { name: "Whole year" }).click();
  await expect(page.locator("[data-trial]")).toContainText("fiscal year 2026");
});

test("the checks read the receivables tie out in words beside its colour, and run the BisTrack check", async ({ page, request }) => {
  await openApprovals(page, request);
  await openTab(page, "checks");
  const tie = page.locator("[data-tie]");
  await expect(tie).toContainText("Receivables ledger");
  await expect(tie).toContainText("$7,000.00");
  await expect(tie).toContainText("In tie");
  await expect(tie.locator("[data-tie-row]")).toContainText("Difference");
  // A bad form is said in words without a request.
  await page.getByRole("button", { name: "Run the check" }).click();
  await expect(page.locator("[data-bis] [data-section-error]")).toContainText("Type a fiscal year and a month from 1 to 12");
  await page.locator('[data-field="bis-year"]').fill("2026");
  await page.locator('[data-field="bis-month"]').fill("7");
  await page.getByRole("button", { name: "Run the check" }).click();
  await expect(page.locator("[data-bis] [data-section-error]")).toContainText("Bistrack snapshot is month 6 but tie-out requested month 7");
  await page.locator('[data-field="bis-month"]').fill("6");
  await page.getByRole("button", { name: "Run the check" }).click();
  const report = page.locator("[data-bis-report]");
  await expect(report).toContainText("Does not tie");
  await expect(report).toContainText("Synthetic test data, not a real BisTrack export");
  await expect(report.getByRole("table", { name: "BisTrack lines" })).toContainText("Only in BisTrack");
  await expect(report.locator("[data-bis-count]")).toContainText("2 accounts differ beyond the tolerance");
  // A tolerance wide enough takes them all in.
  await page.locator('[data-field="bis-tolerance"]').fill("200.00");
  await page.getByRole("button", { name: "Run the check" }).click();
  await expect(page.locator("[data-bis-report]")).toContainText("Ties within tolerance");
});

test("a journal that moves receivables puts the tie out out of tie, said in words with the difference", async ({ page, request }) => {
  await openApprovals(page, request);
  await draftJournal(page, "Receivable booked by hand", "1100 · Accounts Receivable", "4000 · Product Revenue", "50.00");
  await page.getByRole("button", { name: "Review" }).click();
  await page.locator("[data-review]").getByRole("button", { name: "Post journal" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Posted as JE-0007");
  await openTab(page, "checks");
  const tie = page.locator("[data-tie]");
  await expect(tie).toContainText("Out of tie");
  await expect(tie.locator("[data-tie-row]")).toContainText("-$50.00");
});

test("the policy reads the approval threshold and says changing it is not on this screen", async ({ page, request }) => {
  await openApprovals(page, request);
  await openTab(page, "policy");
  const policy = page.locator("[data-policy]");
  await expect(policy.locator("[data-policy-threshold]")).toHaveText("$5,000.00");
  await expect(policy).toContainText("January");
  await expect(policy).toContainText("Weighted average cost");
  await expect(policy).toContainText("not on this screen");
});

test("a section that cannot be read says so in words and offers to try again", async ({ page, request }) => {
  await openApprovals(page, request);
  await page.route("**/api/v1/gl/accounts?*", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify([]) }));
  await openTab(page, "chart");
  await expect(page.locator("[data-section-error]")).toContainText("The chart of accounts was not readable");
  await page.unroute("**/api/v1/gl/accounts?*");
  await page.getByRole("button", { name: "Try again" }).click();
  await expect(page.getByRole("table", { name: "Accounts" })).toContainText("Cash");
});

test("Payables and Receivables reads the ledger and holds every write, naming who may", async ({ page, request }) => {
  await openReader(page, request);
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Ledger");
  await expect(page.locator("[data-tab-id='approvals']")).toHaveCount(0);
  // Journals: the register reads, nothing posts.
  await expect(page.getByRole("button", { name: "New journal" })).toHaveCount(0);
  await expect(page.locator("[data-held]").first()).toContainText("for the Financial Controller or the General Manager");
  await page.getByRole("table", { name: "Journal register" }).getByRole("button", { name: "JE-0004" }).click();
  await expect(page.locator("[data-journal-panel]")).toContainText("JE-0004");
  await expect(page.getByRole("button", { name: "Reverse", exact: true })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Void", exact: true })).toHaveCount(0);
  // Chart, trial balance, periods, checks and policy read.
  await openTab(page, "chart");
  await expect(page.getByRole("table", { name: "Accounts" })).toContainText("Cash");
  await expect(page.getByRole("button", { name: "Add account" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: /^Deactivate/ })).toHaveCount(0);
  await openTab(page, "trial");
  await expect(page.locator("[data-trial]")).toContainText("In balance");
  await expect(page.getByRole("button", { name: /export$/ })).toHaveCount(0);
  await expect(page.locator("[data-trial] [data-held]")).toContainText("export is for the Financial Controller");
  await openTab(page, "periods");
  await expect(page.getByRole("table", { name: "Periods" })).toContainText("August 2026");
  await expect(page.getByRole("button", { name: /^Close |^Reopen / })).toHaveCount(0);
  await openTab(page, "checks");
  await expect(page.locator("[data-tie]")).toContainText("Receivables ledger");
  await expect(page.locator("[data-bis]")).toContainText("for the Financial Controller");
  await expect(page.getByRole("button", { name: "Run the check" })).toHaveCount(0);
  await openTab(page, "policy");
  await expect(page.locator("[data-policy-threshold]")).toHaveText("$5,000.00");
  // The receivables side is still whole.
  await page.getByRole("button", { name: "Receivables", exact: true }).click();
  await expect(page.locator("[data-card]")).toHaveCount(4);
});

test("on a phone the seven tabs share one row, every tab fits the width, and the journal form fits too", async ({ page, request }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openApprovals(page, request);
  const tops = await page.locator("[data-tab-id]").evaluateAll((els) => els.map((el) => Math.round(el.getBoundingClientRect().top)));
  expect(tops).toHaveLength(7);
  expect(new Set(tops).size).toBe(1);
  const sideways = async (): Promise<number> => page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  for (const id of ["approvals", "journals", "chart", "trial", "periods", "checks", "policy"]) {
    await page.locator(`[data-tab-id='${id}']`).scrollIntoViewIfNeeded();
    await openTab(page, id);
    await expect.poll(sideways).toBeLessThanOrEqual(0);
  }
  await draftJournal(page, "Phone", "5200 · Inventory Adjustments", "1000 · Cash", "10.00");
  await expect.poll(sideways).toBeLessThanOrEqual(0);
  await page.getByRole("button", { name: "Review" }).click();
  await expect(page.locator("[data-review]")).toBeInViewport();
  await expect.poll(sideways).toBeLessThanOrEqual(0);
  await page.locator("[data-review]").getByRole("button", { name: "Back to editing" }).click();
  await page.getByRole("button", { name: "Cancel" }).click();
  await openTab(page, "periods");
  await page.getByRole("button", { name: "Close August 2026" }).click();
  await expect(page.locator("[data-confirm]")).toBeInViewport();
  await expect.poll(sideways).toBeLessThanOrEqual(0);
});

test("a lost answer to a close period rereads the periods, never leaving the panel on Reading", async ({ page, request }) => {
  await openApprovals(page, request);
  await openTab(page, "periods");
  await page.route("**/api/v1/gl/periods/*/close", (route) => route.abort("failed"));
  await page.getByRole("button", { name: "Close August 2026" }).click();
  await page.locator("[data-confirm]").getByRole("button", { name: "Close August 2026" }).click();
  await expect(page.locator("[data-error]")).toContainText("may or may not have been saved");
  await expect(page.getByRole("table", { name: "Periods" })).toContainText("August 2026");
  await expect(page.getByText("Reading the fiscal periods.")).toHaveCount(0);
});

test("a lost answer to a reverse rereads the register", async ({ page, request }) => {
  await openApprovals(page, request);
  await openTab(page, "journals");
  await page.getByRole("table", { name: "Journal register" }).getByRole("button", { name: "JE-0004" }).click();
  await page.getByRole("button", { name: "Reverse", exact: true }).click();
  // The server applies the reversal but the answer is lost: the register must show it without a reload.
  await page.route("**/api/v1/gl/journals/*/reverse", async (route) => {
    await route.fetch();
    await route.abort("failed");
  });
  await page.locator("[data-confirm]").getByRole("button", { name: "Reverse JE-0004" }).click();
  await expect(page.locator("[data-error]")).toContainText("may or may not have been saved");
  await expect(page.getByRole("table", { name: "Journal register" }).getByRole("row", { name: /JE-0007/ })).toContainText("Posted");
});
