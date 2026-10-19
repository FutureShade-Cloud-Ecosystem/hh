// Ported from hardscapeos_dibbits f8f4973db317b92296ae8ec7e62a3f54f5dc82ab app/src/_order-depth.ts
// (the change-order half only; the multi-ship and availability halves serve surfaces this port does
// not carry); adapted for HH.

// _order-change.ts: SCOPE:F10 pure helpers for the post-pick change-order surface.
//
// House convention (_pos.ts): DOM-heavy Lit logic is not unit-tested, but the DECISION and
// FORMATTING logic it leans on is extracted here and IS tested (_order-change.test.mjs). The rules
// below decide what a counter user is allowed to press and what colour a chip is, and both are
// things that must be verifiable without driving a browser.
//
// THE ONE RULE THAT GOVERNS EVERY DECISION IN THIS FILE
// The client NEVER evaluates a workflow matrix. Which change-order actions are available is
// computed server-side (core order/change.go) and rides here as `next_actions`. Rendering from the
// server's answer is what makes it impossible to offer a control the server would refuse, and means
// a ratified rule change needs no client redeploy. An ABSENT list means "offer nothing", never
// "offer everything": an older server that does not send the field must not unlock the whole
// surface.
//
// THE TWO HH ADDITIONS the reference did not carry
// 1. wallWords: the role gate answers a walled route with the shared Forbidden body, which names
//    nobody. ROLE-APP-OBLIGATIONS 1: the app maps that refusal to plain words naming who may act,
//    the roles as the default matrix (core roleconfig/default.json) names them. The map below is
//    the census rows of the change routes intersected with that matrix, recorded sub item by sub
//    item in the lane's report.
// 2. statusIsPostPick: the client-side FIRST PICK of which flow the one Change order action opens.
//    It mirrors the core's own status-ladder fallback (order/change.go statusIsPostPick) so the
//    till and the server agree wherever the status ladder is the authority; where the tasks
//    substrate says the pick started earlier than the ladder, the server's word lands at the raise
//    and the surface follows it.

import { changeMoney } from './_order-edit.js';

/** One delta row on a change order (core order.ChangeLine). */
export interface ChangeLine {
  seq: number;
  op: string;
  line_no?: number;
  product_id?: string;
  product_name?: string;
  description: string;
  qty_entered_new?: string;
  uom_entered?: string;
  qty_base_before: number;
  qty_base_after: number;
  qty_delta_base: number;
  unit_price_cents: number;
  price_uom?: string;
  line_delta_cents: number;
}

/** A change order (core order.ChangeOrder). */
export interface ChangeOrder {
  id: string;
  order_id: string;
  branch_id: string;
  change_no: string;
  status: string;
  reason: string;
  delta_cents: number;
  finance_threshold_cents: number;
  finance_required: boolean;
  order_status_at_request: string;
  pick_started_at_request: boolean;
  requested_by?: string;
  requested_at: string;
  yard_acked_by?: string;
  yard_acked_at?: string;
  yard_ack_note?: string;
  yard_task_id?: string;
  finance_approved_by?: string;
  finance_approved_at?: string;
  finance_note?: string;
  decision_note?: string;
  applied_at?: string;
  lines: ChangeLine[];
  next_actions?: string[];
}

/** One ACTIVE stock hold (core order.OrderHold), the half of the reservation read this surface renders. */
export interface OrderHold {
  reservation_id: string;
  product_id: string;
  product_name: string;
  sku: string;
  branch_id: string;
  hold_mode: string;
  qty_base: number;
  status: string;
  created_at: string;
}

// -- change-order actions -------------------------------------------------------------------------

export const ACTION_YARD_ACK = 'yard_ack';
export const ACTION_FINANCE_APPROVE = 'finance_approve';
export const ACTION_APPLY = 'apply';
export const ACTION_REJECT = 'reject';
export const ACTION_CANCEL = 'cancel';

