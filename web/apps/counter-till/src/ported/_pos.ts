// Ported from hardscapeos_dibbits master 87d7941b app/src/_pos.ts; adapted for HH.

// _pos.ts: pure, DOM-free POS helpers for the terminal (unit-testable without a browser).
//
// The parked-ticket record the server persists is deliberately thin: only customer_id (no name)
// and price-less lines (prices re-derive from the engine on resume). So the terminal keeps a
// client-side side-table of the richer DISPLAY facts captured at park time: the selected
// customer and the ticket total: keyed by the server ticket id. It is consulted on resume
// (restore the customer: DIB-246) and by the parked list (show who + how much: DIB-247).

import type { PosCustomer } from './pos-customer-strip.js';
import type { CartLine } from './pos-api.js';
import { balanceAfterDepositCents } from './ports.js';

// One parked ticket's client-side display snapshot, captured in parkCurrent() before the order
// is reset. `customer` is the full account (or null for walk-in); `totalCents` is the ticket
// total at park time (the server stores no prices, so this is the only place it survives).
export interface ParkedInfo {
  customer: PosCustomer | null;
  totalCents: number;
}

/**
 * DIB-246: resume restore. Given a resumed/listed ticket and the park-time snapshot cache
 * (keyed by ticket id), return the customer to show on the resumed sale.
 * • same-session round-trip (snapshot present) → the SAME full customer (id + name + credit)
 * • cold cache (ticket parked in a prior session/terminal: the server kept only the id) → null
 * (walk-in): no name to display, and we do not fabricate one.
 * Pure: the park→resume round-trip is exercised in _pos.test.mjs.
 */
export function restoreParkedCustomer(
  ticket: { id: string; customer_id?: string },
  cache: ReadonlyMap<string, ParkedInfo>,
): PosCustomer | null {
  const snap = cache.get(ticket.id);
  return snap ? snap.customer : null;
}

/**
 * DIB-244: clear-sale confirm gate. A one-click "Clear sale" should ask before wiping real
 * work, but never nag on an already-empty terminal. True only when there is a cart to lose.
 */
export function clearSaleNeedsConfirm(cartLength: number): boolean {
  return cartLength > 0;
}

/**
 * DIB-379/F2: the ring-failure message shown to the cashier. createSale() (pos-api.ts) already
 * parses the server's `error` field for EVERY status, defaulting only to the literal 'sale_failed'
 * when the body didn't parse: so any real detail (409 "pos: open a till session first", 503 an
 * unwired guard, 422 a policy/validation refusal, …) is shown verbatim. Only the unparseable case
 * falls back to a generic "Sale failed (<status>)." Pure: exercised in _pos.test.mjs.
 */
export function ringErrorMessage(err: { status: number; error: string }): string {
  return err.error !== 'sale_failed' ? err.error : `Sale failed (${err.status}).`;
}

// ── DIB-444: the post-sale confirmation ────────────────────────────────────────────────────────────
//
// Reported: "It does not appear to post a sale: it keeps saying 'Sold · #SO-TRT-32 · #INV-20 · PAID'
// but it does not really verify: would be nice if you got more confirmation."
//
// The books were never the problem. A cash sale rung on this build writes, in one transaction: the
// order (INVOICED), its lines, the invoice (PAID), the payment (RECEIVED) with its allocation, two
// balanced GL journals (DR AR / CR revenue + HST, then DR undeposited funds / CR AR) and the stock
// SHIP moves. What the counter got back for all of that was one thin green line squeezed between two
// rows of utility buttons: no amount, no tender, no announcement, nothing that reads as a receipt.
// The tester was right to distrust it and wrong about what was missing.
//
// This is the content of the confirmation, computed as data so the panel cannot invent a claim the
// sale does not support. In particular it must NOT say "PAID" on a sale that was not paid: an
// account sale and a COD sale both come back from the same ring with no payment behind them, and a
// panel that shouts success at a clerk who still has to collect is worse than the thin line was.

