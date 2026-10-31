// The money logic of Pro Pay, in plain framework free code (the screens only render it).
//
// What the core does, read at source (core/internal/payments/service.go and intent.go): it ignores the client's
// idempotency key and re-derives its own from the account, the sorted invoice ids and the funding source; it sums
// max(balance - holdback, 0) itself; a repeat of a settled request returns the original payment; a request whose
// charge is already at the processor is a 409, which is not a retry of the charge; a decline is a 402 and moves no
// money. So the client's duties are narrower than a key: never turn an unknown answer into a second, different request,
// never send two requests for one press, never let a changed selection ride on a held attempt, and never put anything
// but ids and one funding source on the wire. PaySender is that, and nothing else.
//
// Sums shown here (the sticky bar, the sheet's total) are advisory sums of the core's own integer cents. The amount
// charged is the core's, and the receipt shows the core's figure, flagged when it differs from the one shown.
import { tenantPayments, type ApiClient, type Result, type TenantPayment } from "@hardscapehouse/api-client";

// ---- what can be paid ----------------------------------------------------------------------------------------

interface Payable {
  status: string;
  balance_cents: number;
  holdback_cents: number;
  /** The core's quote of what a payment today charges (the open amount less the early payment discount). */
  payoff_cents?: number | undefined;
}

/** What one invoice can pay: the open balance less the legally retained holdback, never below zero. */
export function payableCents(invoice: Pick<Payable, "balance_cents" | "holdback_cents">): number {
  return Math.max(invoice.balance_cents - invoice.holdback_cents, 0);
}

/**
 * What paying this invoice today charges: the core's quote when it sends one (the payable less the early payment
 * discount the invoice's terms allow today), else the payable. The core charges exactly its quote, so this is the figure
 * the screen shows, the sums add up and the Pay button carries.
 */
export function payoffCents(invoice: Pick<Payable, "balance_cents" | "holdback_cents" | "payoff_cents">): number {
  return invoice.payoff_cents ?? payableCents(invoice);
}

/** The early payment discount on offer: its amount and the last day it runs, or none. */
export function discountOffer(invoice: { discount_cents?: number | undefined; discount_by?: string | undefined }): { cents: number; by: string } | null {
  const cents = invoice.discount_cents ?? 0;
  const by = invoice.discount_by ?? "";
  return cents > 0 && by !== "" ? { cents, by } : null;
}

const OPEN_STATUSES = new Set(["FINALIZED", "PARTIALLY_PAID"]);

/** An invoice the person may pick: open, with something payable. */
export function isPayable(invoice: Payable): boolean {
  return OPEN_STATUSES.has(invoice.status) && payableCents(invoice) > 0;
}

// ---- aging ---------------------------------------------------------------------------------------------------

export type Bucket = "current" | "1-30" | "31-60" | "60+";
export const BUCKETS: readonly Bucket[] = ["current", "1-30", "31-60", "60+"];
export const BUCKET_WORDS: Readonly<Record<Bucket, string>> = { current: "Current", "1-30": "1 to 30 days", "31-60": "31 to 60 days", "60+": "Over 60 days" };

function dayNumber(day: string): number {
  const m = /^(\d{4})-(\d{2})-(\d{2})/.exec(day);
  if (m === null) return Number.NaN;
  return Date.UTC(Number(m[1]), Number(m[2]) - 1, Number(m[3])) / 86_400_000;
}

/** Whole calendar days past the due day; 0 on the due day and before it. Both are the dealer's days, read as written. */
export function daysPastDue(dueDate: string, today: string): number {
  const late = dayNumber(today) - dayNumber(dueDate);
  return Number.isFinite(late) && late > 0 ? late : 0;
}

export function agingBucket(daysLate: number): Bucket {
  if (daysLate <= 0) return "current";
  if (daysLate <= 30) return "1-30";
  if (daysLate <= 60) return "31-60";
  return "60+";
}

export interface AgingTotals {
  buckets: Record<Bucket, number>;
  overdueCents: number;
  overdueCount: number;
}

export function agingTotals(rows: readonly (Pick<Payable, "status" | "balance_cents"> & { due_date: string })[], today: string): AgingTotals {
  const buckets: Record<Bucket, number> = { current: 0, "1-30": 0, "31-60": 0, "60+": 0 };
  let overdueCount = 0;
  for (const row of rows) {
    if (!OPEN_STATUSES.has(row.status)) continue;
    const late = daysPastDue(row.due_date, today);
    buckets[agingBucket(late)] += row.balance_cents;
    if (late > 0) overdueCount += 1;
  }
  return { buckets, overdueCents: buckets["1-30"] + buckets["31-60"] + buckets["60+"], overdueCount };
}

