// The Financial Controller's statements pack on the Money Desk, walked in a real browser on the stub: the
// income statement, the balance sheet, the trial balance, the ledger detail behind a line (and whether it ties),
// the receivables aging, the saved packs, the CSV and the printed sheet; and each refusal and state: a manager
// who holds one branch, a role that may read but not save, a failed load, an empty list, a shape the contract
// does not send. The stub's books are cut from the host's own clock, so no calendar value is written here.
import { test, expect, openStatements, openStatementsAs, stubCalls, stubYear, MANAGER, PAYREC } from "./finstmt-helpers.js";
import { openDesk } from "./helpers.js";

// The shared stub names its two branches this way.
const TRENTON = "Trenton (demo)";
const KINGSTON = "Kingston (demo)";

const tile = (page: import("@playwright/test").Page, id: string) => page.locator(`[data-tile="${id}"]`);
const tab = (page: import("@playwright/test").Page, id: string) => page.locator(`[data-tab-id="${id}"]`);

test("the controller's Statements area opens on the income statement with the books' own periods", async ({ page, request }) => {
  const year = await stubYear(request);
  await openStatements(page, request);
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Statements");
  await expect(page.locator("[data-statement-title]")).toHaveText("Income statement");
  await expect(page.locator("[data-statement-sub]")).toHaveText(`Fiscal year ${year} · All branches`);
  // The years and periods are the books' own list, in the books' own words.
  const years = page.getByLabel("Fiscal year");
  await expect(years.locator("option")).toHaveText([`Fiscal year ${year}`, `Fiscal year ${year - 1}`]);
  await expect(page.getByLabel("Period").locator("option").nth(1)).toHaveText("Period 1 · closed");
  await expect(page.getByLabel("Period").locator("option").nth(12)).toHaveText("Period 12 · open");
  // The totals are the server's, and the lines sit under them.
  await expect(tile(page, "revenue")).toContainText("$");
  await expect(page.locator('[data-total="revenue"]')).toHaveText(await tile(page, "revenue").innerText());
  await expect(page.locator('[data-account="4000"]')).toContainText("Product Revenue");
  await expect(page.locator('[data-total="net"]')).toBeVisible();
  // Payables and Receivables has no Statements area.
  const other = await page.context().newPage();
  await openDesk(other, request);
  await expect(other.locator("nav.areas")).toHaveCount(0);
});

test("a period narrows the income statement and says whether it is closed or still open", async ({ page, request }) => {
  const year = await stubYear(request);
  await openStatements(page, request);
  const whole = await tile(page, "revenue").innerText();
  await page.getByLabel("Period").selectOption({ label: "Period 2 · closed" });
  await expect(page.locator("[data-statement-sub]")).toHaveText(`Period 2 of fiscal year ${year} · All branches`);
  await expect(page.locator("[data-statement]")).toContainText("Period closed");
  await expect(tile(page, "revenue")).not.toHaveText(whole);
  await page.getByLabel("Period").selectOption({ label: "Period 6 · open" });
  await expect(page.locator("[data-statement]")).toContainText("Period open: figures can still move");
  // A period with nothing posted says so in words and shows no figure of its own.
  await page.getByLabel("Period").selectOption({ label: "Period 12 · open" });
  await expect(page.locator("[data-empty]")).toContainText("No posted journals in this period.");
  // Another fiscal year starts at its whole year again.
  await page.getByLabel("Fiscal year").selectOption({ label: `Fiscal year ${year - 1}` });
  await expect(page.locator("[data-statement-sub]")).toHaveText(`Fiscal year ${year - 1} · All branches`);
  await expect(page.getByLabel("Period")).toHaveValue("Whole fiscal year");
});

test("a branch narrows the statements, and the whole company is the two branches together", async ({ page, request }) => {
  await openStatements(page, request);
  const both = await tile(page, "revenue").innerText();
  await page.getByLabel("Branch", { exact: true }).selectOption({ label: TRENTON });
  await expect(page.locator("[data-statement-sub]")).toContainText(`· ${TRENTON}`);
  const one = await tile(page, "revenue").innerText();
  expect(one).not.toBe(both);
  await page.getByLabel("Branch", { exact: true }).selectOption({ label: KINGSTON });
  await expect(page.locator("[data-statement-sub]")).toContainText(`· ${KINGSTON}`);
  await expect(tile(page, "revenue")).not.toHaveText(one);
  // The branch choice rides to the balance sheet.
  await tab(page, "balance").click();
  await expect(page.locator("[data-statement-sub]")).toContainText(`· ${KINGSTON}`);
});