/** What the counter is told, and what it is allowed to claim. */
export interface SaleConfirmation {
 /** The one-line verdict, big and unmissable. */
  headline: string;
 /** True only when money actually landed: drives the success styling, not the mere existence of a sale. */
  settled: boolean;
 /** The label over the headline amount: what that number IS. */
  amountLabel: string;
 /** The amount the clerk says out loud, in cents. */
  amountCents: number;
 /** Still owed, in cents. 0 on a settled sale. */
  balanceCents: number;
 /** How it was settled, in counter words. */
  tenderLabel: string;
 /** The qualifying sentence under the figures: empty when there is nothing to qualify. */
  note: string;
}

const TENDER_LABELS: Record<string, string> = {
  CASH: 'Cash',
  CARD: 'Card',
  ACCOUNT: 'On account',
  SPLIT: 'Split tender',
};

/** tenderLabel renders a tender code in counter words, falling back to the code itself. */
export function tenderLabel(tender: string): string {
  return TENDER_LABELS[tender] ?? (tender || 'Tender');
}

/**
 * The fulfilment tiles' own words, for surfaces that report a sale rather than set it: the
 * pre-tender read-back says how the material leaves, in the same words the tile the clerk pressed
 * wears. A code that reaches here unknown reads as itself rather than as a blank line.
 */
const SALE_TYPE_LABELS: Record<string, string> = {
  COLLECT_NOW: 'Collect now',
  COLLECT_LATER: 'Will-call',
  DELIVERED: 'Delivery',
  COD: 'COD',
};

export function fulfilmentLabel(saleType: string): string {
  return SALE_TYPE_LABELS[saleType] ?? (saleType || 'Collect now');
}

/**
 * saleConfirmation turns a committed SaleResult into what the confirmation panel may say.
 *
 * The three shapes a ring can end in, and why each is worded the way it is:
 *
 * PAID payment_id present, nothing outstanding. "Paid in full": the only case that gets
 * the success treatment, because it is the only case where the clerk is done.
 * DEPOSIT a deposit was taken against an account/COD sale. The headline amount is the
 * BALANCE, not the total: the number the clerk still has to collect is the number
 * they need to see. Reporting the total here is how a deposit gets collected twice.
 * ON ACCOUNT no payment at ring. Invoiced and owed. `settled` is false, so the panel does not
 * dress it as money received.
 */
export function saleConfirmation(sale: {
  tender: string;
  sale_type: string;
  total_cents: number;
  payment_id?: string;
  deposit_cents?: number;
  replayed?: boolean;
}): SaleConfirmation {
  const deposit = sale.deposit_cents ?? 0;
  const paidInFull = !!sale.payment_id && deposit === 0;
  const isCOD = sale.sale_type === 'COD';
 // A ring retried over a flaky counter connection returns the ORIGINAL sale rather than making a
 // second one. Saying so is the whole point of the ticket: the clerk who pressed Cash twice needs
 // to know the second press did not take a second payment.
  const note = sale.replayed
    ? 'This ring was retried: the sale above is the original, not a second one.'
    : isCOD && !paidInFull
      ? 'Goods are held until this is paid.'
      : '';

 // DIB-512 · one word per surface. The old card said its one fact three ways at once ("Paid in
 // full" + label "Paid" + a Status row "PAID", under a pill "COMMITTED"): the critique's exact
 // duplicate-fact finding. The headline now carries the outcome in ONE counter word; the amount
 // label survives ONLY where it is load-bearing (an unsettled sale's number is the BALANCE, not
 // the total: that distinction stays spoken). A settled sale's big number needs no caption:
 // 'Paid' above it already says what it is.
  if (paidInFull) {
    return {
      headline: 'Paid',
      settled: true,
      amountLabel: '',
      amountCents: sale.total_cents,
      balanceCents: 0,
      tenderLabel: tenderLabel(sale.tender),
      note,
    };
  }
  if (deposit > 0) {
    // HH note: the reference subtracted the deposit from the total here; HH's core computes the
    // balance, so the port asks the host seam instead (CLOSURE.md names it).
    const balance = balanceAfterDepositCents(sale.total_cents, deposit);
    return {
      headline: 'Deposit taken',
      settled: false,
      amountLabel: 'Balance due',
      amountCents: balance,
      balanceCents: balance,
      tenderLabel: tenderLabel(sale.tender),
      note,
    };
  }
  return {
    headline: isCOD ? 'COD: unpaid' : 'On account',
    settled: false,
    amountLabel: 'Balance due',
    amountCents: sale.total_cents,
    balanceCents: sale.total_cents,
    tenderLabel: tenderLabel(sale.tender),
    note,
  };
}

