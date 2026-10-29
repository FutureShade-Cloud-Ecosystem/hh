// THE LEDGER AGAINST THE REAL CORE (W2-4 (c), the brief BRIEF-M3-W2-4c-ROUND3-r1).
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
//      every month, every cent and every journal number comes from ./world, and so does the AR
//      account's code (AR_CONTROL_ACCOUNT_CODE), which names the account the ledger reads on both sides:
//      the core's row and the screen's row.
//   4. THE ROLE: the ledger's screens are the Financial Controller's, so the spec signs in as
//      that role and reads what that role sees.
//   5. THE BOX: acceptance-real/box.sh up and box.sh down (the part (a) brief's job) bring the
//      core up and take it down. This spec never starts or stops a server, and it writes
//      nothing to the core: every call it makes is a read.
//   6. THE RED: this spec also runs under the stub (test/acceptance-real.stub.config.ts, the
//      small stub variant of the real config) and must fail there, because the stub's world is
//      not the core's.
//
// THE MONTHS ARE READ AT RUN TIME, NEVER WRITTEN DOWN (round 3, S4's P3-1). The seed's AR
// movement lands in the month the box was built in, and migration 0055 dates the seed's demo
// invoices CURRENT_DATE, so that month is not a fact anyone can write in a file. Each leg reads
// the box's own month through the core first (readBoxMonth below), the world names the anchor
// that replaces it when W2-1b's world lands, and no month literal is left in this file or in
// ./world. The box's month is read from the core's AR register: the seeded invoice's own issue
// date (the month the box booted, fixed when the migrations ran) and, for a box whose register
// does not carry that invoice, the core's own reported date.
//
// THE JOURNAL IS FOUND BY ITS LEDGER KEY, ON BOTH SIDES (round 3, S4's P3-2). The ledger's own
// key is the fiscal year and the entry number together, and the world names both. The core side
// pages the core's own journal list, following the page's cursor, until it finds that journal,
// and fails in words if it never does. The screen side finds the register row by the journal's
// own id, so two rows that share an entry number across years cannot confuse it. The desk's
// register reads one page of 100, newest first, so the world names a journal inside that page
// (FROM-PRINCIPAL 403 and 404; W2-10 adds a date range and a way to show more).
//
// TWO EDGES OF THE BOX'S OWN MONTH (round 4, S4's P2 and P3-2). The desk shows the core's current
// fiscal year, so a box built in one year and read in the next fails in words before any click
// (requireDeskYear). And the seeded invoice is dated by the database session's date while the
// posting dates its journal by the event's instant, so a box migrated within hours of a month
// end can put the two in different months: then legs 1 and 2 find an empty month and say so.
//
// THE NAVIGATION, and why it is "./". The real config's base URL is the box's /money-desk/ path,
// so "./" lands on the desk and "/" would escape to the front door at the origin root. The
// harness's own spec (harness.pw.ts) navigates the same way.
//
// A BLANK IS NOT A TIE. Leg 1 asks the core for the world's month end trial balance and requires
// it to carry rows and real totals. An empty core and an empty screen agree with each other, so
// a leg that accepts them passes on a world with nothing posted, which proves nothing about the
// ledger.
//
// THE WORDS ARE THE LABEL, NOT THE VERDICT (round 3, S4's P3-3). The desk reads "In balance" on
// a blank month, because a blank answer balances. So leg 1 compares the screen's own totals with
// the core's own totals FIRST, and reads the words after. The numbers are the fact; the words
// are what the desk calls them.
//
// ONE MORE THING THE FOLD SHOULD KNOW: the trial balance's month buttons are the months of the
// year the trial balance is showing, which is the core's own current year. The world's month end
// therefore has to fall in that year, and the spec says so in the open if it does not.
import { expect, test, type APIRequestContext, type BrowserContext, type Page } from "@playwright/test";
import { asStaff, coreURL } from "./session.js";
import {
  AR_CONTROL_ACCOUNT_CODE,
  BOOT_MONTH_INVOICE_NO,
  LEDGER_JOURNAL_ENTRY_NO,
  LEDGER_JOURNAL_FISCAL_YEAR,
  LEDGER_JOURNAL_MEMO,
  MONTH_END_1100_CENTS,
  MONTH_END_ANCHOR,
  OPEN_MONTH_ANCHOR,
  SIGNED_OFF_BANK_MONTH,
  YEAR_END_MARKED_YEAR,
  monthOfDate,
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

/** The AR register's read: the rows it lists and the date it reports. */
interface CoreARRegister {
  items: { invoice_no: string; issue_date: string }[];
  as_of: string;
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

/**
 * THE BOX'S OWN MONTH (YYYY-MM), read from the core at run time.
 *
 * The seed's AR movement lands in the month the box booted, and the seed names that month on
 * the demo invoice it plants dated CURRENT_DATE: that invoice's own issue date is fixed when
 * the migrations ran, so it names the boot month however long after that the spec runs. It is
 * the month the $700.00 movement is in, so a trial balance read for it carries the seed's
 * rows.
 *
 * The core's own reported date is the fallback, for a box whose register does not carry that
 * invoice: it is the box's wall clock, so it drifts with the run, and the movement's own month
 * does not.
 */
async function readBoxMonth(request: APIRequestContext, headers: StaffHeaders): Promise<string> {
  const register = await coreRead<CoreARRegister>(
    request,
    headers,
    `/api/v1/ar/register?q=${encodeURIComponent(BOOT_MONTH_INVOICE_NO)}&limit=100`,
  );
  const seeded = register.items.find((row) => row.invoice_no === BOOT_MONTH_INVOICE_NO);
  if (seeded !== undefined) return monthOfDate(seeded.issue_date);
  return monthOfDate(register.as_of);
}

/**
 * The desk's trial balance and periods show the core's current fiscal year, and their month
 * buttons and rows are that year's (src/gl/gl-desk.ts asks the core with no year and keeps the
 * answer's fiscal_year). A month from another year is not on the screen at all, so the leg says
 * so in words before it clicks anything: a box built in one year and read in the next would
 * otherwise go red on a missing month for nobody's commit (S4's P2 on round 3, FROM-PRINCIPAL 389).
 */
async function requireDeskYear(request: APIRequestContext, headers: StaffHeaders, month: string): Promise<void> {
  const shown = await coreRead<{ fiscal_year: number }>(request, headers, "/api/v1/gl/trial-balance");
  if (yearOf(month) !== shown.fiscal_year) {
    throw new Error(
      `the world's month ${month} is in ${yearOf(month)} and the desk shows ${shown.fiscal_year}: ` +
        "rebuild the box this year, or set MONTH_END_ANCHOR and OPEN_MONTH_ANCHOR to the world's own months",
    );
  }
}

/**
 * How far the core's journal list is paged, in pages of 100. The desk's own register reads one
 * page of 100 today (src/gl/gl-api.ts, newest first), so on a box holding more than 100 journals
 * the screen side cannot reach an older one and the leg goes red at the register's row: W2-10
 * gives the register a date range and a way to show more (FROM-PRINCIPAL 404).
 */
const MAX_JOURNAL_PAGES = 50;

/**
 * The core's own journal, by the ledger's key: the fiscal year and the entry number together.
 *
 * The core's list serves one page at a time, newest first, and its page carries the cursor for
 * the next one, so the read follows the cursor until it finds the journal. A box with more
 * journals than one page holds keeps the world's journal on a later page, and a box that does
 * not hold it at all fails here, in words.
 */
async function readCoreJournal(
  request: APIRequestContext,
  headers: StaffHeaders,
  entryNo: string,
  fiscalYear: number,
): Promise<CoreJournal> {
  const wanted = Number(entryNo.replace("JE-", ""));
  let cursor: string | undefined;
  for (let page = 0; page < MAX_JOURNAL_PAGES; page += 1) {
    const answer = await coreRead<CoreJournalPage>(
      request,
      headers,
      `/api/v1/gl/journals?limit=100${cursor === undefined ? "" : `&cursor=${encodeURIComponent(cursor)}`}`,
    );
    const found = answer.items.find((journal) => journal.entry_no === wanted && journal.fiscal_year === fiscalYear);
    if (found !== undefined) {
      return coreRead<CoreJournal>(request, headers, `/api/v1/gl/journals/${found.id}`);
    }
    const next = answer.next_cursor;
    if (next === undefined || next === null || next === "") break;
    cursor = next;
  }
  throw new Error(`the core's register holds no ${entryNo} of fiscal year ${fiscalYear}`);
}

/** Signs in as the Financial Controller and lands on the desk. */
async function signIn(page: Page, context: BrowserContext): Promise<void> {
  await asStaff(context, "financial_controller");
  // "./" stays under this config's base URL: the real box serves the desk under /money-desk/,
  // and "/" would escape to the front door at the origin root (the harness's own spec
  // navigates the same way, harness.pw.ts).
  await page.goto("./");
}

// ---- the legs --------------------------------------------------------------------------------------

test("the trial balance ties: the screen's totals are the core's own figures, and debits equal credits", async ({
  page,
  request,
  context,
}) => {
  const headers = staffHeaders(page);
  await signIn(page, context);
  const boxMonth = await readBoxMonth(request, await headers);
  const monthEnd = MONTH_END_ANCHOR === "" ? boxMonth : MONTH_END_ANCHOR;
  await requireDeskYear(request, await headers, monthEnd);
  await page.locator("[data-tab-id='trial']").click();
  await page.locator(`[data-action="gl-trial-month"][data-month="${monthNumber(monthEnd)}"]`).click();
  await expect(page.locator("[data-trial] h3")).toContainText(monthName(monthEnd));

  const core = await coreRead<CoreTrialBalance>(
    request,
    await headers,
    `/api/v1/gl/trial-balance?year=${yearOf(monthEnd)}&month=${monthNumber(monthEnd)}`,
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

  // The screen shows the core's own totals. This comes BEFORE the words below, because the
  // desk reads "In balance" on a blank month (a blank answer balances), so the words can
  // never carry this leg: the numbers are the fact and the words are the label.
  await expect(page.locator("[data-total-debit]")).toHaveText(money(core.total_debit_cents));
  await expect(page.locator("[data-total-credit]")).toHaveText(money(core.total_credit_cents));
  await expect(page.locator("[data-trial]")).toContainText("In balance");
});

test("the month end 1100 balance is the world's known figure", async ({ page, request, context }) => {
  const headers = staffHeaders(page);
  await signIn(page, context);
  const boxMonth = await readBoxMonth(request, await headers);
  const monthEnd = MONTH_END_ANCHOR === "" ? boxMonth : MONTH_END_ANCHOR;
  await requireDeskYear(request, await headers, monthEnd);
  await page.locator("[data-tab-id='trial']").click();
  await page.locator(`[data-action="gl-trial-month"][data-month="${monthNumber(monthEnd)}"]`).click();
  await expect(page.locator("[data-trial] h3")).toContainText(monthName(monthEnd));

  const core = await coreRead<CoreTrialBalance>(
    request,
    await headers,
    `/api/v1/gl/trial-balance?year=${yearOf(monthEnd)}&month=${monthNumber(monthEnd)}`,
  );
  const coreRow = core.rows.find((row) => row.account_code === AR_CONTROL_ACCOUNT_CODE);
  const coreBalance = coreRow === undefined ? 0 : coreRow.balance_cents;

  // The screen's own AR row: its debit less its credit.
  const row = page
    .locator("[data-trial] tbody tr")
    .filter({ has: page.locator("strong", { hasText: new RegExp(`^${AR_CONTROL_ACCOUNT_CODE}$`) }) });
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
  const boxMonth = await readBoxMonth(request, await headers);
  const openMonth = OPEN_MONTH_ANCHOR === "" ? boxMonth : OPEN_MONTH_ANCHOR;
  await requireDeskYear(request, await headers, openMonth);
  await page.locator("[data-tab-id='periods']").click();
  const table = page.getByRole("table", { name: "Periods" });
  await expect(table).toBeVisible();

  // The core's own periods, read straight from the core.
  const core = await coreRead<CorePeriods>(request, await headers, "/api/v1/gl/periods?limit=100");
  expect(core.items.length).toBeGreaterThan(0);

  // The signed off month reads Closed, when the world has signed one off. Today's finance seed
  // signs off no month at all (every period of the box's year is OPEN), so the world names none
  // and the truth on the screen is that no row reads Closed. The world's own signed off month
  // switches this branch: the W2-1b world names the month before its anchor.
  if (SIGNED_OFF_BANK_MONTH === "") {
    for (const period of core.items) expect(period.status).toBe("OPEN");
    await expect(table.getByRole("row").filter({ hasText: "Closed" })).toHaveCount(0);
  } else {
    await expect(table.getByRole("row", { name: new RegExp(monthName(SIGNED_OFF_BANK_MONTH)) })).toContainText("Closed");
  }

  // The open month reads Open.
  await expect(table.getByRole("row", { name: new RegExp(monthName(openMonth)) })).toContainText("Open");

  // The core's own periods read the same on the screen, month for month.
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
    await expect(page.getByRole("button", { name: `Close fiscal year ${yearOf(openMonth)}` })).toHaveCount(1);
  }
});

test("a journal's detail: the panel shows what the core holds", async ({ page, request, context }) => {
  const headers = staffHeaders(page);
  await signIn(page, context);
  await page.locator("[data-tab-id='journals']").click();
  const register = page.getByRole("table", { name: "Journal register" });

  // The core's own journal, by the ledger's key, read from the core's own paged list.
  const journal = await readCoreJournal(request, await headers, LEDGER_JOURNAL_ENTRY_NO, LEDGER_JOURNAL_FISCAL_YEAR);

  // The register reaches the same journal: its rows carry the journal's own id, so the world's
  // journal is found among any number of rows that share its entry number, and the row shows
  // the world's entry number.
  const link = register.locator(`[data-journal-id="${journal.id}"]`);
  await expect(link).toHaveCount(1);
  await expect(link).toHaveText(LEDGER_JOURNAL_ENTRY_NO);
  await link.click();

  const panel = page.locator("[data-journal-panel]");
  await expect(panel.getByRole("heading", { level: 2 })).toContainText(LEDGER_JOURNAL_ENTRY_NO);
  await expect(panel.getByRole("heading", { level: 2 })).toContainText(LEDGER_JOURNAL_MEMO);

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
