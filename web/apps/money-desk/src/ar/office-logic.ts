// The receivables office's plain logic: the checks made before a request is sent, the words for what the server
// answered, and who may do what the role may not. Nothing here computes money, and no address or number a
// customer gave is ever put into words: a contact reads as what is on file, never as the address itself, and a
// message reads as where it goes by role, since the messaging sink is the only way out in the demo.
//
// The limits and sentences follow the core (core/internal/billing: ar_reminders.go, ar_landing.go,
// register_service.go; core/internal/till; core/internal/payments) so a refusal the browser makes is the one the
// server would have made.
import type { BillingAPContact, BillingAPContactRead, BillingLandingPolicy, BillingReminderSend, PaymentsPayout, PaymentsReapplication } from "@hardscapehouse/api-client";
import { refusalWords } from "./ar-logic.js";

// ---- who may, in words, for the walls this slice meets ------------------------------------------------------------

const CONTROLLER_ONLY = "That is for the Financial Controller or the General Manager. Payables and Receivables can read it and work the accounts.";

const WHO_MAY: Record<string, string> = {
  "policy-write": `Changing receivables rules is for the Financial Controller or the General Manager. Payables and Receivables can read the rules the desk runs on.`,
  "auto-hold": `The automatic credit hold switch is for the Financial Controller or the General Manager.`,
  "nsf-policy": `The returned payment fee and its hold are for the Financial Controller or the General Manager to change.`,
  "policy-history": CONTROLLER_ONLY,
  "till-release": `Lifting a hold that a returned payment placed is for the Financial Controller or the General Manager.`,
};

/** A refusal as words: the walls this slice meets name who may, everything else is the receivables desk's own words. */
export function officeRefusal(action: string, error: string, status: number | undefined): string {
  // The gate's shared body is a wall for a role; any other 403 is the core's own sentence (the base plan, a branch
  // the caller cannot reach) and is said as the core wrote it.
  if (status === 403 && error !== "Forbidden" && error.trim() !== "") return refusalWords(action, error, 422);
  if (status === 403 && WHO_MAY[action] !== undefined) return WHO_MAY[action] ?? "";
  return refusalWords(action, error, status);
}

// ---- one key per attempt, kept while the outcome is unknown ------------------------------------------------------

/**
 * Keys for the writes that carry one. A key is minted at the first press of an attempt and sent again on every
 * retry of it; it ends only when the server has answered (a result or a refusal), never on a lost answer or a 5xx.
 * The attempt is named by what it is (the action and the thing it acts on), so the same press retried finds its key
 * and another thing's press never shares it.
 */
export class AttemptKeys {
  readonly #held = new Map<string, string>();
  readonly #mint: () => string;

  constructor(mint: () => string) {
    this.#mint = mint;
  }

  /** The key for this attempt: the one already held, or a fresh one now held. */
  keyFor(attempt: string): string {
    const held = this.#held.get(attempt);
    if (held !== undefined) return held;
    const key = this.#mint();
    this.#held.set(attempt, key);
    return key;
  }

  /** The server answered: the attempt is over and its key ends. */
  settle(attempt: string): void {
    this.#held.delete(attempt);
  }

  held(attempt: string): boolean {
    return this.#held.has(attempt);
  }
}

/** Whether an answer leaves the outcome unknown: the request may have landed and the answer been lost. */
export function outcomeUnknown(status: number | undefined): boolean {
  return status === undefined || status >= 500;
}

// ---- AP contacts ---------------------------------------------------------------------------------------------------

export type ContactCheck = { ok: true; contact: { contact_name: string; email: string; phone: string } } | { ok: false; words: string };

/** The core's ValidateAPContact, said before the request is sent. */
export function validateApContact(input: { name: string; email: string; phone: string }): ContactCheck {
  const name = input.name.trim();
  const email = input.email.trim();
  const phone = input.phone.trim();
  if ([...name].length > 120) return { ok: false, words: "The contact name is at most 120 characters." };
  if (email.length > 254 || phone.length > 32) return { ok: false, words: "An AP email is at most 254 characters and a phone at most 32." };
  if (email !== "" && (!email.includes("@") || /[ \t]/.test(email))) return { ok: false, words: "That AP email address is not an address." };
  if (email === "" && phone === "") return { ok: false, words: "An AP contact needs an email or a phone: one of the two is what makes it a contact." };
  return { ok: true, contact: { contact_name: name, email, phone } };
}