/**
 * DIB-512 · the sale-panel pill AFTER a ring, mirroring the confirmation card's one word: the
 * server's raw lifecycle status ("COMMITTED") is warehouse vocabulary, not counter chrome, and
 * showing it beside "Paid in full" was two vocabularies for one fact. Draft stays the caller's
 * own word for the un-rung state.
 */
export function salePillLabel(sale: {
  tender: string;
  sale_type: string;
  total_cents: number;
  payment_id?: string;
  deposit_cents?: number;
}): string {
  const c = saleConfirmation(sale);
  if (c.settled) return 'Paid';
  if ((sale.deposit_cents ?? 0) > 0) return 'Deposit';
  return sale.sale_type === 'COD' ? 'COD' : 'On account';
}

// ── DIB-488 rev 5 (fix wave): counter-hardening pure helpers ─────────────────────────────────────

/**
 * cleanQty: the ticket-line quantity guard. A scanner burst landing in a focused qty box types a
 * SKU whose Number() is NaN, and `NaN < 1` is false: so the old `if (qty < 1) qty = 1` guard let a
 * literal NaN onto the line (stuck steppers, silent stale totals, a 422 only at ring time). Any
 * non-finite or sub-1 entry snaps to 1: the same answer an emptied box has always produced.
 */
export function cleanQty(qty: number): number {
  return Number.isFinite(qty) && qty >= 1 ? qty : 1;
}

/** The remembered in-flight ring attempt: the request fingerprint and the key minted for it. */
export interface RingKeyMemo {
  fp: string;
  key: string;
}

/**
 * stableRingKey: makes "retry; the sale is replay-safe" TRUE. The idempotency key used to be a
 * local const minted per ringSale() invocation, so a retry after a network failure carried a NEW
 * key: if the first request committed server-side and only the response was lost (the exact case
 * idempotency keys exist for), the advertised retry double-charged the customer.
 *
 * Rule: an IDENTICAL request body (same lines, customer, tender, till: the fingerprint) reuses
 * the previous attempt's key verbatim; ANY change to the sale honestly re-mints. The caller clears
 * the memo on success so the next sale never inherits a key.
 */
export function stableRingKey(prev: RingKeyMemo | null, fp: string, mint: () => string): RingKeyMemo {
  if (prev && prev.fp === fp && prev.key !== '') return prev;
  return { fp, key: mint() };
}

// ── DIB-488 rev 6: mock-parity + cash-tendered pure helpers ──────────────────────────────────────

/**
 * customerInitials: the identity card's chip (rev 6, item A). First letter of the first two words
 * of the name, uppercased; a one-word name gives one letter; an empty/degenerate name gives '?'
 * rather than an empty chip.
 */
export function customerInitials(name: string): string {
  const words = (name ?? '').trim().split(/\s+/).filter((w) => /[\p{L}\p{N}]/u.test(w));
  const letters = words.slice(0, 2).map((w) => {
    const m = w.match(/[\p{L}\p{N}]/u);
    return m ? m[0].toUpperCase() : '';
  });
  const out = letters.join('');
  return out === '' ? '?' : out;
}

/**
 * accountTypeTag: the identity card's account-type tag in counter words. The wire values are the
 * customer_type codes (CONTRACTOR / RETAIL / …); unknown codes render title-cased rather than
 * shouting an internal constant at the counter. Walk-in (no account) is the caller's own 'Cash'.
 */
export function accountTypeTag(customerType: string): string {
  const t = (customerType ?? '').trim();
  if (t === '') return 'Account';
  return t.charAt(0).toUpperCase() + t.slice(1).toLowerCase();
}

/** One parsed answer for the optional cash-tendered quick entry (rev 6, item F). */
export type { CashTenderedPlan } from './ports.js';

// HH note: the reference parsed the typed dollars through a float and computed the change due
// here. HH's rule is that the core computes money, so the computation is left out of the port
// and the seam lives in ports.ts with the reference's signature (CLOSURE.md names it).
export { cashTenderedPlan } from './ports.js';

