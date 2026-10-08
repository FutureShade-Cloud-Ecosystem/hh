// Item 9.7: the Pay balance surface's reads and words, pure. Every figure is the server's integer
// cents; money reaches the screen through the one formatter the caller hands in, and a refusal
// reads in words from the server's fields, never as a code or as cents.

import { cardChargeAnswer } from "../ported/_pos.js";

export type PayMethod = "CASH" | "CARD";

export interface OwingPayment {
  paymentId: string;
  method: string;
  amountCents: number;
}

/** One order that still owes, as the find answers it. */
export interface OwingOrder {
  orderId: string;
  orderNo: string;
  customerId: string;
  customerName: string;
  job: string | null;
  fulfilment: string;
  orderTotalCents: number;
  paidCents: number;
  stillOwedCents: number;
  held: boolean;
  holdWords: string | null;
  payments: OwingPayment[];
}

/** The take's answer. */
export interface TakenBalance {
  orderId: string;
  orderNo: string;
  method: PayMethod;
  amountCents: number;
  paidWords: string;
  released: boolean;
  stillOwedCents: number;
  payments: OwingPayment[];
  replayed: boolean;
}

const METHOD_WORDS: Record<string, string> = { CASH: "Cash", CARD: "Card", CHEQUE: "Cheque", EFT: "EFT", OTHER: "Other" };

export function methodWord(method: string): string {
  return METHOD_WORDS[method] ?? "Other";
}

const FULFILMENT_WORDS: Record<string, string> = { PICKUP: "Pickup", WILL_CALL: "Will-call", DELIVERY: "Delivery" };

export function fulfilmentWord(fulfilment: string): string {
  return FULFILMENT_WORDS[fulfilment] ?? "";
}

/** The money trail line from the take's own payments: "Money trail: $500.00 by Card, then $41.02 by Cash." */
export function moneyTrailWords(payments: readonly OwingPayment[], money: (cents: number) => string): string {
  if (payments.length === 0) return "";
  const parts = payments.map((p) => `${money(p.amountCents)} by ${methodWord(p.method)}`);
  return `Money trail: ${parts.join(", then ")}.`;
}

/** After a take: the server's own "Paid in full" word, and the release words only when it released. */
export function paidInFullWords(orderNo: string, paidWords: string, released: boolean): string {
  return `${orderNo} reads ${paidWords}${released ? " and is released to picking" : ""}.`;
}

export function takeButtonWords(balanceCents: number, money: (cents: number) => string): string {
  return `Take ${money(balanceCents)}`;
}

/** A refusal or failure in words, from the server's fields. */
export function payBalanceRefusalWords(
  err: { status: number; error: string; message?: string; balance_cents?: number; sent_cents?: number },
  money: (cents: number) => string,
): string {
  if (err.error === "pay_balance_whole_only") {
    if (err.message && err.message !== "") return err.message;
    return Number.isInteger(err.balance_cents)
      ? `This door takes the whole balance of ${money(err.balance_cents as number)}, not a part payment.`
      : "This door takes the whole balance, not a part payment.";
  }
  const card = cardChargeAnswer(err.status, err.error, false);
  if (card !== null && card.msg !== "") return card.msg;
  if (err.status === 0) return "No answer came back: press Take again. The same payment cannot be charged twice.";
  if (err.status === 404) return "That order no longer answers at this counter: find it again.";
  if (err.status === 402) return "The card was declined: nothing was charged. Try cash, or another card.";
  if (/^[a-z0-9_]+$/.test(err.error)) return "The payment was refused: nothing was taken. Try again, or ask a manager.";
  return err.error;
}

/**
 * A refused find in words (item 9 visual review P2-1): no answer is one retry line; any other
 * refusal is the server's message when the capture has one, else one plain sentence. The
 * envelope's raw error string or code never reaches the person.
 */
export function payBalanceFindRefusalWords(err: { status: number; error: string; message?: string }): string {
  if (err.status === 0) return "No answer from the server. Try again.";
  if (err.message && err.message !== "") return err.message;
  return "The search was refused: try again, or ask a manager.";
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function whole(value: unknown): value is number {
  return typeof value === "number" && Number.isSafeInteger(value);
}

function readPayments(raw: unknown): OwingPayment[] | null {
  if (!Array.isArray(raw)) return null;
  const out: OwingPayment[] = [];
  for (const entry of raw) {
    if (!isRecord(entry) || typeof entry["payment_id"] !== "string" || typeof entry["method"] !== "string" || !whole(entry["amount_cents"])) {
      return null;
    }
    out.push({ paymentId: entry["payment_id"], method: entry["method"], amountCents: entry["amount_cents"] });
  }
  return out;
}

/** The find answer, row by row: a row that is not a readable order is dropped, never half shown. */
export function readOwingOrders(raw: unknown): OwingOrder[] {
  if (!isRecord(raw) || !Array.isArray(raw["items"])) return [];
  const out: OwingOrder[] = [];
  for (const entry of raw["items"]) {
    if (!isRecord(entry)) continue;
    const payments = readPayments(entry["payments"]);
    if (
      payments === null ||
      typeof entry["order_id"] !== "string" ||
      typeof entry["order_no"] !== "string" ||
      typeof entry["customer_id"] !== "string" ||
      typeof entry["customer_name"] !== "string" ||
      typeof entry["fulfilment"] !== "string" ||
      !whole(entry["order_total_cents"]) ||
      !whole(entry["paid_cents"]) ||
      !whole(entry["still_owed_cents"]) ||
      typeof entry["held_from_picking"] !== "boolean"
    ) {
      continue;
    }
    out.push({
      orderId: entry["order_id"],
      orderNo: entry["order_no"],
      customerId: entry["customer_id"],
      customerName: entry["customer_name"],
      job: typeof entry["job"] === "string" && entry["job"] !== "" ? entry["job"] : null,
      fulfilment: entry["fulfilment"],
      orderTotalCents: entry["order_total_cents"],
      paidCents: entry["paid_cents"],
      stillOwedCents: entry["still_owed_cents"],
      held: entry["held_from_picking"],
      holdWords: typeof entry["hold_words"] === "string" && entry["hold_words"] !== "" ? entry["hold_words"] : null,
      payments,
    });
  }
  return out;
}

/** The take's answer, whole or not at all. */
export function readTaken(raw: unknown): TakenBalance | null {
  if (!isRecord(raw)) return null;
  const payments = readPayments(raw["payments"]);
  const method = raw["method"];
  if (
    payments === null ||
    typeof raw["order_id"] !== "string" ||
    typeof raw["order_no"] !== "string" ||
    (method !== "CASH" && method !== "CARD") ||
    !whole(raw["amount_cents"]) ||
    typeof raw["paid_words"] !== "string" ||
    typeof raw["released_to_picking"] !== "boolean" ||
    !whole(raw["still_owed_cents"])
  ) {
    return null;
  }
  return {
    orderId: raw["order_id"],
    orderNo: raw["order_no"],
    method,
    amountCents: raw["amount_cents"],
    paidWords: raw["paid_words"],
    released: raw["released_to_picking"],
    stillOwedCents: raw["still_owed_cents"],
    payments,
    replayed: raw["replayed"] === true,
  };
}
