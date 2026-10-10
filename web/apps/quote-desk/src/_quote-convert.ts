// _quote-convert.ts: the guided Convert to Order surface's pure decisions (the `_*.ts` house
// convention: DOM-free, unit tested on the Node runner). The server holds every rule: the gate
// that refuses an order, the totals, the stock. This module only routes, words and orders what the
// server answered, and says "not reported" where a read is missing rather than guessing.
import { moneyWord } from "./_quote-edit.js";
import { isUnknownOutcome } from "./_order-payment.js";
import { qtyLabel, spokenRefusal } from "./_quote-workarea.js";

// ── the route ──────────────────────────────────────────────────────────────────

/** The surface's address: #/quotes/<quote id or number>/convert. */
const CONVERT_ROUTE = /^#\/quotes\/([A-Za-z0-9_-]+)\/convert\/?$/;

/** The quote a hash addresses on the convert surface, or "" when the hash is some other place. */
export function convertRouteQuoteId(hash: string): string {
  const match = CONVERT_ROUTE.exec(hash);
  return match === null ? "" : (match[1] ?? "");
}

/** The window event a payment settled after the person left is announced on, so the desk can say it wherever they are. */
export const CONVERT_NOTICE_EVENT = "quote-convert-notice";

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

/** What the counter's account standing read says; an absent field is the read saying nothing. */
export interface HoldRead {
  standing: "walk_in" | "clear" | "past_due" | "on_hold";
  onHold: boolean;
  pastDueCents: number | null;
  oldestBucket: string | null;
}

export interface Standing {
  /** An account customer pays on terms; a Cash On Demand customer has no credit account; UNKNOWN where no read answered. */
  terms: "ACCOUNT" | "COD" | "UNKNOWN";
  termsText: string;
  /** "hold" where the server reports a credit hold; "server" where the server's own check decides at confirm. */
  gate: "hold" | "pastdue" | "clear" | "server" | "cod" | "unknown";
  gateText: string;
}

/** The words for a customer with no credit account (decision log row 999): the order is made, and nothing is released until it is paid. */
export const COD_TERMS_TEXT = "Cash On Demand: nothing is released until it is paid.";

const SERVER_DECIDES = "The server checks the credit account and any credit hold when you confirm, and the server decides.";

/**
 * The server's own rule for a customer with no credit account: a credit limit of nothing. The real
 * gate refuses such a customer's plain conversion and takes the whole payment at its own door.
 */
export function isCashOnDemand(read: StandingIn | null): boolean {
  return read !== null && accountKind(read) === "NO_CREDIT";
}

/**
 * How a customer pays, read once for the desk's every surface (the guided Convert and the payment panel): no credit
 * line at all, the product's own Cash On Demand account (zero-day terms, and a credit line beside them), or an
 * account on terms. The one place the rule is written; the words stay the callers' own.
 */
export type AccountKind = "NO_CREDIT" | "COD_TERMS" | "ON_ACCOUNT";

export function accountKind(read: StandingIn): AccountKind {
  if (read.creditLimitCents <= 0) return "NO_CREDIT";
  if (read.paymentTermsDays === 0) return "COD_TERMS";
  return "ON_ACCOUNT";
}

/** The sentence an accepted quote's payment panel says about how its customer pays, and where the person goes. */
export function acceptedPaymentWords(read: StandingIn | null): string {
  if (read === null) {
    return "This quote is accepted. The customer's account could not be read, so how they pay is not known here. Convert to Order opens the steps, and the server decides at the end.";
  }
  const kind = accountKind(read);
  if (kind === "NO_CREDIT") {
    return "This quote is accepted. This customer has no credit account (Cash On Demand): Convert to Order opens the steps, and its Payment step takes the whole payment before anything is released.";
  }
  if (kind === "COD_TERMS") {
    return "This quote is accepted. This customer is Cash On Demand (pay before release): Convert to Order opens the steps, and nothing is released until it is paid.";
  }
  return `This quote is accepted. This customer is on account (${read.paymentTermsDays} days): Convert to Order opens the steps, and any payment is taken on the order it becomes.`;
}

/**
 * The standing before confirming, from the server's reads and nothing else: the terms, the limit and
 * the open balance as the server reports them, and the hold the counter's standing read reports. No
 * sum is made here and no pass or fail is promised; the server's gate has the last word at confirm.
 */