test("a manager who holds one branch is refused the consolidated books in words, and reads that branch", async ({ page, request }) => {
  await openStatementsAs(page, request, MANAGER);
  await expect(page.locator("[data-error]")).toHaveText("You cannot read the books of that branch. Choose a branch you hold.");
  await expect(page.locator("[data-error]")).not.toContainText("finstmt");
  await expect(page.getByRole("button", { name: "Try again" })).toBeVisible();
  await page.getByLabel("Branch", { exact: true }).selectOption({ label: TRENTON });
  await expect(page.locator("[data-statement]")).toBeVisible();
  await expect(page.locator("[data-error]")).toHaveCount(0);
  await expect(page.locator("[data-statement-sub]")).toContainText(`· ${TRENTON}`);
  // The trial balance has no branch: it is the whole company's, and a manager of one branch is refused it.
  await tab(page, "trial").click();
  await expect(page.locator("[data-error]")).toHaveText("You cannot read the books of that branch. Choose a branch you hold.");
});

test("a refused read names who may read, never the bare gate word", async ({ page, request }) => {
  await page.route("**/api/v1/finstmt/income-statement*", (route) => route.fulfill({ status: 403, contentType: "application/json", body: JSON.stringify({ error: "Forbidden" }) }));
  await openStatements(page, request).catch(() => undefined);
  await expect(page.locator("[data-error]")).toHaveText("Statements are for the Financial Controller or a manager.");
  await expect(page.locator("[data-error]")).not.toHaveText("Forbidden");
});

test("the year before sits beside this year and the change is the books' own figure", async ({ page, request }) => {
  const year = await stubYear(request);
  await openStatements(page, request);
  await page.getByLabel("Compare with the year before", { exact: true }).selectOption({ label: "With the year before" });
  await expect(page.locator("[data-statement-sub]")).toContainText(`with fiscal year ${year - 1}`);
  const table = page.getByRole("table", { name: "Income statement compared with the year before" });
  await expect(table.locator("th").nth(2)).toHaveText(`Fiscal year ${year}`);
  await expect(table.locator("th").nth(3)).toHaveText(`Fiscal year ${year - 1}`);
  await expect(table.locator('[data-account="4000"] [data-change]')).toHaveText(/^[+-]?\$[\d,]+\.\d\d$/);
  await expect(page.locator("[data-statement]")).toContainText("The change is the books' own figure. It is not worked out on this page.");
  await tab(page, "balance").click();
  await expect(page.getByRole("table", { name: "Balance sheet compared with the year before" })).toBeVisible();
  await page.getByLabel("Compare with the year before", { exact: true }).selectOption({ label: "This year alone" });
  await expect(page.getByRole("table", { name: "Balance sheet lines" })).toBeVisible();
});

test("a statement line opens the ledger detail behind it, and the detail ties to the line", async ({ page, request }) => {
  await openStatements(page, request);
  await page.getByLabel("Period").selectOption({ label: "Period 3 · closed" });
  const figure = await page.locator('[data-account="4000"] [data-figure]').innerText();
  await page.locator('[data-account="4000"] [data-action="fs-drill"]').first().click();
  await expect(page.locator("[data-statement-title]")).toHaveText("General ledger detail");
  await expect(page.locator("[data-tie]")).toContainText("Ties to the statement line");
  await expect(page.locator("[data-tie]")).toContainText(figure);
  const lines = page.getByRole("table", { name: "General ledger detail lines" });
  await expect(lines.locator("tbody tr").first()).toContainText("4000");
  await expect(lines.locator("tbody tr").first()).toContainText("Invoice");
  // Back returns to the statement the line came from.
  await page.locator("[data-back]").click();
  await expect(page.locator("[data-statement-title]")).toHaveText("Income statement");
  await expect(page.locator("[data-statement-sub]")).toContainText("Period 3 of fiscal year");
});