/**
 * counterRingMessage: the ring-failure line as the COUNTER should read it. ringErrorMessage()
 * (above) stays the verbatim-detail arm the tests pin; this wraps it for display: the Go module
 * prefix ("pos: ") is internal vocabulary and is stripped, and the two refusals a clerk actually
 * hits mid-shift are reworded to name their fix. Everything else passes through untouched.
 */
export function counterRingMessage(err: { status: number; error: string }): string {
  return humanizeRingError(ringErrorMessage(err));
}

/**
 * INTERNAL_VOCABULARY: the shapes that mean "this sentence was written for a developer".
 *
 * A snake_case identifier is a column name (`deposit_method`, `po_job_ref`, `idempotency_key`). A
 * SCREAMING_SNAKE token is a check-constraint value (`WILL_CALL`, `ACCOUNT_CREDIT`). A UUID is a
 * primary key. None of the three belongs in front of a person with a contractor waiting.
 *
 * A single all-caps word is deliberately NOT matched. ACCOUNT, CASH, CARD, CHEQUE, PICKUP and
 * DELIVERY are words a counter says out loud; it is the underscore that marks a constant.
 */
const INTERNAL_VOCABULARY =
  /\b[a-z][a-z0-9]*(?:_[a-z0-9]+)+\b|\b[A-Z][A-Z0-9]*(?:_[A-Z0-9]+)+\b|[0-9a-f]{8}-[0-9a-f]{4}-/;

/**
 * COUNTER_WORDING: DIB-485. Every refusal on the ring path that named a DATABASE COLUMN or a
 * check-constraint value at the counter, said again in the counter's words.
 *
 * All six of these were reachable and all six were ACTIONABLE, which is why each one is reworded
 * rather than left to the floor below. A clerk who is told "the job address is too long, keep it to
 * 200 characters" fixes it in five seconds; a clerk told "that did not go through" has to guess.
 * Trading a leak for a shrug would be a worse defect than the leak.
 *
 * The return reasons match the words _approval-copy.ts already uses on the approvals screens, so a
 * return refused at the counter and a return reviewed in a worklist name the same reason the same
 * way.
 */
const COUNTER_WORDING: ReadonlyArray<readonly [RegExp, string]> = [
  [/^idempotency_key/, 'That sale did not carry its reference: ring it again, it is replay-safe.'],
  [/^deposit_method must be/, 'A deposit has to be cash, card or cheque.'],
  [/^refund_method must be/, 'A refund has to go back as cash, card or account credit.'],
  [/^po_job_ref/, 'The PO or job reference is too long: keep it to 64 characters.'],
  [/^fulfilment must be/, 'Choose pickup, will-call or delivery.'],
  [/^job_address/, 'The job address is too long: keep it to 200 characters.'],
  [/^job_reference/, 'The job reference is too long: keep it to 64 characters.'],
  [/^cash_tendered_cents must cover/, 'The cash tendered has to cover the sale total.'],
  [/^cash_tendered_cents rides only/, 'Cash tendered applies only to a plain cash sale that is paid in full.'],
  [/^a restocking fee is only supported/, 'A restocking fee can only go on a refund to account credit.'],
  [/^reason_code must be/, 'Pick a return reason: damaged, defect, wrong item, not needed, or other.'],
];

/** The wording map behind counterRingMessage: exported for direct reuse and unit tests. */
export function humanizeRingError(msg: string): string {
  const stripped = msg.replace(/^pos:\s*/, '');
  if (stripped === 'open a till session first') {
    return 'No till session is open: open one with the Open Session button, then complete the sale.';
  }
  const anchor = stripped.match(/^line (\d+): no retail anchor for product$/);
  if (anchor) {
    return `Line ${anchor[1]} has no price on file: price the product (Pricing), or remove the line.`;
  }
  if (stripped === 'no retail anchor for product') {
    return 'A line has no price on file: price the product (Pricing), or remove the line.';
  }

  for (const [pattern, sentence] of COUNTER_WORDING) {
    if (pattern.test(stripped)) return sentence;
  }

 // THE FLOOR. Everything above is a sentence somebody wrote for this screen. Anything else is a
 // server message we have not read, and the honest default is to show it: a clerk who can see
 // "a split tender may carry at most 6 legs" can act on it, and hiding that behind a generic
 // sentence would be the worse defect. But a message still carrying a column name, a constraint
 // value or a key is not a sentence, and passing it through is how DIB-485 happened. Those get the
 // generic line instead, which says the one thing that is always true and always useful: the sale
 // did not go through, and nothing was rung.
  if (INTERNAL_VOCABULARY.test(stripped)) {
    return 'That did not go through, and nothing was rung: check the sale and try again.';
  }
  return stripped;
}

