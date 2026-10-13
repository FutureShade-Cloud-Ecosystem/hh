// The statements slice's plain logic: how the books' own periods are named, how the core's answers are
// read (and refused whole when they are not the shape the contract sends), what each closed word reads
// as, the CSV a statement exports and how a refusal reads out loud. Nothing here computes money: every
// figure is the server's, either shown formatted or carried into the CSV as its decimal digits. Nothing
// here is rendered.
import type {
  FinStmtAgingSummary,
  FinStmtBalanceSheetReport,
  FinStmtComparativeRow,
  FinStmtGLDetailResponse,
  FinStmtGLDetailRow,
  FinStmtIncomeStatementReport,
  FinStmtSchedule,
  FinStmtScheduleInput,
  FinStmtTrialBalanceReport,
  GlPeriod,
} from "@hardscapehouse/api-client";
import { centsDecimal } from "./finstmt-money.js";

export type StmtTab = "income" | "balance" | "trial" | "gl" | "aging" | "packs";

/** The tabs in the order the bar shows them. The words are the ones a statement carries as its title. */
export const TABS: ReadonlyArray<readonly [StmtTab, string]> = [
  ["income", "Income statement"],
  ["balance", "Balance sheet"],
  ["trial", "Trial balance"],
  ["gl", "General ledger detail"],
  ["aging", "Receivables aging"],
  ["packs", "Saved packs"],
];

export function tabTitle(tab: StmtTab): string {
  return TABS.find(([id]) => id === tab)?.[1] ?? "";
}

// ---- periods and scope ----------------------------------------------------------------------------

/** The books number their periods inside a fiscal year; no calendar word is written here. */
export function periodLabel(year: number, month: number): string {
  if (month === 0) return `Fiscal year ${year}`;
  return `Period ${month} of fiscal year ${year}`;
}

/** The fiscal years the books hold periods for, newest first. */
export function periodYears(periods: readonly GlPeriod[]): number[] {
  const years = new Set<number>();
  for (const p of periods) years.add(p.year);
  return [...years].sort((a, b) => b - a);
}

/** The periods of one fiscal year, in order. */
export function periodsOf(periods: readonly GlPeriod[], year: number): GlPeriod[] {
  return periods.filter((p) => p.year === year).sort((a, b) => a.month - b.month);
}

/** The status word of one period, or "" when the books hold no such period. */
export function periodStatusWord(periods: readonly GlPeriod[], year: number, month: number): string {
  if (month === 0) return "";
  const found = periods.find((p) => p.year === year && p.month === month);
  if (found === undefined) return "";
  return found.status === "CLOSED" ? "Closed" : "Open";
}

export function scopeLabel(branchId: string, branches: ReadonlyArray<{ readonly id: string; readonly name: string }>): string {
  if (branchId === "") return "All branches";
  return branches.find((b) => b.id === branchId)?.name ?? "A branch not in the list";
}

// ---- words ----------------------------------------------------------------------------------------

/** An unknown value is opened out, never hidden: "PO_RECEIPT" reads "Po receipt". */
export function openOut(value: string): string {
  const words = value.toLowerCase().replace(/_/g, " ").trim();
  return words === "" ? "" : words.charAt(0).toUpperCase() + words.slice(1);
}

export function accountTypeWord(type: string): string {
  return openOut(type);
}

const BUCKET_WORD: Record<string, string> = {
  CURRENT: "Current",
  "1_30": "1 to 30 days past due",
  "31_60": "31 to 60 days past due",
  "61_90": "61 to 90 days past due",
  "90_PLUS": "Over 90 days past due",
};

export function bucketWord(bucket: string): string {
  return BUCKET_WORD[bucket] ?? openOut(bucket);
}

const SOURCE_WORD: Record<string, string> = {
  MANUAL: "Manual",
  INVOICE: "Invoice",
  PAYMENT: "Payment",
  FULFILLMENT: "Fulfillment",
  PO_RECEIPT: "Purchase receipt",
  ADJUSTMENT: "Adjustment",
  CLOSING: "Year end closing",
};

