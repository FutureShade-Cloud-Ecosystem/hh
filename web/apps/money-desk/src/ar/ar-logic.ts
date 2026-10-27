// The receivables slice's plain logic: the words for what the server answered,
// who may do what the role may not, and the checks made before a request is
// sent. Nothing here computes money; the server decides every figure and every
// refusal, and this file only says them out loud.
//
// Port provenance: the card and status vocabulary follows the original app's
// ar-landing.ts and ar-invoice-register.ts (repository hardscapeos_dibbits,
// commit f8f4973db317b92296ae8ec7e62a3f54f5dc82ab); the refusal words follow the
// role gate's shared body ("Forbidden") and ROLE-APP-OBLIGATIONS 1.
import { centsToField, formatMoney, parseAmountToCents } from "./ar-money.js";
import { stampDay } from "@hardscapehouse/pos-kit/src/ported/host-facts.js";

export type CardBucket = "CURRENT" | "WARN" | "LATE" | "ESCALATION";

type CardDays = { bucket: string; from_days: number; to_days?: number | undefined };

/**
 * A card is titled by the days it covers, built from the tier days the core sends (the dealer's own
 * past due settings), so a dealer who retunes the tiers sees their own ranges. Never a second
 * vocabulary: the old tier words are gone from the screen.
 */
export function cardTitleFor(card: CardDays): string {
  if (card.bucket === "CURRENT") return "Current";
  if (card.to_days === undefined || card.to_days === 0) return `${card.from_days} days and over`;
  if (card.from_days === card.to_days) return card.from_days === 1 ? "1 day" : `${card.from_days} days`;
  return `${card.from_days} to ${card.to_days} days`;
}

/**
 * Whether a card is drawn. A tier the dealer set so that it runs backwards (the escalation day one past
 * the warning day gives a middle card of 61 to 60) covers no day and can hold nothing, so it is left out.
 */
export function cardShown(card: CardDays): boolean {
  return card.bucket === "CURRENT" || card.to_days === undefined || card.to_days === 0 || card.from_days <= card.to_days;
}

/** The statement strip's bands, in the screen's one vocabulary. The last is over 60: the core sums 61 and up into one figure. */
export const STATEMENT_STRIP_LABELS = ["Current", "1 to 30 days", "31 to 60 days", "Over 60 days", "Total due"] as const;

/** What follows the over 60 figure in the statement notice, in the strip's own words. */
export const STATEMENT_OVERDUE_NOTE = "of this balance is over 60 days past due. Please contact our office to arrange payment.";

/** What a card's figure is: the status word for everything past its due date. */
export function cardCovers(card: CardDays): string {
  return card.bucket === "CURRENT" ? "Not yet due" : "Past due";
}

/** The old tier words stay for the Office reminder trail, which names the tier a reminder ran for. */
export const CARD_TITLE: Record<CardBucket, string> = {
  ESCALATION: "Escalation",
  LATE: "Late",
  WARN: "Overdue",
  CURRENT: "Current",
};

export function cardTitle(bucket: string): string {
  return CARD_TITLE[bucket as CardBucket] ?? bucket;
}

const STATUS_WORD: Record<string, string> = {
  OPEN: "Open",
  DUE_SOON: "Due soon",
  OVERDUE: "Past due",
  PARTIALLY_PAID: "Part paid",
  PAID: "Paid",
  DISPUTED: "Disputed",
  CREDIT_HOLD: "Credit hold",
  DRAFT: "Draft",
  VOID: "Void",
  CREDIT: "Credit",
};

export function statusWord(status: string): string {
  return STATUS_WORD[status] ?? status;
}

export type Tone = "good" | "watch" | "stop" | "plain" | "info";

/** The pill's tone; the word is always beside it, so colour never carries the meaning alone. */
export function statusTone(status: string): Tone {
  if (status === "PAID") return "good";
  if (status === "OVERDUE" || status === "CREDIT_HOLD" || status === "DISPUTED" || status === "VOID") return "stop";
  if (status === "DUE_SOON" || status === "PARTIALLY_PAID") return "watch";
  return "plain";
}

export const HOLD_KIND_WORD: Record<string, string> = {
  NSF: "Returned payment",
  AR_AGING: "Aged AR",
  CREDIT_LIMIT: "Over limit",
  COD: "Pay before release",
  MANUAL: "Manual",
};

export function holdKindWord(kind: string): string {
  return HOLD_KIND_WORD[kind] ?? kind;
}

