// The landed cost slice's plain logic: the words for the states and the bases, the one check made
// before a capture is sent, the body the core expects, and the words that settle an action against
// what the core now holds. Nothing here computes money. The server converts a foreign bill with its
// rate, apportions it across the received lines and decides every figure; this file formats and
// says. Amounts a person types become cents through the decimal string (purch-money.ts).
//
// Port provenance: the three views (list, capture, allocate), the three bases and the retro toggle
// follow the original app's landed-cost screens (repository hardscapeos_dibbits, commit
// f8f4973db317b92296ae8ec7e62a3f54f5dc82ab: landed-cost.ts, landedcost-capture.ts,
// landedcost-allocate.ts); the words and the confirm steps are HH's.
import type {
  LandedCost,
  LandedCostBasis,
  LandedCostCreateRequest,
  LandedCostPage,
  LandedCostStatus,
} from "@hardscapehouse/api-client";
import { formatMoney, formatQty, parseAmountToCents, parseQty, parseRate } from "../purch/purch-money.js";
import type { Tone } from "../po/po-logic.js";

export const BASES: readonly LandedCostBasis[] = ["VALUE", "WEIGHT", "QUANTITY"];

const BASIS_WORD: Record<string, string> = { VALUE: "By value", WEIGHT: "By weight", QUANTITY: "By quantity" };

export function basisWord(basis: string): string {
  return BASIS_WORD[basis] ?? basis;
}

const STATUS_WORD: Record<string, string> = {
  DRAFT: "Draft",
  ALLOCATED: "Allocated",
  POSTED: "Applied",
  VOIDED: "Voided",
  RETRO_ADJUSTED: "Retro adjusted",
};

export function statusWord(status: string): string {
  return STATUS_WORD[status] ?? status;
}

/** The pill's tone; the word is always beside it, so colour never carries the meaning alone. */
export function statusTone(status: string): Tone {
  if (status === "POSTED" || status === "RETRO_ADJUSTED") return "good";
  if (status === "ALLOCATED") return "watch";
  if (status === "VOIDED") return "stop";
  return "plain";
}

/** The landed cost list, which the core answers as {"items":null} when there is none. */
export function landedRows(page: Partial<Omit<LandedCostPage, "items">> & { items?: LandedCost[] | null }): LandedCost[] {
  return page.items ?? [];
}

/** Goods must have landed before freight can follow them: a part received, received or closed order. */
export function receivedOrders<T extends { status: string }>(orders: readonly T[]): T[] {
  return orders.filter((o) => o.status === "PARTIAL" || o.status === "RECEIVED" || o.status === "CLOSED");
}

/** The landed costs a late freight bill can follow: applied ones on the same order, themselves not retro. */
export function retroCandidates<T extends { id: string; po_id: string; status: string; retro_of_id?: string }>(items: readonly T[], poId: string): T[] {
  return items.filter((i) => i.po_id === poId && i.status === "POSTED" && (i.retro_of_id === undefined || i.retro_of_id === ""));
}

interface OrderLines {
  readonly id: string;
  readonly po_no: string;
  readonly branch_id: string;
  readonly lines: readonly { id: string; product_id: string; sku: string; description: string; qty_received_base: number; uom: string }[];
}

/** The order's lines that have received goods: the ones a retro count of what is left is asked for. */
export function retroLines(po: OrderLines): OrderLines["lines"][number][] {
  return po.lines.filter((l) => l.qty_received_base > 0);
}

/** What the capture form holds as text; nothing here is a number until it is checked. */
export interface CaptureText {
  readonly poNo: string;
  readonly currency: string;
  readonly fx: string;
  readonly freight: string;
  readonly duty: string;
  readonly handling: string;
  readonly basis: string;
  readonly excludeSpecial: boolean;
  readonly retro: boolean;
  readonly retroOfId: string;
  /** The count still on hand, by PO line id. */
  readonly onHand: Readonly<Record<string, string>>;
}

export type CreateCheck = { ok: true; body: LandedCostCreateRequest } | { ok: false; words: string };

const AMOUNT_WORDS = (what: string): string => `Write the ${what} as dollars and cents, two decimals at most.`;

export function amountCents(textValue: string, what: string): { ok: true; cents: number } | { ok: false; words: string } {
  if (textValue.trim() === "") return { ok: true, cents: 0 };
  const cents = parseAmountToCents(textValue);
  return cents === null ? { ok: false, words: AMOUNT_WORDS(what) } : { ok: true, cents };
}

