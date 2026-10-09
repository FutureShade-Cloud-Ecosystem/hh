// The credit note across orders, the till's side of 10.2's write contract: the body it sends, and
// the committed note it reads back. One key per note, lines grouped by order, one rail. The till
// decides no money rule: the server judges every quantity, the rail, the approval and the totals,
// and the figures read back here are the server's own.
import type { PosReturnAcrossInput, PosReturnPreviewInput } from "@hardscapehouse/api-client";
import type { ReturnDraft } from "./draft.js";

/** The refund rails the contract carries. */
export type RefundMethod = "CASH" | "CARD" | "ACCOUNT_CREDIT";
export const REFUND_METHODS: readonly RefundMethod[] = ["CASH", "CARD", "ACCOUNT_CREDIT"];

/** The drawer at the note's branch, as the cash rail needs it. */
export type DrawerState = { kind: "unknown" } | { kind: "open"; sessionId: string } | { kind: "closed" };

export interface BuildNoteArgs {
  customerId: string;
  /** A walk in sale's receipt or order number: sent with no customer id (the server reads an empty customer with a number as the walk in). */
  walkInNumber?: string | null;
  key: string;
  draft: ReturnDraft;
  method: RefundMethod;
  drawerSessionId: string;
}

export type BuiltNote = { ok: true; input: PosReturnAcrossInput } | { ok: false; words: string };

/**
 * The contract's body. Every line carries its own disposition, which the server reads over the
 * whole note's `restock` flag; the flag is sent only as the summary the contract still requires
 * (true when every picked line goes back to stock). Damaged lines name DAMAGED as their line
 * reason; the rest name OTHER.
 */
export function buildNoteInput(args: BuildNoteArgs): BuiltNote {
  const picked = args.draft.pickedLines();
  if (picked.length === 0) return { ok: false, words: "Pick at least one line to return." };
  if (!args.draft.ready) return { ok: false, words: "Every picked line needs its choice: return to stock, damaged, or nothing returned." };
  const restocking = picked.filter((line) => line.disposition === "RESTOCK").length;
  if (args.method === "CASH" && args.drawerSessionId === "") {
    return { ok: false, words: "Cash is paid from the open drawer, and no drawer is open at this branch." };
  }
  return {
    ok: true,
    input: {
      idempotency_key: args.key,
      ...(args.walkInNumber ? { number: args.walkInNumber } : { customer_id: args.customerId }),
      orders: noteOrders(args.draft),
      refund_method: args.method,
      reason: "",
      restock: restocking === picked.length,
      till_session_id: args.method === "CASH" ? args.drawerSessionId : "",
    },
  };
}

/**
 * Whether a rail may be picked. Cash needs the open drawer; card needs the original capture of every
 * picked order to be refundable; the account rail is always offered, because only the server knows
 * whether the account can spend credit and it refuses in words where it cannot.
 */
export function railAvailable(draft: ReturnDraft, method: RefundMethod, drawer: DrawerState, walkIn = false): boolean {
  // A walk in sale goes back to cash or card only: there is no account to credit (D2).
  if (method === "ACCOUNT_CREDIT" && walkIn) return false;
  if (method === "CASH") return drawer.kind === "open";
  if (method === "CARD") return draft.pickedOrders().every((order) => order.cardRefundable);
  return true;
}

/** One committed child return of the note, as the server answered it. */
export interface NoteReturn {
  returnId: string;
  returnNo: string;
  orderId: string;
  invoiceId: string;
  method: RefundMethod;
  subtotalCents: number;
  taxCents: number;
  totalCents: number;
  /** What came off the order's open invoice when the order was not fully paid; 0 otherwise. */
  invoiceOffsetCents: number;
  /** The split in the server's words, empty when there was none. */
  paymentNote: string;
}

/** The committed credit note. */
export interface CreditNote {
  noteId: string;
  noteNo: string;
  customerId: string;
  branchId: string;
  method: RefundMethod;
  subtotalCents: number;
  taxCents: number;
  totalCents: number;
  returns: NoteReturn[];
  replayed: boolean;
  /** Present when the note is the credit of a swap. */
  swapId: string | null;
}