/** What is on file, in words, never the address or the number itself. */
export function contactLine(read: BillingAPContactRead | null): string {
  if (read === null || !read.tagged || read.contact === undefined) return "No AP contact is tagged on this account.";
  const c: BillingAPContact = read.contact;
  const email = c.email.trim() !== "" ? "email on file" : "no email";
  const phone = c.phone.trim() !== "" ? "phone on file" : "no phone";
  const label = c.contact_name.trim();
  return label === "" ? `AP contact on file: ${email}, ${phone}.` : `AP contact on file, ${label}: ${email}, ${phone}.`;
}

/** The channel a contact can be reached on: email first, then phone; nothing when the contact is empty. */
function reach(c: BillingAPContact): string {
  if (c.email.trim() !== "") return "by email";
  if (c.phone.trim() !== "") return "by phone";
  return "";
}

/** How a statement goes: the core queues it as a text on the notice topic, to the number the account consented to. The AP contact takes no part. */
export const STATEMENT_ROUTE_LINE = "A statement with a balance is queued as a text to the account's consented number when notices are on.";

export function statementHow(delivery: string): string {
  if (delivery === "QUEUED") return "Queued as a text to the account's consented number";
  if (delivery === "SENT") return "Sent as a text to the account's consented number";
  if (delivery === "FAILED") return "Did not send";
  return "Not queued";
}

/** The notice after an AP contact is saved: only reminders go to it. */
export const SAVED_NOTICE = "The AP contact is saved. Reminders for this account go to it through the messaging sink.";

// ---- statement runs ------------------------------------------------------------------------------------------------

export function runModeWord(mode: string): string {
  return mode === "INTERIM" ? "One account" : "Month end, every account";
}

export function runNoticeWords(run: { notices_enabled: boolean; notice_queued_count: number; notice_suppressed_count: number }): string {
  if (!run.notices_enabled) return "Statement notices are switched off for this dealer, so nothing was queued.";
  const n = run.notice_queued_count;
  const queued = `${n} ${n === 1 ? "statement" : "statements"} went to the messaging sink`;
  return run.notice_suppressed_count > 0 ? `${queued}; ${run.notice_suppressed_count} had no AP contact or was held back.` : `${queued}.`;
}

const DELIVERY_WORD: Record<string, string> = { NOT_QUEUED: "Not queued", QUEUED: "Queued", SENT: "Sent", FAILED: "Failed" };

export function deliveryWord(status: string): string {
  return DELIVERY_WORD[status] ?? status;
}

// ---- reminders -----------------------------------------------------------------------------------------------------

export type Channel = "EMAIL" | "SMS";

export function channelWord(channel: string): string {
  return channel === "SMS" ? "text" : "email";
}

const SEND_STATUS_WORD: Record<string, string> = { QUEUED: "Queued", DELIVERED: "Went to the messaging sink", FAILED: "Failed: nothing left the building" };

export function sendStatusWord(status: string): string {
  return SEND_STATUS_WORD[status] ?? status;
}

/** Who a send went to, by the kind of recipient and never by address. */
export function recipientWords(send: Pick<BillingReminderSend, "recipient_kind">): string {
  return send.recipient_kind === "FALLBACK" ? "the account's own address, which you confirmed" : "the AP contact";
}

export function reminderResultWords(send: BillingReminderSend, invoiceNo: string): string {
  if (send.status === "FAILED") return `Reminder for ${invoiceNo} did not go: nothing left the building.`;
  const stamp = send.watermark !== undefined && send.watermark !== "" ? ` It was stamped ${send.watermark}.` : "";
  if (send.status === "QUEUED") return `Reminder for ${invoiceNo} is queued for ${recipientWords(send)} by ${channelWord(send.channel)}.${stamp}`;
  return `Reminder for ${invoiceNo} went to ${recipientWords(send)} by ${channelWord(send.channel)}, through the messaging sink.${stamp}`;
}