export function standingOf(read: StandingIn | null, hold: HoldRead | null): Standing {
  if (read === null) {
    return {
      terms: "UNKNOWN",
      termsText: "The customer's terms could not be read.",
      gate: "unknown",
      gateText: `The customer's account could not be read. ${SERVER_DECIDES}`,
    };
  }
  if (isCashOnDemand(read)) {
    return {
      terms: "COD",
      termsText: COD_TERMS_TEXT,
      gate: "cod",
      gateText: "There is no credit account to check. The whole payment is taken to create the order, and the server decides.",
    };
  }
  const codTerms = accountKind(read) === "COD_TERMS";
  const facts = `Credit limit ${moneyWord(read.creditLimitCents)}, open balance ${moneyWord(read.openBalanceCents)}, as the server reports them.`;
  // Zero-day terms are the product's Cash On Demand account even where a credit line stands: said as the terms, not as "0 days".
  const termsText = codTerms ? COD_TERMS_TEXT : `Account customer, ${read.paymentTermsDays} days.`;
  if (hold === null) {
    return {
      terms: "ACCOUNT",
      termsText,
      gate: "server",
      gateText: `${facts} The account's standing could not be read, so a credit hold is not confirmed here. ${SERVER_DECIDES}`,
    };
  }
  const pastDue = hold.pastDueCents !== null && hold.pastDueCents > 0 ? ` Past due ${moneyWord(hold.pastDueCents)}${hold.oldestBucket === null ? "" : `, oldest ${hold.oldestBucket} days`}.` : "";
  if (hold.onHold) {
    return {
      terms: "ACCOUNT",
      termsText,
      gate: "hold",
      gateText: `${facts} The server reports a credit hold on this account.${pastDue} The order is refused while the hold stands, so ask accounts receivable to release it first.`,
    };
  }
  if (hold.standing === "past_due") {
    return {
      terms: "ACCOUNT",
      termsText,
      gate: "pastdue",
      gateText: `${facts} The account is past due but not on hold.${pastDue} ${SERVER_DECIDES}`,
    };
  }
  if (hold.standing === "walk_in") {
    return {
      terms: "ACCOUNT",
      termsText,
      gate: "server",
      gateText: `${facts} The server reports no standing on file for this customer. ${SERVER_DECIDES}`,
    };
  }
  return {
    terms: "ACCOUNT",
    termsText,
    gate: "clear",
    gateText: `${facts} The account is in good standing: nothing past due and no credit hold. The server still checks the credit limit when you confirm, and the server decides.`,
  };
}

/** The credit gate's COD policy, as the plain conversion words it: this customer has no credit account. */
export function isCodRefusal(detail: string): boolean {
  return /\(COD\)|no credit; cash or card only/i.test(detail);
}

/**
 * True when the refusal is the core's convert key reuse refusal (convert_idempotency.go
 * ErrConvertKeyReused). It is proof the earlier keyed attempt made an order, so the desk settles the
 * attempt by re reading the quote and shows that order; the core's advice to use a new key is never
 * passed on, and no fresh key is minted from it.
 */
export function isConvertKeyReusedRefusal(detail: string): boolean {
  return /used for a different conversion/i.test(detail);
}

/**
 * What the convert route says about a quote that is already converted: an order was already created
 * for it, named when the document family answered. This surface never asks for a second conversion,
 * whatever the dealer's multi accept switch would let the core do with a fresh key.
 */
export function convertedStopWords(quoteNo: string, orderNo: string | null): string {
  return `Quote ${quoteNo} is converted. An order was already created for this quote${orderNo === null ? "" : `: ${orderNo}`}. No second order is made here.`;
}

/**
 * The server's refusal at confirm, in plain words, with what to do next. The refusal's own sentence
 * (spoken, its module prefix peeled) stays the first thing said; the next step follows it. Only an
 * answer in words proves nothing was created; a lost or 5xx answer says the outcome is not known.
 */
