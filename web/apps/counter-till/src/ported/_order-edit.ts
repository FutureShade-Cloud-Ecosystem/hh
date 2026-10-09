// Ported from hardscapeos_dibbits f8f4973db317b92296ae8ec7e62a3f54f5dc82ab app/src/_order-edit.ts; adapted for HH.

// _order-edit.ts: DIB-175 pure helpers for the staff order-edit surface.
//
// House convention (_pos.ts): DOM-heavy Lit component logic is not unit-tested, but the
// decision + formatting logic it leans on is extracted here and IS tested (_order-edit.test.mjs).
// That keeps the rules that decide what a counter user may touch out of the render function, where
// they would only ever be verifiable by driving a browser.
//
// HH adaptation: the money rendering goes through the shared dollars word (pos-kit's
// _money-format, the one reviewed spelling the whole ported surface uses), and the empty marker is
// the word '(none)' rather than the reference's dash glyph (the house rule bans the dash characters
// everywhere, and a word reads on a screen reader where a lone glyph does not).

import { formatDollars } from '@hardscapehouse/pos-kit/src/ported/_money-format.js';

/** The staff order read (core order.OrderView). */
export interface OrderView {
  id: string;
  status: string;
  branch_id: string;
  legal_actions: string[];
  /** DIB-175: which fields THIS caller may edit right now, computed server-side from editRules. */
  editable_fields?: string[];
}

/** One column-level change (core order.OrderFieldChange). */
export interface OrderFieldChange {
  field: string;
  line_no: number;
  before?: string;
  after?: string;
  actor_id?: string;
  actor_name?: string;
  request_id?: string;
  occurred_at: string;
}

export interface OrderEditHistory {
  order_id: string;
  changes: OrderFieldChange[];
}

/** The edit-command vocabulary the server reports in editable_fields (core order.EditField). */
export const EDIT_LINE_QTY = 'line_qty';
export const EDIT_LINE_ADD = 'line_add';
export const EDIT_LINE_REMOVE = 'line_remove';
export const EDIT_REQUESTED_DATE = 'requested_date';

/**
 * canEdit reports whether the SERVER offered this edit field.
 *
 * The client never evaluates the edit-state matrix itself: that lives once, server-side, in
 * editRules (core order/edit.go), and rides here as editable_fields. Rendering from the server's
 * answer is what makes it impossible to offer a control the server would refuse, and means a
 * ratified matrix change needs no client redeploy. A missing/absent list means "offer nothing",
 * never "offer everything": an older server that does not send the field must not unlock the whole
 * surface.
 */
export function canEdit(view: OrderView | null, field: string): boolean {
  return !!view?.editable_fields?.includes(field);
}

/** True when the caller may perform ANY edit on this order: drives whether the panel renders at all. */
export function hasAnyEdit(view: OrderView | null): boolean {
  return (view?.editable_fields?.length ?? 0) > 0;
}

/**
 * Matches the backend's maxEditQty (order/edit.go). It is not a business cap: quantities ride into
 * the audit trail as strings, and the audit boundary redacts any string with 7+ consecutive digits,
 * so a 1,000,000 quantity would land in the change history as "[redacted]". Refusing it here means
 * the dialog rejects exactly what the API would.
 */
export const MAX_EDIT_QTY = 999999;

/**
 * validateQty mirrors the server's parseEditQty so the operator gets an immediate, identical answer
 * rather than a round-trip. Returns null when valid, else the message to show.
 *
 * The server re-validates regardless: this is a courtesy, never the gate.
 */
export function validateQty(raw: string): string | null {
  const s = raw.trim();
  if (s === '') return 'Enter a quantity.';
  const n = Number(s);
  if (!Number.isFinite(n)) return 'Quantity must be a number.';
  if (n <= 0) return 'Quantity must be more than zero: remove the line instead.';
  if (n > MAX_EDIT_QTY) return `Quantity must be ${MAX_EDIT_QTY.toLocaleString('en-CA')} or less.`;
  return null;
}

/** validateDate accepts an empty value (which CLEARS the date) or a YYYY-MM-DD calendar date. */
export function validateDate(raw: string): string | null {
  const s = raw.trim();
  if (s === '') return null; // clearing the requested date is deliberate, not an error
  if (!/^\d{4}-\d{2}-\d{2}$/.test(s)) return 'Use the date format YYYY-MM-DD.';
  const d = new Date(`${s}T00:00:00Z`);
  if (Number.isNaN(d.getTime())) return 'That is not a real date.';
  // Round-trip guards against a well-formed but non-existent date (2026-02-31 computing March).
  if (d.toISOString().slice(0, 10) !== s) return 'That is not a real date.';
  return null;
}