// ── DIB-DUPLINE: one product, one line ─────────────────────────────────────────────────────────
//
// Reported from the live counter: "adding the same product to a sale twice creates a second line
// instead of raising the quantity on the existing one." addProduct() appended unconditionally, so
// three scans of one SKU built three lines of 1 and the clerk's running quantity was scattered
// across the ticket.
//
// The rule is NOT "same product id → merge". Merging rewrites a line the clerk may have shaped by
// hand, and a merge that silently drops that shaping is a worse bug than the duplicate. So a merge
// is allowed only when the two adds are genuinely the SAME THING: same product, same sell unit, and
// a target line that carries no line-level state a merge would destroy.
//
// Everything below is pure and DOM-free: the decision is unit-tested here, and pos-terminal.ts
// only executes it (and hands the merged line back to the SAME pricing engine a fresh add uses).

/**
 * What a merge is allowed to do with each field of a cart line.
 *
 * 'carry' : the field survives a merge honestly: it is either the merge KEY itself (product,
 * uom), the quantity being summed, or a value the pricing engine re-derives for the
 * merged line anyway (every money field, the breakdown, the price-pending flag).
 * 'block' : the field is the clerk's own work on THIS line. A merge would destroy it, so a line
 * carrying it never accepts a merge; the add appends a second line instead, and the
 * terminal says why.
 *
 * This map is typed `Record<keyof Required<CartLine>, …>` ON PURPOSE. The day a lane adds a field
 * to CartLine: a manual price override, a per-line discount, a line note, a substituted UoM: the
 * BUILD breaks here until that lane classifies it. Nothing new can start being silently merged
 * away by omission; the decision has to be written down.
 */
export type LineFieldRule = 'carry' | 'block';

export const LINE_FIELD_RULES: Record<keyof Required<CartLine>, LineFieldRule> = {
  product: 'carry', // the merge key
  uom: 'carry', // the merge key's second half: compared, never overwritten
  qty: 'carry', // the summed field
  unitPriceCents: 'carry', // re-derived: reprice() asks the engine for the merged quantity
  priceUom: 'carry', // re-derived
  lineTotalCents: 'carry', // re-derived
  breakdown: 'carry', // re-derived
  priceMissing: 'carry', // re-derived (a merge that resolves clears it)
};

/**
 * lineMergeBlockers: the RUNTIME half of the same guard, and it fails CLOSED.
 *
 * The compile-time map above protects this build. This protects the ones it cannot see: the till
 * snapshot is rehydrated verbatim from storage, so a ticket parked by a NEWER build can hand this
 * one a line carrying a field this build has never heard of. An unknown field is treated as the
 * clerk's own work: it blocks the merge: because the alternative is deleting a price override
 * written by a build we do not understand.
 *
 * A 'block' field that is absent (undefined/null/'') is not blocking: an untouched line is still
 * an untouched line.
 */
export function lineMergeBlockers(line: unknown): string[] {
  if (!line || typeof line !== 'object') return ['not-a-line'];
  const out: string[] = [];
  for (const [key, value] of Object.entries(line as Record<string, unknown>)) {
    const rule = (LINE_FIELD_RULES as Record<string, LineFieldRule | undefined>)[key];
    if (rule === 'carry') continue;
    if (value === undefined || value === null || value === '') continue; // present-but-empty is untouched
    out.push(key); // 'block', or a field this build does not know: same answer either way
  }
  return out.sort();
}

/** UoM comparison for the merge key: 'sf' and ' SF ' are one unit, not two. */
export function sameUom(a: string, b: string): boolean {
  return a.trim().toUpperCase() === b.trim().toUpperCase();
}

