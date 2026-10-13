// The bank reconciliation slice's money helpers: integer cents in, the words a person reads out. Money is integer
// cents everywhere in this app and the server computes every figure; this file formats, and parses the two
// amounts a person types (a statement balance, a rule tolerance) through the decimal string and never a float
// multiply. An absent figure reads as a placeholder, never a plausible zero.
//
// Port provenance: the parse is the receivables slice's (src/ar/ar-money.ts), itself the Counter Till's
// (web/apps/counter-till/src/money.ts). The signed form is new: a statement balance may be overdrawn, and the
// bank writes a negative with a minus sign or in brackets.
import { formatDollars, plainDollars } from "@hardscapehouse/pos-kit/src/ported/_money-format.js";

export const MONEY_PLACEHOLDER = "(no figure)";

/** 2525575 reads "$25,255.75"; anything that is not an integer reads the placeholder. */
export function formatMoney(cents: number | null | undefined): string {
  if (typeof cents !== "number" || !Number.isInteger(cents)) return MONEY_PLACEHOLDER;
  return formatDollars(cents);
}

/** 61050 reads "610.50": the shape a person types back into an amount box. */
export function centsToField(cents: number): string {
  return plainDollars(cents);
}

const UNSIGNED = /^([0-9]{1,3}(?:,[0-9]{3})+|[0-9]{1,9})(?:\.([0-9]{1,2}))?$/;

function unsignedCents(text: string): number | null {
  const match = text.trim().replace(/^\$/, "").match(UNSIGNED);
  if (match === null) return null;
  const dollars = (match[1] ?? "").replace(/,/g, "");
  if (dollars.length > 9) return null;
  return Number(`${dollars}${(match[2] ?? "").padEnd(2, "0")}`);
}

/**
 * What was typed in the statement balance box, as signed integer cents: "25,255.75" is 2525575, "-1,200.5" and
 * "(45.00)" are negative. Refused as null, and nothing is sent: more than two decimals, a misplaced separator,
 * any non-digit, more than nine digits before the point, or nothing.
 */
export function parseBalanceToCents(text: string): number | null {
  const trimmed = text.trim();
  if (trimmed.startsWith("(") && trimmed.endsWith(")")) {
    const inner = unsignedCents(trimmed.slice(1, -1));
    return inner === null ? null : -inner;
  }
  if (trimmed.startsWith("-")) {
    const inner = unsignedCents(trimmed.slice(1));
    return inner === null ? null : -inner;
  }
  return unsignedCents(trimmed);
}

/** A match tolerance: dollars and cents, never negative. */
export function parseToleranceToCents(text: string): number | null {
  return unsignedCents(text);
}