export interface ReminderPreview {
  /** Why nothing would go, in words; empty when something would. */
  readonly blocked: string;
  readonly lines: readonly string[];
}

/**
 * What a reminder would do before it is sent, from the dealer's policy and the account's contact. It is a
 * reading of the core's ResolveTarget, never a second judge: the server decides, and a refusal is said as it wrote it.
 */
export function reminderPreview(input: { policy: BillingLandingPolicy; channel: Channel; contact: BillingAPContactRead | null | "error"; confirm: boolean }): ReminderPreview {
  const { policy, channel, contact, confirm } = input;
  const word = channelWord(channel);
  if (policy.ar_tier !== "PRO") return { blocked: "Reminders are part of accelerated AR, and this dealer is on the base plan.", lines: [] };
  if (channel === "EMAIL" && !policy.reminder_email_enabled) return { blocked: "Email reminders are switched off for this dealer.", lines: [] };
  if (channel === "SMS" && !policy.reminder_sms_enabled) return { blocked: "Text reminders are switched off for this dealer.", lines: [] };
  if (contact === null) return { blocked: "", lines: ["Checking the contact."] };
  if (contact === "error") return { blocked: "", lines: ["The contact could not be read. The server will still decide where it goes."] };
  const tagged = contact.tagged && contact.contact !== undefined;
  const reachable = tagged && (channel === "SMS" ? (contact.contact?.phone.trim() ?? "") !== "" : (contact.contact?.email.trim() ?? "") !== "");
  if (reachable) {
    const label = contact.contact?.contact_name.trim() ?? "";
    return { blocked: "", lines: [`Goes to the AP contact${label === "" ? "" : ` (${label})`}, by ${word}, through the messaging sink.`] };
  }
  const who = tagged ? `No AP contact is reachable by ${word}` : "Nobody is tagged for AP on this account";
  if (policy.untagged_fallback === "REFUSE") return { blocked: `${who}, and this dealer does not fall back to the account's own address.`, lines: [] };
  return {
    blocked: "",
    lines: [confirm ? `${who}. The account's own address will be used, as you confirmed.` : `${who}. The account's own address is used only if you confirm it below.`],
  };
}

// ---- the paper path ------------------------------------------------------------------------------------------------

export type PaperResolveCheck = { ok: true; status: "MOVED_TO_PAPER" | "DISMISSED"; note: string } | { ok: false; words: string };

/** The core's ResolvePaperPathFlag, said before the request is sent. */
export function paperResolveCheck(status: string, note: string): PaperResolveCheck {
  if (status !== "MOVED_TO_PAPER" && status !== "DISMISSED") return { ok: false, words: "Choose how the flag closes: moved to paper, or dismissed." };
  const trimmed = note.trim();
  if (trimmed === "") return { ok: false, words: "Say why in a few words: the next person to open this account reads it, not the flag." };
  if ([...trimmed].length > 300) return { ok: false, words: "That note is longer than 300 characters." };
  return { ok: true, status, note: trimmed };
}

// ---- time ----------------------------------------------------------------------------------------------------------

const DAY_MS = 86_400_000;

/**
 * A moment as the days since it, counted from elapsed time: "today" inside the first day, then "1 day ago". A
 * timestamp is an instant, not a calendar date, and the desk does not guess the dealer's zone to cut it into days.
 */
export function relativeStamp(stamp: string, nowMs: number): string {
  const at = Date.parse(stamp);
  if (Number.isNaN(at)) return "";
  const days = Math.max(0, Math.floor((nowMs - at) / DAY_MS));
  if (days === 0) return "today";
  return `${days} ${days === 1 ? "day" : "days"} ago`;
}

// ---- an invoice: send, dispute, a payment reversal ------------------------------------------------------------------