/** The capture form's text, checked, as the body the core expects. Nothing is sent when a check fails. */
export function buildCreate(form: CaptureText, po: OrderLines | null): CreateCheck {
  if (po === null || form.poNo === "") return { ok: false, words: "Pick the purchase order the freight belongs to." };
  const freight = amountCents(form.freight, "freight");
  if (!freight.ok) return freight;
  const duty = amountCents(form.duty, "duty");
  if (!duty.ok) return duty;
  const handling = amountCents(form.handling, "handling");
  if (!handling.ok) return handling;
  if (freight.cents === 0 && duty.cents === 0 && handling.cents === 0) {
    return { ok: false, words: "Write at least one of freight, duty or handling: a landed cost of nothing costs nothing." };
  }
  const usd = form.currency === "USD";
  let rate: number | undefined;
  if (usd) {
    const checked = parseRate(form.fx);
    if (!checked.ok) return { ok: false, words: "Write the exchange rate as a positive number, six decimals at most, such as 1.352." };
    rate = checked.value;
  }
  const basis = BASES.find((b) => b === form.basis) ?? "VALUE";
  const body: LandedCostCreateRequest = {
    po_id: po.id,
    po_ref: po.po_no,
    branch_id: po.branch_id,
    currency: usd ? "USD" : "CAD",
    freight_cents: freight.cents,
    duty_cents: duty.cents,
    handling_cents: handling.cents,
    allocation_basis: basis,
    exclude_special_buys: form.excludeSpecial,
  };
  if (rate !== undefined) body.fx_rate = rate;
  if (form.retro) {
    if (form.retroOfId === "") return { ok: false, words: "Pick the landed cost this late freight follows." };
    const onHand: { product_id: string; po_line_id: string; on_hand_base: number }[] = [];
    for (const line of retroLines(po)) {
      const counted = parseQty(form.onHand[line.id] ?? "", { allowZero: true });
      if (!counted.ok) {
        return { ok: false, words: `Write how many of ${line.sku} are still on hand, as a number with four decimals at most (0 if none).` };
      }
      onHand.push({ product_id: line.product_id, po_line_id: line.id, on_hand_base: counted.value });
    }
    body.retro_of_id = form.retroOfId;
    body.on_hand = onHand;
  }
  return { ok: true, body };
}

export type Action = "apply" | "retro" | "void";

/**
 * What a press of the three consequential buttons will do, in words, shown before it is sent. The
 * total is the server's own preview total; an absent one reads as the placeholder, never zero.
 */
export function confirmWords(
  action: Action,
  doc: { po_ref: string; allocation_basis: string; retro_of_id?: string },
  totalCents: number | undefined,
): string {
  const order = doc.po_ref === "" ? "the order" : doc.po_ref;
  const total = formatMoney(totalCents);
  const basis = basisWord(doc.allocation_basis).toLowerCase();
  if (action === "apply") {
    return `Apply ${total} of landed cost to ${order}, ${basis}. This raises the cost of the goods received on it and posts to the books. It cannot be edited after; a mistake is undone by voiding it.`;
  }
  if (action === "retro") {
    return `Apply ${total} of late freight to ${order}, ${basis}. The share for goods still on hand raises their cost; the share for goods already sold goes straight to cost of goods sold. Posts to the books.`;
  }
  return `Void the landed cost of ${total} on ${order}. This reverses what applying it posted and takes the cost back off the goods.`;
}

const EXPECTED: Record<Action, LandedCostStatus> = { apply: "POSTED", retro: "RETRO_ADJUSTED", void: "VOIDED" };

/**
 * The core answers an apply or a retro of a document that is not a draft with the same body as a
 * real one (it is a quiet no-op), so what happened is settled by reading the document again:
 * null when the action took effect, otherwise the words saying what the core holds.
 */
export function settleWords(action: Action, answered: string, before: string): string | null {
  const after = answered;
  if (after === EXPECTED[action]) {
    // Already in the wanted state before the press: the press did nothing.
    return before === after ? `Nothing changed: it was already ${statusWord(after).toLowerCase()}.` : null;
  }
  if (action === "void") return `It was not voided: the core reads it as ${statusWord(after).toLowerCase()}.`;
  if (after === "DRAFT") return "It is still a draft: the core did not apply it.";
  return `It was not applied: the core reads it as ${statusWord(after).toLowerCase()}.`;
}

/** The weight a share was split by, as the core wrote it: cents for a value split, kilograms, or a quantity. */
export function basisFigure(basis: string, weight: number): string {
  if (basis === "VALUE") return formatMoney(weight);
  if (basis === "WEIGHT") return `${weight.toLocaleString("en-CA", { maximumFractionDigits: 4 })} kg`;
  return formatQty(weight, "");
}
