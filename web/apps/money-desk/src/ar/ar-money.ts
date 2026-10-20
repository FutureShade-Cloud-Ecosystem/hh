// The receivables slice's one money helper: integer cents in, the words a
// person reads out. Money is integer cents everywhere in this app and the
// server computes every figure; this file formats, and parses the one amount
// a person types, through the decimal string and never a float multiply.
// An absent figure reads as a placeholder, never a plausible zero.
//
// Port provenance: parseAmountToCents is the Counter Till's (web/apps/counter-till/src/money.ts),
// itself from the original app's cash tendered field.
import { formatDollars, plainDollars } from "@hardscapehouse/pos-kit/src/ported/_money-format.js";

export const MONEY_PLACEHOLDER = "Not available";

/** 96000 reads "$960.00"; anything that is not an integer reads the placeholder. */
export function formatMoney(cents: number | null | undefined): string {
  if (typeof cents !== "number" || !Number.isInteger(cents)) return MONEY_PLACEHOLDER;
  return formatDollars(cents);
}

/** 96000 reads "960.00": the shape a person types back into an amount box. */
export function centsToField(cents: number): string {
  return plainDollars(cents);
}

/**
 * What was typed in an amount box, as integer cents: "19.68" is 1968, "$20,000.00" is 2000000.
 * A leading dollar sign and well formed thousands groups are read; refused as null, and nothing
 * is sent: more than two decimals, a sign, a misplaced separator, any non-digit, more than nine
 * digits before the point, or nothing.
 */
export function parseAmountToCents(text: string): number | null {
  const trimmed = text.trim().replace(/^\$/, "");
  if (trimmed === "") return null;
  const match = trimmed.match(/^([0-9]{1,3}(?:,[0-9]{3})+|[0-9]{1,9})(?:\.([0-9]{1,2}))?$/);
  if (match === null) return null;
  const dollars = (match[1] ?? "").replace(/,/g, "");
  if (dollars.length > 9) return null;
  const fraction = (match[2] ?? "").padEnd(2, "0");
  return Number(`${dollars}${fraction}`);
}
