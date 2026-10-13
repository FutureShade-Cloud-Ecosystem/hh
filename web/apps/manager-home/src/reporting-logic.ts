// Manager Home's reporting decisions, with no DOM and no network in sight:
// the cell formatters the column kinds drive, the parameter form's defaults
// and its one guard, the catalogue order, the result summary line, the
// reachability rules that mirror the core's guard tiers, and the words a
// walled refusal becomes.
//
// Port provenance: the reporting console's pure half (repository
// hardscapeos_dibbits, read only; see PROVENANCE.md), narrowed to HH's rule
// that the server decides what a report contains and whether a caller may
// run it. A client-side rule that disagrees with the server is worse than
// none: it produces a screen that refuses something the server would have
// allowed. The one guard kept client side is the original's own: a required
// field left empty, a submit that cannot possibly succeed.
//
// MONEY AND COMMISSION FIGURES ARE SERVER FIGURES ONLY. Cents arrive
// computed; this file formats them for a person and sums nothing, converts
// nothing and derives nothing. The original's client side period totals and
// its derived margin percent are deliberately not ported.
import { formatDollars } from "@hardscapehouse/pos-kit/src/ported/_money-format.js";
import { errorWords } from "./manager-logic.js";

// -- the panes ----------------------------------------------------------------------

/** The four panes the reporting view owns, in the order a person looks for them. */
export type ReportingPane = "templates" | "runs" | "rules" | "accruals";

export const REPORTING_PANES: readonly { readonly pane: ReportingPane; readonly word: string; readonly note: string }[] =
  Object.freeze([
    { pane: "templates", word: "Templates", note: "The report catalogue. Pick a report, set its dates, run it, print it, take the CSV." },
    { pane: "runs", word: "Runs", note: "The reports and statements already frozen, each with the document that was printed." },
    { pane: "rules", word: "Commission rules", note: "The rate cards commission is computed from. Reading is for managers; writing is for the dealer's leadership." },
    { pane: "accruals", word: "Accruals", note: "The commission register: preview a period, freeze a rep's accrual, read the register." },
  ]);

export function paneWord(pane: ReportingPane): string {
  return REPORTING_PANES.find((p) => p.pane === pane)?.word ?? pane;
}

// -- reachability, mirroring the core's guards ---------------------------------------
//
// The core decides. These two rules only decide whether the landing draws the
// door and whether the rules pane draws its write controls, from the same
// tier facts the mint's resolver stamped; a caller whose tier says no and
// reaches the view anyway sees the core's own refusal, never a client veto.

/** The reporting view opens for the guard's manager window: tier one and two (IsManager). */
export function canOpenReporting(tier: number): boolean {
  return Number.isInteger(tier) && tier >= 1 && tier <= 2;
}

/** Commission rule writes ride the guard's admin window: tier one global (IsAdmin). */
export function canAdminCommissionRules(tier: number, scope: string): boolean {
  return tier === 1 && scope === "GLOBAL";
}

// -- formatting ----------------------------------------------------------------------

/** Money renders from the core's integer cents through the shared formatter. */
export function money(cents: number): string {
  return formatDollars(cents);
}

/** A quantity to at most 4 decimals with trailing zeros trimmed ("12", not "12.0000"). */
export function qty(v: number): string {
  const n = Number.isFinite(v) ? v : 0;
  return String(Number(n.toFixed(4)));
}

/** A basis point rate as a percentage ("500" becomes "5.00%"). */
export function bps(v: number): string {
  const n = Number.isFinite(v) ? v : 0;
  return `${(n / 100).toFixed(2)}%`;
}

/**
 * A typed commission percent to integer basis points, through the decimal
 * string (never a float multiply): digits, an optional point, at most two
 * decimals, between 0 and 100. Anything else is refused in words and nothing is
 * sent ("2.5x", "1,5", "2.505" and "2e1" never stand a rule at another rate).
 */
export function parseRateToBps(text: string): { bps: number } | { error: string } {
  const refusal = { error: "The rate reads as a percent between 0 and 100, with at most two decimals." };
  const match = text.trim().match(/^([0-9]{1,3})(?:\.([0-9]{1,2}))?$/);
  if (match === null) return refusal;
  const value = Number(`${match[1]}${(match[2] ?? "").padEnd(2, "0")}`);
  return value > 10000 ? refusal : { bps: value };
}

/** A column kind that right aligns: comparing magnitudes needs aligned digits. */
export function isNumericKind(kind: string): boolean {
  return kind === "cents" || kind === "qty" || kind === "int";
}

