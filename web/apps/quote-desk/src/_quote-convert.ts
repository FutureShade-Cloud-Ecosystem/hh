// _quote-convert.ts: the guided Convert to Order surface's pure decisions (the `_*.ts` house
// convention: DOM-free, unit tested on the Node runner). The server holds every rule: the gate
// that refuses an order, the totals, the stock. This module only routes, words and orders what the
// server answered, and says "not reported" where a read is missing rather than guessing.
import { moneyWord } from "./_quote-edit.js";
import { qtyLabel, spokenRefusal } from "./_quote-workarea.js";

// ── the route ──────────────────────────────────────────────────────────────────

/** The surface's address: #/quotes/<quote id or number>/convert. */
const CONVERT_ROUTE = /^#\/quotes\/([A-Za-z0-9_-]+)\/convert\/?$/;

/** The quote a hash addresses on the convert surface, or "" when the hash is some other place. */
export function convertRouteQuoteId(hash: string): string {
  const match = CONVERT_ROUTE.exec(hash);
  return match === null ? "" : (match[1] ?? "");
}

/** The quotes list's own address, where the done screen sends the person back. */
export const QUOTES_ROUTE = "#/quotes";

// ── the steps ──────────────────────────────────────────────────────────────────

export const CONVERT_STEPS = ["review", "fulfilment", "standing", "payment", "confirm"] as const;
export type ConvertStep = (typeof CONVERT_STEPS)[number];

export const STEP_LABEL: Record<ConvertStep, string> = {
  review: "Review",
  fulfilment: "Fulfilment",
  standing: "Customer standing",
  payment: "Payment",
  confirm: "Confirm",
};

export function stepAfter(step: ConvertStep): ConvertStep {
  const at = CONVERT_STEPS.indexOf(step);
  return CONVERT_STEPS[Math.min(at + 1, CONVERT_STEPS.length - 1)] ?? step;
}

export function stepBefore(step: ConvertStep): ConvertStep {
  const at = CONVERT_STEPS.indexOf(step);
  return CONVERT_STEPS[Math.max(at - 1, 0)] ?? step;
}

// ── fulfilment ─────────────────────────────────────────────────────────────────

export type Fulfilment = "PICKUP" | "WILL_CALL";

/** The server's two (QuoteConvertRequest.fulfillment). */
export const FULFILMENTS: ReadonlyArray<{ value: Fulfilment; label: string; hint: string }> = [
  { value: "PICKUP", label: "Pickup", hint: "The customer collects the order at the yard." },
  { value: "WILL_CALL", label: "Will-call", hint: "The order is held at the yard until the customer calls for it." },
];

export function fulfilmentLabel(value: Fulfilment): string {
  return FULFILMENTS.find((f) => f.value === value)?.label ?? value;
}

/** What the server says about delivery at conversion: it is not chosen here, it is booked afterwards. */
export const DELIVERY_NOTE =
  "Delivery is not chosen when an order is created. A delivery is booked by scheduling a stop once the order exists.";

// ── stock ──────────────────────────────────────────────────────────────────────

export interface LineStockIn {
  grade: string;
  qtyBase: number;
  availableBase: number;
}

export interface StockWords {
  /** "ok" covers the line; "short" will wait on stock; "unknown" is not reported, never read as none. */
  kind: "ok" | "short" | "unknown";
  text: string;
}

/** A line's stock at the branch, in words; only a short line carries the warning. */
export function stockWords(stock: LineStockIn | null | undefined, branchName: string): StockWords {
  const where = branchName === "" ? "the branch" : branchName;
  if (stock === null || stock === undefined) return { kind: "unknown", text: "Stock not reported." };
  if (stock.grade === "GREEN") return { kind: "ok", text: `${qtyLabel(stock.availableBase)} available at ${where}.` };
  if (stock.grade === "THIN") {
    return {
      kind: "short",
      text: `Only ${qtyLabel(stock.availableBase)} of ${qtyLabel(stock.qtyBase)} available at ${where}. The order will wait on stock for the rest.`,
    };
  }
  if (stock.grade === "NONE") {
    return { kind: "short", text: `None available at ${where}. The order will wait on stock until it arrives.` };
  }
  return { kind: "unknown", text: "Stock not reported." };
}

// ── the customer's standing ────────────────────────────────────────────────────

/** The account facts the customer search reads (the picker's own shape). */
export interface StandingIn {
  name: string;
  paymentTermsDays: number;
  creditLimitCents: number;
  openBalanceCents: number;
}