/**
 * canDoChangeAction reports whether the SERVER offered this action on this change.
 *
 * Fails closed on an absent list, for the reason in the file header.
 */
export function canDoChangeAction(co: ChangeOrder | null, action: string): boolean {
  if (!co || !Array.isArray(co.next_actions)) return false;
  return co.next_actions.includes(action);
}

/** The POST path segment for each change-order action (mirrors RegisterChangeRoutes). */
const CHANGE_ACTION_PATH: Record<string, string> = {
  [ACTION_YARD_ACK]: 'yard-ack',
  [ACTION_FINANCE_APPROVE]: 'finance-approve',
  [ACTION_APPLY]: 'apply',
  [ACTION_REJECT]: 'reject',
  [ACTION_CANCEL]: 'cancel',
};

/** changeActionPath maps an action to its endpoint; '' for an action this client does not know. */
export function changeActionPath(action: string): string {
  return CHANGE_ACTION_PATH[action] ?? '';
}

/**
 * offeredChangeActions is the server's next_actions narrowed to the ones this surface can perform: an action
 * with no endpoint here (the one tap `sign_apply` is the Manager Home's, the till applies in two steps) is not
 * rendered as a button that does nothing. An absent list offers nothing.
 */
export function offeredChangeActions(co: ChangeOrder | null): string[] {
  if (!co || !Array.isArray(co.next_actions)) return [];
  return co.next_actions.filter((a) => changeActionPath(a) !== '');
}

const CHANGE_ACTION_LABEL: Record<string, string> = {
  [ACTION_YARD_ACK]: 'Yard: acknowledge',
  [ACTION_FINANCE_APPROVE]: 'Approve (finance)',
  [ACTION_APPLY]: 'Apply change',
  [ACTION_REJECT]: 'Reject',
  [ACTION_CANCEL]: 'Withdraw',
};

/** changeActionLabel renders an action for a button, falling back to the raw code so an action the
 *  server adds later still appears rather than silently vanishing. */
export function changeActionLabel(action: string): string {
  return CHANGE_ACTION_LABEL[action] ?? action;
}

/**
 * The shared Forbidden body the role gate writes (core authz middleware.go, BodyForbidden). Exactly
 * this word is the wall; any other sentence is the server's own and stands verbatim.
 */
export const FORBIDDEN_WORD = 'Forbidden';

/**
 * wallWords names, in plain words, who may act where the role gate walls a change route. The roles
 * are the default matrix's own (roleconfig/default.json), read against each route's census row:
 *
 *   yard-ack          x-access yard-pick work    -> yard_team, yard_manager
 *   finance-approve   x-access money-desk manage -> financial_controller, general_manager
 *   apply             x-access quote-desk manage -> sales_manager
 *
 * A dealer's own configuration can move these cells; the wall sentence says what the SHIPPED matrix
 * says, and the server's own refusal always has the last word.
 */
const WALL_WORDS: Record<string, string> = {
  [ACTION_YARD_ACK]: 'Only the yard team or the yard manager may acknowledge a change for the yard.',
  [ACTION_FINANCE_APPROVE]:
    'Only the financial controller or the general manager may sign a change over the threshold.',
  [ACTION_APPLY]: 'Only the sales manager may apply a change to the order.',
};

/** wallWords returns the plain sentence for a walled action, or '' for an unwalled one. */
export function wallWords(action: string): string {
  return WALL_WORDS[action] ?? '';
}

/**
 * refusalWords renders one refusal for the surface: the server's own sentence verbatim, except the
 * role gate's bare Forbidden word, which names nobody and is replaced by the plain wall sentence
 * that names who may act. A wall sentence the map does not carry keeps the server's word: an honest
 * refusal beats an invented permission.
 */
export function refusalWords(action: string, serverMessage: string): string {
  if (serverMessage === FORBIDDEN_WORD) {
    const wall = wallWords(action);
    if (wall !== '') return wall;
  }
  return serverMessage;
}

