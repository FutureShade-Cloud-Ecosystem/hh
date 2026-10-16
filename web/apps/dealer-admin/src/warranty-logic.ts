// The Warranty surface's decisions, with no DOM in sight: the kinds and
// periods the boards name, the threshold's dollars parse, the fixed
// allowance's percent parse, the kind switch's payload defaults, and the
// refusal words. No browser, no network.
//
// THE MONEY SHAPES ARE THE APP'S OWN (pricing-logic.ts): a typed amount
// goes to integer cents or basis points through the same strict hundredths
// grammar, never a float parse, so what the dealer typed is what the wire
// carries. The threshold and the allowance both accept their display form
// again (the "$" the field shows, the "%" the percent shows), and nothing
// else.
import { centsFromMoneyInput } from "./pricing-logic.js";

// -- the kinds and periods -----------------------------------------------------------------------------

/** The boards' closed set, in the segmented control's left to right order. */
export const AGREEMENT_KINDS: readonly string[] = Object.freeze([
  "claim_by_claim",
  "periodic_credit_note",
  "fixed_allowance",
]);

/** The fixed allowance the board shows when a row arrives without one: 1 percent. */
export const DEFAULT_ALLOWANCE_BP = 100;

/** The period a kind lands on when the row carries none yet. */
export function defaultPeriodFor(kind: string): "monthly" | "quarterly" {
  return kind === "fixed_allowance" ? "quarterly" : "monthly";
}

/** A kind as the segmented control's word, degrading honestly: an unmapped value shows as itself. */
export function kindWord(kind: string): string {
  if (kind === "claim_by_claim") return "Claim by claim";
  if (kind === "periodic_credit_note") return "Periodic credit note";
  if (kind === "fixed_allowance") return "Fixed allowance";
  return kind;
}

/** A period as the select's word. */
export function periodWord(period: string): string {
  if (period === "monthly") return "Monthly";
  if (period === "quarterly") return "Quarterly";
  return period;
}

// -- the threshold ---------------------------------------------------------------------------------------

/**
 * Dollars a person typed to integer cents, the field's own "$50.00" form
 * accepted again; null when it is not one. The strict hundredths grammar is
 * pricing-logic's, so "1.005" is a mistyped "1.05" and "1e3" is not a
 * thousand here either.
 */
export function parseThresholdDollars(input: string): number | null {
  return centsFromMoneyInput(input.trim().replace(/^\$/, ""));
}

/** The threshold's fault in words, the core's own rule spoken before the write. */
export function thresholdFault(cents: number): string {
  if (cents < 0) {
    return "The threshold must be zero or more cents: a negative one would classify every defect a claim.";
  }
  return "";
}

// -- the fixed allowance ---------------------------------------------------------------------------------

/** Basis points as the field's own percent word: 100 reads "1%", 125 reads "1.25%". */
export function allowanceWords(bp: number): string {
  const pct = bp / 100;
  const whole = Number.isInteger(pct) ? String(pct) : String(Number(pct.toFixed(2)));
  return `${whole}%`;
}

/**
 * A percent a person typed to integer basis points, the field's own "1%"
 * form accepted again; null when it is not one.
 */
export function parseAllowancePct(input: string): number | null {
  return centsFromMoneyInput(input.trim().replace(/%$/, ""));
}

/** The allowance's fault in words, the core's own cap spoken before the write. */
export function allowanceFault(bp: number): string {
  if (bp < 0) {
    return "The allowance must be zero or more basis points of the vendor's purchases.";
  }
  if (bp > 10000) {
    return "The allowance tops out at 10000 basis points: a vendor crediting more than the whole of its purchases is a typo, not a deal.";
  }
  return "";
}

// -- the kind switch -------------------------------------------------------------------------------------

/** One agreement row as the read carries it, narrowed to what the switch decides from. */
export interface AgreementLike {
  readonly agreement_kind: string;
  readonly period?: string;
  readonly allowance_bp: number;
}

/**
 * The payload a kind switch sends: the kind chosen, the period kept when
 * the row carries one and the kind's standard one when not, the allowance
 * kept for a fixed allowance and cleared for the others. One click lands
 * on a sane, server-legal payload; nothing waits on a second.
 */
export function nextAgreementFor(
  row: AgreementLike,
  kind: string,
): { agreement_kind: string; period: "monthly" | "quarterly" | undefined; allowance_bp: number } {
  if (kind === "claim_by_claim") {
    return { agreement_kind: kind, period: undefined, allowance_bp: 0 };
  }
  const period = row.period === "monthly" || row.period === "quarterly" ? row.period : defaultPeriodFor(kind);
  const allowance = kind === "fixed_allowance" ? (row.allowance_bp > 0 ? row.allowance_bp : DEFAULT_ALLOWANCE_BP) : 0;
  return { agreement_kind: kind, period, allowance_bp: allowance };
}

// -- the refusals ------------------------------------------------------------------------------------------

/** A refused read or write names who may act; the core's own sentence passes whole. */
export function warrantyRefusalWords(error: string, status: number | undefined): string {
  if (status === 403) {
    return "You may not change the warranty settings. Changing them is the dealer's administrators' own.";
  }
  return error !== "" ? error : "Try again in a moment.";
}