export function sourceWord(source: string): string {
  return SOURCE_WORD[source] ?? openOut(source);
}

const REPORT_WORD: Record<string, string> = {
  INCOME_STATEMENT: "Income statement",
  BALANCE_SHEET: "Balance sheet",
  TRIAL_BALANCE: "Trial balance",
  GL_DETAIL: "General ledger detail",
  AR_AGING: "Receivables aging",
};

export const REPORTS: readonly string[] = Object.keys(REPORT_WORD);
export const CADENCES: readonly string[] = ["DAILY", "WEEKLY", "MONTHLY"];

export function reportWord(report: string): string {
  return REPORT_WORD[report] ?? openOut(report);
}

export function cadenceWord(cadence: string): string {
  return openOut(cadence);
}

export function entryLabel(entryNo: number): string {
  return `JE-${String(entryNo).padStart(4, "0")}`;
}

// ---- reading the core's answers -------------------------------------------------------------------

type Obj = Record<string, unknown>;

function isObj(v: unknown): v is Obj {
  return v !== null && typeof v === "object" && !Array.isArray(v);
}
const isInt = (v: unknown): v is number => typeof v === "number" && Number.isInteger(v);
const isStr = (v: unknown): v is string => typeof v === "string";

function hasStrings(o: Obj, keys: readonly string[]): boolean {
  return keys.every((k) => isStr(o[k]));
}
function hasInts(o: Obj, keys: readonly string[]): boolean {
  return keys.every((k) => isInt(o[k]));
}
function listOf(v: unknown, row: (r: Obj) => boolean): boolean {
  return Array.isArray(v) && v.every((r) => isObj(r) && row(r));
}

const statementRow = (r: Obj): boolean => hasStrings(r, ["account_code", "account_name", "account_type"]) && isInt(r["amount_cents"]);

export function readIncome(v: unknown): FinStmtIncomeStatementReport | null {
  if (!isObj(v) || !hasInts(v, ["year", "month", "revenue_cents", "expense_cents", "net_income_cents"]) || !isStr(v["branch_id"])) return null;
  if (!listOf(v["revenue"], statementRow) || !listOf(v["expenses"], statementRow)) return null;
  return v as unknown as FinStmtIncomeStatementReport;
}

export function readBalance(v: unknown): FinStmtBalanceSheetReport | null {
  if (!isObj(v) || !hasInts(v, ["year", "total_assets_cents", "total_liabilities_cents", "total_equity_cents", "net_income_cents"])) return null;
  if (!isStr(v["branch_id"]) || typeof v["balanced"] !== "boolean") return null;
  if (!listOf(v["assets"], statementRow) || !listOf(v["liabilities"], statementRow) || !listOf(v["equity"], statementRow)) return null;
  return v as unknown as FinStmtBalanceSheetReport;
}

export function readTrial(v: unknown): FinStmtTrialBalanceReport | null {
  if (!isObj(v) || !hasInts(v, ["year", "total_debit_cents", "total_credit_cents"]) || typeof v["in_balance"] !== "boolean") return null;
  const row = (r: Obj): boolean => hasStrings(r, ["account_code", "account_name", "account_type", "normal_balance"]) && hasInts(r, ["total_debit_cents", "total_credit_cents", "balance_cents"]);
  if (!listOf(v["rows"], row)) return null;
  return v as unknown as FinStmtTrialBalanceReport;
}

export function readComparative(v: unknown): { rows: FinStmtComparativeRow[] } | null {
  if (!isObj(v)) return null;
  const row = (r: Obj): boolean => hasStrings(r, ["account_code", "account_name", "account_type"]) && hasInts(r, ["current_cents", "prior_cents", "delta_cents"]);
  if (!listOf(v["rows"], row)) return null;
  return v as unknown as { rows: FinStmtComparativeRow[] };
}

