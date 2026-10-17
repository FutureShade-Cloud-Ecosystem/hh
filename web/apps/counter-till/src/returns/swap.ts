// The swap, the till's side of 10.5: the replacement goods (what goes out), the bodies it sends for the
// preview and the ring, and the server's answers read back. The credit, the new order, what is
// spent, what goes back and what is still owed are the SERVER's figures (Q8); nothing here adds,
// subtracts or prorates a cent.
import type { PosSwapInput } from "@hardscapehouse/api-client";
import type { ReturnDraft } from "./draft.js";
import { noteOrders, readNote, readPreview, REFUND_METHODS, type CreditNote, type RefundMethod } from "./note.js";

/** The shortfall's tender, as the contract names it for the new order. */
export type ShortfallTender = "CASH" | "ACCOUNT" | "CARD";

/** One line of replacement goods, in the unit the product is sold by. */
export interface ReplacementLine {
  productId: string;
  name: string;
  soldBy: string;
  qty: number;
}

export interface ReplacementProduct {
  productId: string;
  name: string;
  soldBy: string;
}

/** The goods going out: a basket of products with a quantity each, framework free. */
export class ReplacementBasket {
  #lines: ReplacementLine[] = [];

  get lines(): readonly ReplacementLine[] {
    return this.#lines;
  }

  /** Add a product; adding one already in the basket steps its quantity. */
  add(product: ReplacementProduct): void {
    const existing = this.#lines.find((line) => line.productId === product.productId);
    if (existing !== undefined) existing.qty += 1;
    else this.#lines.push({ productId: product.productId, name: product.name, soldBy: product.soldBy, qty: 1 });
  }

  /** A blank, zero, negative or unreadable quantity takes the line out. */
  setQty(productId: string, raw: string): { ok: true } | { ok: false; words: string } {
    const index = this.#lines.findIndex((line) => line.productId === productId);
    if (index < 0) return { ok: false, words: "That product is not in the replacement goods." };
    const parsed = Number(raw.trim());
    if (raw.trim() === "" || !Number.isFinite(parsed) || parsed <= 0) this.#lines.splice(index, 1);
    else this.#lines[index].qty = parsed;
    return { ok: true };
  }

  remove(productId: string): void {
    this.#lines = this.#lines.filter((line) => line.productId !== productId);
  }

  clear(): void {
    this.#lines = [];
  }
}

export interface SwapBodyArgs {
  customerId: string;
  draft: ReturnDraft;
  basket: ReplacementBasket;
  tender: ShortfallTender;
}

function swapLines(basket: ReplacementBasket): PosSwapInput["lines"] {
  return basket.lines.map((line) => ({ product_id: line.productId, qty_entered: line.qty, uom_entered: line.soldBy }));
}

/**
 * The preview's body: the credit's lines and the replacement lines, the shortfall's tender, and no
 * key or drawer of its own (the contract's shared body wants both, so empty ones ride; the server
 * judges neither on a preview). Null while nothing is picked or nothing is going out.
 */
export function buildSwapPreviewInput(args: SwapBodyArgs): PosSwapInput | null {
  if (args.draft.pickedLines().length === 0 || args.basket.lines.length === 0) return null;
  return {
    idempotency_key: "00000000-0000-4000-8000-000000000000",
    customer_id: args.customerId,
    orders: noteOrders(args.draft),
    reason: "",
    till_session_id: "00000000-0000-4000-8000-000000000000",
    tender: args.tender,
    lines: swapLines(args.basket),
  };
}

export type BuiltSwap = { ok: true; input: PosSwapInput } | { ok: false; words: string };

/** The ring's body: one key per drafted swap, the open drawer, and a card token only for a card shortfall. */
export function buildSwapInput(args: SwapBodyArgs & { key: string; drawerSessionId: string; paymentToken: string }): BuiltSwap {
  if (args.draft.pickedLines().length === 0) return { ok: false, words: "Pick at least one line to return." };
  if (!args.draft.ready) return { ok: false, words: "Every picked line needs its choice: return to stock, damaged, or nothing returned." };
  if (args.basket.lines.length === 0) return { ok: false, words: "Add the replacement goods the credit will pay for." };
  if (args.drawerSessionId === "") return { ok: false, words: "The new order rings into the open drawer, and no drawer is open at this branch." };
  if (args.tender === "CARD" && args.paymentToken === "") {
    return { ok: false, words: "A card shortfall needs a card: the simulated card is not available here." };
  }
  return {
    ok: true,
    input: {
      idempotency_key: args.key,
      customer_id: args.customerId,
      orders: noteOrders(args.draft),
      reason: "",
      till_session_id: args.drawerSessionId,
      tender: args.tender,
      ...(args.tender === "CARD" ? { payment_token: args.paymentToken } : {}),
      lines: swapLines(args.basket),
    },
  };
}

