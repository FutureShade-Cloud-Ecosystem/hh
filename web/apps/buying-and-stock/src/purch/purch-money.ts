// The purchasing areas' money and quantity text. Money is integer cents; the server computes every
// figure and this file formats, and parses the one amount a person types, through the decimal string
// and never a float multiply (1.005 * 100 is 100.49999999999999). An absent figure reads as a
// placeholder, never a plausible zero.
//
// Port provenance: parseAmountToCents is the Money Desk's (web/apps/money-desk/src/ar/ar-money.ts),
// itself from the Counter Till and the original app's cash tendered field.
import { plainDollars } from "@hardscapehouse/pos-kit/src/ported/_money-format.js";

export { formatMoney, formatQty, MONEY_PLACEHOLDER, QTY_PLACEHOLDER } from "../po/po-money.js";

/** 125050 reads "1250.50": the shape a person types back into an amount box. */
export function centsToField(cents: number): string {
  return plainDollars(cents);
}


/**
 * What was typed in an amount box, as integer cents: "19.68" is 1968, "$20,000.00" is 2000000.
 * A leading dollar sign and well formed thousands groups are read; refused as null, and nothing is
 * sent: more than two decimals, a sign, a misplaced separator, any non-digit, more than nine digits
 * before the point, or nothing.
 */
export function parseAmountToCents(text: string): number | null {
  const trimmed = text.trim().replace(/^\$/, "");
  if (trimmed === "") return null;
  const match = trimmed.match(/^([0-9]{1,3}(?:,[0-9]{3})+|[0-9]{1,9})(?:\.([0-9]{1,2}))?$/);
  if (match === null) return null;
  const whole = (match[1] ?? "").replace(/,/g, "");
  if (whole.length > 9) return null;
  const fraction = (match[2] ?? "").padEnd(2, "0");
  return Number(`${whole}${fraction}`);
}

export type NumberCheck = { ok: true; value: number } | { ok: false };

/**
 * A quantity as typed: a positive decimal of at most four places (the core holds numeric(19,4)),
 * no sign, no grouping, no exponent. A count of what is left may be zero when the caller says so.
 */
export function parseQty(text: string, opts?: { allowZero?: boolean }): NumberCheck {
  const value = text.trim();
  const match = value.match(/^(\d{1,12})(?:\.(\d{1,4}))?$/);
  if (match === null) return { ok: false };
  const parsed = Number(value);
  if (!Number.isFinite(parsed) || (parsed === 0 && opts?.allowZero !== true)) return { ok: false };
  return { ok: true, value: parsed };
}

/** An exchange rate as typed: a positive decimal of at most six places, no sign, no grouping. */
export function parseRate(text: string): NumberCheck {
  const value = text.trim();
  const match = value.match(/^(\d{1,4})(?:\.(\d{1,6}))?$/);
  if (match === null) return { ok: false };
  const parsed = Number(value);
  return parsed > 0 ? { ok: true, value: parsed } : { ok: false };
}