export function readGlDetail(v: unknown): FinStmtGLDetailResponse | null {
  if (!isObj(v) || !isInt(v["tie_cents"])) return null;
  const row = (r: Obj): boolean =>
    hasStrings(r, ["line_id", "journal_id", "source", "account_code", "account_name", "account_type", "branch_id", "memo", "posted_at"]) &&
    hasInts(r, ["entry_no", "period_year", "period_month", "debit_cents", "credit_cents"]);
  if (!listOf(v["rows"], row)) return null;
  return v as unknown as FinStmtGLDetailResponse;
}

export function readAging(v: unknown): FinStmtAgingSummary | null {
  if (!isObj(v) || !isInt(v["grand_total_cents"]) || !isStr(v["branch_id"])) return null;
  const bucket = (r: Obj): boolean => isStr(r["bucket"]) && isInt(r["cents"]);
  const row = (r: Obj): boolean => hasStrings(r, ["customer_id", "customer_name", "source_type", "source_ref_id", "branch_id", "bucket"]) && hasInts(r, ["amount_cents", "age_days"]);
  if (!listOf(v["buckets"], bucket) || !listOf(v["rows"], row)) return null;
  return v as unknown as FinStmtAgingSummary;
}

const scheduleRow = (r: Obj): boolean => hasStrings(r, ["id", "report", "recipients", "cadence", "branch_id"]) && typeof r["is_active"] === "boolean";

/**
 * The saved packs. The contract's page names them `items`; the core writes them under `schedules` today
 * (recorded in its shape census). Both are read, anything else is refused, never half read.
 */
export function readSchedules(v: unknown): FinStmtSchedule[] | null {
  if (!isObj(v)) return null;
  const list = Array.isArray(v["items"]) ? v["items"] : v["schedules"];
  if (!listOf(list, scheduleRow)) return null;
  return list as FinStmtSchedule[];
}

export function readPeriods(v: unknown): GlPeriod[] | null {
  if (!isObj(v)) return null;
  if (!listOf(v["items"], (r) => isStr(r["id"]) && hasInts(r, ["year", "month"]) && isStr(r["status"]))) return null;
  return v["items"] as GlPeriod[];
}

// ---- the saved pack form --------------------------------------------------------------------------

export interface PackForm {
  readonly report: string;
  readonly recipients: string;
  readonly cadence: string;
  readonly branchId: string;
  readonly active: boolean;
}

export type PackCheck = { readonly ok: true; readonly input: FinStmtScheduleInput } | { readonly ok: false; readonly message: string };

/** The same three checks the core makes, in its order, so a bad form never leaves the browser. */
export function validatePack(form: PackForm): PackCheck {
  if (!REPORTS.includes(form.report)) return { ok: false, message: "Choose the statement the pack carries." };
  if (!CADENCES.includes(form.cadence)) return { ok: false, message: "Choose how often the pack goes." };
  const recipients = form.recipients.split(",").map((r) => r.trim()).filter((r) => r !== "");
  if (recipients.length === 0) return { ok: false, message: "Give at least one recipient e-mail." };
  for (const address of recipients) {
    if (!/^[^\s@]+@[^\s@]+$/.test(address)) return { ok: false, message: `That is not an e-mail: ${address}.` };
  }
  return {
    ok: true,
    input: {
      report: form.report as FinStmtScheduleInput["report"],
      recipients: recipients.join(","),
      cadence: form.cadence as FinStmtScheduleInput["cadence"],
      branch_id: form.branchId,
      is_active: form.active,
    },
  };
}

// ---- the CSV --------------------------------------------------------------------------------------

