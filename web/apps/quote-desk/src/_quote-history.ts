// Ported from hardscapeos_dibbits master 87d7941b app/src/_quote-edit.ts (the
// change history half) and quote-edits.ts; adapted for HH.
//
// _quote-history.ts: the change history's pure tables (DOM-free, unit tested
// on the Node runner). The server sends the trail already in causal order
// (the audit sequence, never the wall clock), so nothing here re-sorts: the
// trail is only GROUPED, because one action emits several column rows and
// showing them as separate entries would make one edit look like five. The
// discount changes ride the same trail (QD-2 adds the rows server side).
import { moneyWord } from "./_quote-edit.js";

/** One column level change, field for field the contract's QuoteFieldChange. */
export interface QuoteFieldChange {
  field: string;
  line_no: number;
  before?: string;
  after?: string;
  actor_id?: string;
  actor_name?: string;
  request_id?: string;
  occurred_at: string;
}

const FIELD_LABEL: Record<string, string> = {
  qty_entered: "Quantity",
  qty_base: "Quantity (base)",
  unit_price_cents: "Unit price",
  line_total_cents: "Line total",
  subtotal_cents: "Quote subtotal",
  total_cents: "Quote total",
  valid_until: "Expiry date",
  line_added: "Line added",
  line_removed: "Line removed",
  disc_pct: "Discount",
  floor_cleared_by: "Floor cleared by",
};

/** A column name for the viewer; an unknown field reads as itself so a field added server first still shows. */
export function fieldLabel(field: string): string {
  return FIELD_LABEL[field] ?? field;
}

/** Fields whose before and after are integer cents and must read as money. */
const MONEY_FIELDS = new Set(["unit_price_cents", "line_total_cents", "subtotal_cents", "total_cents"]);

/**
 * One side of a before to after pair. An empty side is a creation's before or
 * a removal's after, and reads as the word "none", never a blank or a zero.
 */
export function formatChangeValue(field: string, value: string | undefined): string {
  if (value === undefined || value === "") return "none";
  if (field === "disc_pct") return Number.isFinite(Number(value)) ? `${value}%` : value;
  if (!MONEY_FIELDS.has(field)) return value;
  const cents = Number(value);
  return Number.isFinite(cents) ? moneyWord(cents) : value;
}

/**
 * groupChanges folds the flat trail into one group per action: rows that
 * carry the same request id are one action; a row with no request id stands
 * alone, since nothing proves it belongs to another. Input order is kept and
 * a group appears where its first row does.
 */
export function groupChanges(changes: readonly QuoteFieldChange[]): QuoteFieldChange[][] {
  const groups: QuoteFieldChange[][] = [];
  const byRequest = new Map<string, QuoteFieldChange[]>();
  for (const c of changes) {
    const key = c.request_id;
    if (key === undefined || key === "") {
      groups.push([c]);
      continue;
    }
    const existing = byRequest.get(key);
    if (existing !== undefined) {
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
 * The row that best says what a person DID, so a group leads with the action
 * (a discount, a quantity, an added line) and not with a derived column. The
 * discount leads over the price it moved.
 */
export function summariseGroup(group: readonly QuoteFieldChange[]): QuoteFieldChange | undefined {
  for (const field of ["line_added", "line_removed", "disc_pct", "qty_entered", "valid_until"]) {
    const hit = group.find((c) => c.field === field);
    if (hit !== undefined) return hit;
  }
  return group[0];
}

/** Who made a change: the display name, else a short id, else "system" for an automated change. */
export function changeActor(c: Pick<QuoteFieldChange, "actor_name" | "actor_id">): string {
  if (c.actor_name !== undefined && c.actor_name !== "") return c.actor_name;
  if (c.actor_id !== undefined && c.actor_id !== "") return c.actor_id.slice(0, 8);
  return "system";
}

/** The headline of one change: the field, the line it moved on, and both sides. */
export function historyHeadline(c: QuoteFieldChange): string {
  const line = c.line_no > 0 ? `, line ${c.line_no}` : "";
  return `${fieldLabel(c.field)}${line}: ${formatChangeValue(c.field, c.before)} → ${formatChangeValue(c.field, c.after)}`;
}
