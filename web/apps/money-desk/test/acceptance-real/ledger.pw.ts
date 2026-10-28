// THE LEDGER AGAINST THE REAL CORE (W2-4 (c), the brief BRIEF-M3-W2-4c-r1).
//
// WHAT THIS SPEC PROVES, the four things the brief names:
//   1. the trial balance ties: debits equal credits, and the screen's totals are the core's own
//      figures;
//   2. the month end 1100 balance is the world's known figure;
//   3. the periods screen shows the closed month, the open month and the year end marker's state;
//   4. a journal's detail: the panel shows what the core holds.
//
// WHAT IT PORTS, the stub specs that already prove this ground on the stub (test/ledger.pw.ts,
// test/books.pw.ts and the periods specs), and only what holds on the real core: the trial balance
// reads "In balance" and the server's totals (ledger.pw.ts), the trial balance takes a month
// (books.pw.ts), the periods table names each month and its status (books.pw.ts), and the journal
// register's detail panel shows the journal's lines (books.pw.ts). The stub specs' own actions
// (closing a period, closing the year, posting a journal) are writes, and this spec reads.
//
// THE HARNESS THIS SPEC WRITES AGAINST (W2-4-HARNESS-INTERFACE-r1, the part (a) brief lands).
// Every assumption is named here, so the fold catches a mismatch:
//   1. THE CONFIG: web/apps/money-desk/test/acceptance-real.config.ts, with testDir
//      "./acceptance-real" and testMatch "*.pw.ts". This file is one of those specs.
//   2. THE SESSION SIGNER: ./session exports asStaff(context, who) and coreURL(). asStaff puts
//      the session for the world's named staff user of that role on the browser context, so the
//      next page.goto lands signed in; coreURL() is the box's base URL, and this spec reads the
//      core's API directly through it, with the bearer the app itself sent.
//   3. THE WORLD: ./world exports the world's known figures. No figure is written in this file:
//      every month, every cent and every journal number comes from ./world.
//   4. THE ROLE: the ledger's screens are the Financial Controller's, so the spec signs in as
//      that role and reads what that role sees.
//   5. THE BOX: acceptance-real/box.sh up and box.sh down (the part (a) brief's job) bring the
//      core up and take it down. This spec never starts or stops a server, and it writes
//      nothing to the core: every call it makes is a read.
//   6. THE RED: this spec also runs under the stub (test/acceptance-real.stub.config.ts, the
//      small stub variant of the real config) and must fail there, because the stub's world is
//      not the core's. The legs that fail there are the ones that read ./world.
//
// ONE MORE THING THE FOLD SHOULD KNOW: the trial balance's month buttons are the months of the
// year the trial balance is showing, which is the core's own current year. The world's month end
// therefore has to fall in that year, and the spec says so in the open if it does not.
import { expect, test, type APIRequestContext, type BrowserContext, type Page } from "@playwright/test";
import { asStaff, coreURL } from "./session.js";
import {
  LEDGER_JOURNAL_ENTRY_NO,
  LEDGER_JOURNAL_MEMO,
  MONTH_END,
  MONTH_END_1100_CENTS,
  OPEN_MONTH,
  SIGNED_OFF_BANK_MONTH,
  YEAR_END_MARKED_YEAR,
} from "./world.js";

// ---- the core's own answers, the shapes the routes serve -----------------------------------------

interface CoreTrialBalance {
  fiscal_year: number;
  month?: number;
  rows: {
    account_code: string;
    account_name: string;
    account_type: string;
    normal_balance: string;
    debit_cents: number;
    credit_cents: number;
    balance_cents: number;
  }[];
  total_debit_cents: number;
  total_credit_cents: number;
  balance_cents: number;
}

interface CorePeriods {
  items: { id: string; year: number; month: number; status: string; closed_at?: string }[];
  next_openable_year: number;
}

interface CoreJournal {
  id: string;
  fiscal_year: number;
  entry_no: number;
  memo: string;
  status: string;
  derived_status: string;
  lines: {
    line_no: number;
    account_code: string;
    account_name?: string;
    debit_cents: number;
    credit_cents: number;
    memo?: string;
  }[];
}

interface CoreJournalPage {
  items: CoreJournal[];
  next_cursor?: string | null;
}

/** The bearer and the org the app itself sent, so a direct read reads the core as the app reads it. */
interface StaffHeaders {
  readonly authorization: string;
  readonly org: string;
}

// ---- the helpers ----------------------------------------------------------------------------------

const MONTH_NAMES = [
  "January",
  "February",
  "March",
  "April",
  "May",
  "June",
  "July",
  "August",
  "September",
  "October",
  "November",
  "December",
];

/** "2026-08" reads "August 2026", the words the desk names a period by. */
function monthName(ym: string): string {
  const [year, month] = ym.split("-");
  return `${MONTH_NAMES[Number(month) - 1] ?? month} ${year}`;
}

