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
      customer_id: args.customerId,
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
export function railAvailable(draft: ReturnDraft, method: RefundMethod, drawer: DrawerState): boolean {
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
  return { returnId, returnNo, orderId, invoiceId, method, subtotalCents, taxCents, totalCents };
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
export function buildPreviewInput(customerId: string, draft: ReturnDraft): PosReturnPreviewInput | null {
  if (draft.pickedLines().length === 0) return null;
  return { customer_id: customerId, orders: noteOrders(draft) };
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
  const lines = new Map<string, PreviewLine>();
  for (const orderRaw of orders) {
    const order = record(orderRaw);
    if (order === null || !Array.isArray(order["lines"])) return null;
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
  return { subtotalCents, taxCents, totalCents, lines };
}