function record(raw: unknown): Record<string, unknown> | null {
  return raw !== null && typeof raw === "object" && !Array.isArray(raw) ? (raw as Record<string, unknown>) : null;
}
const filled = (value: unknown): string | null => (typeof value === "string" && value !== "" ? value : null);
const cents = (value: unknown): number | null => (typeof value === "number" && Number.isSafeInteger(value) ? value : null);
const rail = (value: unknown): RefundMethod | null =>
  typeof value === "string" && REFUND_METHODS.includes(value as RefundMethod) ? (value as RefundMethod) : null;

function readChild(raw: unknown): NoteReturn | null {
  const r = record(raw);
  if (r === null) return null;
  const returnId = filled(r["return_id"]);
  const returnNo = filled(r["return_no"]);
  const orderId = filled(r["order_id"]);
  const invoiceId = filled(r["invoice_id"]);
  const method = rail(r["refund_method"]);
  const subtotalCents = cents(r["subtotal_cents"]);
  const taxCents = cents(r["tax_cents"]);
  const totalCents = cents(r["total_cents"]);
  if (returnId === null || returnNo === null || orderId === null || invoiceId === null || method === null) return null;
  if (subtotalCents === null || taxCents === null || totalCents === null) return null;
  if (r["status"] !== "COMMITTED") return null;
  const offset = r["invoice_offset_cents"] === undefined ? 0 : cents(r["invoice_offset_cents"]);
  const paymentNote = r["payment_note"] === undefined ? "" : typeof r["payment_note"] === "string" ? r["payment_note"] : null;
  if (offset === null || paymentNote === null) return null;
  return { returnId, returnNo, orderId, invoiceId, method, subtotalCents, taxCents, totalCents, invoiceOffsetCents: offset, paymentNote };
}

/** Field by field, never a cast: the committed note the Done step spells, or null. */
export function readNote(raw: unknown): CreditNote | null {
  const r = record(raw);
  if (r === null) return null;
  const noteId = filled(r["note_id"]);
  const noteNo = filled(r["note_no"]);
  const customerId = filled(r["customer_id"]);
  const branchId = filled(r["branch_id"]);
  const method = rail(r["refund_method"]);
  const subtotalCents = cents(r["subtotal_cents"]);
  const taxCents = cents(r["tax_cents"]);
  const totalCents = cents(r["total_cents"]);
  const replayed = r["replayed"];
  const rawReturns = r["returns"];
  if (noteId === null || noteNo === null || customerId === null || branchId === null || method === null) return null;
  if (subtotalCents === null || taxCents === null || totalCents === null || typeof replayed !== "boolean") return null;
  if (!Array.isArray(rawReturns) || rawReturns.length === 0) return null;
  const returns: NoteReturn[] = [];
  for (const entry of rawReturns) {
    const child = readChild(entry);
    if (child === null) return null;
    returns.push(child);
  }
  const swapId = filled(r["swap_id"]);
  return { noteId, noteNo, customerId, branchId, method, subtotalCents, taxCents, totalCents, returns, replayed, swapId };
}

/** The picked lines grouped by order, each with its own disposition once chosen: the body's `orders`, shared by the note, the preview and the swap. */
export function noteOrders(draft: ReturnDraft): PosReturnAcrossInput["orders"] {
  const orders: PosReturnAcrossInput["orders"] = [];
  for (const line of draft.pickedLines()) {
    let entry = orders.find((candidate) => candidate.order_id === line.orderId);
    if (entry === undefined) {
      entry = { order_id: line.orderId, lines: [] };
      orders.push(entry);
    }
    entry.lines.push({
      order_line_id: line.orderLineId,
      qty_base: line.qty,
      reason_code: line.disposition === "DAMAGED" ? "DAMAGED" : "OTHER",
      ...(line.disposition === null ? {} : { disposition: line.disposition }),
    });
  }
  return orders;
}

/**
 * The preview's body: the same lines as the note, without a key, a rail or a drawer, and with a
 * disposition only once one is chosen. Null when nothing is picked.
 */
