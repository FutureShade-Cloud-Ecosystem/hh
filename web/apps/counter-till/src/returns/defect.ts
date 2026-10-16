// The Counter Till Return Drawer's threshold sentence, walk change 6 item 4 (boards
// `ret-returns-lines-across-orders-manufacturer-defect` over the threshold and under the threshold).
// When the manufacturer defect is logged on a return line, the drawer shows the clerk a one line
// summary that names the dollar threshold (the dealer's warranty claim threshold), the cost at cost
// for the picked quantity (quantity * unit cost), and what happens next: at or over the threshold
// it becomes a warranty claim, strictly under it it is logged as shrinkage. The line total at cost
// is the rule the WARRANTY-DEFAULTS card set: "the claim threshold compares the line total at cost
// (quantity * unit cost)". Money is integer cents end to end so the comparison is exact.
//
// The "at or over" rule matches the backend's CostOverClaimThreshold (cost >= threshold) so the
// desk's sentence and the core's rule cannot come to mean two things. A cost that lands at exactly
// the threshold is a warranty claim. Money is integer cents in the prototype too, kept by rounding
// the displayed cost to the nearest cent; the comparison itself is on integer cents, never a float.

import { formatMoney } from "../money.js";

/** The defect's line cost at cost, in integer cents: quantity * unit cost. */
export function defectCostCents(qty: number, unitCostCents: number): number {
  if (!Number.isFinite(qty) || !Number.isFinite(unitCostCents)) return 0;
  if (qty <= 0 || unitCostCents <= 0) return 0;
  // The picked quantity is the customer's line quantity, never a fractional slip the till
  // multiplied itself: multiply as integer cents, then round once on a non integer product.
  return Math.round(qty * unitCostCents);
}

/**
 * The one threshold predicate. Mirrors the backend's `CostOverClaimThreshold` so the screen never
 * disagrees with the core: at OR over the threshold is a warranty claim, strictly under it is
 * shrinkage. Integer cents end to end, no float equality, no tolerance.
 */
export function defectCostOverThreshold(costCents: number, thresholdCents: number): boolean {
  if (!Number.isFinite(costCents) || !Number.isFinite(thresholdCents)) return false;
  if (thresholdCents <= 0) return false;
  return Math.trunc(costCents) >= Math.trunc(thresholdCents);
}

/**
 * Format the claim threshold through the till's single money formatter, trimming trailing `.00`
 * on whole-dollar thresholds so 5000 cents reads as "$50" as shown on the walk boards.
 */
function formatThresholdDollars(cents: number): string {
  return formatMoney(cents).replace(/\.00$/, "");
}

export type DefectSentenceKind = "over" | "under";

export interface DefectSentence {
  kind: DefectSentenceKind;
  /** The whole sentence for the drawer. */
  words: string;
  costCents: number;
  thresholdCents: number;
}

/**
 * The exact sentence the till shows for the manufacturer defect reason. The wording mirrors the
 * walk board ("Over the $50 claim threshold ($56.80 at cost): this goes to a warranty claim." and
 * "Under the $50 claim threshold ($12.30 at cost): logged as shrinkage.") so the dealer reading the
 * desk can confirm the rule by sight. A threshold that is zero or non integer is refused with a
 * null sentence and a refusal word the view renders, instead of guessing.
 */
export function defectThresholdSentence(
  costCents: number,
  thresholdCents: number,
): DefectSentence | null {
  if (!Number.isFinite(costCents) || !Number.isFinite(thresholdCents)) return null;
  if (thresholdCents < 0 || costCents < 0) return null;
  const cost = Math.trunc(costCents);
  const threshold = Math.trunc(thresholdCents);
  const thresholdDollars = formatThresholdDollars(threshold);
  const costDollars = formatMoney(cost);
  if (defectCostOverThreshold(cost, threshold)) {
    return {
      kind: "over",
      costCents: cost,
      thresholdCents: threshold,
      words: `Over the ${thresholdDollars} claim threshold (${costDollars} at cost): this goes to a warranty claim.`,
    };
  }
  return {
    kind: "under",
    costCents: cost,
    thresholdCents: threshold,
    words: `Under the ${thresholdDollars} claim threshold (${costDollars} at cost): logged as shrinkage.`,
  };
}
