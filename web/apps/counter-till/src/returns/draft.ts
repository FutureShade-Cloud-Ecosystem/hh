// The returns draft: the framework-free state behind the Lines step. One credit note is drafted from
// lines of several of the customer's orders; each picked line has a quantity (capped at what the
// server said is open) and its own disposition. The draft holds the person's choices only: every
// figure on the screen is the server's, and the till decides no money rule. The commit re-guards all
// of it; these refusals only keep an impossible request off the wire and say why in words.
import type { CustomerLine, CustomerOrder } from "./lines.js";

/** What happens to a returned line: back on the shelf, written off as damaged, or no goods back. */
export type Disposition = "RESTOCK" | "DAMAGED" | "NO_RETURN";

export const DISPOSITIONS: readonly Disposition[] = ["RESTOCK", "DAMAGED", "NO_RETURN"];

export const DISPOSITION_WORDS: Record<Disposition, string> = {
  RESTOCK: "Return to stock",
  DAMAGED: "Refund, do not restock (damaged)",
  NO_RETURN: "Refund, nothing returned",
};

export type DraftAnswer = { ok: true } | { ok: false; words: string };

/** One picked line with the facts the Lines panel and the refund step need. */
export interface PickedLine {
  orderId: string;
  orderNo: string;
  orderLineId: string;
  description: string;
  qty: number;
  disposition: Disposition | null;
}

function shown(value: number): string {
  return String(Number(value.toFixed(4)));
}

export class ReturnDraft {
  readonly #orders: readonly CustomerOrder[];
  readonly #qty = new Map<string, number>();
  readonly #disposition = new Map<string, Disposition>();

  constructor(orders: readonly CustomerOrder[]) {
    this.#orders = orders;
  }

  get orders(): readonly CustomerOrder[] {
    return this.#orders;
  }

  #find(orderLineId: string): { order: CustomerOrder; line: CustomerLine } | null {
    for (const order of this.#orders) {
      const line = order.lines.find((candidate) => candidate.orderLineId === orderLineId);
      if (line !== undefined) return { order, line };
    }
    return null;
  }

  /** The branch the draft is already tied to by a picked line, or null while nothing is picked. */
  get branchId(): string | null {
    for (const orderLineId of this.#qty.keys()) {
      const hit = this.#find(orderLineId);
      if (hit !== null) return hit.order.branchId;
    }
    return null;
  }

  qtyOf(orderLineId: string): number {
    return this.#qty.get(orderLineId) ?? 0;
  }

  dispositionOf(orderLineId: string): Disposition | null {
    return this.#disposition.get(orderLineId) ?? null;
  }

  /**
   * Draft a line's quantity from what was typed or stepped to. Blank, zero, negative or unreadable
   * takes the line out; a quantity above the open quantity is refused here, before the wire, and the
   * draft keeps what it had.
   */
  setQty(orderLineId: string, raw: string): DraftAnswer {
    const hit = this.#find(orderLineId);
    if (hit === null) return { ok: false, words: "That line is not one of this customer's lines." };
    const parsed = Number(raw.trim());
    if (raw.trim() === "" || !Number.isFinite(parsed) || parsed <= 0) {
      this.#qty.delete(orderLineId);
      this.#disposition.delete(orderLineId);
      return { ok: true };
    }
    const { order, line } = hit;
    if (!order.returnable) return { ok: false, words: `${order.orderNo} cannot be returned from here: ${order.notReturnableReason}` };
    if (line.qtyOpen <= 0) {
      return { ok: false, words: `Nothing on ${line.description} is open to return: every unit has gone back already.` };
    }
    if (parsed > line.qtyOpen + 1e-6) {
      return {
        ok: false,
        words: `${line.description} on ${order.orderNo}: only ${shown(line.qtyOpen)} open to return, so ${shown(parsed)} cannot go back.`,
      };
    }
    const tied = this.branchId;
    if (tied !== null && tied !== order.branchId && !this.#qty.has(orderLineId)) {
      const here = this.#orders.find((candidate) => candidate.branchId === tied)?.branchName ?? "";
      const there = order.branchName;
      return {
        ok: false,
        words: `A credit note is for one branch: this credit note is at ${here === "" ? "one branch" : here}, and ${order.orderNo} is at ${there === "" ? "another" : there}. Finish this credit note first, then start another for that branch.`,
      };
    }
    this.#qty.set(orderLineId, parsed);
    return { ok: true };
  }

  /** Choose what happens to a line. Choosing on an unpicked line picks one unit of it. */
  setDisposition(orderLineId: string, value: string): DraftAnswer {
    if (!DISPOSITIONS.includes(value as Disposition)) {
      return { ok: false, words: "Choose return to stock, damaged, or nothing returned." };
    }
    if (this.qtyOf(orderLineId) === 0) {
      const picked = this.setQty(orderLineId, "1");
      if (!picked.ok) return picked;
    }
    this.#disposition.set(orderLineId, value as Disposition);
    return { ok: true };
  }

  /** The picked lines in the order the screen lists them (orders newest first, lines as answered). */
  pickedLines(): PickedLine[] {
    const picked: PickedLine[] = [];
    for (const order of this.#orders) {
      for (const line of order.lines) {
        const qty = this.#qty.get(line.orderLineId);
        if (qty === undefined) continue;
        picked.push({
          orderId: order.orderId,
          orderNo: order.orderNo,
          orderLineId: line.orderLineId,
          description: line.description,
          qty,
          disposition: this.#disposition.get(line.orderLineId) ?? null,
        });
      }
    }
    return picked;
  }

  /** The orders that hold at least one picked line, in the order the screen lists them. */
  pickedOrders(): CustomerOrder[] {
    return this.#orders.filter((order) => order.lines.some((line) => this.#qty.has(line.orderLineId)));
  }

  get orderCount(): number {
    return new Set(this.pickedLines().map((line) => line.orderId)).size;
  }

  get missingDispositions(): number {
    return this.pickedLines().filter((line) => line.disposition === null).length;
  }

  /** At least one line is picked and every picked line has its disposition. */
  get ready(): boolean {
    const picked = this.pickedLines();
    return picked.length > 0 && picked.every((line) => line.disposition !== null);
  }
}