/**
 * The one cell formatter, driven by the column kind the server declared. Every
 * table cell goes through it, so a money column cannot render as a raw integer
 * in one report and as dollars in another. An absent value renders empty.
 */
export function formatCell(kind: string, value: unknown): string {
  if (value === null || value === undefined) return "";
  switch (kind) {
    case "cents":
      return money(Number(value));
    case "qty":
      return qty(Number(value));
    case "int": {
      const n = Number(value);
      return Number.isFinite(n) ? grouped(n) : String(value);
    }
    case "bool":
      return value ? "Yes" : "No";
    default:
      return String(value);
  }
}

/** Grouped integer: 1234 becomes "1,234". */
function grouped(value: number): string {
  const sign = value < 0 ? "-" : "";
  const digits = String(Math.trunc(Math.abs(value)));
  const out: string[] = [];
  for (let i = digits.length; i > 0; i -= 3) out.unshift(digits.slice(Math.max(0, i - 3), i));
  return sign + out.join(",");
}

/** A totals-row cell, or empty where a column does not total. */
export function totalCell(col: { name: string; kind: string; total: boolean }, totals: Readonly<Record<string, unknown>>): string {
  if (!col.total) return "";
  const v = totals?.[col.name];
  if (v === undefined || v === null) return "";
  return formatCell(col.kind, v);
}

// -- the parameter form ----------------------------------------------------------------

/**
 * A template's form seeded with its date parameters at the current month to
 * date: the window someone opening a report almost always wants, and a form
 * that is never submitted empty by a person who did not realise dates were
 * required. `today` is injected so a test does not go flaky at a month
 * boundary.
 */
export function defaultParams(params: readonly { name: string; kind: string }[], today: Date): Record<string, string> {
  const out: Record<string, string> = {};
  const iso = (d: Date) => d.toISOString().slice(0, 10);
  const monthStart = new Date(Date.UTC(today.getUTCFullYear(), today.getUTCMonth(), 1));
  for (const p of params) {
    if (p.kind !== "date") continue;
    out[p.name] = p.name === "to_date" ? iso(today) : iso(monthStart);
  }
  return out;
}

/**
 * The required parameters a form has not filled, by the labels it shows. This
 * is the ONLY client-side validation: it stops a submit the server would
 * certainly refuse, and nothing else. Format checking is deliberately absent.
 */
export function missingRequired(params: readonly { name: string; required: boolean; label: string }[], values: Record<string, string>): string[] {
  return params.filter((p) => p.required && !(values[p.name] ?? "").trim()).map((p) => p.label || p.name);
}

/**
 * The parameter map a run, an export or a print sends. Empty values are
 * DROPPED rather than sent as empty strings: the server refuses unknown
 * parameters and treats an absent optional as "no filter", so sending
 * `branch_id=` would be a value, not an absence.
 */
export function runParams(values: Record<string, string>): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(values)) {
    const trimmed = (v ?? "").trim();
    if (trimmed) out[k] = trimmed;
  }
  return out;
}

/**
 * Whether a print needs the "print at" pick: the server refuses a
 * consolidated print with no branch (the print queue's branch is required), so
 * when the effective parameters name no branch the form offers its own pick.
 */
export function printAtNeeded(values: Record<string, string>): boolean {
  return (values.branch_id ?? "").trim() === "";
}

/** The export's download name, built from the report code and its dates like the core builds its own. */
export function csvName(code: string, params: Record<string, string>): string {
  const from = (params.from_date ?? "").trim();
  const to = (params.to_date ?? "").trim();
  let name = code;
  if (from !== "") name += `_${from}`;
  if (to !== "") name += `_${to}`;
  return `${name}.csv`;
}

// -- the catalogue ---------------------------------------------------------------------

/**
 * The categories in the order a person looks for them rather than
 * alphabetically: the money you made, then what it cost, then who owes you,
 * then the physical business, then the back office. Anything the server adds
 * later sorts to the end, in the order the server sent it, so a new category
 * appears rather than disappearing.
 */
export const CATEGORY_ORDER: readonly string[] = Object.freeze([
  "SALES",
  "MARGIN",
  "AR",
  "PAYMENTS",
  "INVENTORY",
  "PURCHASING",
  "DELIVERY",
  "WARRANTY",
  "CREDIT",
  "TAX",
  "COMMISSION",
]);