// ---- the fee, shown before a method is chosen -----------------------------------------------------------------

export type MethodKind = "CARD" | "BANK";

/**
 * The fee the core adds to a charge. The core charges exactly the summed payable balance and the tenant contract
 * carries no fee field, so this is zero for every method. It is one function so a fee the core later serves changes
 * here and nowhere else; showing a percentage the core will not apply would be a confident wrong number.
 */
export function feeCents(_kind: MethodKind, _amountCents: number): number {
  return 0;
}

export function chargeFor(kind: MethodKind, amountCents: number): { feeCents: number; totalCents: number; feeWords: string } {
  const fee = feeCents(kind, amountCents);
  return { feeCents: fee, totalCents: amountCents + fee, feeWords: fee === 0 ? "No fee" : "Fee added" };
}

// ---- reading the pay answer -----------------------------------------------------------------------------------

export type PayOutcome =
  | { kind: "paid"; payment: TenantPayment; differs: boolean }
  | { kind: "declined"; words: string }
  | { kind: "refused"; words: string; status: number }
  | { kind: "unknown"; words: string }
  | { kind: "blocked"; words: string };

const UNKNOWN_WORDS = "We could not confirm this payment. It may have gone through. Check it before paying again.";
const WAITING_WORDS = "A payment is still waiting for an answer. Check it first.";

const CHECK_WORDS = "This payment could not be confirmed as stopped. Check Statements, then refresh before paying again.";

/**
 * paid; declined (402, no money moved); refused (401 and 403 at the guards, no money moved; 400 and 404 only say the
 * request was not accepted, never that no money moved); unknown (409, 5xx, no answer, and a 400 that reads "balance
 * changed", which the core sends after an approved charge when the settle finds the balance moved: a possible success).
 */
export function classifyPayment(result: Result<TenantPayment>, expectedCents = Number.NaN): PayOutcome {
  if (result.ok) return { kind: "paid", payment: result.value, differs: Number.isFinite(expectedCents) && result.value.amount_cents !== expectedCents };
  const status = result.status;
  if (status === 402) return { kind: "declined", words: "The payment was declined. No money moved." };
  if (status === 401) return { kind: "refused", status, words: "You are signed out. Sign in again. No money moved." };
  if (status === 403) return { kind: "refused", status, words: "Your role does not allow paying invoices. No money moved." };
  if (status === 400 && /balance changed/i.test(result.error)) return { kind: "unknown", words: UNKNOWN_WORDS };
  if (status !== undefined && status >= 400 && status < 500 && status !== 409) return { kind: "refused", status, words: CHECK_WORDS };
  return { kind: "unknown", words: UNKNOWN_WORDS };
}

// ---- the sender -----------------------------------------------------------------------------------------------

/** One payment attempt: the invoices and exactly one funding source (a stored method id or a one time token). */
export interface PayBody {
  invoiceIds: readonly string[];
  methodId?: string | undefined;
  nonce?: string | undefined;
}

interface Attempt {
  key: string;
  canon: string;
  expectedCents: number;
  wire: { invoice_ids: string[]; payment_method_id?: string; nonce?: string; idempotency_key: string };
}

export interface PaySenderOptions {
  /** The capability gate: false makes every press a refusal with no request. */
  canPay: () => boolean;
  /** The deadline of one send; a send that outlives it is unknown, never failed. */
  timeoutMs?: number;
  newKey?: () => string;
}

function check(body: PayBody): { ok: true; ids: string[] } | { ok: false; words: string } {
  const ids = [...body.invoiceIds].sort();
  if (ids.length === 0) return { ok: false, words: "Pick at least one invoice." };
  if (new Set(ids).size !== ids.length) return { ok: false, words: "An invoice can be picked only once." };
  const method = body.methodId !== undefined && body.methodId !== "";
  const nonce = body.nonce !== undefined && body.nonce !== "";
  if (method === nonce) return { ok: false, words: "Choose one way to pay." };
  return { ok: true, ids };
}

export class PaySender {
  readonly #client: ApiClient;
  readonly #canPay: () => boolean;
  readonly #timeoutMs: number;
  readonly #newKey: () => string;
  #held: Attempt | null = null;
  #inflight: { attempt: Attempt; promise: Promise<PayOutcome> } | null = null;
  #settled: { canon: string; outcome: Extract<PayOutcome, { kind: "paid" }> } | null = null;

  constructor(client: ApiClient, options: PaySenderOptions) {
    this.#client = client;
    this.#canPay = options.canPay;
    this.#timeoutMs = options.timeoutMs ?? 20_000;
    this.#newKey = options.newKey ?? (() => crypto.randomUUID());
  }

