// The liens slice's money helpers: integer cents in, the words a person reads out. Money is integer cents everywhere
// in this app and the server computes every figure; this file formats, and parses the two figures a person types
// (a contract amount and a holdback percent) through the decimal string and never a float multiply. An absent
// figure reads as a placeholder, never a plausible zero.
//
// Port provenance: the amount parse is the receivables slice's (src/ar/ar-money.ts), itself the Counter Till's
// (web/apps/counter-till/src/money.ts). The original's job form read the contract through Number(...) * 100, which
// rounds a half cent the wrong way; that line is not carried.
import { formatDollars } from "@hardscapehouse/pos-kit/src/ported/_money-format.js";

export const MONEY_PLACEHOLDER = "(no figure)";

/** 1250000 reads "$12,500.00"; anything that is not an integer reads the placeholder. */
export function formatMoney(cents: number | null | undefined): string {
  if (typeof cents !== "number" || !Number.isInteger(cents)) return MONEY_PLACEHOLDER;
  return formatDollars(cents);
}

/**
 * What was typed in an amount box, as integer cents: "19.68" is 1968, "$125,000.00" is 12500000. A leading dollar
 * sign and well formed thousands groups are read; refused as null, and nothing is sent: more than two decimals, a
 * sign, a misplaced separator, any non-digit, more than nine digits before the point, or nothing.
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

/**
 * What was typed in the holdback percent box, as basis points the core stores: "10" is 1000, "7.5" is 750, "7.25%"
 * is 725. Nothing typed is 0, which the core reads as its own default. Refused as null: zero, above one hundred,
 * more than two decimals, a sign or any non-digit.
 */
export function parsePercentToBps(text: string): number | null {
  const trimmed = text.trim().replace(/%$/, "");
  if (trimmed === "") return 0;
  const match = trimmed.match(/^([0-9]{1,3})(?:\.([0-9]{1,2}))?$/);
  if (match === null) return null;
  const bps = Number(`${match[1] ?? ""}${(match[2] ?? "").padEnd(2, "0")}`);
  return bps >= 1 && bps <= 10000 ? bps : null;
}

/** 1000 reads "10%", 750 reads "7.5%": the digits of the basis points with a point put in, no sum. */
export function bpsWords(bps: number | null | undefined): string {
  if (typeof bps !== "number" || !Number.isInteger(bps) || bps < 0) return MONEY_PLACEHOLDER;
  const digits = String(bps).padStart(3, "0");
  const whole = digits.slice(0, -2);
  const fraction = digits.slice(-2).replace(/0+$/, "");
  return `${whole}${fraction === "" ? "" : `.${fraction}`}%`;
}