export interface Standing {
  /** An account customer pays on terms; a customer with no credit account pays on delivery. */
  terms: "ACCOUNT" | "COD" | "UNKNOWN";
  termsText: string;
  /** "within" the limit, "over" it (the gate will refuse), "none" for a cash customer, "unknown" where no read answered. */
  gate: "within" | "over" | "none" | "unknown";
  gateText: string;
}

/**
 * The standing before confirming, from the customer read and the quote total. The server's gate has
 * the last word: this is the heads up so a refusal is never a surprise, and it is worded as such.
 */
export function standingOf(read: StandingIn | null, totalCents: number): Standing {
  if (read === null) {
    return {
      terms: "UNKNOWN",
      termsText: "Terms not reported.",
      gate: "unknown",
      gateText: "The credit position could not be read, so the server's credit check will decide when you confirm.",
    };
  }
  if (read.creditLimitCents <= 0) {
    return {
      terms: "COD",
      termsText: "Cash on delivery: no credit account, so payment is due on release.",
      gate: "none",
      gateText: "No credit account, so there is no credit limit to check.",
    };
  }
  const termsText = `Account customer, ${read.paymentTermsDays} days.`;
  const after = read.openBalanceCents + totalCents;
  if (after > read.creditLimitCents) {
    return {
      terms: "ACCOUNT",
      termsText,
      gate: "over",
      gateText: `This order would take the account to ${moneyWord(after)} against a limit of ${moneyWord(read.creditLimitCents)}, over by ${moneyWord(after - read.creditLimitCents)}. The server will refuse it at confirm unless the account is brought back within its limit first.`,
    };
  }
  return {
    terms: "ACCOUNT",
    termsText,
    gate: "within",
    gateText: `The account stays within its limit: ${moneyWord(after)} against ${moneyWord(read.creditLimitCents)}. The server checks again at confirm.`,
  };
}

/**
 * The server's refusal at confirm, in plain words, with what to do next. The refusal's own sentence
 * (spoken, its module prefix peeled) stays the first thing said; the next step follows it.
 */
export function refusalWords(status: number | undefined, detail: string): string {
  const said = spokenRefusal(detail.trim());
  const credit = /credit|limit|hold/i.test(said);
  if (status === 409 && credit) {
    return `${said}${said.endsWith(".") ? "" : "."} Nothing was created. Ask the customer to settle the account, or have a manager release the credit hold, then convert again.`;
  }
  if (status === 409) {
    return `${said}${said.endsWith(".") ? "" : "."} Nothing was created. Go back to the quote, check its status, then convert again.`;
  }
  if (status === 403) {
    return `${said || "This role may not convert quotes."} Nothing was created. Ask a manager to convert it.`;
  }
  return `${said || "The order could not be created."}${said === "" || said.endsWith(".") ? "" : "."} Nothing was created. Try again, and if it keeps failing tell a manager.`;
}

// ── the payment ────────────────────────────────────────────────────────────────

export const PAY_METHODS = ["CASH", "CARD", "CHEQUE", "EFT"] as const;
export type PayMethod = (typeof PAY_METHODS)[number];

export const PAY_METHOD_LABEL: Record<PayMethod, string> = { CASH: "Cash", CARD: "Card", CHEQUE: "Cheque", EFT: "EFT" };

/** The longest reference the field takes; a cheque number or a terminal reference is short. */
export const MAX_REFERENCE = 60;

/**
 * The amount typed, in dollars and cents, to integer cents through the decimal string (never a
 * float multiply). Anything that is not plain dollars and cents is refused in words; zero is refused
 * because the server takes a positive amount only.
 */
export function parsePayAmount(raw: string): { cents: number } | { error: string } {
  const text = raw.trim();
  const match = /^(\d{1,7})(?:\.(\d{1,2}))?$/.exec(text);
  if (match === null) {
    return { error: `"${text}" is not a dollar amount. Type it as 50 or 50.25, or skip the payment.` };
  }
  const cents = Number(match[1]) * 100 + Number((match[2] ?? "").padEnd(2, "0"));
  if (cents < 1) return { error: "The payment must be more than zero. Type it as 50 or 50.25, or skip the payment." };
  return { cents };
}

/** A fresh key per intended payment, kept across a retry so a replay posts nothing twice. */
export function newIdempotencyKey(random: () => string = () => crypto.randomUUID()): string {
  return random();
}