export function buildPreviewInput(
  customerId: string,
  draft: ReturnDraft,
  method: RefundMethod | null = null,
  walkInNumber: string | null = null,
): PosReturnPreviewInput | null {
  if (draft.pickedLines().length === 0) return null;
  return {
    ...(walkInNumber ? { number: walkInNumber } : { customer_id: customerId }),
    orders: noteOrders(draft),
    ...(method === null ? {} : { refund_method: method }),
  };
}

/** One order's share of the credit, with the server's split when a rail was named. */
export interface PreviewOrder {
  orderId: string;
  orderNo: string;
  totalCents: number;
  /** What goes back in cash or card; null when no rail was named. */
  railCents: number | null;
  /** What comes off the order's open invoice; null when no rail was named. */
  invoiceOffsetCents: number | null;
  /** The split in the server's words, empty when nothing is split. */
  railNote: string;
}

/** One line's priced figures, the server's own. */
export interface PreviewLine {
  subtotalCents: number;
  taxCents: number;
  totalCents: number;
}

/** The credit so far, as the server priced it; the note total includes tax. */
export interface CreditPreview {
  subtotalCents: number;
  taxCents: number;
  totalCents: number;
  /** Each picked line's figures by order line id. */
  lines: Map<string, PreviewLine>;
  /** Each order's share, in the order the server answered. */
  orders: PreviewOrder[];
  /** With a rail named: the sums of what goes back in cash or card and what comes off the open invoices. */
  railCents: number | null;
  invoiceOffsetCents: number | null;
}

/** Field by field, never a cast: the credit so far, or null so that no total shows rather than half of one. */
export function readPreview(raw: unknown): CreditPreview | null {
  const r = record(raw);
  if (r === null) return null;
  const subtotalCents = cents(r["subtotal_cents"]);
  const taxCents = cents(r["tax_cents"]);
  const totalCents = cents(r["total_cents"]);
  const orders = r["orders"];
  if (subtotalCents === null || taxCents === null || totalCents === null) return null;
  if (!Array.isArray(orders) || orders.length === 0) return null;
  const noteRail = r["rail_cents"] === undefined ? null : cents(r["rail_cents"]);
  const noteOffset = r["invoice_offset_cents"] === undefined ? null : cents(r["invoice_offset_cents"]);
  if ((r["rail_cents"] === undefined) !== (r["invoice_offset_cents"] === undefined)) return null;
  if ((r["rail_cents"] !== undefined && noteRail === null) || (r["invoice_offset_cents"] !== undefined && noteOffset === null)) return null;
  const lines = new Map<string, PreviewLine>();
  const previewOrders: PreviewOrder[] = [];
  for (const orderRaw of orders) {
    const order = record(orderRaw);
    if (order === null || !Array.isArray(order["lines"])) return null;
    const orderId = filled(order["order_id"]);
    const orderNo = filled(order["order_no"]);
    const orderTotal = cents(order["total_cents"]);
    if (orderId === null || orderNo === null || orderTotal === null) return null;
    if ((order["rail_cents"] === undefined) !== (order["invoice_offset_cents"] === undefined)) return null;
    const orderRail = order["rail_cents"] === undefined ? null : cents(order["rail_cents"]);
    const orderOffset = order["invoice_offset_cents"] === undefined ? null : cents(order["invoice_offset_cents"]);
    if ((order["rail_cents"] !== undefined && orderRail === null) || (order["invoice_offset_cents"] !== undefined && orderOffset === null)) return null;
    const railNote = order["rail_note"] === undefined ? "" : typeof order["rail_note"] === "string" ? order["rail_note"] : null;
    if (railNote === null) return null;
    previewOrders.push({ orderId, orderNo, totalCents: orderTotal, railCents: orderRail, invoiceOffsetCents: orderOffset, railNote });
    for (const lineRaw of order["lines"]) {
      const line = record(lineRaw);
      if (line === null) return null;
      const id = filled(line["order_line_id"]);
      const sub = cents(line["subtotal_cents"]);
      const tax = cents(line["tax_cents"]);
      const total = cents(line["total_cents"]);
      if (id === null || sub === null || tax === null || total === null) return null;
      lines.set(id, { subtotalCents: sub, taxCents: tax, totalCents: total });
    }
  }
  return { subtotalCents, taxCents, totalCents, lines, orders: previewOrders, railCents: noteRail, invoiceOffsetCents: noteOffset };
}