function csvCell(cell: string): string {
  // A cell that a spreadsheet would run as a formula is made plain text. Only the one shape the core writes for money
  // (an optional minus, digits, a point, two digits) is left alone; typed text never is, even when it starts with a digit after a minus.
  const defanged = /^[=+\-@\t\r]/.test(cell) && !/^-?\d+\.\d{2}$/.test(cell) ? `'${cell}` : cell;
  return /[",\r\n]/.test(defanged) ? `"${defanged.replace(/"/g, '""')}"` : defanged;
}

export function csvDocument(rows: ReadonlyArray<readonly string[]>): string {
  return rows.map((r) => r.map(csvCell).join(",") + "\r\n").join("");
}

type StatementRows = Array<string[]>;

export function comparativeRows(rows: readonly FinStmtComparativeRow[]): StatementRows {
  const out: StatementRows = [["Account", "Name", "This year", "Last year", "Change"]];
  for (const r of rows) out.push([r.account_code, r.account_name, centsDecimal(r.current_cents), centsDecimal(r.prior_cents), centsDecimal(r.delta_cents)]);
  return out;
}

/** Reads a CSV text into rows: quoted cells, doubled quotes and line breaks inside a cell are kept. */
export function parseCsv(text: string): string[][] {
  const rows: string[][] = [];
  let row: string[] = [];
  let cell = "";
  let quoted = false;
  let started = false;
  for (let i = 0; i < text.length; i += 1) {
    const c = text.charAt(i);
    if (quoted) {
      if (c === '"' && text.charAt(i + 1) === '"') {
        cell += '"';
        i += 1;
      } else if (c === '"') quoted = false;
      else cell += c;
    } else if (c === '"') {
      quoted = true;
      started = true;
    } else if (c === ",") {
      row.push(cell);
      cell = "";
      started = true;
    } else if (c === "\n" || c === "\r") {
      if (c === "\r" && text.charAt(i + 1) === "\n") i += 1;
      if (started || cell !== "") {
        row.push(cell);
        rows.push(row);
      }
      row = [];
      cell = "";
      started = false;
    } else {
      cell += c;
      started = true;
    }
  }
  if (started || cell !== "") {
    row.push(cell);
    rows.push(row);
  }
  return rows;
}

/** The core's CSV, read and written again so no cell can run as a formula when the file is opened. */
export function defangCsv(text: string): string {
  return csvDocument(parseCsv(text));
}

const FILE_BASE: Record<string, string> = {
  income: "income-statement",
  balance: "balance-sheet",
  trial: "trial-balance",
  gl: "general-ledger-detail",
  aging: "receivables-aging",
};

/** The file is named for the statement and the period it covers; an aging is as of now and names no period. */
export function csvFilename(tab: StmtTab, year: number, month: number): string {
  const base = FILE_BASE[tab] ?? "statement";
  if (tab === "aging") return `${base}.csv`;
  const monthPart = (tab === "income" || tab === "gl") && month > 0 ? `-period-${month}` : "";
  return `${base}-fiscal-year-${year}${monthPart}.csv`;
}

// ---- refusals -------------------------------------------------------------------------------------

function sentence(text: string): string {
  const trimmed = text.trim();
  if (trimmed === "") return "";
  const cap = trimmed.charAt(0).toUpperCase() + trimmed.slice(1);
  return /[.!?]$/.test(cap) ? cap : `${cap}.`;
}

/**
 * A refusal in words. The role gate answers a walled route with its shared "Forbidden" body, which tells
 * a person nothing, so the app names who may do it. The branch fence and the core's checks keep the
 * core's own sentence, put into a sentence.
 */
export function refusalWords(action: "read" | "pack", message: string, status: number): string {
  if (status === 401) return "Your sign in has lapsed. Sign in again.";
  if (status === 403) {
    if (message.includes("cannot access this branch")) return "You cannot read the books of that branch. Choose a branch you hold.";
    return action === "pack" ? "Saving a pack is for the Financial Controller." : "Statements are for the Financial Controller or a manager.";
  }
  if (status === 404) return "That pack is gone. Reload to see the list.";
  const words = sentence(message);
  return words === "" ? "The books did not answer. Try again." : words;
}
