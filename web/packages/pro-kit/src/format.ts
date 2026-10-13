// The words for money, tax, quantity and days on a Pro screen.
//
// Money is the dealer's US dollars and reads `$1,234.00`, never with currency
// letters (the one formatter is pos-kit's formatDollars, so every Pro screen
// agrees with the counter). Money is integer cents the core computed: nothing
// here adds, multiplies or rounds a price, and tax is shown exactly as the core
// returned it (the dealer's California rate is the core's business, never this
// file's). An absent or unusable figure reads as NO_FIGURE, never as a
// plausible zero (the "never render a confident wrong number" rule).
import { css } from "lit";
import { formatDollars } from "@hardscapehouse/pos-kit/src/ported/_money-format.js";

/** What an unknown figure reads as. Plain and sentence case; never a zero. */
export const NO_FIGURE = "n/a";

function usable(cents: number | null | undefined): cents is number {
  return typeof cents === "number" && Number.isFinite(cents);
}

export function money(cents: number | null | undefined): string {
  return usable(cents) ? formatDollars(cents) : NO_FIGURE;
}

export interface TotalsInput {
  subtotal_cents?: number | null;
  tax_cents?: number | null;
  total_cents?: number | null;
}

/** Subtotal, tax and total as the core returned them, each through money(). No arithmetic. */
export function totalsLine(input: TotalsInput): { subtotal: string; tax: string; total: string } {
  return { subtotal: money(input.subtotal_cents), tax: money(input.tax_cents), total: money(input.total_cents) };
}

/** A quantity as the core's decimal string with its unit after it; no parsing, no rounding. */
export function qtyWithUnit(qty: string | null | undefined, unit: string | null | undefined): string {
  if (typeof qty !== "string" || qty.trim() === "") return NO_FIGURE;
  const u = typeof unit === "string" ? unit.trim() : "";
  return u === "" ? qty.trim() : `${qty.trim()} ${u}`;
}

// The dealer's own zone: a day means the dealer's day, never UTC's (a UTC day
// rolls over mid afternoon west of Greenwich). The demo dealer is in San Diego.
const HOUSE_ZONE = "America/Los_Angeles";

/** A timestamp as the dealer's calendar day in short words, for example `Mar 1`. */
export function houseDay(iso: string | null | undefined): string {
  if (typeof iso !== "string") return NO_FIGURE;
  const when = new Date(iso);
  if (Number.isNaN(when.getTime())) return NO_FIGURE;
  return new Intl.DateTimeFormat("en-US", { timeZone: HOUSE_ZONE, month: "short", day: "numeric" }).format(when);
}

/** The mono face for ids, quantities and totals, with tabular figures so a column lines up. */
export const monoStyles = css`
  .mono {
    font-family: var(--hh-font-mono);
    font-variant-numeric: tabular-nums;
  }
`;