test("the ledger detail narrows by account and shows more on request", async ({ page, request }) => {
  await openStatements(page, request);
  await tab(page, "gl").click();
  await expect(page.locator("[data-count]")).toContainText("Showing 100 of");
  await page.getByRole("button", { name: "Show more" }).click();
  await expect(page.getByRole("button", { name: "Show more" })).toHaveCount(0);
  await expect(page.locator("[data-count]")).toHaveText(/Showing (\d+) of \1 lines\./);
  await page.getByLabel("Account number").fill("1100");
  await page.getByRole("button", { name: "Show detail" }).click();
  const rows = page.getByRole("table", { name: "General ledger detail lines" }).locator("tbody tr");
  await expect(rows.first()).toContainText("1100");
  const count = await rows.count();
  for (let i = 0; i < count; i += 1) await expect(rows.nth(i)).toContainText("1100");
  await page.getByRole("button", { name: "All accounts" }).click();
  await expect(page.locator("[data-count]")).toContainText("Showing 100 of");
});

test("the balance sheet balances by the server's own flag and carries each section's total", async ({ page, request }) => {
  await openStatements(page, request);
  await tab(page, "balance").click();
  await expect(page.locator("[data-statement-title]")).toHaveText("Balance sheet");
  await expect(page.locator("[data-statement]")).toContainText("Balanced");
  for (const id of ["assets", "liabilities", "equity", "net"]) await expect(tile(page, id)).toContainText("$");
  await expect(page.locator('[data-total="assets"]')).toHaveText(await tile(page, "assets").innerText());
  await expect(page.locator('[data-account="1100"]')).toContainText("Accounts Receivable");
  await expect(page.locator('[data-account="2200"]')).toContainText("Sales Tax Payable");
  // A cash flow statement is marked later in words: the books do not serve one.
  await expect(page.locator("[data-statement]")).toContainText("A cash flow statement is");
  await expect(page.locator("[data-statement]")).toContainText("the books do not serve one yet");
  // A figure on the sheet ties to its detail, the year's whole detail since a balance sheet is a year's position.
  const figure = await page.locator('[data-account="1000"] [data-figure]').innerText();
  await page.locator('[data-account="1000"] [data-action="fs-drill"]').first().click();
  await expect(page.locator("[data-tie]")).toContainText("Ties to the statement line");
  await expect(page.locator("[data-tie]")).toContainText(figure);
});

test("the trial balance reads in balance with the server's totals and takes no branch", async ({ page, request }) => {
  await openStatements(page, request);
  await tab(page, "trial").click();
  await expect(page.locator("[data-statement-title]")).toHaveText("Trial balance");
  await expect(page.locator("[data-statement]")).toContainText("In balance");
  await expect(page.locator("[data-statement-sub]")).toContainText("All branches");
  await expect(page.getByLabel("Branch", { exact: true })).toHaveCount(0);
  await expect(page.locator('[data-total="debit"]')).toHaveText(await page.locator('[data-total="credit"]').innerText());
  await expect(page.locator('[data-account="1000"]')).toContainText("Asset");
});

test("the receivables aging shows its bands and total, and a branch narrows it", async ({ page, request }) => {
  await openStatements(page, request);
  await tab(page, "aging").click();
  await expect(page.locator("[data-statement-title]")).toHaveText("Receivables aging");
  await expect(page.locator("[data-statement-sub]")).toHaveText("As of now · All branches");
  for (const band of ["CURRENT", "1_30", "31_60", "61_90", "90_PLUS"]) await expect(page.locator(`[data-bucket="${band}"]`)).toContainText("$");
  await expect(page.locator("[data-statement]")).toContainText("Over 90 days past due");
  await expect(page.locator("[data-statement]")).toContainText("Summit Ridge Builders");
  const all = await page.locator('[data-total="grand"]').innerText();
  await page.getByLabel("Branch", { exact: true }).selectOption({ label: TRENTON });
  await expect(page.locator('[data-total="grand"]')).not.toHaveText(all);
  await expect(page.locator("[data-statement-sub]")).toHaveText(`As of now · ${TRENTON}`);
});

