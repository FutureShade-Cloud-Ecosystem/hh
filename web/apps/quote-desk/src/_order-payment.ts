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
//   only for a new deliberate payment. The attempt belongs to ITS QUOTE and
//   is kept across quote switches within the session: a person who leaves
//   mid payment and comes back finds the same key, the same figures and the
//   same not-known note, so a retry replays the first result on the server
//   and never posts a second payment. AttemptBook is the whole rule;
// - the receipt's wording: the server's own figures, row by row, never a
//   figure this side computes;
// - the refusal and unknown outcome wording, in the app's spoken voice.
//
// It holds no rule about who may pay, what a cash customer owes or what a
// balance is: those are the server's, and the panel shows the server's words.
import { parseAmountCents } from "@hardscapehouse/pos-kit/src/ported/ports.js";
import { docStatusWord, kindLabel, type RelatedDoc } from "./_related-docs.js";
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
 * What the cashier typed, as integer cents, with the words that teach the refusal when the desk
 * will not send it. null cents when it is not a number this desk can send (empty, not digits, or
 * more than two decimal places): an empty box and a deliberate zero are different things, and
 * the server judges zero, not this parse. Dollars in, cents out, through the digits: the typed
 * string is split at the point and the fraction padded, never multiplied.
 *
 * THE APP'S OWN GUARD SITS IN FRONT of the shared pos-kit parse (parseAmountCents), which strips
 * every comma and space because people paste from invoices: on this desk a comma or a space is a
 * decimal separator waiting to happen ("0,50" for fifty cents becomes fifty DOLLARS when the
 * separator is stripped), so a comma or a space is accepted only as valid thousands grouping
 * (groups of exactly three digits after the first, and only left of the point) and anything else
 * is refused with the words that teach the point. The shared helper itself stays untouched: the
 * till's own tender boxes share it, and their exposure is the principal's to rule on. Anything
 * above Number.MAX_SAFE_INTEGER cents is refused too: the shared parse of a very long digit
 * string rounds through a float and can land a cent off, so the desk sends nothing it cannot
 * name exactly.
 */
export interface TypedAmount {
  /** The parsed cents, null when the desk refuses to send what was typed. */
  cents: number | null;
  /** "" when the cents parsed; the words that teach the refusal when they did not. */
  problem: string;
}

/** The words that teach the point, for a comma or a space that is not thousands grouping. */
export const COMMA_WORDS = "Use a point for cents, for example 12.50.";

/** The words that teach the grouping: one kind of separator, every three digits. */
export const GROUPING_WORDS = "Type the digits and use one separator every three digits, like 1,000.50.";

/** The words for an amount that is not a number this desk can send. */
export const NOT_NUMBER_WORDS = "The amount is not a number this desk can send: type dollars and cents, like 40.00.";

/** The words for an amount no counter can name exactly in integer cents. */
export const OVERSAFE_WORDS = "The amount is larger than this desk can send.";

/**
 * The desk's own shape check, in front of the shared pos-kit parse: what was typed must be digits,
 * with at most one decimal point, a fraction of one or two digits, and (left of the point) digits
 * with at most one kind of thousands separator in groups of exactly three. Lowercase letters,
 * signs, unicode digits, a second point, any junk, are refused here in the desk's own words, so
 * the refusal is never silent and never the shared helper's NaN leaking through: a decimal comma
 * or a space in the fraction keeps the words that teach the point; junk that is not a number at
 * all gets the not a number words; a mixed grouping (a comma and a space in one figure) gets the
 * grouping words. An empty box is not a problem here: the panel does not send one.
 */
export function typedAmountProblem(raw: string): string {
  const t = raw.trim();
  if (t === "") return "";
  const body = t.startsWith("$") ? t.slice(1) : t;
  const pointAt = body.indexOf(".");
  if (pointAt !== -1 && body.indexOf(".", pointAt + 1) !== -1) return NOT_NUMBER_WORDS;
  const whole = pointAt === -1 ? body : body.slice(0, pointAt);
  const fraction = pointAt === -1 ? "" : body.slice(pointAt + 1);
  if (/[,\s]/.test(fraction)) return COMMA_WORDS;
  if (/[^0-9]/.test(fraction) || fraction.length > 2) return NOT_NUMBER_WORDS;
  if (/[^0-9, ]/.test(whole)) return NOT_NUMBER_WORDS;
  if (whole === "") return "";
  const comma = whole.includes(",");
  const space = whole.includes(" ");
  if (comma && space) return GROUPING_WORDS;
  if (comma || space) {
    const sep = comma ? "," : " ";
    if (!new RegExp(`^[0-9]{1,3}(${sep}[0-9]{3})*$`).test(whole)) return COMMA_WORDS;
  }
  return "";
}