import type { BillingReminderResult, PaymentsReversalResult, PaymentsVoidResult } from "@hardscapehouse/api-client";
import { formatMoney, parseAmountToCents } from "./ar-money.js";

export function sendInvoiceWords(result: BillingReminderResult): string {
  if (result.replayed > 0 && result.sent === 0) return "That invoice was already sent on this key, so nothing new went.";
  const skipped = Object.entries(result.skipped ?? {});
  if (skipped.length > 0) {
    const reasons = [...new Set(skipped.map(([, why]) => why))];
    if (reasons.length === 1) return `The invoice did not go: ${reasons[0]}.`;
    return `The invoice did not go: ${skipped.length} invoices were skipped.`;
  }
  return "The invoice went to the account's contact through the messaging sink.";
}

// The server's own word for a delivery that did not go. The desk says it, but the reason is the
// server's: there is no queue behind the desk, so a failed delivery is not a reminder waiting to be
// retried by anything (FW1-06b round 2, S6's P2-2).
const FAILED_DELIVERY = "delivery failed";

/**
 * What the account page's Remind run says happened (FW1-06b): sent, or nothing sent and why. The
 * server decides all of it; this words its answer. A run that says sent when nothing left the
 * building is the one thing this screen must never do, so anything the server did not count as sent
 * is named with its reason.
 */
export function reminderRunWords(result: BillingReminderResult): { notice: string; sent: boolean } {
  const skipped = Object.entries(result.skipped ?? {});
  if (result.replayed > 0 && result.sent === 0 && skipped.length === 0) {
    return { notice: `Nothing new went out: ${result.replayed} had already been sent.`, sent: false };
  }
  if (result.sent === 0) {
    const reasons = [...new Set(skipped.map(([, why]) => why))];
    if (reasons.length === 1 && reasons[0].startsWith(FAILED_DELIVERY)) {
      return { notice: `No reminder went out: ${reasons[0]}.`, sent: false };
    }
    if (reasons.length === 1) return { notice: `No reminder was sent: ${reasons[0]}.`, sent: false };
    return { notice: `No reminder was sent: ${skipped.length} ${skipped.length === 1 ? "invoice was" : "invoices were"} skipped.`, sent: false };
  }
  let notice = `Reminder sent for ${result.sent} ${result.sent === 1 ? "invoice" : "invoices"} to the account's contact email.`;
  if (skipped.length > 0) notice += ` ${skipped.length} ${skipped.length === 1 ? "was" : "were"} not sent: nothing went out, send it again from the desk.`;
  if (result.replayed > 0) notice += ` ${result.replayed} had already been sent.`;
  return { notice, sent: true };
}

const DISPUTE_REASON_WORD: Record<string, string> = { SHORT_DELIVERY: "Short delivery", DAMAGED: "Damaged", PRICING: "Pricing", NOT_ORDERED: "Not ordered", OTHER: "Other" };
const DISPUTE_OUTCOME_WORD: Record<string, string> = { PAID: "Paid", CREDITED: "Credited", WRITTEN_OFF: "Written off", WITHDRAWN: "Withdrawn" };

export const DISPUTE_REASONS: readonly string[] = ["SHORT_DELIVERY", "DAMAGED", "PRICING", "NOT_ORDERED", "OTHER"];
export const DISPUTE_OUTCOMES: readonly string[] = ["PAID", "CREDITED", "WRITTEN_OFF", "WITHDRAWN"];

export function disputeReasonWord(reason: string): string {
  return DISPUTE_REASON_WORD[reason] ?? reason;
}

export function disputeOutcomeWord(outcome: string): string {
  return DISPUTE_OUTCOME_WORD[outcome] ?? outcome;
}

export type DisputeCheck = { ok: true; reason: string; amountCents: number; note: string } | { ok: false; words: string };

