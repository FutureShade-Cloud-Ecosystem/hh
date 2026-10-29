// The T5018 view's own vocabulary: the report the core sends, the state the view holds, and the
// words the file a bookkeeper hands the contractor's book wears.
//
// The shape is the contract's own (core/api/fragments/purchasing.yaml, the route
// /api/v1/purchasing/t5018 and its schemas PurchasingT5018Report and PurchasingT5018Row, W3-T51),
// taken from the generated client, so the screen cannot drift from the route.
//
// Money is the server's: the view reads the figures the route sends and never adds them up, the
// same rule the payables board beside it holds (test/no-app-money.test.mjs). The total the route
// sends is the total the screen shows.

import type { PurchasingT5018Report, PurchasingT5018Row } from "@hardscapehouse/api-client";

export type { PurchasingT5018Report, PurchasingT5018Row };

/** The view's own state: the read, the year it is for, and the words a refusal wears. */
export interface T5018Model {
  readonly state: "loading" | "ready" | "error";
  readonly report: PurchasingT5018Report | null;
  readonly error: string;
  /** The year the report is for, as the operator picked it. */
  readonly year: number;
  /** True while the file is on its way, so the button says so and is not pressed twice. */
  readonly downloading: boolean;
  /** The words the last press left under the controls. */
  readonly notice: string;
}

/**
 * The year the view opens on: the dealer's current year. The view never reads a clock of its own
 * for a figure the server owns, but the year picker needs a starting year, and the server's own
 * answer for the current year is the year the report carries, so the first read settles it.
 */
export function currentYear(): number {
  return new Date().getFullYear();
}

/** The file's name: the year it covers. */
export function t5018Filename(year: number): string {
  return `t5018-${year}.csv`;
}

/**
 * How many marked vendors the report carries, said so the way a person says it: a form with two
 * lines on it and a form with none on it are different things to say.
 */
export function t5018VendorsWord(count: number): string {
  if (count === 0) return "No vendor is marked as a subcontractor";
  if (count === 1) return "1 vendor is marked as a subcontractor";
  return `${count} vendors are marked as subcontractors`;
}

/**
 * A row's business number, as the screen says it: the number the contractor gave, or the mark
 * that there is none. A blank cell would read as an oversight, so the screen says what it is.
 */
export function businessNumberWords(row: PurchasingT5018Row): string {
  if (row.business_number_missing) return "(missing)";
  return row.business_number;
}

/**
 * Which empty a year is. The report lists every marked vendor, paid or not, so no rows means no
 * vendor is marked yet, and rows that total nothing mean the marked vendors were not paid that
 * year. Those are two different claims, so the screen says each in its own words.
 */
export function t5018EmptyKind(report: PurchasingT5018Report): "none-marked" | "none-paid" | null {
  if (report.items.length === 0) return "none-marked";
  return report.total_cents === 0 ? "none-paid" : null;
}

/** True when the contractor gave no business number, so the screen can flag it as a warning. */
export function businessNumberIsMissing(row: PurchasingT5018Row): boolean {
  return row.business_number_missing;
}

/** The CRA construction-income scope sentence the report carries at the top. */
export const T5018_CONSTRUCTION_INCOME_NOTE =
  "T5018 applies only where construction is more than half the dealer's business income.";

/** The rows the screen shows: the route's order, which is the vendor's name in order. */
export function t5018Rows(report: PurchasingT5018Report): readonly PurchasingT5018Row[] {
  return report.items;
}

/** The construction-income rule note shown under the T5018 report's title. */
export function t5018RuleNote(report: PurchasingT5018Report | null): string {
  if (report !== null && typeof report.rule_note === "string" && report.rule_note !== "") {
    return report.rule_note;
  }
  return T5018_CONSTRUCTION_INCOME_NOTE;
}

/** The server's Box 22 figure for a row (construction subcontractor services share, tax included). */
export function t5018Box22Cents(row: PurchasingT5018Row): number {
  return row.box_22_cents;
}

/** The server's under-threshold words when a subcontractor's pre-tax services are under $500. */
export function t5018ThresholdWords(row: PurchasingT5018Row): string {
  if (!row.under_threshold) return "";
  return row.threshold_note !== undefined && row.threshold_note !== "" ? row.threshold_note : "under the $500 threshold, no slip";
}

/** The server's unmarked bill lines words when a subcontractor has paid lines with no goods/services mark. */
export function t5018UnmarkedWords(row: PurchasingT5018Row): string {
  if (row.unmarked_line_count <= 0) return "";
  return row.unmarked_lines_note ?? "";
}
