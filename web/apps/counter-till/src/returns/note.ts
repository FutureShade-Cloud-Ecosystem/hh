// The credit note across orders, the till's side of 10.2's write contract: the body it sends, and
// the committed note it reads back. One key per note, lines grouped by order, one rail. The till
// decides no money rule: the server judges every quantity, the rail, the approval and the totals,
// and the figures read back here are the server's own.
import type { PosReturnAcrossInput } from "@hardscapehouse/api-client";
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
 * The contract's body. The write carries ONE restock flag for the whole note (per line dispositions
 * are later server work), so a note whose lines are all "return to stock" restocks, a note with none
 * of them does not, and a mix is refused here in words rather than sent as something it is not.
 * Damaged lines name DAMAGED as their line reason; the rest name OTHER.
 */
export function buildNoteInput(args: BuildNoteArgs): BuiltNote {
  const picked = args.draft.pickedLines();
  if (picked.length === 0) return { ok: false, words: "Pick at least one line to return." };
  if (!args.draft.ready) return { ok: false, words: "Every picked line needs its choice: return to stock, damaged, or nothing returned." };
  const restocking = picked.filter((line) => line.disposition === "RESTOCK").length;
  if (restocking !== 0 && restocking !== picked.length) {
    return {
      ok: false,
      words:
        "The refund takes one choice for the whole note for now: either every line goes back to stock, or none does. Put the lines that differ on a credit note of their own.",
    };
  }
  if (args.method === "CASH" && args.drawerSessionId === "") {
    return { ok: false, words: "Cash is paid from the open drawer, and no drawer is open at this branch." };
  }
  const orders: PosReturnAcrossInput["orders"] = [];
  for (const line of picked) {
    let entry = orders.find((candidate) => candidate.order_id === line.orderId);
    if (entry === undefined) {
      entry = { order_id: line.orderId, lines: [] };
      orders.push(entry);
    }
    entry.lines.push({
      order_line_id: line.orderLineId,
      qty_base: line.qty,
      reason_code: line.disposition === "DAMAGED" ? "DAMAGED" : "OTHER",
    });
  }
  return {
    ok: true,
    input: {
      idempotency_key: args.key,
      customer_id: args.customerId,
      orders,
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
  return { noteId, noteNo, customerId, branchId, method, subtotalCents, taxCents, totalCents, returns, replayed };
}
