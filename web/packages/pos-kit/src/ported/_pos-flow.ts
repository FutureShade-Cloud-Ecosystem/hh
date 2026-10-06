// Ported from hardscapeos_dibbits master 87d7941b app/src/_pos-flow.ts; adapted for HH.

// _pos-flow.ts: the counter sale-session flow, derived from the sale itself.
//
// THE SHAPE THE CLIENT ASKED FOR AFTER A DAY AT THE COUNTER: Customer → Items → Payment →
// Summary. Customer moved to the front because the price of everything below depends on it: a
// contract account's prices, and the savings against retail, are only knowable once the account
// is on the sale. Summary is new: the receipt moment (what sold, what was paid, the numbers and
// the onward actions) is a STEP now, not a card that appears under the payment block.
//
// THE RULING NUANCE IS UNCHANGED AND NON-NEGOTIABLE: the stepper TRACKS, it does not GATE. This is
// not a wizard. A walk-in cash sale is scanned and rung without anyone touching the customer form,
// and the Customer step honestly reports itself open while that happens. Clicking a step opens its
// card; it locks nothing, and nothing here can refuse anything.
//
// That is why this module is pure derivation rather than component state: a step is "complete"
// because the SALE DATA says so (a line exists, a customer is resolved, a tender is chosen, the
// ring committed), not because anyone pressed Next. There is no Next. Un-doing the data
// un-completes the step. DOM-free per the `_*.ts` house convention so the whole state table is
// unit-testable on the Node runner (_pos-flow.test.mjs).

export type FlowStepId = 'customer' | 'items' | 'payment' | 'summary';

/** One rendered step: number, label, and the two derived flags the stepper paints. */
export interface FlowStep {
  id: FlowStepId;
  n: 1 | 2 | 3 | 4;
  label: string;
  /** The sale data satisfies this step (a check mark, never a lock). */
  complete: boolean;
 /** The step the session's attention is on: see deriveFlow. */
  current: boolean;
}

/**
 * The facts of the sale session that the flow reads. All of them are sale data the terminal already
 * holds: nothing here is a stepper's own bookkeeping.
 */
export interface SaleSessionFacts {
  /** Cart lines on the ticket. */
  lineCount: number;
  /** An account customer is on the sale. */
  customerSelected: boolean;
  /**
   * The operator explicitly chose the walk-in / cash-customer posture. Walk-in is also the
 * terminal's DEFAULT posture, and a default is not a decision: the step reads complete only when
   * someone actually resolved the question (picked an account, or confirmed walk-in).
   */
  walkInConfirmed: boolean;
  /** The chosen tender ('' until one of the payment tiles is picked). */
  payMethod: string;
 /** The ring committed: a sold ticket satisfies every step by definition. */
  rungDone: boolean;
}

/** The order the steps render in, and the order `current` walks. */
export const FLOW_ORDER: FlowStepId[] = ['customer', 'items', 'payment', 'summary'];

const FLOW_LABELS: Record<FlowStepId, string> = {
  customer: 'Customer',
  items: 'Items',
  payment: 'Payment',
  summary: 'Summary',
};

/** The label a step wears in the stepper and on its card header. */
export function flowLabel(id: FlowStepId): string {
  return FLOW_LABELS[id];
}

/**
 * deriveFlow: the whole stepper, as data.
 *
 * customer complete when the customer question is RESOLVED: an account picked or walk-in
 * explicitly confirmed. (See walkInConfirmed above for why the default doesn't count.)
 * items complete when the ticket holds at least one line.
 * payment complete when a tender is chosen.
 * summary complete when the ring committed. Nothing else completes it, because nothing else
 * has happened yet: there is no receipt before there is a sale.
 *
 * `current` is the first incomplete step among the three WORKING steps; when all three are done it
 * rests on payment (the ready-to-ring posture, unchanged from the shipped stepper). A committed
 * ring moves it: and only a committed ring moves it: to Summary. Summary is deliberately outside
 * the first-open walk: an un-rung sale must never point at a receipt that does not exist.
 */
export function deriveFlow(f: SaleSessionFacts): FlowStep[] {
  const complete: Record<FlowStepId, boolean> = {
    customer: f.rungDone || f.customerSelected || f.walkInConfirmed,
    items: f.rungDone || f.lineCount > 0,
    payment: f.rungDone || f.payMethod !== '',
    summary: f.rungDone,
  };
  const working: FlowStepId[] = ['customer', 'items', 'payment'];
  const firstOpen = working.find((id) => !complete[id]);
  const currentId: FlowStepId = f.rungDone ? 'summary' : (firstOpen ?? 'payment');
  return FLOW_ORDER.map((id, i) => ({
    id,
    n: (i + 1) as 1 | 2 | 3 | 4,
    label: FLOW_LABELS[id],
    complete: complete[id],
    current: id === currentId,
  }));
}