/** The core's OpenDispute checks, said before the request is sent; the amount is parsed through the decimal string. */
export function disputeCheck(input: { reason: string; amountText: string; note: string; balanceCents: number }): DisputeCheck {
  if (!DISPUTE_REASONS.includes(input.reason)) return { ok: false, words: "Choose why the customer disputes it." };
  if (input.amountText.trim() === "") return { ok: false, words: "Enter the disputed amount, such as 100.00." };
  const cents = parseAmountToCents(input.amountText);
  if (cents === null) return { ok: false, words: "Enter the amount as dollars and cents, such as 100.00." };
  if (cents === 0) return { ok: false, words: "The amount must be more than zero." };
  if (cents > input.balanceCents) return { ok: false, words: `That is more than the invoice balance of ${formatMoney(input.balanceCents)}.` };
  return { ok: true, reason: input.reason, amountCents: cents, note: input.note.trim() };
}

export type ReversalCheck = { ok: true; reason: string } | { ok: false; words: string };

export function reversalCheck(reason: string): ReversalCheck {
  const trimmed = reason.trim();
  if (trimmed === "") return { ok: false, words: "Enter why the payment is being reversed, such as keyed on the wrong account." };
  return { ok: true, reason: trimmed };
}

/** What a reversal undid, from the server's own answer. */
export function reversalWords(result: PaymentsReversalResult, paymentNo: string): string {
  const n = result.reopened_invoices.length;
  const kind = result.label === "VOID" ? "a void, the same day" : "a reversal";
  return `Payment ${paymentNo} is reversed as ${kind}. ${formatMoney(result.amount_cents)} is owing again on ${n} ${n === 1 ? "invoice" : "invoices"}.`;
}

export type VoidCheck = { ok: true; reason: string } | { ok: false; words: string };

/** The true void's own check, apart from the reversal's: the receipt is undone as never taken (FW1-06b). */
export function voidCheck(reason: string): VoidCheck {
  const trimmed = reason.trim();
  if (trimmed === "") return { ok: false, words: "Enter why the receipt is being voided, such as keyed for the wrong amount." };
  return { ok: true, reason: trimmed };
}

/** What a void undid, from the server's own answer: the receipt never happened and nothing is held. */
export function voidWords(result: PaymentsVoidResult, paymentNo: string): string {
  const n = result.reopened_invoices.length;
  return `Payment ${paymentNo} is voided as never received. ${formatMoney(result.amount_cents)} is owing again on ${n} ${n === 1 ? "invoice" : "invoices"}, and nothing is held for the customer.`;
}

export type ReapplyCheck = { ok: true; invoiceId: string; amountCents: number; reason: string } | { ok: false; words: string };

/** The core's re-application checks, said before the request is sent; the amount is parsed through the decimal string. */
export function reapplyCheck(input: { invoiceId: string; amountText: string; reason: string; heldCents: number; balanceCents: number | null }): ReapplyCheck {
  if (input.invoiceId === "" || input.balanceCents === null) return { ok: false, words: "Choose the invoice to put the money on." };
  if (input.amountText.trim() === "") return { ok: false, words: "Enter the amount to apply, such as 100.00." };
  const cents = parseAmountToCents(input.amountText);
  if (cents === null) return { ok: false, words: "Enter the amount as dollars and cents, such as 100.00." };
  if (cents === 0) return { ok: false, words: "The amount must be more than zero." };
  if (cents > input.heldCents) return { ok: false, words: `That is more than the ${formatMoney(input.heldCents)} still held for this payment.` };
  if (cents > input.balanceCents) return { ok: false, words: `That is more than the invoice's open balance of ${formatMoney(input.balanceCents)}.` };
  const reason = input.reason.trim();
  if (reason === "") return { ok: false, words: "Enter why the money is being applied, such as keyed on the wrong account." };
  return { ok: true, invoiceId: input.invoiceId, amountCents: cents, reason };
}

/** What a re-application did, from the server's own answer: the money moved, and what is still held. */
export function reapplyWords(result: PaymentsReapplication, paymentNo: string): string {
  const where = result.lines.map((l) => l.invoice_no).join(" and ");
  const left = result.held_after_cents === 0 ? "Nothing is held for this payment now." : `${formatMoney(result.held_after_cents)} is still held for this payment.`;
  return `${formatMoney(result.applied_cents)} of payment ${paymentNo} is applied to invoice ${where}. ${left}`;
}

