// The tax slice's figures: integer cents in, the words a person reads out, and the one rate a person types.
// Money is integer cents everywhere in this app and the server computes every figure; this file formats. The
// rate is the only number a person types here, and it is parsed through its decimal string (basis points are
// whole hundredths of a percent), never through a float multiply: 7.75 percent is 775, and 1.005 * 100 is not
// 100.5. An absent figure reads as a placeholder, never a plausible zero.
//
// Port provenance: the percent word is the original app's fmtBps (app/src/tax-api.ts) without its locale
// call, so a rate reads the same on every machine; the money format is the receivables slice's (src/ar/ar-money.ts).
import { formatDollars } from "@hardscapehouse/pos-kit/src/ported/_money-format.js";

export const MONEY_PLACEHOLDER = "(no figure)";
export const RATE_PLACEHOLDER = "(no rate)";

/** 482375 reads "$4,823.75"; anything that is not an integer reads the placeholder. */
export function formatMoney(cents: number | null | undefined): string {
  if (typeof cents !== "number" || !Number.isInteger(cents)) return MONEY_PLACEHOLDER;
  return formatDollars(cents);
}

/** 775 reads "7.75%", 0 reads "0%", 1000 reads "10%", 5 reads "0.05%"; anything not a whole number reads the placeholder. */
export function formatRate(bps: number | null | undefined): string {
  if (typeof bps !== "number" || !Number.isInteger(bps) || bps < 0) return RATE_PLACEHOLDER;
  const whole = Math.trunc(bps / 100);
  const hundredths = bps % 100;
  if (hundredths === 0) return `${whole}%`;
  const fraction = String(hundredths).padStart(2, "0").replace(/0$/, "");
  return `${whole}.${fraction}%`;
}

/** 775 reads "7.75": the shape a person types back into the rate box. */
export function bpsToField(bps: number): string {
  return formatRate(bps).replace(/%$/, "");
}

const PERCENT = /^([0-9]{1,3})(?:\.([0-9]{1,2}))?$/;

/**
 * What was typed in the rate box, as basis points: "7.75" and "7.75%" are 775, "0" is 0, "10" is 1000. Refused as
 * null, and nothing is sent: more than two decimals, any non-digit, a sign, a rate above 100 percent, or nothing.
 */
export function parseRateToBps(text: string): number | null {
  const match = text.trim().replace(/%$/, "").trim().match(PERCENT);
  if (match === null) return null;
  const bps = Number(`${match[1] ?? ""}${(match[2] ?? "").padEnd(2, "0")}`);
  return bps > 10000 ? null : bps;
}