export function parseTypedAmount(raw: string): TypedAmount {
  const problem = typedAmountProblem(raw);
  if (problem !== "") return { cents: null, problem };
  const cents = typedCents(raw.trim());
  if (cents === null) return { cents: null, problem: NOT_NUMBER_WORDS };
  if (cents > Number.MAX_SAFE_INTEGER) return { cents: null, problem: OVERSAFE_WORDS };
  return { cents, problem: "" };
}

/** The shared pos-kit parse the desk's own guard (parseTypedAmount) sits in front of. */
export function typedCents(raw: string): number | null {
  const parsed = parseAmountCents(raw);
  return Number.isNaN(parsed) ? null : parsed;
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
 * What a kept attempt is doing right now.
 *
 * - "in-flight": the POST is out. No second attempt may be opened for the
 *   quote (the button is held, the fields are locked).
 * - "not-known": no answer the desk can read came back (no status, a 5xx).
 *   The fields stay locked to the attempt's own figures and a retry sends
 *   exactly them with the same key, so the server replays the first result.
 * - "held": the server answered that THIS KEY took a different payment. The
 *   attempt's figures stay visible and locked (a retry is still offered, for
 *   the case the server state differs), and the way forward is a deliberate
 *   new payment on a fresh key, offered only after the family is re-read.
 * - "kept": a plain refusal (the amounts, a role, a frozen state). The key
 *   stays so the corrected retry is the same attempt, and the fields stay
 *   editable so the correction can be typed.
 */
export type AttemptPhase = "in-flight" | "not-known" | "held" | "kept";

/** One payment attempt, exactly as it was sent, kept for its quote. */
export interface PaymentAttempt {
  key: string;
  /** The amount box's own text when the attempt was made, restored verbatim. */
  amountRaw: string;
  amountCents: number;
  tender: TenderMethod;
  reference: string;
  /** The order the payment was aimed at, "" for the one action on an accepted quote. */
  orderId: string;
  phase: AttemptPhase;
}

/** The figures a new attempt is opened from. */
export interface AttemptInput {
  amountRaw: string;
  amountCents: number;
  tender: TenderMethod;
  reference: string;
  orderId: string;
}

/**
 * AttemptBook holds one payment attempt per quote, keyed by the quote's id,
 * and keeps it across quote switches: a payment belongs to the quote it was
 * made on, so leaving and returning neither drops its key nor mints a second
 * one. A key is minted ONCE per attempt and kept across its retries (a
 * refusal, a dropped answer), so a retry replays the first result on the
 * server and posts nothing a second time. Only a payment the server took (or
 * a deliberate new payment after the re-read) ends an attempt. A different
 * quote is a different payment and holds its own attempt. The book dies with
 * the panel that owns it: sign out ends the session and nothing is carried.
 */
export class AttemptBook {
  private readonly attempts = new Map<string, PaymentAttempt>();
  private readonly mint: () => string;

  constructor(mint: () => string = mintPaymentKey) {
    this.mint = mint;
  }

  /** The quote's kept attempt, whatever its phase, or null when it has none. */
  attempt(quoteId: string): PaymentAttempt | null {
    return this.attempts.get(quoteId) ?? null;
  }

  /**
   * Begin (or re-arm) the quote's attempt. With no attempt held it mints a
   * fresh key. Over a "kept" attempt (a plain refusal) it reuses the SAME key
   * with the corrected figures: the corrected retry is the same attempt. An
   * attempt still out is returned untouched: the panel's busy flag is what
   * keeps a second POST from ever being opened, and the book never re-arms
   * one behind its back.
   */
  open(quoteId: string, input: AttemptInput): PaymentAttempt {
    const existing = this.attempts.get(quoteId);
    if (existing !== undefined) {
      if (existing.phase === "in-flight") return existing;
      const rearmed: PaymentAttempt = { ...input, key: existing.key, phase: "in-flight" };
      this.attempts.set(quoteId, rearmed);
      return rearmed;
    }
    const attempt: PaymentAttempt = { ...input, key: this.mint(), phase: "in-flight" };
    this.attempts.set(quoteId, attempt);
    return attempt;
  }

