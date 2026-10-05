// _order-payment.ts: the counter payment panel's pure logic (QD-5), DOM-free
// so the Node runner can drive it. The component (order-payment-panel.ts)
// renders; it does not judge.
//
// WHAT THIS FILE HOLDS, and what it deliberately does not:
//
// - the tender roster, exactly as the contract names the four counter tenders
//   (CASH, CARD, CHEQUE, EFT), and their counter words for the receipt;
// - the amount parse: what the cashier typed in DOLLARS, read to integer
//   cents through the digits, never through a float multiply. The parse is
//   the pos-kit's named helper (ports.ts parseAmountCents), the one reviewed
//   implementation the till's own tender boxes use, reached by deep path;
// - the idempotency key's lifecycle: minted once per payment ATTEMPT, kept
//   across retries of the same attempt (a refusal, an unknown outcome), new
//   only for a new deliberate payment. PaymentKeyRing is the whole rule;
// - the receipt's wording: the server's own figures, row by row, never a
//   figure this side computes;
// - the refusal and unknown outcome wording, in the app's spoken voice.
//
// It holds no rule about who may pay, what a cash customer owes or what a
// balance is: those are the server's, and the panel shows the server's words.
import { parseAmountCents } from "@hardscapehouse/pos-kit/src/ported/ports.js";
import { moneyWord, quoteErrorMessage } from "./_quote-edit.js";
import { spokenRefusal } from "./_quote-workarea.js";

/** The four tenders the contract's quoteTakeOrderPayment accepts, in its own order. */
export const TENDER_METHODS = ["CASH", "CARD", "CHEQUE", "EFT"] as const;
export type TenderMethod = (typeof TENDER_METHODS)[number];

/** A tender's counter word for the receipt: the contract's own name, spoken. */
export function tenderWord(method: string): string {
  switch (method) {
    case "CASH":
      return "Cash";
    case "CARD":
      return "Card";
    case "CHEQUE":
      return "Cheque";
    case "EFT":
      return "EFT";
    default:
      return method;
  }
}

/**
 * What the cashier typed, as integer cents. null when it is not a number this
 * desk can send (empty, not digits, or more than two decimal places): an
 * empty box and a deliberate zero are different things, and the server judges
 * zero, not this parse. Dollars in, cents out, through the digits: the typed
 * string is split at the point and the fraction padded, never multiplied.
 */
export function typedCents(raw: string): number | null {
  const cents = parseAmountCents(raw);
  return Number.isNaN(cents) ? null : cents;
}

// ── the idempotency key ─────────────────────────────────────────────────────

/** A UUID from the platform when it offers one. */
function randomUUID(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  return "";
}

/** The in memory fallback for a runtime with no crypto.randomUUID: a counter on a random root. */
const fallbackRoot = Math.floor(Math.random() * 0xffffffff).toString(16).padStart(8, "0");
let fallbackCount = 0;
function fallbackUUID(): string {
  fallbackCount += 1;
  return `key-${fallbackRoot}-${Date.now().toString(16)}-${fallbackCount}`;
}

/** One idempotency key, minted once per payment attempt. */
export function mintPaymentKey(): string {
  const fromPlatform = randomUUID();
  return fromPlatform !== "" ? fromPlatform : fallbackUUID();
}

/**
 * PaymentKeyRing holds the key of the payment now being attempted. The rule
 * is the ruling one: a key is minted ONCE per attempt and kept across retries
 * of that attempt, so a retry after a refusal, a timeout or a dropped answer
 * replays the first result on the server and posts nothing a second time.
 * Only a completed payment ends an attempt: the next deliberate payment mints
 * a fresh key. A different quote is a different payment, so a quote switch
 * resets the ring.
 */
export class PaymentKeyRing {
  private key = "";
  private readonly mint: () => string;

  constructor(mint: () => string = mintPaymentKey) {
    this.mint = mint;
  }

  /** The key of the attempt now being made, minting it on first use. */
  current(): string {
    if (this.key === "") this.key = this.mint();
    return this.key;
  }

  /** True when a key is already held (an attempt is under way or was refused). */
  held(): boolean {
    return this.key !== "";
  }

  /**
   * The attempt ended. tookPayment true means the server took the money: the
   * attempt is complete and the next one mints a fresh key. A refusal or an
   * unknown outcome keeps the key, so the retry is the same attempt.
   */
  settled(tookPayment: boolean): void {
    if (tookPayment) this.key = "";
  }

  /** A new quote is a new payment: the next attempt mints a fresh key. */
  reset(): void {
    this.key = "";
  }
}

// ── the receipt ─────────────────────────────────────────────────────────────

/** One proven row of the receipt the panel shows after a payment. */
export interface ReceiptRow {
  label: string;
  value: string;
}

/** The receipt's figures as the server answered them: never a figure this side computes. */
export interface ReceiptAnswer {
  orderNo: string;
  invoiceNo: string;
  paymentNo: string;
  method: string;
  amountCents: number;
  balanceCents: number;
  receivedBy: string;
  reference: string;
}

/**
 * Who took the payment, as the server stored it. The answer carries the staff
 * member's id; when it is the signed in caller's own id the receipt says
 * "you", and any other id is shown as the server holds it. No name is
 * invented for an id this side cannot resolve.
 */
export function receivedByWord(receivedBy: string, callerId: string): string {
  return receivedBy === callerId && callerId !== "" ? "you" : receivedBy;
}

/** The receipt: every figure the server answered, row by row, in the order a counter reads them. */
export function receiptRows(r: ReceiptAnswer, callerId: string): ReceiptRow[] {
  return [
    { label: "Invoice", value: r.invoiceNo },
    { label: "Order", value: r.orderNo },
    { label: "Payment", value: r.paymentNo },
    { label: "Tender", value: tenderWord(r.method) },
    { label: "Amount", value: moneyWord(r.amountCents) },
    { label: "Balance on the invoice", value: moneyWord(r.balanceCents) },
    { label: "Received by", value: receivedByWord(r.receivedBy, callerId) },
    { label: "Reference as recorded", value: r.reference === "" ? "none given" : r.reference },
  ];
}

/** Said under the receipt when the one action also converted the quote (a customer with no credit account). */
export function convertedNote(): string {
  return "This one action converted the quote, raised the order's invoice and took the payment: the order never existed unpaid.";
}

/** Said under the receipt when the answer is a replay: the key had already taken this payment. */
export function replayedNote(): string {
  return "This key had already taken a payment, so nothing was posted a second time. The figures above are the first payment as the server recorded them; the balance is the live one.";
}

/** Said when no answer came back, or what came back could not be read. */
export function unknownOutcomeNote(): string {
  return "The outcome of that payment is not known: no answer the desk can read came back. Trying again with the same amount and method is safe: the server replays the first result and posts nothing a second time.";
}

/** A refusal in the app's spoken voice; the server's own sentence is the message. */
export function paymentRefusalWords(status: number | undefined, error: string): string {
  return spokenRefusal(quoteErrorMessage(status, error, "take this payment"));
}

/**
 * True when an answer must be read as an UNKNOWN outcome rather than a
 * refusal the server chose to send: no status (nothing answered, or a success
 * body this side could not read) or a 5xx. A retry after either reuses the
 * same key.
 */
export function isUnknownOutcome(status: number | undefined): boolean {
  return status === undefined || status >= 500;
}
