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

export type Tone = "good" | "watch" | "stop" | "plain";

/** The pill's tone; the word is always beside it, so colour never carries the meaning alone. */
export function statusTone(status: string): Tone {
  if (status === "PAID") return "good";
  if (status === "OVERDUE" || status === "CREDIT_HOLD" || status === "DISPUTED" || status === "VOID") return "stop";
  if (status === "DUE_SOON" || status === "PARTIALLY_PAID") return "watch";
  return "plain";
}

export const HOLD_KIND_WORD: Record<string, string> = {
  NSF: "Bounced payment",
  AR_AGING: "Aged balance",
  CREDIT_LIMIT: "Over the limit",
  COD: "Pay before release",
  MANUAL: "Placed by hand",
};

export function holdKindWord(kind: string): string {
  return HOLD_KIND_WORD[kind] ?? kind;
}

/** What each walled move is, and who may do it (the matrix: Money Desk at Administer or Manage). */
const WHO_MAY: Record<string, string> = {
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

export type PaymentCheck = { ok: true; amountCents: number } | { ok: false; words: string };

export function validatePayment(input: { amountText: string; reference: string; balanceCents: number }): PaymentCheck {
  if (input.amountText.trim() === "") return { ok: false, words: "Enter the amount received, such as 100.00." };
  const cents = parseAmountToCents(input.amountText);
  if (cents === null) return { ok: false, words: "Enter the amount as dollars and cents, such as 100.00." };
  if (cents === 0) return { ok: false, words: "The amount must be more than zero." };
  if (cents > input.balanceCents) return { ok: false, words: `That is more than the invoice balance of ${formatMoney(input.balanceCents)}.` };
  if (input.reference.trim() === "") return { ok: false, words: "Enter a reference, such as a cheque number." };
  return { ok: true, amountCents: cents };
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
