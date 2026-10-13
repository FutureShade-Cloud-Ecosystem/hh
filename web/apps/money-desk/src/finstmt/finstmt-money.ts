// The statements slice's money helpers: integer cents in, the words a person reads out, and the plain
// decimal string a spreadsheet reads. Money is integer cents everywhere in this app and the server
// computes every figure; this file only formats. An absent figure reads as a placeholder, never a
// plausible zero. The decimal string is built from the digits of the cents, not by division, so no
// float touches a figure.
import { formatDollars } from "@hardscapehouse/pos-kit/src/ported/_money-format.js";

export const MONEY_PLACEHOLDER = "(no figure)";

/** 750000 reads "$7,500.00"; anything that is not an integer reads the placeholder. */
export function formatMoney(cents: number | null | undefined): string {
  if (typeof cents !== "number" || !Number.isInteger(cents)) return MONEY_PLACEHOLDER;
  return formatDollars(cents);
}

/** 123456 reads "1234.56" and -5 reads "-0.05": plain, ungrouped, no mark, the shape a spreadsheet takes as a number. */
export function centsDecimal(cents: number): string {
  if (!Number.isInteger(cents)) return "";
  const digits = String(Math.abs(cents)).padStart(3, "0");
  return `${cents < 0 ? "-" : ""}${digits.slice(0, -2)}.${digits.slice(-2)}`;
}