/** Why an add appended instead of merging: the terminal turns this into the on-screen outcome. */
export type AppendReason =
 /** The product is not on the ticket at all. Nothing to say: the new line IS the outcome. */
  | 'new-product'
 /** The product is on the ticket, but in a different sell unit. Two units, two lines. */
  | 'different-uom'
 /** The product is on the ticket, but that line carries the clerk's own line-level work. */
  | 'line-state';

export interface AddDecision {
  action: 'merge' | 'append';
 /** The merged line's index, or -1 on an append. */
  index: number;
 /** The target line's quantity BEFORE the add (0 on an append). */
  fromQty: number;
 /** The quantity the affected line carries AFTER the add (the added qty on an append). */
  toQty: number;
 /** On an append: why. On a merge: 'merged'. */
  reason: AppendReason | 'merged';
 /** On a 'different-uom' append: the unit the existing line is in (for the outcome line). */
  blockedByUom?: string;
}

/**
 * decideAdd: the whole rule, in one pure function.
 *
 * Walks the ticket for a line that can honestly absorb this add and returns the FIRST one. When
 * some line holds the product but cannot take the add, the reason travels back so the terminal can
 * say why a second line appeared instead of a quantity going up: an unexplained second line is
 * the bug we are fixing, wearing a different hat.
 *
 * A 'different-uom' answer outranks a 'line-state' answer in the reporting, because the unit is
 * the thing a clerk can see on the ticket and reconcile at a glance.
 */
export function decideAdd(
  cart: readonly CartLine[],
  productId: string,
  uom: string,
  addQty: number,
): AddDecision {
  const add = cleanQty(addQty);
  let reason: AppendReason = 'new-product';
  let blockedByUom: string | undefined;
  for (let i = 0; i < cart.length; i++) {
    const line = cart[i];
    if (!line || line.product?.id !== productId) continue;
    if (lineMergeBlockers(line).length > 0) {
      if (reason === 'new-product') reason = 'line-state';
      continue;
    }
    if (!sameUom(line.uom, uom)) {
      reason = 'different-uom';
      blockedByUom = line.uom;
      continue;
    }
    const fromQty = cleanQty(line.qty);
    return { action: 'merge', index: i, fromQty, toQty: fromQty + add, reason: 'merged' };
  }
  return { action: 'append', index: -1, fromQty: 0, toQty: add, reason, blockedByUom };
}

/**
 * addOutcomeMessage: what the counter is told, in the scan box's existing role="status" line.
 *
 * A plain first add says NOTHING here: the line appearing on the ticket is the outcome, and the
 * scan box already confirms the scan itself. Only the two surprising answers speak: a quantity
 * that moved on a line already on screen, and a second line that was deliberate.
 */
export function addOutcomeMessage(
  d: AddDecision,
  productName: string,
  uom: string,
): { msg: string; kind: 'ok' | 'warn' } | null {
  if (d.action === 'merge') {
    return { msg: `${productName} · ${d.fromQty} → ${d.toQty} ${uom}`, kind: 'ok' };
  }
  if (d.reason === 'different-uom') {
    return {
      msg: `${productName} added as a second line: the line on the ticket is in ${d.blockedByUom}, this one is in ${uom}.`,
      kind: 'warn',
    };
  }
  if (d.reason === 'line-state') {
    return {
      msg: `${productName} added as a second line: the line on the ticket carries its own pricing and was left alone.`,
      kind: 'warn',
    };
  }
  return null; // new-product: the line is the answer
}

/**
 * echoAddMessage: what the counter is told when an add was SWALLOWED as the second half of one
 * gesture (DIB-553, bounce 2).
 *
 * The guard's first cut discarded the second press with NO feedback at all. A real add flashes the
 * line and says "Black Mulch · 1 → 2 CYD"; a suppressed one said nothing, so the two gestures a
 * person is most likely to make: a habitual double-click, and two fast Enter presses meaning "two
 * of these": were answered by the till going quiet. Silence is the wrong answer to an ambiguous
 * press: it reads as a dropped keystroke, and it hides the one case where the guard is wrong.
 *
 * So it says what happened, what the ticket holds now, and where the answer is. `qty` is the
 * quantity ON the line after the swallow: the same number a merge would have shown: so the
 * outcome is readable without looking away, and `kind: 'warn'` because this is the one add outcome
 * where the till deliberately did something other than what the hand asked for.
 *
 * Returns null when the product is not on the ticket, which cannot happen from the shipped surfaces
 * (an echo implies the first half landed) but would be a lie on the status line if it did.
 */