/** The month number of "2026-08". */
function monthNumber(ym: string): number {
  return Number(ym.split("-")[1]);
}

/** The year of "2026-08". */
function yearOf(ym: string): number {
  return Number(ym.split("-")[0]);
}

/** 1202500 reads "$12,025.00", the shape the desk shows (src/gl/gl-money.ts). */
function money(cents: number): string {
  const sign = cents < 0 ? "-" : "";
  const absolute = Math.abs(cents);
  const dollars = (absolute / 100).toLocaleString("en-CA", { minimumFractionDigits: 2, maximumFractionDigits: 2 });
  return `${sign}$${dollars}`;
}

/** "$12,025.00" reads 1202500; an empty cell reads 0. */
function readMoney(text: string): number {
  const trimmed = text.trim().replace(/^\$/, "").replace(/,/g, "");
  if (trimmed === "") return 0;
  return Math.round(Number(trimmed) * 100);
}

/** The bearer and the org the app sent on its own first authorised call to the core. */
function staffHeaders(page: Page): Promise<StaffHeaders> {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error("the app sent no authorised call to the core")), 60_000);
    page.on("request", (request) => {
      void request.allHeaders().then((headers) => {
        const authorization = headers["authorization"];
        if (authorization === undefined || authorization === "") return;
        clearTimeout(timer);
        resolve({ authorization, org: headers["x-org"] ?? "" });
      });
    });
  });
}

/** One read of the core, the way the app reads it: the bearer it sent, on the box's own base URL. */
async function coreRead<T>(request: APIRequestContext, headers: StaffHeaders, path: string): Promise<T> {
  const response = await request.get(`${coreURL()}${path}`, {
    headers: {
      authorization: headers.authorization,
      ...(headers.org === "" ? {} : { "x-org": headers.org }),
    },
  });
  if (!response.ok()) {
    throw new Error(`the core refused ${path} with ${response.status()}: ${await response.text()}`);
  }
  return (await response.json()) as T;
}

/** Signs in as the Financial Controller and lands on the desk. */
async function signIn(page: Page, context: BrowserContext): Promise<void> {
  await asStaff(context, "financial_controller");
  await page.goto("/");
}

// ---- the legs --------------------------------------------------------------------------------------

test("the trial balance ties: the screen's totals are the core's own figures, and debits equal credits", async ({
  page,
  request,
  context,
}) => {
  const headers = staffHeaders(page);
  await signIn(page, context);
  await page.locator("[data-tab-id='trial']").click();
  await page.locator(`[data-action="gl-trial-month"][data-month="${monthNumber(MONTH_END)}"]`).click();
  await expect(page.locator("[data-trial] h3")).toContainText(monthName(MONTH_END));

  const core = await coreRead<CoreTrialBalance>(
    request,
    await headers,
    `/api/v1/gl/trial-balance?year=${yearOf(MONTH_END)}&month=${monthNumber(MONTH_END)}`,
  );

  // The core's own books carry the world's journals: the trial balance has rows and real totals.
  // A blank answer would let this leg pass with an empty screen against an empty core, which
  // proves nothing about the ledger (K3's P1: the leg passed on a world with nothing posted).
  expect(core.rows.length, "the core's trial balance carries no rows").toBeGreaterThan(0);
  expect(core.total_debit_cents, "the core's trial balance carries no debit total").toBeGreaterThan(0);
  expect(core.total_credit_cents, "the core's trial balance carries no credit total").toBeGreaterThan(0);

  // The core's own books tie: debits equal credits.
  expect(core.total_debit_cents).toBe(core.total_credit_cents);
  expect(core.balance_cents).toBe(0);

  // The screen shows the core's own totals, and reads in balance.
  await expect(page.locator("[data-trial]")).toContainText("In balance");
  await expect(page.locator("[data-total-debit]")).toHaveText(money(core.total_debit_cents));
  await expect(page.locator("[data-total-credit]")).toHaveText(money(core.total_credit_cents));
});

test("the month end 1100 balance is the world's known figure", async ({ page, request, context }) => {
  const headers = staffHeaders(page);
  await signIn(page, context);
  await page.locator("[data-tab-id='trial']").click();
  await page.locator(`[data-action="gl-trial-month"][data-month="${monthNumber(MONTH_END)}"]`).click();
  await expect(page.locator("[data-trial] h3")).toContainText(monthName(MONTH_END));

  const core = await coreRead<CoreTrialBalance>(
    request,
    await headers,
    `/api/v1/gl/trial-balance?year=${yearOf(MONTH_END)}&month=${monthNumber(MONTH_END)}`,
  );
  const coreRow = core.rows.find((row) => row.account_code === "1100");
  const coreBalance = coreRow === undefined ? 0 : coreRow.balance_cents;

  // The screen's own 1100 row: its debit less its credit.
  const row = page.locator("[data-trial] tbody tr").filter({ has: page.locator("strong", { hasText: /^1100$/ }) });
  let screenBalance = 0;
  if ((await row.count()) > 0) {
    const cells = await row.locator("td").allInnerTexts();
    screenBalance = readMoney(cells[3] ?? "") - readMoney(cells[4] ?? "");
  }

  // The screen shows the core's figure.
  expect(screenBalance).toBe(coreBalance);
  // And the core's figure is the world's known figure.
  expect(coreBalance).toBe(MONTH_END_1100_CENTS);
});