  /** Move the quote's attempt to a new phase (a late answer, a refusal, a retry's outcome). */
  mark(quoteId: string, phase: AttemptPhase): void {
    const existing = this.attempts.get(quoteId);
    if (existing !== undefined) this.attempts.set(quoteId, { ...existing, phase });
  }

  /** The attempt ended in a payment the server took: the next deliberate payment mints a fresh key. */
  clear(quoteId: string): void {
    this.attempts.delete(quoteId);
  }

  /** The session ended (a new client, sign out): no attempt of one session is another's. */
  clearAll(): void {
    this.attempts.clear();
  }

  /** An attempt kept across a sign out rejoins the book for its quote: the same key, so a retry replays the first outcome. */
  restore(quoteId: string, attempt: PaymentAttempt): void {
    this.attempts.set(quoteId, attempt);
  }

  /** Every attempt still out, with the quote it belongs to: what a sign out waits for. */
  out(): Array<{ quoteId: string; attempt: PaymentAttempt }> {
    const rows: Array<{ quoteId: string; attempt: PaymentAttempt }> = [];
    for (const [quoteId, attempt] of this.attempts) {
      if (attempt.phase === "in-flight") rows.push({ quoteId, attempt });
    }
    return rows;
  }

  /** Every attempt whose outcome no answer decided (not known, held, or still out): what a sign out keeps for the next sign in. */
  unsettled(): Array<{ quoteId: string; attempt: PaymentAttempt }> {
    const rows: Array<{ quoteId: string; attempt: PaymentAttempt }> = [];
    for (const [quoteId, attempt] of this.attempts) {
      if (attempt.phase !== "kept") rows.push({ quoteId, attempt });
    }
    return rows;
  }
}

// ── the attempts a sign out keeps for the next sign in ──────────────────────

/**
 * The attempts that were still out, or whose outcome no answer had decided,
 * when a session ended. They are kept HERE, past the session, so the next
 * sign in meets them as known attempts with the same idempotency key: a retry
 * then replays the server's first outcome instead of posting a second
 * payment. Page memory only: a reload of the page ends the session and these
 * with it.
 */
const leftoverAttempts = new Map<string, PaymentAttempt>();

/** Keep a quote's attempt past the session that made it. */
export function keepLeftoverAttempt(quoteId: string, attempt: PaymentAttempt): void {
  leftoverAttempts.set(quoteId, { ...attempt });
}

/** The quote's kept attempt, moved out: the next session owns it now. */
export function takeLeftoverAttempt(quoteId: string): PaymentAttempt | null {
  const attempt = leftoverAttempts.get(quoteId);
  if (attempt === undefined) return null;
  leftoverAttempts.delete(quoteId);
  return attempt;
}

/** The entry page's words for a payment whose outcome no answer decided when the person signed out. */
export function leftoverAttemptNote(quoteNo: string, attempt: PaymentAttempt): string {
  const figures = `a payment of ${moneyWord(attempt.amountCents)} by ${tenderWord(attempt.tender)} on ${quoteNo}`;
  if (attempt.phase === "held") {
    return `When you signed out, the server had said this key took a different payment: ${figures} is kept as a known attempt for the next sign in, and a new payment starts after a fresh read.`;
  }
  return `When you signed out, ${figures} was still out, or its outcome was not known. It is kept as a known attempt, not dropped: the next sign in finds it with the same key, and trying again is safe, because the server answers with the first outcome and never posts a second payment.`;
}

/** The entry page's words for a payment the server took while the person was signing out. */
export function leftoverTakenNote(
  quoteNo: string,
  answer: { amountCents: number; method: string; paymentNo: string },
): string {
  return `As you signed out, a payment of ${moneyWord(answer.amountCents)} by ${tenderWord(answer.method)} on ${quoteNo} was taken and recorded by the server as ${answer.paymentNo}. It was not dropped, and it was not asked for twice.`;
}