/** What each walled move is, and who may do it (the matrix: Money Desk at Administer or Manage). */
const WHO_MAY: Record<string, string> = {
  load: "Your role cannot open receivables. Ask the General Manager or the Financial Controller to widen your access.",
  "release-hold":
    "Releasing a credit hold is for the Financial Controller or the General Manager. Payables and Receivables can see the hold, chase the account and record the payment that clears it.",
  "approve-journal":
    "Approving a parked journal is for the Financial Controller or the General Manager. Payables and Receivables can see what is waiting but cannot decide it.",
};

const SERVER_PREFIX = /^[a-z][a-z0-9]*: /;

function sentence(text: string): string {
  const bare = text.trim().replace(SERVER_PREFIX, "");
  if (bare === "") return "";
  const cased = bare.charAt(0).toUpperCase() + bare.slice(1);
  return /[.!?]$/.test(cased) ? cased : `${cased}.`;
}

/**
 * A refusal or failure as the words a person reads. A 403 from the role gate
 * names who may do the move; a figure the server worded in cents reads in
 * dollars; the server's own sentence is otherwise kept, only capitalised.
 */
export function refusalWords(action: string, error: string, status: number | undefined): string {
  if (status === 403) {
    return WHO_MAY[action] ?? "Your role may not do that. Ask the General Manager or the Financial Controller.";
  }
  if (status === 401) return "Your sign in has ended. Sign in again.";
  if (status === undefined) return "The server could not be reached. Nothing was changed. Try again.";
  if (status >= 500) return "The server hit a problem. Nothing was changed. Try again.";
  const asDollars = error.replace(/\b(\d+) cents\b/g, (_whole, digits: string) => formatMoney(Number(digits)));
  const words = sentence(asDollars);
  return words === "" ? "The server refused that." : words;
}

/**
 * What a recorded payment's failure means for the person, read from the HTTP status alone, never by
 * matching words in the error string (FP 187). A lost answer or a 5xx leaves the outcome unknown: the
 * server may have written the payment before the answer was lost, so the words say so and send the
 * person to check the account before taking anything again (the desk holds the payment's key and
 * sends it again, so pressing again sends the same payment and cannot post the money twice). A 409 is
 * the route's replay conflict: the earlier press landed as a different payment and THIS form's
 * payment was not recorded (both things said, the form kept for a fresh press), and it is the same
 * answer whatever wording the conflict carries (a different payment, or another account's). A worded
 * 422 is the route's pre-write refusal of a JSON body it read and checked, with nothing written. A
 * 401 or a 403 is the gate's answer, not the route's: when the account already holds a key from an
 * earlier press it leaves the outcome unknown and the key kept. The same holds for every other 4xx
 * while a key is held (a 400 for a body mangled in flight, a 404, a 408, a 429): none of them is the
 * route's answer for the held key, so none of them ends it.
 */
export type PaymentOutcome = { kind: "unknown" | "refused" | "recorded-other"; words: string };

export function paymentOutcome(error: string, status: number | undefined, heldKey = false): PaymentOutcome {
  if (status === undefined || status >= 500) {
    return {
      kind: "unknown",
      words:
        "The server did not give a clear answer, so this payment may or may not have been recorded. Check the account's payments and invoice balances before taking the money again. Press Record payment again to send the same payment: this desk sends the payment's own key, and if the earlier press was already recorded the server will say so.",
    };
  }
  if (heldKey && (status === 401 || status === 403)) {
    // The gate answered before the payment route saw the request, so it says nothing about the earlier
    // press: the key is kept and the outcome stays unknown. The 401's own sentence already says to sign
    // in, so its press instruction follows it; the 403 names who may record the payment, and its press
    // instruction waits for a role that can (the same role pressing again draws the same 403).
    const press =
      status === 401
        ? "Then press Record payment"
        : "Press Record payment again only once you are signed in with a role that can record it";
    return {
      kind: "unknown",
      words: `The earlier press may or may not have been recorded. ${refusalWords("payment", error, status)} ${press}: this desk sends the same key again, and the server will say if the earlier press was already recorded.`,
    };
  }
  if (status === 409) {
    // The route's replay conflict for this key: the earlier press landed as a different payment and
    // this form's payment was not recorded. Both things are true and both are said, whatever wording
    // the conflict itself carries.
    return {
      kind: "recorded-other",
      words:
        "This payment was not recorded. The earlier press was recorded as a different payment. Check the account, then press Record payment to record this one.",
    };
  }
  if (heldKey && status !== 422) {
    // The payment route's own refusal of a body it read answers 422 in its words; a 404 is its no
    // existence leak answer, which its replay walk can also give for a payment that landed, and every
    // other 4xx (400, 408, 413, 429, a proxy's answer) is not the route's answer for this key at all.
    // None of them says what happened to the earlier press, so the key is kept and the outcome stays
    // unknown.
    return {
      kind: "unknown",
      words:
        "The earlier press may or may not have been recorded. This answer does not say what happened to it. Check the account's payments and invoice balances before taking the money again. Press Record payment again to send the same payment: this desk sends the same key, and the server will say if the earlier press was already recorded.",
    };
  }
  return { kind: "refused", words: `The payment was not recorded. ${refusalWords("payment", error, status)}` };
}