export type PayoutKind = "DRAWER" | "BANK" | "WRITE_OFF";

/** The dealer's words for each way held money leaves, as the picker and the notice say them. */
export const PAYOUT_KIND_WORDS: Record<PayoutKind, { pick: string; done: string; button: string }> = {
  DRAWER: { pick: "Cash from the drawer", done: "is handed back from the drawer", button: "Hand it back" },
  BANK: { pick: "Cheque or transfer", done: "is paid by cheque or transfer", button: "Pay it out" },
  WRITE_OFF: { pick: "Write off after the count", done: "is written off against the count's shortage", button: "Write it off" },
};

export type PayoutCheck = { ok: true; kind: PayoutKind; amountCents: number; reason: string; reference: string } | { ok: false; words: string };

/** The core's pay out checks, said before the request is sent; the amount is parsed through the decimal string. */
export function payoutCheck(input: { kind: string; amountText: string; reason: string; reference: string; heldCents: number }): PayoutCheck {
  if (input.kind !== "DRAWER" && input.kind !== "BANK" && input.kind !== "WRITE_OFF") return { ok: false, words: "Choose how the money leaves." };
  if (input.amountText.trim() === "") return { ok: false, words: "Enter the amount, such as 100.00." };
  const cents = parseAmountToCents(input.amountText);
  if (cents === null) return { ok: false, words: "Enter the amount as dollars and cents, such as 100.00." };
  if (cents === 0) return { ok: false, words: "The amount must be more than zero." };
  if (cents > input.heldCents) return { ok: false, words: `That is more than the ${formatMoney(input.heldCents)} still held for this payment.` };
  const reference = input.reference.trim();
  if (input.kind === "BANK" && reference === "") return { ok: false, words: "Enter the cheque number or transfer reference." };
  const reason = input.reason.trim();
  if (reason === "") return { ok: false, words: "Enter why the money is leaving, such as the customer asked for it back." };
  return { ok: true, kind: input.kind, amountCents: cents, reason, reference: input.kind === "BANK" ? reference : "" };
}

/** What a pay out did, from the server's own answer: the money that left and what is still held. */
export function payoutWords(result: PaymentsPayout, paymentNo: string): string {
  const how = PAYOUT_KIND_WORDS[result.kind as PayoutKind]?.done ?? "has left";
  const left = result.held_after_cents === 0 ? "Nothing is held for this payment now." : `${formatMoney(result.held_after_cents)} is still held for this payment.`;
  return `${formatMoney(result.amount_cents)} of payment ${paymentNo} ${how}. ${left}`;
}

// ---- settings: the receivables rules, the register, the returned payment fee, automatic holds ----------------------------

import type { BillingAutoHoldSetting, BillingDispatchGateResult } from "@hardscapehouse/api-client";

export interface LandingForm {
  readonly warn: string;
  readonly escalate: string;
  readonly cadence: string;
  readonly maxPerCycle: string;
  readonly window: string;
  readonly minSends: string;
  readonly email: boolean;
  readonly sms: boolean;
  readonly fallback: string;
  readonly tier: string;
}

export type LandingCheck = { ok: true; policy: Omit<BillingLandingPolicy, "branch_id"> } | { ok: false; words: string };

interface Knob {
  readonly key: keyof LandingForm;
  /** How the knob starts a sentence, and how it reads after "Enter". */
  readonly subject: string;
  readonly entry: string;
  readonly lo: number;
  readonly hi: number;
  readonly unit: "days" | "";
}

const KNOBS: readonly Knob[] = [
  { key: "warn", subject: "Overdue warning", entry: "Overdue warning", lo: 1, hi: 364, unit: "days" },
  { key: "escalate", subject: "Escalation", entry: "Escalation", lo: 2, hi: 365, unit: "days" },
  { key: "cadence", subject: "Days between reminders", entry: "Days between reminders", lo: 1, hi: 90, unit: "" },
  { key: "maxPerCycle", subject: "Reminders per cycle", entry: "Reminders per cycle", lo: 1, hi: 20, unit: "" },
  { key: "window", subject: "The paper path window", entry: "the paper path window", lo: 7, hi: 365, unit: "days" },
  { key: "minSends", subject: "Reminders before the paper path flag", entry: "Reminders before the paper path flag", lo: 1, hi: 20, unit: "" },
];