export function echoAddMessage(
  productName: string,
  qty: number,
  uom: string,
): { msg: string; kind: 'ok' | 'warn' } | null {
  if (!(qty > 0)) return null;
  return {
    msg: `${productName} · still ${qty} ${uom}: that second press was too quick to be a second unit. Change the amount on the line.`,
    kind: 'warn',
  };
}

// ─────────────────────────────────────────────────────────────────────────────────────────────
// REPRICE STALENESS: decision D17, round 3.
//
// THE DEFECT THIS EXISTS TO CLOSE, and it was worse than the double-click it sits beside.
// `reprice()` awaits /pricing/resolve and then writes the answer back into `this.cart[idx]`. Round
// 2 added a guard that the slot still held the same PRODUCT, which genuinely closed line
// resurrection across New Sale / Clear / Park. But the write-back was `{...line, …money }` where
// `line` was the whole PRE-AWAIT SNAPSHOT: so product identity survived the guard and the
// operator's QUANTITY was restored from the snapshot. Measured, with route interception on
// POST /api/v1/pricing/resolve and the first answer held 600ms:
//
// • add Red Mulch, then add it again 800ms later (deliberate, far outside the 300ms echo
// window): the ticket went 1 → 2 → and then back to **1** when the first add's stale answer
// landed. A deliberate unit silently deleted, on the surface that takes the money.
// • add, then type 7 in the line's own qty box at +500ms: 7 → **1**, silently.
// • ticket A repriced, New Sale, ticket B adds the same product at qty 9: A's answer lands on B
// and takes it 9 → **1**: a previous customer's answer on the next customer's ticket, which
// is the exact sentence the round-2 guard's own docstring claimed to prevent.
// • park Red×6, fresh ticket, add Red, resume the parked ticket: the resumed line 6 → **1**.
//
// The control isolates the cause: delay the SECOND resolve instead so the answers arrive in order,
// and the typed quantity survives. It is out-of-order arrival plus a snapshot write-back: ordinary
// jitter on one endpoint, not exotic latency.
//
// THE IDIOM WAS ALREADY IN THE FILE, three hundred lines away: `refreshAddons()` guards its await
// with `if (seq !== this.ticketFactsSeq) return`. This is that, made per-SLOT (two lines can be in
// flight at once and one must not cancel the other) and made pure so it can be tested: the rule
// inside a Lit component cannot be, decorators being a syntax error under the type-stripping
// runner. That is why round 2's `stillOurs()` had ZERO coverage in 3311 frontend tests and forcing
// it to `true` reproduced the original defect with nothing going red.
//
// BOTH HALVES ARE HERE AND BOTH MATTER. `repriceAnswerIsCurrent` stops a stale answer applying at
// all; `applyPriceAnswer` writes only the money fields onto the line that is THERE, so even an
// answer that slips past the guard cannot carry old operator state back onto the ticket.

/** What one line looked like at the instant its pricing round trip was issued. */
export interface RepriceStamp {
 /** The cart generation: bumped only when the cart is REPLACED wholesale (new sale, park, resume, snapshot). */
  gen: number;
 /** The slot's own token: bumped on every reprice issued for that slot. */
  seq: number;
  productId: string;
  qty: number;
  uom: string;
}

/** Stamp a line at the moment its round trip starts. */
export function repriceStamp(
  gen: number,
  seq: number,
  line: { product: { id: string }; qty: number; uom: string },
): RepriceStamp {
  return { gen, seq, productId: line.product.id, qty: line.qty, uom: line.uom };
}

/**
 * Is a pricing answer still about the line that is in the slot NOW?
 *
 * Four independent reasons to say no, each of which was measured biting a different gesture:
 * gen : the ticket was replaced wholesale, so this answer belongs to a previous customer.
 * seq : a NEWER reprice was issued for this slot, so this answer has been superseded (the
 * double-add and the qty-box edit both land here: every quantity change reprices).
 * product: the slot holds something else entirely (round 2's guard; it was right, just alone).
 * qty/uom: the answer was computed for a quantity or a unit the line no longer has, so its
 * `line_total_cents` is arithmetic about a ticket that does not exist. Volume breaks make
 * this a PRICE error and not only a total error.
 *
 * Deliberately fails closed on a missing line: an answer for a slot that is gone applies to nothing.
 */