export function refusalWords(status: number | undefined, detail: string, afterLostAnswer = false): string {
  // No answer at all, or a 5xx, may follow a conversion that committed: the order's creation is not
  // known, and only the quote's state on reload says. A retry cannot make a second order, because the
  // core converts only from ACCEPTED.
  if (isUnknownOutcome(status)) {
    return "Whether the order was created is not known yet: the answer was lost. Check the quote's state on reload before anything else is done. Convert again: the same key is sent, so the server answers the first order and never makes a second.";
  }
  const said = spokenRefusal(detail.trim());
  if (status === 409 && afterLostAnswer) {
    return `${said}${said.endsWith(".") ? "" : "."} An earlier attempt whose answer was lost may have created the order. Check the quote's state on reload before converting again.`;
  }
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

/**
 * The refusal of the Cash On Demand action (convert, bill and take the whole payment in one
 * transaction), in the server's own words: no credit advice, a cash customer has no credit to settle.
 * A refusal answers whole, so nothing was created and nothing was taken. No answer at all (the
 * network dropped, the request timed out) is not a refusal: the action may have happened, so the
 * person is told to try again, which sends the same payment key and so can never take it twice.
 */
export function codRefusalWords(status: number | undefined, detail: string): string {
  if (status === undefined) {
    return "We could not confirm whether the order was created and the payment taken. Try again: the same payment is sent with the same key, so it can never be taken twice.";
  }
  const said = spokenRefusal(detail.trim());
  const lead = said === "" ? "The server did not accept the order and payment." : `${said}${said.endsWith(".") ? "" : "."}`;
  if (status >= 500 || !isWordedRefusal(detail)) {
    return `${isWordedRefusal(detail) ? `${lead} ` : ""}We could not confirm whether the order was created and the payment taken. Try again: the same payment is sent with the same key, so it can never be taken twice.`;
  }
  const reload = status === 400 || status === 409 ? " If the quote was changed since this page opened, reload the page and try again." : "";
  return `${lead} Nothing was created and nothing was taken.${reload}`;
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

/** What a payment is, to the server: the tender, the amount and the reference. */
export interface PaymentIntent {
  method: PayMethod;
  amountCents: number;
  reference: string;
}

/** An intended payment and the one key it is sent under. */
export interface KeyedIntent extends PaymentIntent {
  key: string;
}

/**
 * The key follows the intended payment: the same tender, amount and reference keep the key they
 * were first sent under, so a retry of a payment whose answer was lost replays and posts nothing
 * twice; any change is a new payment and gets a fresh key (the server refuses one key used for two
 * different payments).
 */
export function paymentKeyFor(prev: KeyedIntent | null, intent: PaymentIntent, mint: () => string = () => crypto.randomUUID()): KeyedIntent {
  const same = prev !== null && prev.method === intent.method && prev.amountCents === intent.amountCents && prev.reference === intent.reference;
  return { ...intent, key: same ? prev.key : mint() };
}

/**
 * True when a refusal carries a sentence the server wrote. The client's own text for a bodyless answer
 * ("HTTP 400"), a bare status phrase ("Forbidden", "Bad Request") and a proxy's page are not the
 * server's words, so they prove nothing about whether money moved: only a sentence does.
 */
export function isWordedRefusal(detail: string): boolean {
  const said = spokenRefusal((detail ?? "").trim());
  if (said === "" || said.startsWith("<") || /^HTTP \d{3}$/i.test(said)) return false;
  return !/^(bad request|unauthorized|forbidden|not found|conflict|gone|method not allowed|request timeout|unprocessable entity|unprocessable content|too many requests|payment required)\.?$/i.test(said);
}

/**
 * Why a payment against an existing order was not taken, in words. Only a refusal the server answered
 * in words (a 4xx with its sentence) proves nothing was taken. No answer at all (the network dropped,
 * the request timed out) or a 5xx may follow a write that committed, so the outcome is unknown: the
 * words say so, send the person to check the order's balance before anything else is taken, and
 * offer no second payment. A 4xx with no sentence of the server's own (no body, a bare status phrase) is
 * unknown too, because only the server's words prove nothing was taken; a retry from the desk sends the same payment key and can never take it twice.
 */
export function paymentNotTakenWords(status: number | undefined, detail: string, amountCents: number): string {
  if (isUnknownOutcome(status) || !isWordedRefusal(detail)) {
    return `We could not confirm whether the payment of ${moneyWord(amountCents)} was taken. Check the order's balance (reload the order) before anything else is taken. Trying again here sends the same payment key, so it can never be taken twice.`;
  }
  const said = spokenRefusal(detail.trim());
  const why = said === "" ? "the server did not accept it" : said;
  return `The payment of ${moneyWord(amountCents)} was not taken: ${why}${why.endsWith(".") ? "" : "."} Nothing was charged. Try the payment again, or take it at the counter.`;
}

/**
 * What the person is told when the order was made while they were on another screen and the payment
 * they typed for it was settled after they left: taken, or not taken and why, or not known.
 */
export function awayNotice(input: { orderNo: string; quoteNo: string; amountCents: number; paid: { paymentNo: string } | null; status?: number; said?: string }): string {
  const made = `Order ${input.orderNo} was created from quote ${input.quoteNo}`;
  if (input.paid !== null) return `${made}, and the payment of ${moneyWord(input.amountCents)} was taken (${input.paid.paymentNo}).`;
  if (isUnknownOutcome(input.status) || !isWordedRefusal(input.said ?? "")) return `${made}. ${paymentNotTakenWords(input.status, "", input.amountCents)}`;
  const said = (input.said ?? "").trim();
  const why = said === "" ? "the server did not accept it" : said;
  return `${made}, but the payment of ${moneyWord(input.amountCents)} was not taken: ${why}${why.endsWith(".") ? "" : "."} Nothing was charged. Take it at the counter.`;
}

/** The Cash On Demand action left behind while the person was away, said plainly. */
export function awayCodNotice(input: { quoteNo: string; amountCents: number; paid: { orderNo: string; paymentNo: string } | null; words: string }): string {
  if (input.paid !== null) {
    return `Order ${input.paid.orderNo} was created from quote ${input.quoteNo}, and the Cash On Demand payment of ${moneyWord(input.amountCents)} was taken (${input.paid.paymentNo}).`;
  }
  return `Quote ${input.quoteNo} was not converted and the Cash On Demand payment of ${moneyWord(input.amountCents)} was not taken. ${input.words}`;
}