/**
 * statusIsPostPick mirrors the core's own status-ladder fallback (order/change.go): PICKING, READY
 * and PARTIALLY_FULFILLED mean the yard has the goods, which is post-pick. Used ONLY to pick which
 * flow the one Change order action opens first; the server re-decides at the raise and its 409
 * names the line-edit path when the ladder disagrees.
 */
export function statusIsPostPick(status: string): boolean {
  return status === 'PICKING' || status === 'READY' || status === 'PARTIALLY_FULFILLED';
}

/**
 * changeStageSummary describes WHERE a change sits in the two-signature ladder, in one sentence a
 * counter user can act on. It is the copy that answers "why can't I press Apply?", which is the
 * question this whole surface exists to make answerable.
 *
 * It reads the server's status and finance_required: it does NOT re-derive the threshold, because
 * the threshold in force is stamped on the row and re-deriving it from today's configuration would
 * answer a different question (order/change.go states this).
 */
export function changeStageSummary(co: ChangeOrder): string {
  switch (co.status) {
    case 'PENDING_YARD':
      return co.finance_required
        ? 'Waiting on the yard. This change is over the approval threshold, so finance signs after them.'
        : 'Waiting on the yard.';
    case 'PENDING_FINANCE':
      return `The yard has acknowledged this. It is over the ${changeMoney(
        co.finance_threshold_cents,
      )} threshold, so it needs a finance approval before it can be applied.`;
    case 'APPROVED':
      return 'Approved. Applying writes the change to the order and re-runs its stock reservations.';
    case 'APPLIED':
      return 'Applied to the order.';
    case 'REJECTED':
      return 'Rejected.';
    case 'CANCELLED':
      return 'Withdrawn.';
    default:
      return co.status;
  }
}

/** Chip class per change-order status. Unmapped statuses fall back to neutral so a backend-first
 *  status still renders. */
const CHANGE_STATUS_CHIP: Record<string, string> = {
  PENDING_YARD: 'chip amber',
  PENDING_FINANCE: 'chip amber',
  APPROVED: 'chip blue',
  APPLIED: 'chip green',
  REJECTED: 'chip red',
  CANCELLED: 'chip',
};

export function changeStatusChip(status: string): string {
  return CHANGE_STATUS_CHIP[status] ?? 'chip';
}

/**
 * formatQty renders a DECIMAL(19,4) quantity without trailing-zero noise: 12.0000 reads "12",
 * 12.5000 reads "12.5". A quantity padded to four decimals on every row is unreadable on a screen
 * that shows twenty of them.
 */
export function formatQty(q: number): string {
  if (!Number.isFinite(q)) return '0';
  const rounded = Math.round(q * 10000) / 10000;
  return String(rounded);
}

/**
 * describeDelta renders one delta row as a sentence: what is changing and by how much.
 *
 * A REMOVE says "removed", not "0": "line 3 to 0" reads as a quantity edit, and the two are
 * different things to the person signing them.
 */
export function describeDelta(l: ChangeLine): string {
  const name = l.product_name || l.description || l.product_id || 'line';
  switch (l.op) {
    case 'ADD':
      return `Add ${formatQty(l.qty_base_after)} of ${name}`;
    case 'REMOVE':
      return `Remove line ${l.line_no}: ${name} (${formatQty(l.qty_base_before)})`;
    case 'QTY':
      return `Line ${l.line_no} ${name}: ${formatQty(l.qty_base_before)} to ${formatQty(l.qty_base_after)}`;
    default:
      return `${l.op} ${name}`;
  }
}

/**
 * thrownMessage renders a CAUGHT EXCEPTION (a dropped connection, an aborted fetch) as one
 * sentence.
 *
 * It is distinct from a non-ok Response body: the two are different failures and deserve different
 * words. A network drop is not a refusal, and reporting it as one sends a user looking for a
 * permission they already have.
 */
export function thrownMessage(e: unknown): string {
  if (e instanceof Error && e.message) return e.message;
  return 'could not reach the server';
}
