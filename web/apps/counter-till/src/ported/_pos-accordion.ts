// Ported from hardscapeos_dibbits master 87d7941b app/src/_pos-accordion.ts; adapted for HH.

// _pos-accordion.ts: the counter's single-column accordion: which card is open, what a CLOSED
// card still says, and where the keyboard lands when one opens.
//
// WHY AN ACCORDION AT ALL. The client used the two-panel counter for a day and asked for one
// column with one step open at a time. The reason is the counter itself: a person standing at a
// till with a customer in front of them is doing exactly one thing, and four half-visible panels
// made them hunt for it. Opening a step closes the others, and the open card is scrolled to.
//
// AND WHY A CLOSED CARD MUST STILL SPEAK. Collapsing is only acceptable if it hides CHROME, never
// FACTS. A clerk mid-sale needs to know who the customer is, how many items are on the ticket and
// what it comes to, and how they are paying: at a glance, from any step. So every collapsed card
// carries its own one-line summary of exactly that, and this module is where those lines are
// decided: pure, DOM-free (the `_*.ts` house convention), and unit-tested without a browser.
//
// DISCLOSURE, NOT DISPLAY:NONE. The card body is not RENDERED while closed: so it is not in the
// tab order, not in the accessibility tree, and not reachable by a screen reader's virtual cursor.
// A `display: none` accordion is a trap: the ids below (headerId/panelId) exist so the header
// button can carry real aria-expanded/aria-controls semantics against a region that genuinely
// comes and goes. Hiding with CSS alone would leave every control inside reachable by Tab while
// invisible, which is worse than no accordion at all.

import type { FlowStepId } from '@hardscapehouse/pos-kit/src/ported/_pos-flow.js';

/** The DOM id of a step's header button: the thing aria-labelledby on the region points at. */
export function headerId(step: FlowStepId): string {
  return `step-h-${step}`;
}

/** The DOM id of a step's disclosed region: the thing aria-controls on the header points at. */
export function panelId(step: FlowStepId): string {
  return `step-p-${step}`;
}

/**
 * The facts a collapsed card summarises. Same sale data the flow derivation reads, plus the two
 * display strings the terminal already formats (the customer's name, the money formatter's output)
 *: formatting money is the terminal's job, deciding what is WORTH saying is this module's.
 */
export interface StepSummaryFacts {
  customerName: string;
  walkInConfirmed: boolean;
  lineCount: number;
  totalLabel: string;
  tenderLabel: string;
  orderNo: string;
  rungDone: boolean;
}

/**
 * summaryFor: the one line a CLOSED card shows.
 *
 * Each line is the fact a counter person would otherwise have to open the card to see, and nothing
 * else: who is on the sale, how big the ticket is, how it is being paid, what the sale was called.
 * An empty string means the card has nothing worth saying yet and renders no summary at all: an
 * empty summary slot is quieter than a placeholder, and a placeholder would be a lie about state.
 *
 * The customer line reports the POSTURE ("Walk-in") even before anyone confirms it, because that
 * is what the sale will ring as if nobody touches it: and a clerk deserves to know that from the
 * closed card. Whether the QUESTION was answered is the stepper's business (deriveFlow), not this
 * line's; the two say different things on purpose.
 */
export function summaryFor(step: FlowStepId, f: StepSummaryFacts): string {
  switch (step) {
    case 'customer':
      return f.customerName || 'Walk-in';
    case 'items':
      if (f.lineCount === 0) return '';
      return `${f.lineCount} ${f.lineCount === 1 ? 'item' : 'items'} · ${f.totalLabel}`;
    case 'payment':
      if (f.rungDone) return f.tenderLabel;
      return f.tenderLabel || '';
    case 'summary':
      return f.rungDone ? f.orderNo : '';
  }
}

/**
 * advanceOn: the expansion follows the sale forward, and ONLY forward.
 *
 * Two moments move it, and they are the two the client named:
 * 'customer-resolved' the account (or walk-in) is answered, so the next thing anyone does is
 * put items on the ticket.
 * 'sale-rung' the money is done; the receipt IS the next thing.
 *
 * Everything else returns null, meaning "leave the operator where they are". This restraint is the
 * whole design: an accordion that re-opens itself on every state change fights the person using
 * it, and a counter person who deliberately opened Customer mid-ticket must not have the card
 * yanked shut because a scan landed.
 */
export type FlowAdvance = 'customer-resolved' | 'sale-rung';

export function advanceOn(event: FlowAdvance): FlowStepId | null {
  switch (event) {
    case 'customer-resolved':
      return 'items';
    case 'sale-rung':
      return 'summary';
    default:
      return null;
  }
}

/**
 * focusTargetFor: the FIRST MEANINGFUL CONTROL of a step, as a selector the terminal queries in
 * its own shadow root.
 *
 * "Meaningful" means the control the person came to use, not the first tabbable node in the card:
 * opening Items puts the caret in the scan box (the counter's rest state: a scanner burst must
 * land somewhere useful the instant the card opens), opening Customer lands on the Walk-in tile
 * (one press resolves the question), opening Payment lands on the tender group's roving stop, and
 * opening Summary lands on the receipt action. A null answer means "focus the region itself",
 * which is the honest fallback for a card whose controls have not mounted.
 */
export function focusTargetFor(step: FlowStepId): string | null {
  switch (step) {
    case 'customer':
      return '[data-testid="cust-walkin"]';
    case 'items':
      return 'hos-pos-scan-box';
    case 'payment':
      return '.paytiles button[tabindex="0"]';
    case 'summary':
      return '[data-testid="sold-print"]';
    default:
      return null;
  }
}

/**
 * scrollPlan: WHERE the page must land so the newly opened step is FULLY in view.
 *
 * The client's words were "to always have the step, in view 100%", and that is a stronger ask than
 * scrollIntoView's usual answer. `block: 'start'` always drags the card to the top of the window,
 * which is jarring when the card was already completely visible, and `block: 'nearest'` is happy to
 * leave a card whose header is on screen but whose body runs off the bottom: the exact failure the
 * client described. So the decision is measured, and it is made here, as arithmetic:
 *
 * already fully visible → null (do not scroll at all; a scroll nobody asked for is jank)
 * fits in the viewport → the smallest scroll that brings the whole card in, with a margin
 * taller than the viewport → align its top, because there is no scroll that shows all of it and
 * the top is where its header and first control are
 *
 * `top` is the card's position in DOCUMENT space and `scrollY` the window's, so the result is an
 * absolute scroll target. Returning null (rather than the current scrollY) is deliberate: the
 * caller can then skip the scroll entirely instead of issuing a zero-distance smooth scroll, which
 * some browsers still animate.
 */
export function scrollPlan(
  top: number,
  height: number,
  scrollY: number,
  viewportHeight: number,
  margin = 12,
): number | null {
  if (!Number.isFinite(top) || !Number.isFinite(height) || !Number.isFinite(viewportHeight)) return null;
  if (viewportHeight <= 0) return null;
  const viewTop = scrollY;
  const viewBottom = scrollY + viewportHeight;
  const cardTop = top;
  const cardBottom = top + height;
  if (height + margin * 2 > viewportHeight) {
    // Taller than the window: nothing shows all of it, so show its head.
    return Math.max(0, cardTop - margin);
  }
  if (cardTop >= viewTop + margin && cardBottom <= viewBottom - margin) return null; // already whole
  if (cardTop < viewTop + margin) return Math.max(0, cardTop - margin); // it is above: pull it down
  return Math.max(0, cardBottom + margin - viewportHeight); // it is below: pull it up, minimally
}