test("the periods screen shows the closed month, the open month and the year end marker's state", async ({
  page,
  request,
  context,
}) => {
  const headers = staffHeaders(page);
  await signIn(page, context);
  await page.locator("[data-tab-id='periods']").click();
  const table = page.getByRole("table", { name: "Periods" });
  await expect(table).toBeVisible();

  // The signed off month reads Closed, and the open month reads Open.
  await expect(table.getByRole("row", { name: new RegExp(monthName(SIGNED_OFF_BANK_MONTH)) })).toContainText("Closed");
  await expect(table.getByRole("row", { name: new RegExp(monthName(OPEN_MONTH)) })).toContainText("Open");

  // The core's own periods read the same on the screen, month for month.
  const core = await coreRead<CorePeriods>(request, await headers, "/api/v1/gl/periods?limit=100");
  expect(core.items.length).toBeGreaterThan(0);
  for (const period of core.items) {
    const name = new RegExp(monthName(`${period.year}-${String(period.month).padStart(2, "0")}`));
    await expect(table.getByRole("row", { name })).toContainText(period.status === "OPEN" ? "Open" : "Closed");
  }

  // The year end marker's state.
  if (YEAR_END_MARKED_YEAR > 0) {
    // The year carries the marker: it cannot be closed again, and every month of it reads Closed.
    await expect(page.getByRole("button", { name: `Close fiscal year ${YEAR_END_MARKED_YEAR}` })).toHaveCount(0);
    for (const period of core.items.filter((p) => p.year === YEAR_END_MARKED_YEAR)) {
      const name = new RegExp(monthName(`${period.year}-${String(period.month).padStart(2, "0")}`));
      await expect(table.getByRole("row", { name })).toContainText("Closed");
    }
  } else {
    // No year carries the marker yet: the world's own year can still be closed.
    await expect(page.getByRole("button", { name: `Close fiscal year ${yearOf(OPEN_MONTH)}` })).toHaveCount(1);
  }
});

test("a journal's detail: the panel shows what the core holds", async ({ page, request, context }) => {
  const headers = staffHeaders(page);
  await signIn(page, context);
  await page.locator("[data-tab-id='journals']").click();
  const register = page.getByRole("table", { name: "Journal register" });
  const link = register.getByRole("button", { name: LEDGER_JOURNAL_ENTRY_NO });
  await expect(link).toHaveCount(1);
  await link.click();

  const panel = page.locator("[data-journal-panel]");
  await expect(panel.getByRole("heading", { level: 2 })).toContainText(LEDGER_JOURNAL_ENTRY_NO);
  await expect(panel.getByRole("heading", { level: 2 })).toContainText(LEDGER_JOURNAL_MEMO);

  // The core's own journal, read directly: the register first, then the journal it names.
  const page_ = await coreRead<CoreJournalPage>(request, await headers, "/api/v1/gl/journals?limit=100");
  const entryNo = Number(LEDGER_JOURNAL_ENTRY_NO.replace("JE-", ""));
  const summary = page_.items.find((journal) => journal.entry_no === entryNo);
  if (summary === undefined) throw new Error(`the core's register holds no ${LEDGER_JOURNAL_ENTRY_NO}`);
  const journal = await coreRead<CoreJournal>(request, await headers, `/api/v1/gl/journals/${summary.id}`);

  // The lines on screen are the core's lines, line for line.
  const rows = panel.getByRole("table", { name: "Journal lines" }).getByRole("row");
  await expect(rows).toHaveCount(journal.lines.length + 1);
  for (const line of journal.lines) {
    const row = rows.filter({ has: page.locator("strong", { hasText: new RegExp(`^${line.account_code}$`) }) });
    await expect(row).toHaveCount(1);
    const cells = await row.locator("td").allInnerTexts();
    expect(cells[0] ?? "").toContain(line.account_code);
    expect(cells[cells.length - 2] ?? "").toBe(line.debit_cents > 0 ? money(line.debit_cents) : "");
    expect(cells[cells.length - 1] ?? "").toBe(line.credit_cents > 0 ? money(line.credit_cents) : "");
  }

  // The journal ties: its debits equal its credits.
  const debit = journal.lines.reduce((sum, line) => sum + line.debit_cents, 0);
  const credit = journal.lines.reduce((sum, line) => sum + line.credit_cents, 0);
  expect(debit).toBe(credit);
});
