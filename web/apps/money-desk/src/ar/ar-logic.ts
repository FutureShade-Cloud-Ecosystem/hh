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

export type CardBucket = "CURRENT" | "WARN" | "LATE" | "ESCALATION";

export const CARD_TITLE: Record<CardBucket, string> = {
  ESCALATION: "Escalation",
  LATE: "Late",
  WARN: "Overdue",
  CURRENT: "Current",
};

export function cardTitle(bucket: string): string {
  return CARD_TITLE[bucket as CardBucket] ?? bucket;
}

/** The days a card spans, in the words the landing draws. */
export function cardRange(card: { bucket: string; from_days: number; to_days?: number | undefined }): string {
  if (card.bucket === "CURRENT") return "Not yet due";
  if (card.to_days === undefined || card.to_days === 0) return `${card.from_days} days and over`;
  return `${card.from_days} to ${card.to_days} days`;
}

const STATUS_WORD: Record<string, string> = {
  OPEN: "Open",
  DUE_SOON: "Due soon",
  OVERDUE: "Overdue",
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
 * What a recorded payment's failure means for the person. A lost answer or a 5xx leaves the outcome unknown: the
 * server may have written the payment before the answer was lost, so the words say so and send the person to check
 * the account before taking anything again (pressing again with nothing changed carries the same key and posts
 * once). A worded 4xx is the route refusing with nothing written, in the route's own words.
 */
export type PaymentOutcome = { kind: "unknown" | "refused"; words: string };

export function paymentOutcome(error: string, status: number | undefined): PaymentOutcome {
  if (status === undefined || status >= 500) {
    return {
      kind: "unknown",
      words:
        "The server did not give a clear answer, so this payment may or may not have been recorded. Check the account's payments and invoice balances before taking the money again. Pressing Record payment again with nothing changed is safe: it is the same payment and posts once.",
    };
  }
  return { kind: "refused", words: `The payment was not recorded. ${refusalWords("payment", error, status)}` };
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

function dayNumber(date: string): number {
  return Math.floor(Date.parse(`${date.slice(0, 10)}T00:00:00Z`) / DAY_MS);
}

/** A calendar date as a count from the server's own day: "122 days ago", "in 12 days", "today". */
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
  if (overdue) marks.push({ word: `${row.days_overdue}d overdue`, tone: "stop", dashed: false });
  else if (row.status === "DISPUTED") marks.push({ word: "disputed", tone: "info", dashed: false });
  else marks.push({ word: statusWord(row.status).toLowerCase(), tone: statusTone(row.status), dashed: false });
  if (typeof row.promised_date === "string" && row.promised_date !== "") marks.push({ word: "promise open", tone: "watch", dashed: true });
  if (row.disputed && (overdue || row.status !== "DISPUTED")) marks.push({ word: "disputed", tone: "info", dashed: false });
  return marks;
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