/** The route's ceiling on a payment's invoice list (core/internal/payments/received_apply.go, MaxApplyInvoices):
 * the browser refuses a pick over it locally, in the route's own words, before any body is sent (not
 * even a preview's), while the server answers the replay judgement ahead of validateApply, so a pick
 * over the ceiling never reaches either door. */
export const MAX_APPLY_INVOICES = 100;

/** The ceiling's words for a pick over it: the route's own sentence first, then the pick's count. */
export function pickCeilingWords(picked: number): string {
  return `A received payment may name at most ${MAX_APPLY_INVOICES} invoices; record it as more than one payment. ${picked} invoices are picked.`;
}

export type PromiseCheck = { ok: true; promisedCents: number } | { ok: false; words: string };

export function validatePromise(input: { amountText: string; date: string; by: string; note: string; balanceCents: number }): PromiseCheck {
  if (input.amountText.trim() === "") return { ok: false, words: "Enter the amount promised, such as 100.00." };
  const cents = parseAmountToCents(input.amountText);
  if (cents === null) return { ok: false, words: "Enter the amount as dollars and cents, such as 100.00." };
  if (cents === 0) return { ok: false, words: "The amount must be more than zero." };
  if (cents > input.balanceCents) return { ok: false, words: `That is more than the invoice balance of ${formatMoney(input.balanceCents)}.` };
  if (input.date.trim() === "") return { ok: false, words: "Pick the date the customer promised to pay by." };
  if (input.by.trim() === "") return { ok: false, words: "Enter who made the promise." };
  return { ok: true, promisedCents: cents };
}

export { centsToField };

// ---- days, marks and holds as the Money Desk references draw them -----------------------------

const DAY_MS = 86_400_000;

// A date only value is already a calendar day; a timestamp is the day it falls on in the dealer's zone.
function dayNumber(date: string): number {
  return Math.floor(Date.parse(`${stampDay(date)}T00:00:00Z`) / DAY_MS);
}

/** A calendar date as a count from the server's own day: "122 days ago", "in 12 days", "today". */
/**
 * Whether an invoice document still has to be collected: the core's own unpaid set (stored status
 * FINALIZED or PARTIALLY_PAID). A draft and a void invoice are not owed, and a paid one is settled, so
 * none of them is listed as open, picked, allocated to or put on a statement.
 */
export function isUnpaidInvoice(row: { stored_status: string; doc_type?: string | undefined }): boolean {
  return (row.stored_status === "FINALIZED" || row.stored_status === "PARTIALLY_PAID") && (row.doc_type === undefined || row.doc_type === "INVOICE");
}

export function relativeDay(date: string, asOf: string): string {
  const gap = dayNumber(asOf) - dayNumber(date);
  if (gap === 0) return "today";
  const n = Math.abs(gap);
  const unit = n === 1 ? "day" : "days";
  return gap > 0 ? `${n} ${unit} ago` : `in ${n} ${unit}`;
}

export interface Mark {
  readonly word: string;
  readonly tone: Tone;
  /** A dashed outline: a second mark that rides the row (a promise), never a status of its own. */
  readonly dashed: boolean;
}

/**
 * The marks a register row carries. The first is the status with the day count as its word; a
 * promise or a dispute rides the row as a second mark, never a colour alone.
 */
export function rowMarks(row: { status: string; days_overdue: number; disputed: boolean; promised_date?: string | null | undefined }): Mark[] {
  const marks: Mark[] = [];
  const overdue = row.days_overdue > 0 && row.status !== "PAID";
  if (overdue) marks.push({ word: `${row.days_overdue}d past due`, tone: "stop", dashed: false });
  else if (row.status === "DISPUTED") marks.push({ word: "disputed", tone: "info", dashed: false });
  else marks.push({ word: statusWord(row.status).toLowerCase(), tone: statusTone(row.status), dashed: false });
  if (typeof row.promised_date === "string" && row.promised_date !== "") marks.push({ word: "promise open", tone: "watch", dashed: true });
  if (row.disputed && (overdue || row.status !== "DISPUTED")) marks.push({ word: "disputed", tone: "info", dashed: false });
  return marks;
}