/**
 * True when a refusal is the server saying THIS KEY took a different payment
 * (the core's and the stub's own sentence). Such a key can never take another
 * payment: retrying it any number of ways loops on the same refusal, so the
 * panel holds the attempt and offers the deliberate way forward.
 */
export function isDifferentPaymentRefusal(error: string): boolean {
  return error.includes("took a different payment");
}

// ── the balance a payment here is bounded by ────────────────────────────────

/**
 * The balance a payment on this quote is bounded by, as the reads carry it. Never a figure this
 * side computes: every arm is a figure the server sent.
 *
 * - No invoice yet: the door raises the agreed invoice, whose opening balance IS the agreed
 *   total the order row (or, before any order, the quote row) carries.
 * - A FINALIZED (Open) invoice: nothing has been applied, so the balance is its total.
 * - A PAID invoice: the balance is zero.
 * - A PARTIALLY_PAID invoice: the balance is the live figure the receipt of this session's
 *   payment answered, when that receipt is for this invoice. Without it, null: the family read
 *   carries the part-paid state but not the figure, and a guessed balance is a confident wrong
 *   number. Anything else (a draft, a void, more than one invoice or order) is null too.
 */
export function paymentTargetBalance(
  agreed: { totalCents: number } | null,
  invoice: RelatedDoc | null,
  receipt: { invoiceId: string; balanceCents: number } | null,
): number | null {
  if (invoice === null) return agreed === null ? null : agreed.totalCents;
  if (invoice.status === "PAID") return 0;
  if (invoice.status === "FINALIZED") return invoice.totalCents;
  if (invoice.status === "PARTIALLY_PAID" && receipt !== null && receipt.invoiceId === invoice.id) return receipt.balanceCents;
  return null;
}

/**
 * The figures line under the form: the server's own documents in its own words, and the balance
 * a payment here is bounded by when the reads carry it (the family read carries the documents
 * and their totals; the balance comes from paymentTargetBalance, never from arithmetic here).
 */
export function figuresLine(quote: RelatedDoc | null, order: RelatedDoc | null, invoices: RelatedDoc[], balanceCents: number | null, orderCount: number): string {
  if (order === null) {
    if (quote === null) return "";
    const parts = [`${kindLabel(quote.kind)} ${quote.number} is ${docStatusWord(quote.kind, quote.status)} at ${moneyWord(quote.totalCents)}, the agreed total`];
    if (balanceCents !== null) parts.push(`balance ${moneyWord(balanceCents)}`);
    return parts.join(" · ");
  }
  const parts = [`${kindLabel(order.kind)} ${order.number} is ${docStatusWord(order.kind, order.status)} at ${moneyWord(order.totalCents)}`];
  for (const inv of invoices) parts.push(`${kindLabel(inv.kind)} ${inv.number} is ${docStatusWord(inv.kind, inv.status)} at ${moneyWord(inv.totalCents)}`);
  if (orderCount > 1) parts.push(`the server holds ${orderCount} orders for this quote and will say which one to pay`);
  else if (balanceCents !== null) parts.push(`balance ${moneyWord(balanceCents)}`);
  else if (invoices.length > 0) parts.push("the balance it still owes is not carried by this read, the server holds it");
  return parts.join(" · ");
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

/** The words a payment's role refusal is said in when the gate's body carries none of its own. */
export const PAYMENT_ROLE_WORDS = "Taking a payment on this order is not open to your login. Ask a manager or the counter.";

/** A refusal in the app's spoken voice; the server's own sentence is the message. */
export function paymentRefusalWords(status: number | undefined, error: string): string {
  if (status === 403) {
    const body = (error ?? "").trim();
    // The authz gate's body is the single word "Forbidden" or nothing at all, which says nothing
    // a cashier can act on; when this door's own role rule refuses it carries a sentence of its
    // own (it names the role and who to ask) and the server's words are always the message. Only
    // the wordless body gets the desk's own sentence, which names what to do.
    if (body !== "" && body !== "Forbidden") return spokenRefusal(quoteErrorMessage(403, body, "take this payment"));
    return PAYMENT_ROLE_WORDS;
  }
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
