// The below-floor row's words, pure: the one sentence the breakdown chip paints
// when the server says a line sits under the dealer's margin floor. Held here,
// DOM-free, so the withholding rule (a policy-ruled caller reads the words
// without the margin figures) is unit-pinned without a browser: the chip's
// render passes the breakdown it was given and the figures flag, and this
// module decides the sentence.

import { centsAmount } from './_money-format.js';

/** The facts the below-floor sentence needs, a subset of PriceBreakdown. */
export interface BelowFloorFacts {
  detail: string;
  margin_bps?: number;
  floor_bps?: number;
}

/**
 * belowFloorWords: the blocked row's whole sentence.
 *
 * The base words are the server's own detail when it gives one, else the
 * standing sentence. The margin pair rides only when the figures are present
 * AND the caller is allowed them: a host that withholds the figures from the
 * data (the counter till's policy gate) and a host that clears the flag paint
 * the same base words, so the two refusals cannot drift apart.
 */
export function belowFloorWords(b: BelowFloorFacts, figuresVisible: boolean): string {
  const base = b.detail || 'Below the configured margin floor: manager approval required';
  if (!figuresVisible || b.margin_bps === undefined || b.floor_bps === undefined) return base;
  return `${base} (margin ${centsAmount(b.margin_bps)}% < floor ${centsAmount(b.floor_bps)}%)`;
}