const BUCKET_WORD: Record<string, string> = {
  CURRENT: "Current",
  "1_30": "1 to 30 days",
  "31_60": "31 to 60 days",
  "61_90": "61 to 90 days",
  "90_PLUS": "Over 90 days",
};

/** The older labels the core still stamps, read back to the same one vocabulary. */
const BUCKET_WORD_BY_LABEL: Record<string, string> = {
  Current: "Current",
  "1 to 30": "1 to 30 days",
  "31 to 60": "31 to 60 days",
  "61 to 90": "61 to 90 days",
  "91 and over": "Over 90 days",
};

/**
 * The band's words for the screen: from the core's bucket key when it sends one, else from its label.
 * The core's own label ("91 and over") is not shown; the key names the band.
 */
export function bucketWord(key: string | undefined, label: string): string {
  return (key === undefined ? undefined : BUCKET_WORD[key]) ?? BUCKET_WORD_BY_LABEL[label] ?? label;
}

/** C8 5-bucket human label and machine key for a given days-past-due count. Mirrors the server's
 * BucketLabelFor function in core/internal/billing/register_model.go at every boundary (inclusive at
 * the top of each band, so day 90 is in 61 to 90 and day 91 in Over 90), in the screen's one vocabulary.
 * The key is the core's; the label is the screen's words for it.
 */
export function bucketLabelFor(daysOverdue: number): { label: string; key: string } {
  const key = daysOverdue <= 0 ? "CURRENT" : daysOverdue <= 30 ? "1_30" : daysOverdue <= 60 ? "31_60" : daysOverdue <= 90 ? "61_90" : "90_PLUS";
  return { label: BUCKET_WORD[key] ?? key, key };
}

/** "N days past due" / "1 day past due" phrasing for the due-column line. */
export function daysLateWord(daysOverdue: number): string {
  const n = Math.max(0, Math.floor(daysOverdue));
  const unit = n === 1 ? "day" : "days";
  return `${n} ${unit} past due`;
}

/** What the server's split says about one invoice, in the references' words. */
export function lineWords(line: { outcome: string; stays_open_cents: number }): string {
  if (line.outcome === "CLOSES") return "closes";
  if (line.outcome === "PART") return `part, ${formatMoney(line.stays_open_cents)} stays open`;
  return "untouched";
}

/** The small tags beside a hold's kind: auto when the sweep placed it, override in force while one runs. */
export function holdMarks(hold: { auto_placed: boolean; override_until?: string | null | undefined }, nowMs: number): string[] {
  const marks: string[] = [];
  if (hold.auto_placed) marks.push("auto");
  if (typeof hold.override_until === "string" && Date.parse(hold.override_until) > nowMs) marks.push("override in force");
  return marks;
}

/** Roles that may lift or override a hold (the matrix: Money Desk at Administer and Manage, row 345). */
export function canLiftHolds(roleCode: string): boolean {
  return roleCode === "financial_controller" || roleCode === "general_manager";
}

/** The true void of a mis-keyed receipt is a manager act, the same pair the lift hold gate trusts (FW1-06b). The
 * server re-refuses: this only hides the button from roles the route would turn away. */
export function canVoidReceipt(roleCode: string): boolean {
  return canLiftHolds(roleCode);
}

/** Whether a receipt was taken today, the browser's reading of the dealer day. The core's gate is the
 * authority and answers in its own words when the day has turned; this only keeps the offer honest on screen. */
export function isSameDealerDay(receivedAtText: string, nowMs: number): boolean {
  const then = new Date(receivedAtText);
  const now = new Date(nowMs);
  return then.getFullYear() === now.getFullYear() && then.getMonth() === now.getMonth() && then.getDate() === now.getDate();
}

export function paymentReferenceRequired(method: string): boolean {
  return ["CHEQUE", "EFT", "WIRE"].includes(method.trim().toUpperCase());
}

export function worklistActionWord(action: string): string {
  switch (action) {
    case "record_payment": return "Record payment";
    case "review_promise": return "Review promise";
    case "review_credit_hold": return "Review credit hold";
    case "apply_credit": return "Review credit";
    default: return "Open account";
  }
}

export function overpaymentPreview(answer: { unapplied_cents: number } | null, previewError: string): boolean {
  if (answer !== null && answer.unapplied_cents > 0) return true;
  return previewError.toLowerCase().includes("can take at most");
}