export function repriceAnswerIsCurrent(
  stamp: RepriceStamp,
  gen: number,
  seq: number,
  line: { product: { id: string }; qty: number; uom: string } | undefined | null,
): boolean {
  if (gen !== stamp.gen) return false;
  if (seq !== stamp.seq) return false;
  if (!line) return false;
  if (line.product.id !== stamp.productId) return false;
  if (line.qty !== stamp.qty) return false;
  if (!sameUom(line.uom, stamp.uom)) return false;
  return true;
}

/**
 * WHO OWNS EACH FIELD OF A CART LINE when a pricing answer comes back.
 *
 * Same shape and the same reason as LINE_FIELD_RULES above: typed `Record<keyof Required<CartLine>,
 * …>` so the day a lane adds a field: a manual price override, a per-line discount, a line note:
 * the BUILD breaks here until that lane says whether a late pricing answer is allowed to overwrite
 * it. Round 2's spread of a pre-await snapshot was exactly the silent "everything" answer this map
 * makes impossible to give by omission.
 */
export type PriceFieldOwner =
 /** The engine computed it; a current answer is authoritative and overwrites. */
  | 'engine'
 /** The person at the till owns it. A pricing answer NEVER writes it: that is the money defect. */
  | 'operator';

export const PRICE_ANSWER_FIELDS: Record<keyof Required<CartLine>, PriceFieldOwner> = {
  product: 'operator', // what they put on the ticket
  qty: 'operator', // THE defect: restored from a stale snapshot, deleting deliberate units
  uom: 'operator', // the sell unit the add chose; the answer is priced FOR it, not ABOUT it
  unitPriceCents: 'engine',
  priceUom: 'engine',
  lineTotalCents: 'engine',
  breakdown: 'engine',
  priceMissing: 'engine',
};

/** The money half of a /pricing/resolve answer, in cart-line terms. */
export interface PriceAnswer {
  unitPriceCents: number;
  priceUom: string;
  lineTotalCents: number;
  breakdown: CartLine['breakdown'];
  priceMissing: boolean;
}

/**
 * Write a pricing answer onto the line that is THERE.
 *
 * Takes the CURRENT line, never the pre-await snapshot, and copies across only the fields
 * PRICE_ANSWER_FIELDS marks 'engine'. The map drives the copy rather than describing it, so the
 * census cannot rot into a comment that disagrees with the code under it.
 */
export function applyPriceAnswer(current: CartLine, answer: PriceAnswer): CartLine {
  const out: Record<string, unknown> = { ...current };
  const src = answer as unknown as Record<string, unknown>;
  for (const [field, owner] of Object.entries(PRICE_ANSWER_FIELDS)) {
    if (owner === 'engine') out[field] = src[field];
  }
  return out as unknown as CartLine;
}

/**
 * D17, round 3: resuming a parked ticket is about to DISCARD the ticket on screen.
 *
 * `onParkedResume` awaits `listProducts()` (a full catalogue read) and then calls `newSale()`,
 * which empties the cart. Anything the operator put on the ticket while that round trip was in
 * flight is gone, and nothing asked and nothing said. It is the same shape as the reprice defect:
 * await something slow, then write into state the user may have changed: except the state it
 * writes over is the whole ticket.
 *
 * The wider case is the same case: resuming while ANY ticket is open discards it. Clear sale and
 * New Sale both ask first (`clearSaleNeedsConfirm`) and resume did not, which was an inconsistency
 * before the round trip made it a race. So the rule is the same rule, asked at the same moment the
 * other two ask it: with the count, because "3 lines" is what makes the question answerable.
 *
 * Returns null when there is nothing to lose: an empty terminal must never nag.
 */
export function resumeDiscardMessage(cartLength: number, ticketNo: string): string | null {
  if (!clearSaleNeedsConfirm(cartLength)) return null;
  const n = cartLength;
  return (
    `Resume ${ticketNo}? The ${n} line${n === 1 ? '' : 's'} on the ticket in front of you ` +
    `will be discarded: park it first if you need it.`
  );
}