test("the CSV is made from the figures on the page, in plain decimals, and named for the period", async ({ page, request }) => {
  const year = await stubYear(request);
  await openStatements(page, request);
  await page.getByLabel("Period").selectOption({ label: "Period 3 · closed" });
  await expect(page.locator("[data-statement-sub]")).toContainText("Period 3");
  const net = await tile(page, "net").innerText();
  const [download] = await Promise.all([page.waitForEvent("download"), page.locator("[data-csv]").click()]);
  expect(download.suggestedFilename()).toBe(`income-statement-fiscal-year-${year}-period-3.csv`);
  const path = await download.path();
  const { readFileSync } = await import("node:fs");
  const text = readFileSync(path, "utf8");
  expect(text.startsWith("Account,Name,Amount\r\n")).toBe(true);
  expect(text).toContain("Revenue,,\r\n");
  expect(text).toMatch(/\r\n4000,Product Revenue,\d+\.\d\d\r\n/);
  expect(text).toContain(`,Net income,${net.replace(/[$,]/g, "")}\r\n`);
  expect(text).not.toContain("$");
  await expect(page.locator("[data-notice]")).toContainText("The file is made from the figures on this page.");
});

test("a printed statement carries its title, period and scope and nothing around it", async ({ page, request }) => {
  await openStatements(page, request);
  await page.getByLabel("Branch", { exact: true }).selectOption({ label: TRENTON });
  await expect(page.locator("[data-statement-sub]")).toContainText(TRENTON);
  await page.emulateMedia({ media: "print" });
  await expect(page.locator("hos-app-header")).toBeHidden();
  await expect(page.locator("nav.areas")).toBeHidden();
  await expect(page.locator(".fs-tabs")).toBeHidden();
  await expect(page.locator(".fs-controls")).toBeHidden();
  await expect(page.locator("[data-statement-title]")).toBeVisible();
  await expect(page.locator("[data-statement-sub]")).toContainText(TRENTON);
  await expect(page.getByRole("table", { name: "Income statement lines" })).toBeVisible();
  await expect(page.locator(".fs-print-only")).toBeVisible();
  await page.emulateMedia({ media: "screen" });
  await expect(page.locator(".fs-tabs")).toBeVisible();
});

test("a saved pack is saved, changed and deleted after asking, and the list follows", async ({ page, request }) => {
  await openStatements(page, request);
  await tab(page, "packs").click();
  await expect(page.locator("[data-pack]")).toHaveCount(2);
  await expect(page.locator("[data-packs]")).toContainText("Sending packs by e-mail is");
  await expect(page.locator("[data-packs]")).toContainText("nothing is sent yet");
  await page.getByLabel("Statement", { exact: true }).selectOption({ label: "Trial balance" });
  await page.getByLabel("How often").selectOption({ label: "Daily" });
  await page.getByLabel("Recipients").fill("accounts@hh-demo.invalid, owner@hh-demo.invalid");
  await page.getByRole("button", { name: "Save pack" }).click();
  await expect(page.locator("[data-notice]")).toHaveText("Pack saved.");
  await expect(page.locator("[data-pack]")).toHaveCount(3);
  const mine = page.locator("[data-pack]").filter({ hasText: "Trial balance" });
  await expect(mine).toContainText("accounts@hh-demo.invalid, owner@hh-demo.invalid");
  await expect(mine).toContainText("Daily");
  // The form is empty again for the next one.
  await expect(page.getByLabel("Recipients")).toHaveValue("");
  // Change it: the form fills from the pack, and the change lands.
  await mine.getByRole("button", { name: "Change the Trial balance pack" }).click();
  await expect(page.getByLabel("Recipients")).toHaveValue("accounts@hh-demo.invalid,owner@hh-demo.invalid");
  await page.getByLabel("How often").selectOption({ label: "Weekly" });
  await page.getByLabel("State of the pack").selectOption({ label: "Paused" });
  await page.getByRole("button", { name: "Save change" }).click();
  await expect(page.locator("[data-notice]")).toHaveText("Pack changed.");
  await expect(page.locator("[data-pack]").filter({ hasText: "Trial balance" })).toContainText("Weekly");
  await expect(page.locator("[data-pack]").filter({ hasText: "Trial balance" })).toContainText("Paused");
  // Delete asks first; keeping it changes nothing.
  const row = page.locator("[data-pack]").filter({ hasText: "Trial balance" });
  await row.getByRole("button", { name: "Delete the Trial balance pack" }).click();
  await expect(row).toContainText("Delete this pack?");
  await row.getByRole("button", { name: "Keep it" }).click();
  await expect(page.locator("[data-pack]")).toHaveCount(3);
  await row.getByRole("button", { name: "Delete the Trial balance pack" }).click();
  await row.getByRole("button", { name: "Yes, delete" }).click();
  await expect(page.locator("[data-notice]")).toHaveText("Pack deleted.");
  await expect(page.locator("[data-pack]")).toHaveCount(2);
});