/** Human labels for the audit `field` vocabulary the change history reports. */
const FIELD_LABEL: Record<string, string> = {
  qty_entered: 'Quantity',
  qty_base: 'Quantity (base)',
  unit_price_cents: 'Unit price',
  line_total_cents: 'Line total',
  subtotal_cents: 'Order subtotal',
  total_cents: 'Order total',
  requested_date: 'Requested date',
  line_added: 'Line added',
  line_removed: 'Line removed',
  delivery_instructions: 'Delivery instructions',
};

/** fieldLabel renders a column name for the viewer, falling back to the raw code so a field added
 *  backend-first still reads sensibly rather than disappearing. */
export function fieldLabel(field: string): string {
  return FIELD_LABEL[field] ?? field;
}

/** Fields whose before/after are integer CENTS and must render as money, not as a bare number. */
const MONEY_FIELDS = new Set([
  'unit_price_cents',
  'line_total_cents',
  'subtotal_cents',
  'total_cents',
]);

/** The port's one money word: integer cents in, the shared dollars spelling out. Lives here so the
 *  trail's money cells and the change surface's delta cells say the same thing the same way. */
export function changeMoney(cents: number): string {
  return formatDollars(cents);
}

/**
 * formatChangeValue renders one side of a before-to-after pair for display. Money fields arrive as
 * integer cents (the backend records them as int64 precisely so the audit boundary cannot redact
 * them) and are shown as dollars; everything else passes through. An empty value renders as
 * '(none)', which is what a creation's `before` and a removal's `after` legitimately are.
 */
export function formatChangeValue(field: string, value: string | undefined): string {
  if (value === undefined || value === '') return '(none)';
  if (!MONEY_FIELDS.has(field)) return value;
  const cents = Number(value);
  if (!Number.isFinite(cents)) return value;
  return formatDollars(cents);
}

/**
 * groupChanges folds the flat change list into one group per operator ACTION.
 *
 * A single qty edit emits several rows (qty_entered, qty_base, line_total_cents, and the order
 * totals), which the backend correlates by stamping each with the same audit request id. Showing
 * them as separate history entries would make one edit look like five. Rows without a request id
 * (an older row, or a server that did not stamp one) each stand alone rather than being lumped
 * together, since we cannot prove they belong to the same action.
 *
 * Input order is preserved (the server returns oldest to newest), and groups appear in the order
 * their first row does.
 */
export function groupChanges(changes: readonly OrderFieldChange[]): OrderFieldChange[][] {
  const groups: OrderFieldChange[][] = [];
  const byRequest = new Map<string, OrderFieldChange[]>();
  for (const c of changes) {
    const key = c.request_id;
    if (!key) {
      groups.push([c]);
      continue;
    }
    const existing = byRequest.get(key);
    if (existing) {
      existing.push(c);
      continue;
    }
    const fresh = [c];
    byRequest.set(key, fresh);
    groups.push(fresh);
  }
  return groups;
}

/**
 * summariseGroup picks the row that best describes what an operator DID, so a grouped entry can
 * lead with "Quantity 2.0000 to 7.0000" rather than an arbitrary member. Derived columns
 * (qty_base and the order totals) are never the headline: they are consequences, not the action.
 */
export function summariseGroup(group: readonly OrderFieldChange[]): OrderFieldChange | undefined {
  const PRIORITY = ['line_added', 'line_removed', 'qty_entered', 'requested_date'];
  for (const field of PRIORITY) {
    const hit = group.find((c) => c.field === field);
    if (hit) return hit;
  }
  return group[0];
}

/**
 * changeActor resolves who made a change: the server-composed display name, else a short id, else
 * "system" for an automated/portal change carrying no attribution. Mirrors the settings history's
 * who ladder: the name is composed by the server, never looked up here.
 */
export function changeActor(c: Pick<OrderFieldChange, 'actor_name' | 'actor_id'>): string {
  if (c.actor_name) return c.actor_name;
  if (c.actor_id) return c.actor_id.slice(0, 8);
  return 'system';
}