/** A whole number typed in a box, or null: digits only, no sign, no fraction. */
function wholeNumber(text: string): number | null {
  const t = text.trim();
  return /^\d{1,9}$/.test(t) ? Number(t) : null;
}

/** The core's ValidateARLandingPolicy, said before the request is sent: the six ranges in order, the vocabularies, the edge order. */
export function landingPolicyCheck(form: LandingForm): LandingCheck {
  const n: Record<string, number> = {};
  for (const k of KNOBS) {
    const v = wholeNumber(String(form[k.key]));
    if (v === null) return { ok: false, words: `Enter ${k.entry} as a whole number${k.unit === "days" ? " of days" : ""}.` };
    if (v < k.lo || v > k.hi) return { ok: false, words: `${k.subject} must be between ${k.lo} and ${k.hi}${k.unit === "days" ? " days" : ""}.` };
    n[k.key] = v;
  }
  if (form.fallback !== "CONFIRM" && form.fallback !== "REFUSE") return { ok: false, words: "Choose what happens when no AP contact is tagged." };
  if (form.tier !== "BASE" && form.tier !== "PRO") return { ok: false, words: "Choose the plan." };
  if ((n["warn"] ?? 0) >= (n["escalate"] ?? 0)) return { ok: false, words: `Overdue warning (${n["warn"]}) must be fewer than escalation (${n["escalate"]}).` };
  return {
    ok: true,
    policy: {
      aging_warn_days: n["warn"] ?? 0,
      aging_escalate_days: n["escalate"] ?? 0,
      reminder_cadence_days: n["cadence"] ?? 0,
      reminder_max_per_cycle: n["maxPerCycle"] ?? 0,
      black_hole_window_days: n["window"] ?? 0,
      black_hole_min_sends: n["minSends"] ?? 0,
      reminder_email_enabled: form.email,
      reminder_sms_enabled: form.sms,
      untagged_fallback: form.fallback,
      ar_tier: form.tier,
    },
  };
}

export interface RegisterForm {
  readonly visibility: string;
  readonly dueSoon: string;
  readonly cooldown: string;
  readonly tones: readonly string[];
  readonly showCredits: boolean;
}

export type RegisterCheck =
  | { ok: true; values: { account_position_visibility: string; due_soon_days: number; reminder_cooldown_hours: number; reminder_tones: string[]; show_credits_default: boolean } }
  | { ok: false; words: string };

export function registerPolicyCheck(form: RegisterForm): RegisterCheck {
  if (!["MANAGER_UP", "ALL_STAFF", "HIDDEN"].includes(form.visibility)) return { ok: false, words: "Choose who sees an account's position." };
  const due = wholeNumber(form.dueSoon);
  if (due === null) return { ok: false, words: "Enter the due soon window as a whole number of days." };
  if (due < 1 || due > 90) return { ok: false, words: "The due soon window must be between 1 and 90 days." };
  const cool = wholeNumber(form.cooldown);
  if (cool === null) return { ok: false, words: "Enter the reminder cooldown as a whole number of hours." };
  if (cool > 720) return { ok: false, words: "The reminder cooldown must be between 0 and 720 hours." };
  return { ok: true, values: { account_position_visibility: form.visibility, due_soon_days: due, reminder_cooldown_hours: cool, reminder_tones: [...form.tones], show_credits_default: form.showCredits } };
}

export interface NsfForm {
  readonly fee: string;
  readonly taxable: boolean;
  readonly code: string;
  readonly autoHold: boolean;
  readonly notes: string;
}

export type NsfCheck = { ok: true; policy: { fee_cents: number; fee_taxable: boolean; fee_account_code: string; auto_hold: boolean; notes: string } } | { ok: false; words: string };

