// The ledger slice's one money helper: integer cents in, the words a person
// reads out. Money is integer cents everywhere in this app and the server
// computes every figure; this file only formats. An absent figure reads as a
// placeholder, never a plausible zero.
import { formatDollars } from "@hardscapehouse/pos-kit/src/ported/_money-format.js";

export const MONEY_PLACEHOLDER = "(no figure)";

/** 750000 reads "$7,500.00"; anything that is not an integer reads the placeholder. */
export function formatMoney(cents: number | null | undefined): string {
  if (typeof cents !== "number" || !Number.isInteger(cents)) return MONEY_PLACEHOLDER;
  return formatDollars(cents);
}

/**
 * What was typed in an amount box, as integer cents: "19.68" is 1968, "$20,000.00" is 2000000.
 * Read through the decimal string, never a float multiply. A leading dollar sign and well formed
 * thousands groups are read; refused as null, and nothing is sent: more than two decimals, a sign,
 * a misplaced separator, any non-digit, more than nine digits before the point, or nothing.
 * The same parse as the receivables slice's (src/ar/ar-money.ts, from the Counter Till).
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