test("a pack that is not complete is refused in the browser and nothing is sent", async ({ page, request }) => {
  await openStatements(page, request);
  await tab(page, "packs").click();
  await expect(page.locator("[data-pack]")).toHaveCount(2);
  const before = await stubCalls(request);
  await page.getByRole("button", { name: "Save pack" }).click();
  await expect(page.locator("[data-error]")).toHaveText("Choose the statement the pack carries.");
  await page.getByLabel("Statement", { exact: true }).selectOption({ label: "Income statement" });
  await page.getByRole("button", { name: "Save pack" }).click();
  await expect(page.locator("[data-error]")).toHaveText("Choose how often the pack goes.");
  await page.getByLabel("How often").selectOption({ label: "Monthly" });
  await page.getByRole("button", { name: "Save pack" }).click();
  await expect(page.locator("[data-error]")).toHaveText("Give at least one recipient e-mail.");
  await page.getByLabel("Recipients").fill("nobody");
  await page.getByRole("button", { name: "Save pack" }).click();
  await expect(page.locator("[data-error]")).toHaveText("That is not an e-mail: nobody.");
  expect(await stubCalls(request)).toBe(before);
  await expect(page.locator("[data-pack]")).toHaveCount(2);
});

test("a pack refused by the core names who may save it, and changes nothing", async ({ page, request }) => {
  await openStatementsAs(page, request, PAYREC);
  await expect(page.locator("[data-statement]")).toBeVisible();
  await tab(page, "packs").click();
  await expect(page.locator("[data-pack]")).toHaveCount(2);
  await page.getByLabel("Statement", { exact: true }).selectOption({ label: "Receivables aging" });
  await page.getByLabel("How often").selectOption({ label: "Daily" });
  await page.getByLabel("Recipients").fill("accounts@hh-demo.invalid");
  await page.getByRole("button", { name: "Save pack" }).click();
  await expect(page.locator("[data-error]")).toHaveText("Saving a pack is for the Financial Controller.");
  await expect(page.locator("[data-error]")).not.toHaveText("Forbidden");
  await expect(page.locator("[data-pack]")).toHaveCount(2);
});

test("a recipient the core does not accept is said in the core's own words", async ({ page, request }) => {
  await openStatements(page, request);
  await tab(page, "packs").click();
  await page.route("**/api/v1/finstmt/schedules", (route) => (route.request().method() === "POST" ? route.fulfill({ status: 400, contentType: "application/json", body: JSON.stringify({ error: 'invalid recipient e-mail: "a@@b"' }) }) : route.continue()));
  await page.getByLabel("Statement", { exact: true }).selectOption({ label: "Trial balance" });
  await page.getByLabel("How often").selectOption({ label: "Daily" });
  await page.getByLabel("Recipients").fill("a@b");
  await page.getByRole("button", { name: "Save pack" }).click();
  await expect(page.locator("[data-error]")).toHaveText('Invalid recipient e-mail: "a@@b".');
});

test("while the books load the screen says so, a failed load offers Try again, and an empty list says so", async ({ page, request }) => {
  let held = true;
  await page.route("**/api/v1/gl/periods*", async (route) => {
    if (held) {
      held = false;
      await route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: "internal error" }) });
      return;
    }
    await route.continue();
  });
  await openStatements(page, request).catch(() => undefined);
  await expect(page.locator("[data-error]")).toHaveText("Internal error.");
  await page.getByRole("button", { name: "Try again" }).click();
  await expect(page.locator("[data-statement]")).toBeVisible();
  // A statement read that fails offers Try again for that tab alone.
  let failed = true;
  await page.route("**/api/v1/finstmt/trial-balance*", async (route) => {
    if (failed) {
      failed = false;
      await route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: "internal error" }) });
      return;
    }
    await route.continue();
  });
  await tab(page, "trial").click();
  await expect(page.locator("[data-error]")).toHaveText("Internal error.");
  await page.getByRole("button", { name: "Try again" }).click();
  await expect(page.locator("[data-statement]")).toContainText("In balance");
  // An empty list of packs says so.
  await page.route("**/api/v1/finstmt/schedules*", (route) => (route.request().method() === "GET" ? route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ schedules: [] }) }) : route.continue()));
  await tab(page, "packs").click();
  await expect(page.locator("[data-empty]")).toContainText("No packs saved.");
});