function record(raw: unknown): Record<string, unknown> | null {
  return raw !== null && typeof raw === "object" && !Array.isArray(raw) ? (raw as Record<string, unknown>) : null;
}
const filled = (value: unknown): string | null => (typeof value === "string" && value !== "" ? value : null);
const cents = (value: unknown): number | null => (typeof value === "number" && Number.isSafeInteger(value) ? value : null);
const rail = (value: unknown): RefundMethod | null =>
  typeof value === "string" && REFUND_METHODS.includes(value as RefundMethod) ? (value as RefundMethod) : null;

/** The server's figures for a draft swap. */
export interface SwapPreviewRead {
  creditCents: number;
  newOrderSubtotalCents: number;
  newOrderTaxCents: number;
  newOrderCents: number;
  appliedCents: number;
  leftoverCents: number;
  shortfallCents: number;
  /** The rail the leftover goes back by: the original tender (Q7). */
  leftoverMethod: RefundMethod;
}

export function readSwapPreview(raw: unknown): SwapPreviewRead | null {
  const r = record(raw);
  if (r === null) return null;
  const credit = readPreview(r["credit"]);
  const newOrder = record(r["new_order"]);
  if (credit === null || newOrder === null) return null;
  const creditCents = credit.totalCents;
  const newSub = cents(newOrder["subtotal_cents"]);
  const newTax = cents(newOrder["tax_cents"]);
  const newTotal = cents(newOrder["total_cents"]);
  const applied = cents(r["applied_cents"]);
  const leftover = cents(r["leftover_cents"]);
  const shortfall = cents(r["shortfall_cents"]);
  const method = rail(r["leftover_method"]);
  if (newSub === null || newTax === null || newTotal === null) return null;
  if (applied === null || leftover === null || shortfall === null || method === null) return null;
  return {
    creditCents,
    newOrderSubtotalCents: newSub,
    newOrderTaxCents: newTax,
    newOrderCents: newTotal,
    appliedCents: applied,
    leftoverCents: leftover,
    shortfallCents: shortfall,
    leftoverMethod: method,
  };
}

/** The new order of a committed swap. */
export interface SwapSale {
  swapId: string;
  orderId: string;
  orderNo: string;
  invoiceNo: string;
  tender: string;
  subtotalCents: number;
  taxCents: number;
  totalCents: number;
}

/** A committed swap: the credit note and the new order, each naming the swap. */
export interface SwapDone {
  swapId: string;
  note: CreditNote;
  sale: SwapSale;
  creditCents: number;
  appliedCents: number;
  leftoverCents: number;
  shortfallCents: number;
  leftoverMethod: RefundMethod;
  replayed: boolean;
}

function readSale(raw: unknown): SwapSale | null {
  const r = record(raw);
  if (r === null) return null;
  const swapId = filled(r["swap_id"]);
  const orderId = filled(r["order_id"]);
  const orderNo = filled(r["order_no"]);
  const invoiceNo = filled(r["invoice_no"]);
  const tender = filled(r["tender"]);
  const subtotalCents = cents(r["subtotal_cents"]);
  const taxCents = cents(r["tax_cents"]);
  const totalCents = cents(r["total_cents"]);
  if (swapId === null || orderId === null || orderNo === null || invoiceNo === null || tender === null) return null;
  if (subtotalCents === null || taxCents === null || totalCents === null || r["status"] !== "COMMITTED") return null;
  return { swapId, orderId, orderNo, invoiceNo, tender, subtotalCents, taxCents, totalCents };
}

/** Field by field, never a cast: the committed swap the Done step spells, or null. */
export function readSwapResult(raw: unknown): SwapDone | null {
  const r = record(raw);
  if (r === null) return null;
  const swapId = filled(r["swap_id"]);
  const note = readNote(r["note"]);
  const sale = readSale(r["sale"]);
  const credit = cents(r["credit_cents"]);
  const applied = cents(r["applied_cents"]);
  const leftover = cents(r["leftover_cents"]);
  const shortfall = cents(r["shortfall_cents"]);
  const method = rail(r["leftover_method"]);
  const replayed = r["replayed"];
  if (swapId === null || note === null || sale === null || typeof replayed !== "boolean") return null;
  if (credit === null || applied === null || leftover === null || shortfall === null || method === null) return null;
  // Both documents must name this swap, or they are not one swap.
  if (note.swapId !== swapId || sale.swapId !== swapId) return null;
  return {
    swapId,
    note,
    sale,
    creditCents: credit,
    appliedCents: applied,
    leftoverCents: leftover,
    shortfallCents: shortfall,
    leftoverMethod: method,
    replayed,
  };
}