export function orderCategories<T extends { code: string }>(cats: readonly T[]): T[] {
  const rank = (c: T) => {
    const i = CATEGORY_ORDER.indexOf(c.code);
    return i === -1 ? CATEGORY_ORDER.length : i;
  };
  return [...cats].sort((a, b) => rank(a) - rank(b));
}

/** Locate a template across the grouped catalogue, or null. */
export function findTemplate<T extends { code: string }>(cats: readonly { templates: readonly T[] }[], code: string): T | null {
  for (const c of cats) {
    for (const t of c.templates) if (t.code === code) return t;
  }
  return null;
}

// -- the result's one line ----------------------------------------------------------------

/**
 * The line under a result grid: the row count and the leading money total,
 * because "83 rows" alone tells a reader nothing about whether they ran the
 * right thing. Both figures are the server's.
 */
export function resultSummary(res: { rowCount: number; columns: readonly { name: string; kind: string; total: boolean; label: string }[]; totals: Readonly<Record<string, unknown>> } | null): string {
  if (!res) return "";
  const rows = `${grouped(res.rowCount)} row${res.rowCount === 1 ? "" : "s"}`;
  const moneyCol = res.columns.find((c) => c.total && c.kind === "cents");
  if (!moneyCol) return rows;
  const total = res.totals?.[moneyCol.name];
  if (total === undefined || total === null) return rows;
  return `${rows} · ${moneyCol.label} ${money(Number(total))}`;
}

// -- the words a refusal carries -----------------------------------------------------------

/**
 * A reporting read's refusal. The server's own sentence passes through
 * untouched (a branch-scope refusal says exactly what happened); the role
 * gate's one word body becomes words naming who the surface is for. Anything
 * else falls to the shared error words.
 */
export function reportingRefusalWords(error: string, status: number | undefined): string {
  if (status === 403) {
    if (error.trim() === "Forbidden") {
      return "This report is beyond the catalogue: the runs and the commission register are the manager roles'.";
    }
    return error !== "" ? error : "The report did not load.";
  }
  return errorWords(error);
}

/** Who keeps the commission rules, as the guard's admin window names them (tier one global). */
export const COMMISSION_RULE_KEEPER_WORDS = "Commission rules are kept by the general manager and the financial controller.";

/** A rule write's refusal: the wall becomes words naming who writes, the rest stays the server's own. */
export function commissionRuleRefusalWords(error: string, status: number | undefined): string {
  if (status === 403 && error.trim() === "Forbidden") {
    return `You may not write commission rules. ${COMMISSION_RULE_KEEPER_WORDS}`;
  }
  return errorWords(error);
}

// -- commission words -------------------------------------------------------------------------

/** The two bases a rate may apply to, in the words the form shows and the wire's closed pair. */
export const BASIS_CHOICES: readonly { readonly value: string; readonly word: string }[] = Object.freeze([
  { value: "REVENUE", word: "A percent of revenue" },
  { value: "MARGIN", word: "A percent of gross margin" },
]);

/** What a rate was applied to: "MARGIN" alone has been read as "margin percent" too often to show raw. */
export function basisLabel(basis: string): string {
  if (basis === "MARGIN") return "on gross margin";
  if (basis === "REVENUE") return "on revenue";
  return basis;
}

/** A drill-through line that earned nothing because no rule covered it. A zero with no explanation reads as an omission. */
export function lineIsUnpaid(line: { noRuleReason?: string; rateBps: number; commissionCents: number }): boolean {
  return !!line.noRuleReason || (line.rateBps === 0 && line.commissionCents === 0);
}

/** The human name for a computed period. */
export function periodLabel(p: { rep_user_id: string; rep_name: string; period_start: string; period_end: string }): string {
  return `${p.rep_name || p.rep_user_id} · ${p.period_start} to ${p.period_end}`;
}

/**
 * The house-queue caveat, stated from the server's own numbers, or empty when
 * there is none. A commission report whose revenue quietly falls short of the
 * sales report is a report two people will spend an afternoon reconciling; the
 * server reports the gap, this says it.
 */
export function unattributedWords(periods: readonly { unattributedCount: number; unattributedCents: number }[]): string {
  const p = periods.find((x) => x.unattributedCount > 0);
  if (!p) return "";
  return (
    `${money(p.unattributedCents)} of revenue over ${grouped(p.unattributedCount)} line` +
    `${p.unattributedCount === 1 ? "" : "s"} is not attributed to any rep (the house queue). ` +
    `It earns no commission and is not in the figures above.`
  );
}