test("the core's page of packs and the key it writes today are both read, and anything else is refused, never half read", async ({ page, request }) => {
  await openStatements(page, request);
  const row = { id: "5c4a0000-0000-4000-9000-0000000000b1", report: "TRIAL_BALANCE", recipients: "a@b.example", cadence: "DAILY", branch_id: "", is_active: true };
  let body: unknown = { items: [row], next_cursor: null, limit: 100 };
  await page.route("**/api/v1/finstmt/schedules*", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) }));
  await tab(page, "packs").click();
  await expect(page.locator("[data-pack]")).toHaveCount(1);
  body = { rows: [row] };
  await openStatements(page, request);
  await tab(page, "packs").click();
  await expect(page.locator("[data-error]")).toHaveText("The list of saved packs was not in a shape this screen can read.");
  await expect(page.locator("[data-pack]")).toHaveCount(0);
  // A statement whose figures are not integers is refused whole too.
  await page.route("**/api/v1/finstmt/income-statement*", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ year: 1, month: 0, branch_id: "", revenue: "none", expenses: [], revenue_cents: 1.5, expense_cents: 0, net_income_cents: 0 }) }));
  await tab(page, "income").click();
  await page.getByLabel("Branch", { exact: true }).selectOption({ label: TRENTON });
  await expect(page.locator("[data-error]")).toHaveText("The income statement was not in a shape this screen can read.");
});

test("on a phone the text tables become cards and no screen scrolls the page sideways", async ({ page, request }) => {
  await page.setViewportSize({ width: 390, height: 900 });
  await openStatements(page, request);
  const sideways = async (): Promise<boolean> => page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth + 1);
  for (const id of ["income", "balance", "trial", "gl", "aging", "packs"]) {
    await tab(page, id).click();
    await expect(page.locator("[data-statement], [data-packs]").first()).toBeVisible();
    expect(await sideways(), `${id} scrolls sideways`).toBe(false);
  }
  // The ledger detail is a stack of cards, each line labelled by its own field names.
  await tab(page, "gl").click();
  const first = page.getByRole("table", { name: "General ledger detail lines" }).locator("tbody tr").first();
  await expect(first.locator("td").first()).toHaveAttribute("data-label", "Entry");
  expect(await first.evaluate((el) => getComputedStyle(el).display)).toBe("block");
  expect(await page.getByRole("table", { name: "General ledger detail lines" }).locator("thead").evaluate((el) => getComputedStyle(el).display)).toBe("none");
  // The statement itself keeps its figures and puts the code under the name.
  await tab(page, "income").click();
  await expect(page.locator('[data-account="4000"] .fs-code-narrow')).toBeVisible();
  await expect(page.locator('[data-account="4000"] [data-figure]')).toBeVisible();
});

test("the tabs and the controls work from the keyboard, and focus is visible", async ({ page, request }) => {
  await openStatements(page, request);
  await tab(page, "income").focus();
  await page.keyboard.press("Tab");
  await expect(tab(page, "balance")).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(page.locator("[data-statement-title]")).toHaveText("Balance sheet");
  // The tab with focus draws a ring.
  await tab(page, "balance").focus();
  await page.keyboard.press("Shift+Tab");
  await page.keyboard.press("Tab");
  const ring = await tab(page, "balance").evaluate((el) => getComputedStyle(el).outlineStyle);
  expect(ring).not.toBe("none");
  // A choice of books is made with the keys alone.
  const year = page.getByLabel("Fiscal year");
  await year.focus();
  await page.keyboard.press("ArrowDown");
  await expect(page.locator("[data-statement-sub]")).toContainText("Fiscal year");
  const chosen = await year.inputValue();
  expect(chosen).toMatch(/^Fiscal year \d+$/);
  // The statement line's account number is a button, reachable and pressable by the keys.
  await tab(page, "income").click();
  const link = page.locator('[data-account="4000"] [data-action="fs-drill"]').first();
  await link.focus();
  await page.keyboard.press("Enter");
  await expect(page.locator("[data-statement-title]")).toHaveText("General ledger detail");
});