/** The fee is typed in dollars and sent in cents, through the decimal string; the core refuses a negative one. */
export function nsfPolicyCheck(form: NsfForm): NsfCheck {
  if (form.fee.trim() === "") return { ok: false, words: "Enter the fee, such as 35.00. Zero means no fee." };
  const cents = parseAmountToCents(form.fee);
  if (cents === null) return { ok: false, words: "Enter the fee as dollars and cents, such as 35.00." };
  return { ok: true, policy: { fee_cents: cents, fee_taxable: form.taxable, fee_account_code: form.code.trim(), auto_hold: form.autoHold, notes: form.notes.trim() } };
}

const FIELD_WORD: Record<string, string> = {
  aging_warn_days: "Overdue warning",
  aging_escalate_days: "Escalation",
  reminder_cadence_days: "Days between reminders",
  reminder_max_per_cycle: "Reminders per cycle",
  black_hole_window_days: "Paper path window",
  black_hole_min_sends: "Reminders before the paper path flag",
  reminder_email_enabled: "Email reminders",
  reminder_sms_enabled: "Text reminders",
  untagged_fallback: "With no AP contact tagged",
  ar_tier: "Plan",
};

export function policyFieldWord(field: string): string {
  return FIELD_WORD[field] ?? field;
}

const VALUE_WORD: Record<string, string> = { BASE: "Base", PRO: "Accelerated AR", CONFIRM: "Ask to confirm the account's address", REFUSE: "Refuse to send" };

export function policyValueWord(field: string, value: string): string {
  if (field.endsWith("_enabled")) return value === "true" ? "On" : "Off";
  return VALUE_WORD[value] ?? value;
}

/** The switch as lines a person can read: what it is, what it would apply, and what it cannot do. */
export function autoHoldLines(s: BillingAutoHoldSetting): string[] {
  const lines = [
    s.auto_hold_enabled
      ? "Automatic holds are on. The credit sweep places holds by itself, and each one can be lifted or overridden here."
      : "Automatic holds are off. The credit sweep reports what it would hold and places nothing by itself.",
    s.aging_hold_enabled
      ? `Aged receivables: held past ${s.aging_bucket_days} days overdue once ${formatMoney(s.aging_threshold_cents)} or more is in that age.`
      : "Aged receivables: not held automatically.",
    s.limit_hold_enabled
      ? s.limit_tolerance_cents > 0
        ? `Over the credit limit: held when a balance goes over the limit by more than ${formatMoney(s.limit_tolerance_cents)}.`
        : "Over the credit limit: held when a balance goes over the limit."
      : "Over the credit limit: not held automatically.",
    s.cod_enforced ? "Pay before release accounts are held until the balance is paid." : "Pay before release accounts are not held automatically.",
  ];
  if (!s.thresholds_editable) lines.push("The thresholds are the dealer's policy and are read only here.");
  if (!s.switch_wired) lines.push("The switch is not wired on this server, so it is shown but cannot be changed.");
  return lines;
}

export function dispatchWords(result: BillingDispatchGateResult, orderNo: string): string {
  if (result.allowed) return `Order ${orderNo} may go out: nothing on the account stops it.`;
  const reason = result.reason.trim();
  return `Order ${orderNo} is stopped: ${/[.!?]$/.test(reason) ? reason : `${reason}.`}`;
}

// ---- the credit sweep ------------------------------------------------------------------------------------------------

import type { BillingEvaluationResult } from "@hardscapehouse/api-client";

/** What a sweep did: it places holds only when automatic holds are on, and otherwise reports what it would hold. */
export function sweepWords(r: BillingEvaluationResult): string {
  const accounts = `${r.evaluated} ${r.evaluated === 1 ? "account" : "accounts"}`;
  if (r.dry_run) return `The sweep checked ${accounts}. It would hold ${r.would_hold === 0 ? "none" : r.would_hold} and placed none, because automatic holds are off.`;
  return `The sweep checked ${accounts} and placed ${r.placed} ${r.placed === 1 ? "hold" : "holds"}.`;
}
