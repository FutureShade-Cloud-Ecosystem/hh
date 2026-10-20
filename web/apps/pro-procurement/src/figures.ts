// Unknown figures say what is missing in words, never "n/a" (the glossary's ruling): Not priced for a
// price, Not counted for a quantity. The kit's money() and qtyWithUnit() carry the figures that are there.
import { calendarDay, houseDay, money, NO_FIGURE, qtyWithUnit } from "@hardscapehouse/pro-kit";

/** A price or total as dollars, or Not priced when the core sent none. */
export function priceWords(cents: number | null | undefined): string {
  return typeof cents === "number" && Number.isFinite(cents) ? money(cents) : "Not priced";
}

/** A quantity with its unit, or Not counted when the core sent none. */
export function qtyWords(qty: string | null | undefined, unit: string | null | undefined): string {
  return typeof qty !== "string" || qty.trim() === "" ? "Not counted" : qtyWithUnit(qty, unit);
}

/**
 * A day the core sent, in the Pro family ("Mar 1"): a bare day is read as written, a moment in the
 * dealer's zone, and anything unreadable says Not set, never "n/a".
 */
export function dayWords(value: string | null | undefined): string {
  if (typeof value !== "string" || value.trim() === "") return "Not set";
  if (/^\d{4}-\d{2}-\d{2}$/.test(value)) {
    const shown = calendarDay(value);
    return shown === NO_FIGURE ? "Not set" : shown;
  }
  return Number.isNaN(new Date(value).getTime()) ? "Not set" : houseDay(value);
}

/** Subtotal, tax and total as the core returned them, each through priceWords(). No arithmetic. */
export function totalWords(input: { subtotal_cents?: number | null; tax_cents?: number | null; total_cents?: number | null }): { subtotal: string; tax: string; total: string } {
  return { subtotal: priceWords(input.subtotal_cents), tax: priceWords(input.tax_cents), total: priceWords(input.total_cents) };
}

/** A plan's price line: the estimate once it has a line, No lines yet before. */
export function planEstimate(project: { line_count: number; subtotal_cents?: number | null }): { label: string; figure: string } {
  return project.line_count > 0
    ? { label: "Estimated", figure: priceWords(project.subtotal_cents) }
    : { label: "Price", figure: "No lines yet" };
}