  /**
   * The attempt held for an unknown outcome, if any: its screen offers Check payment and nothing else. It is the
   * sender's own wire, read only, so a reopened screen is built from what Check payment will send.
   */
  get waiting(): { expectedCents: number; invoiceIds: readonly string[]; methodId?: string; nonce?: string } | null {
    const held = this.#held;
    if (held === null) return null;
    const { invoice_ids, payment_method_id, nonce } = held.wire;
    const view: { expectedCents: number; invoiceIds: readonly string[]; methodId?: string; nonce?: string } = { expectedCents: held.expectedCents, invoiceIds: [...invoice_ids] };
    if (payment_method_id !== undefined) view.methodId = payment_method_id;
    if (nonce !== undefined) view.nonce = nonce;
    return view;
  }

  /** A different person signed in: the held attempt and the last receipt are not theirs. */
  forget(): void {
    this.#held = null;
    this.#settled = null;
  }

  /** Forget the last receipt (the sheet closed): a later press of the same selection is a new request to the core. */
  clearSettled(): void {
    this.#settled = null;
  }

  pay(body: PayBody, expectedCents: number): Promise<PayOutcome> {
    if (!this.#canPay()) return Promise.resolve({ kind: "blocked", words: "Your role cannot pay invoices." });
    const checked = check(body);
    if (!checked.ok) return Promise.resolve({ kind: "blocked", words: checked.words });
    const canon = JSON.stringify([checked.ids, body.methodId ?? "", body.nonce ?? "", expectedCents]);

    if (this.#inflight !== null) {
      // The same press again shares the one request. Anything else waits for it: one charge is never raced by another.
      return this.#inflight.attempt.canon === canon ? this.#inflight.promise : Promise.resolve({ kind: "blocked", words: WAITING_WORDS });
    }
    if (this.#held !== null && this.#held.canon !== canon) return Promise.resolve({ kind: "blocked", words: WAITING_WORDS });
    if (this.#held === null && this.#settled !== null && this.#settled.canon === canon) return Promise.resolve(this.#settled.outcome);

    const attempt = this.#held ?? this.#start(checked.ids, body, canon, expectedCents);
    const promise = this.#send(attempt);
    this.#inflight = { attempt, promise };
    return promise;
  }

  #start(ids: string[], body: PayBody, canon: string, expectedCents: number): Attempt {
    const key = this.#newKey();
    const wire: Attempt["wire"] = { invoice_ids: ids, idempotency_key: key };
    if (body.methodId !== undefined && body.methodId !== "") wire.payment_method_id = body.methodId;
    else wire.nonce = body.nonce as string;
    return { key, canon, expectedCents, wire };
  }

  async #send(attempt: Attempt): Promise<PayOutcome> {
    let outcome: PayOutcome;
    try {
      const answer = await tenantPayments.tenantCreatePayment(this.#client, attempt.wire, { timeoutMs: this.#timeoutMs });
      outcome = classifyPayment(answer, attempt.expectedCents);
    } catch {
      outcome = { kind: "unknown", words: UNKNOWN_WORDS };
    }
    this.#inflight = null;
    if (outcome.kind === "unknown") {
      this.#held = attempt;
    } else if (this.#held === attempt && outcome.kind === "refused" && (outcome.status === 400 || outcome.status === 401 || outcome.status === 404)) {
      // A check of a held attempt answered 400, 404 or 401 (a lapsed session): none of them says the charge failed, so it stays held.
    } else {
      this.#held = null;
      if (outcome.kind === "paid") this.#settled = { canon: attempt.canon, outcome };
    }
    return outcome;
  }
}

// ---- the hosted card frame's hand over ------------------------------------------------------------------------

/**
 * Reads what the processor's hosted frame sent up: a token and nothing else. The shape is checked (a short string of
 * token characters) and anything that looks like card digits is refused, so even a misbehaving frame cannot make the
 * app carry a card to the core. The caller has already checked the sender is the frame it mounted.
 */
export function readHostedToken(data: unknown): string | null {
  if (typeof data !== "object" || data === null) return null;
  const message = data as { type?: unknown; token?: unknown };
  if (message.type !== "hh-hosted-token" || typeof message.token !== "string") return null;
  const token = message.token;
  if (token.length === 0 || token.length > 200 || !/^[A-Za-z0-9._:-]+$/.test(token)) return null;
  const digits = token.replace(/[-.:_]/g, "");
  if (/^\d{12,19}$/.test(digits)) return null;
  return token;
}
